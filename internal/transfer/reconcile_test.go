package transfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/core"
)

// FR-K03, NFR-I04: direct edits on a disk (add / modify / delete / bit rot / stale partial) are reconciled by SCAN.
func TestScanReconcilesDirectEdits(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 1<<30), v.disk("d2", 1<<30)
	vol := v.volume(0, d1, d2)
	src := filepath.Join(v.root, "src")
	writeFile(t, filepath.Join(src, "keep.bin"), 1000)
	writeFile(t, filepath.Join(src, "gone.bin"), 1000)
	writeFile(t, filepath.Join(src, "edit.bin"), 1000)
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if j = v.wait(j.ID); j.State != "DONE" {
		t.Fatalf("copy: %s %s", j.State, j.Error)
	}
	root1 := d1.DataRoot
	// direct edits (Finder / Explorer)
	writeFile(t, filepath.Join(root1, "p", "new folder", "orphan.txt"), 42)
	writeFile(t, filepath.Join(d2.DataRoot, "only-on-2.txt"), 7)
	os.Remove(filepath.Join(root1, "p", "gone.bin"))
	writeFile(t, filepath.Join(root1, "p", "edit.bin"), 2000)
	os.WriteFile(filepath.Join(root1, "p", "x.bin.continuum-partial-job_01deadjob"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root1, ".DS_Store"), []byte("x"), 0o644)

	rep, err := v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Added) != 2 || len(rep.Removed) != 1 || len(rep.Modified) != 1 || len(rep.Partials) != 1 || rep.PartialsFixed != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if objs, err := v.c.Locate(v.ctx, vol.ID, "/p/new folder/orphan.txt"); err != nil || objs[0].Size != 42 {
		t.Fatalf("orphan not cataloged: %v", err)
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/p/new folder"); err != nil {
		t.Fatal("folder created on disk must become a logical folder")
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/p/gone.bin"); err == nil {
		t.Fatal("externally deleted file must leave the catalog")
	}
	if objs, _ := v.c.Locate(v.ctx, vol.ID, "/p/edit.bin"); objs[0].Size != 2000 {
		t.Fatal("modified size not updated")
	}
	var tombs int
	v.c.DB.QueryRow(v.ctx, `SELECT COUNT(*) FROM tombstones WHERE reason='external_delete'`).Scan(&tombs)
	if tombs != 1 {
		t.Fatalf("tombstones: %d", tombs)
	}

	// second scan is idempotent; fix-partials removes the dead job's partial
	rep, _ = v.e.Scan(v.ctx, vol.Slug, ScanOptions{FixPartials: true})
	if len(rep.Added)+len(rep.Removed)+len(rep.Modified) != 0 || rep.PartialsFixed != 1 {
		t.Fatalf("rescan: %+v", rep)
	}

	// full: same size + mtime but different bytes → corrupt
	p := filepath.Join(root1, "p", "keep.bin")
	if _, err := v.e.Scan(v.ctx, vol.Slug, ScanOptions{Full: true}); err != nil { // hashes everything first
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	b[0] ^= 0xff
	os.WriteFile(p, b, 0o640)
	os.Chtimes(p, fi.ModTime(), fi.ModTime())
	rep, _ = v.e.Scan(v.ctx, vol.Slug, ScanOptions{Full: true})
	if len(rep.Corrupt) != 1 || rep.Corrupt[0].LogicalPath != "/p/keep.bin" {
		t.Fatalf("corrupt: %+v", rep.Corrupt)
	}
	got, _ := v.c.GetVolume(v.ctx, vol.ID)
	if got.LastScanAt == "" {
		t.Fatal("last_scan_at not set")
	}
}

// Offline disk: its files are kept (never tombstoned) — DP09 / PRD §11.6.
func TestScanSkipsOfflineDisk(t *testing.T) {
	v := newEnv(t)
	d1 := v.disk("d1", 1<<30)
	vol := v.volume(0, d1)
	writeFile(t, filepath.Join(d1.DataRoot, "a.txt"), 10)
	v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	os.Rename(d1.DataRoot, d1.DataRoot+".unplugged")
	rep, err := v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 || len(rep.Skipped) != 1 {
		t.Fatalf("offline disk must be skipped: %+v", rep)
	}
}

// FR-K04, A07: same path on two disks → duplicate (identical) or conflict (differs); resolve moves losers to trash.
func TestConflictDetectAndResolve(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 1<<30), v.disk("d2", 1<<30)
	vol := v.volume(0, d1, d2)
	writeFile(t, filepath.Join(d1.DataRoot, "same.bin"), 100)
	b, _ := os.ReadFile(filepath.Join(d1.DataRoot, "same.bin"))
	os.WriteFile(filepath.Join(d2.DataRoot, "same.bin"), b, 0o644)
	writeFile(t, filepath.Join(d1.DataRoot, "diff.txt"), 10)
	writeFile(t, filepath.Join(d2.DataRoot, "diff.txt"), 20)
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(d2.DataRoot, "diff.txt"), later, later)

	rep, err := v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Conflicts != 2 {
		t.Fatalf("conflicts: %d", rep.Conflicts)
	}
	cs, _ := v.e.Conflicts(v.ctx, vol.ID, "open")
	kinds := map[string]string{}
	var diffID string
	for _, c := range cs {
		kinds[c.LogicalPath] = c.Kind
		if c.LogicalPath == "/diff.txt" {
			diffID = c.ID
		}
	}
	if kinds["/same.bin"] != "duplicate" || kinds["/diff.txt"] != "conflict" {
		t.Fatalf("kinds: %v", kinds)
	}
	// rescan does not duplicate rows
	if rep, _ = v.e.Scan(v.ctx, vol.Slug, ScanOptions{}); rep.Conflicts != 2 {
		t.Fatalf("rescan conflicts: %d", rep.Conflicts)
	}
	if n, err := v.e.ResolveDuplicates(v.ctx, vol.Slug); err != nil || n != 1 {
		t.Fatalf("resolve duplicates: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(d2.DataRoot, "same.bin")); !os.IsNotExist(err) {
		t.Fatal("duplicate on d2 must move to trash")
	}
	if _, err := v.e.ResolveConflict(v.ctx, diffID, "newest"); err != nil {
		t.Fatal(err)
	}
	objs, _ := v.c.Locate(v.ctx, vol.ID, "/diff.txt")
	if len(objs) != 1 || objs[0].DiskID != d2.ID || objs[0].Size != 20 {
		t.Fatalf("newest should keep d2: %+v", objs)
	}
	trash, _ := v.e.Trash(v.ctx, vol.Slug)
	if len(trash) != 2 {
		t.Fatalf("trash: %d", len(trash))
	}
	if rep, _ = v.e.Scan(v.ctx, vol.Slug, ScanOptions{}); rep.Conflicts != 0 || len(rep.Added) != 0 {
		t.Fatalf("after resolve: %+v", rep)
	}

	// keep both → renamed "name (disk).ext"
	writeFile(t, filepath.Join(d1.DataRoot, "b.txt"), 5)
	writeFile(t, filepath.Join(d2.DataRoot, "b.txt"), 6)
	v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	cs, _ = v.e.Conflicts(v.ctx, vol.ID, "open")
	if len(cs) != 1 {
		t.Fatalf("open: %d", len(cs))
	}
	if _, err := v.e.ResolveConflict(v.ctx, cs[0].ID, "both"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/b (d2).txt"); err != nil {
		t.Fatal("keep both must rename the second copy")
	}
}

func TestScanOrphanReviewPolicy(t *testing.T) {
	v := newEnv(t)
	d1 := v.disk("d1", 1<<30)
	vol := v.volume(0, d1)
	review := "review"
	if _, err := v.c.UpdateVolume(v.ctx, vol.ID, core.UpdateVolumeRequest{OrphanPolicy: &review}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(d1.DataRoot, "o.txt"), 3)
	rep, _ := v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	objs, err := v.c.Locate(v.ctx, vol.ID, "/o.txt")
	if err != nil || len(rep.Added) != 1 || objs[0].State != "review" {
		t.Fatalf("review policy: %v %+v", err, objs)
	}
}

// Folders removed outside Continuum leave the catalog; logical-only folders (mkdir without ordinal) stay.
func TestScanPrunesVanishedFolders(t *testing.T) {
	v := newEnv(t)
	d1 := v.disk("d1", 1<<30)
	vol := v.volume(0, d1)
	writeFile(t, filepath.Join(d1.DataRoot, "old", "deep", "a.txt"), 3)
	v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	if _, err := v.e.Mkdir(v.ctx, vol.Slug, "/planned/2027", 0); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(d1.DataRoot, "old"))
	rep, _ := v.e.Scan(v.ctx, vol.Slug, ScanOptions{})
	if rep.DirsRemoved != 2 || len(rep.Removed) != 1 {
		t.Fatalf("report: %+v", rep)
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/old"); err == nil {
		t.Fatal("/old must be pruned")
	}
	for _, p := range []string{"/planned", "/planned/2027"} {
		if _, err := v.c.Locate(v.ctx, vol.ID, p); err != nil {
			t.Fatalf("%s (logical-only) must stay", p)
		}
	}
}
