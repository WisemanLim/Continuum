package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"continuum/internal/disk"
	"continuum/internal/secrets"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
)

const GB = int64(1) << 30

// fakeStat simulates disk sizes per path prefix (PRD FR-P08 style tests).
type fakeStat map[string]disk.Capacity

func (f fakeStat) stat(path string) (disk.Capacity, error) {
	best, bestLen := disk.Capacity{Total: 1 << 40, Free: 1 << 40, Available: 1 << 40}, -1
	for p, c := range f {
		if len(p) > bestLen && (path == p || len(path) > len(p) && path[:len(p)+1] == p+string(filepath.Separator)) {
			best, bestLen = c, len(p)
		}
	}
	return best, nil
}

func newCore(t *testing.T, fs fakeStat) (*Core, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(storetest.URL(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := Open(context.Background(), db, fs.stat)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func mkdisk(t *testing.T, root, name string) string {
	p := filepath.Join(root, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAttachPreflightAndMarker(t *testing.T) {
	ctx := context.Background()
	c, root := newCore(t, fakeStat{})
	p := mkdisk(t, root, "disk-01")
	res, err := c.Attach(ctx, AttachRequest{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Preflight.Checks) != 10 {
		t.Fatalf("want 10 preflight checks, got %d", len(res.Preflight.Checks))
	}
	m, err := disk.ReadMarker(filepath.Join(p, disk.DataRootName))
	if err != nil || m.DiskID != res.Disk.ID || m.ControllerID != c.ControllerID {
		t.Fatalf("marker mismatch: %+v %v", m, err)
	}
	if res.Disk.Name != "disk-01" || res.Disk.Role != RoleAlwaysOn || res.Disk.State != StateOnline {
		t.Fatalf("unexpected disk %+v", res.Disk)
	}
	// same path again → marker re-attach, not duplicate
	res2, err := c.Attach(ctx, AttachRequest{Path: p})
	if err != nil || !res2.Reattach || res2.Disk.ID != res.Disk.ID {
		t.Fatalf("re-attach expected: %+v %v", res2, err)
	}
}

func TestReattachAfterMove(t *testing.T) {
	ctx := context.Background()
	c, root := newCore(t, fakeStat{})
	p := mkdisk(t, root, "usb")
	res, err := c.Attach(ctx, AttachRequest{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "usb-renamed")
	if err := os.Rename(p, moved); err != nil {
		t.Fatal(err)
	}
	c.RefreshCapacity(ctx, res.Disk.ID)
	if d, _ := c.GetDisk(ctx, res.Disk.ID); d.State != StateOffline {
		t.Fatalf("expected OFFLINE after removal, got %s", d.State)
	}
	res2, err := c.Attach(ctx, AttachRequest{Path: moved})
	if err != nil || res2.Disk.ID != res.Disk.ID || res2.Disk.MountPath != moved || res2.Disk.State != StateOnline {
		t.Fatalf("marker identity should survive path change: %+v %v", res2, err)
	}
}

func TestPreflightRejectsForeignController(t *testing.T) {
	ctx := context.Background()
	c, root := newCore(t, fakeStat{})
	p := mkdisk(t, root, "d")
	dr := filepath.Join(p, disk.DataRootName)
	os.MkdirAll(dr, 0o755)
	disk.WriteMarker(dr, disk.Marker{Schema: 1, ControllerID: "ctl_other", DiskID: "dsk_other"})
	if _, err := c.Attach(ctx, AttachRequest{Path: p}); CodeOf(err) != CodeInvalid {
		t.Fatalf("foreign controller marker must fail preflight, got %v", err)
	}
	if _, err := c.Attach(ctx, AttachRequest{Path: p, Adopt: true}); err != nil {
		t.Fatalf("adopt should succeed: %v", err)
	}
}

func TestReserveMustBeBelowHalf(t *testing.T) {
	ctx := context.Background()
	fs := fakeStat{}
	c, root := newCore(t, fs)
	p := mkdisk(t, root, "small")
	fs[p] = disk.Capacity{Total: uint64(10 * GB), Free: uint64(10 * GB), Available: uint64(10 * GB)}
	mf := 6 * GB
	if _, err := c.Attach(ctx, AttachRequest{Path: p, MinFreeBytes: &mf}); CodeOf(err) != CodeInvalid {
		t.Fatalf("min_free >= 50%% total must fail, got %v", err)
	}
}

func TestVolumeLifecycle(t *testing.T) {
	ctx := context.Background()
	fs := fakeStat{}
	c, root := newCore(t, fs)
	var ids []string
	for i, spec := range []struct {
		name  string
		total int64
		free  int64
		role  string
	}{{"disk-01", 50 * 1024 * GB, 18 * 1024 * GB, RoleAlwaysOn}, {"win-d", 200 * GB, 42 * GB, RoleAlwaysOn}, {"mac-ext", 1024 * GB, 512 * GB, RolePortable}} {
		p := mkdisk(t, root, spec.name)
		fs[p] = disk.Capacity{Total: uint64(spec.total), Free: uint64(spec.free), Available: uint64(spec.free)}
		res, err := c.Attach(ctx, AttachRequest{Path: p, Role: spec.role})
		if err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
		ids = append(ids, res.Disk.ID)
	}
	mf := 100 * GB
	v, err := c.CreateVolume(ctx, CreateVolumeRequest{Name: "Research Pool", MinFreeBytes: &mf, DiskIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if v.Slug != "research-pool" || len(v.Disks) != 3 || v.Disks[0].Ordinal != 1 || v.Disks[2].Name != "mac-ext" {
		t.Fatalf("bad volume %+v", v)
	}
	// FR-V10: usable = Σ(total − min_free), free = Σ effective_free
	wantUsable := (50*1024*GB - mf) + (200*GB - mf) + (1024*GB - mf)
	wantFree := 18*1024*GB + 42*GB + 512*GB
	if v.Summary.UsableBytes != wantUsable || v.Summary.FreeBytes != wantFree || v.Summary.DiskCount != 3 {
		t.Fatalf("summary %+v, want usable=%d free=%d", v.Summary, wantUsable, wantFree)
	}
	if len(v.Warnings) == 0 {
		t.Fatal("portable disk in volume must raise FR-V09 warning")
	}
	if _, err := c.CreateVolume(ctx, CreateVolumeRequest{Name: "research pool", DiskIDs: ids[:1]}); CodeOf(err) != CodeConflict {
		t.Fatalf("duplicate slug must conflict: %v", err)
	}

	// reorder with optimistic locking (FR-V20)
	v2, err := c.SetDisks(ctx, v.ID, SetDisksRequest{DiskIDs: []string{ids[2], ids[0], ids[1]}, ExpectedRevision: v.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Disks[0].ID != ids[2] || v2.Revision != v.Revision+1 {
		t.Fatalf("reorder failed: %+v", v2.Disks)
	}
	if _, err := c.SetDisks(ctx, v.ID, SetDisksRequest{DiskIDs: ids, ExpectedRevision: v.Revision}); CodeOf(err) != CodeRevisionMismatch {
		t.Fatalf("stale revision must be ABORTED, got %v", err)
	}
	if m, _ := disk.ReadMarker(v2.Disks[0].DataRoot); m.OrdinalSnapshot != 1 || m.VolumeSlug != "research-pool" {
		t.Fatalf("marker not updated after reorder: %+v", m)
	}

	// update config
	pol := PolicyTopdirAffinity
	v3, err := c.UpdateVolume(ctx, v.ID, UpdateVolumeRequest{ExpectedRevision: v2.Revision, PlacementPolicy: &pol})
	if err != nil || v3.PlacementPolicy != pol {
		t.Fatalf("update: %v %+v", err, v3)
	}

	// export → import on a fresh controller (FR-V22)
	bundle, err := c.ExportVolume(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := newCore(t, fs)
	v4, err := c2.ImportVolume(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(v4.Disks) != 3 || v4.Disks[0].Name != "mac-ext" || v4.PlacementPolicy != pol {
		t.Fatalf("import mismatch: %+v", v4)
	}

	// delete keeps files (registry only)
	if err := c.DeleteVolume(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if ds, _ := c.ListDisks(ctx, DiskFilter{Unassigned: true}); len(ds) != 3 {
		t.Fatalf("disks should be unassigned after volume delete, got %d", len(ds))
	}
}

func TestDetachMarksObjectsOffline(t *testing.T) {
	ctx := context.Background()
	c, root := newCore(t, fakeStat{})
	res, _ := c.Attach(ctx, AttachRequest{Path: mkdisk(t, root, "a")})
	v, err := c.CreateVolume(ctx, CreateVolumeRequest{Name: "v", DiskIDs: []string{res.Disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	c.DB.Exec(ctx, `INSERT INTO objects (object_id, volume_id, logical_path, parent_path, name, object_type, disk_id, physical_rel_path, size_bytes, created_at, updated_at)
		VALUES ('obj_1', ?, '/f', '/', 'f', 'file', ?, 'f', 1, ?, ?)`, v.ID, res.Disk.ID, now, now)
	if _, err := c.Detach(ctx, res.Disk.ID); err != nil {
		t.Fatal(err)
	}
	var st string
	c.DB.QueryRow(ctx, `SELECT state FROM objects WHERE object_id='obj_1'`).Scan(&st)
	if st != "offline" {
		t.Fatalf("object state %q, want offline", st)
	}
	if err := c.Unregister(ctx, res.Disk.ID); CodeOf(err) != CodeConflict {
		t.Fatalf("unregister with objects must be refused: %v", err)
	}
	if _, err := c.Enable(ctx, res.Disk.ID); err != nil {
		t.Fatal(err)
	}
	c.DB.QueryRow(ctx, `SELECT state FROM objects WHERE object_id='obj_1'`).Scan(&st)
	if st != "ok" {
		t.Fatalf("object state %q after enable", st)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Research Pool": "research-pool", "  Media__2026 ": "media-2026", "연구 풀 A": "a"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// FR-S06: secret settings live in the secret store; a legacy plaintext row is migrated out of the DB.
func TestSecretSettings(t *testing.T) {
	c, _ := newCore(t, fakeStat{})
	ctx := context.Background()
	c.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('notify.webhook_url', 'https://legacy.example/hook')`)
	c.Secrets = &secrets.File{Path: filepath.Join(t.TempDir(), "secrets.json")}
	if v := c.Setting(ctx, "notify.webhook_url", ""); v != "https://legacy.example/hook" {
		t.Fatalf("migrated value: %q", v)
	}
	var n int
	c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM settings WHERE key='notify.webhook_url'`).Scan(&n)
	if n != 0 {
		t.Fatal("plaintext secret must leave the settings table")
	}
	pw, _ := c.EnsureSetting(ctx, "access.smb_password", func() string { return "generated-1" })
	again, _ := c.EnsureSetting(ctx, "access.smb_password", func() string { return "generated-2" })
	if pw != "generated-1" || again != pw {
		t.Fatalf("ensure: %q %q", pw, again)
	}
	c.SetSetting(ctx, "notify.format", "discord") // ordinary settings stay in the DB
	c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM settings WHERE key='notify.format'`).Scan(&n)
	if n != 1 {
		t.Fatal("non-secret setting must stay in the DB")
	}
}
