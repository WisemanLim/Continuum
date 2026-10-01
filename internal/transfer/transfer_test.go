package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
)

const MiB = int64(1) << 20

type fakeStat map[string]disk.Capacity

// stat simulates a disk of fixed total size whose free space shrinks with the bytes stored under it.
func (f fakeStat) stat(p string) (disk.Capacity, error) {
	best, key, bl := disk.Capacity{Total: 1 << 40, Free: 1 << 40, Available: 1 << 40}, "", -1
	for k, c := range f {
		if len(k) > bl && (p == k || len(p) > len(k) && p[:len(k)+1] == k+string(filepath.Separator)) {
			best, key, bl = c, k, len(k)
		}
	}
	if key != "" {
		var used uint64
		filepath.Walk(key, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				used += uint64(fi.Size())
			}
			return nil
		})
		if used > best.Total {
			used = best.Total
		}
		best.Free, best.Available = best.Total-used, best.Total-used
	}
	return best, nil
}

type env struct {
	t     *testing.T
	ctx   context.Context
	c     *core.Core
	e     *Engine
	root  string
	fs    fakeStat
	dbURL string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	url := storetest.URL(t, root)
	db, err := store.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fs := fakeStat{}
	c, err := core.Open(context.Background(), db, fs.stat)
	if err != nil {
		t.Fatal(err)
	}
	e := New(c)
	e.RetryBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(e.Shutdown)
	return &env{t: t, ctx: context.Background(), c: c, e: e, root: root, fs: fs, dbURL: url}
}

func (v *env) disk(name string, total int64) core.Disk {
	v.t.Helper()
	p := filepath.Join(v.root, "disks", name)
	os.MkdirAll(p, 0o755)
	v.fs[p] = disk.Capacity{Total: uint64(total), Free: uint64(total), Available: uint64(total)}
	zero := int64(0)
	res, err := v.c.Attach(v.ctx, core.AttachRequest{Path: p, Name: name, MinFreeBytes: &zero})
	if err != nil {
		v.t.Fatal(err)
	}
	// preflight needed a small reserve for tiny test disks; placement must use the volume default
	d, err := v.c.SetMinFree(v.ctx, res.Disk.ID, nil)
	if err != nil {
		v.t.Fatal(err)
	}
	return d
}

func (v *env) volume(minFree int64, disks ...core.Disk) core.Volume {
	v.t.Helper()
	var ids []string
	for _, d := range disks {
		ids = append(ids, d.ID)
	}
	vol, err := v.c.CreateVolume(v.ctx, core.CreateVolumeRequest{Name: "research-pool", MinFreeBytes: &minFree, DiskIDs: ids})
	if err != nil {
		v.t.Fatal(err)
	}
	return vol
}

func writeFile(t *testing.T, p string, size int64) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	b := make([]byte, size)
	rand.Read(b)
	if err := os.WriteFile(p, b, 0o640); err != nil {
		t.Fatal(err)
	}
}

// dataset: 10 folders × 300 files, total 30 MiB (scaled R05).
func r05Dataset(t *testing.T, root string) string {
	src := filepath.Join(root, "src", "dataset")
	per := 30 * MiB / 3000
	for f := 0; f < 10; f++ {
		for i := 0; i < 300; i++ {
			writeFile(t, filepath.Join(src, fmt.Sprintf("folder%02d", f), fmt.Sprintf("file%04d.bin", i)), per)
		}
	}
	return src
}

func (v *env) wait(id string) *Job {
	v.e.Wait(id)
	j, err := v.e.Get(v.ctx, id)
	if err != nil {
		v.t.Fatal(err)
	}
	return j
}

func countFiles(t *testing.T, root string) (n int, total int64) {
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() && filepath.Base(p) != disk.MarkerName {
			n++
			total += fi.Size()
		}
		return nil
	})
	return
}

// FR-P08 end-to-end (scaled ÷1024): 25/15 MiB disks, 1 MiB reserve, 30 MiB → ≈24/6 MiB physically.
func TestCopyFRP08EndToEnd(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("disk-01", 25*MiB), v.disk("disk-02", 15*MiB)
	v.volume(1*MiB, d1, d2)
	src := r05Dataset(t, v.root)

	yes := true
	req := PlanRequest{Volume: "research-pool", Source: src, Destination: "/research/dataset", Spillover: &yes}
	plan, err := v.e.Plan(v.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Sufficient || plan.Files != 3000 || plan.Dirs != 10 || len(plan.Placements) != 2 {
		t.Fatalf("plan: %+v", plan.Plan)
	}
	j, err := v.e.Start(v.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	j = v.wait(j.ID)
	if j.State != JobDone {
		t.Fatalf("job %s: %s", j.State, j.Error)
	}
	n1, b1 := countFiles(t, d1.DataRoot)
	n2, b2 := countFiles(t, d2.DataRoot)
	t.Logf("disk-01 %.2f MiB (%d files) · disk-02 %.2f MiB (%d files)", float64(b1)/float64(MiB), n1, float64(b2)/float64(MiB), n2)
	if n1+n2 != 3000 || b1 < 23*MiB || b1 > 25*MiB || b2 < 5*MiB || b2 > 7*MiB {
		t.Fatalf("physical placement %d/%d files, %d/%d bytes", n1, n2, b1, b2)
	}
	// catalog: 3000 files, all BLAKE3-verified (NFR-I02)
	var cnt, unverified int
	v.c.DB.QueryRow(v.ctx, `SELECT COUNT(*), SUM(CASE WHEN checksum IS NULL OR verified_at IS NULL THEN 1 ELSE 0 END) FROM objects WHERE object_type='file'`).Scan(&cnt, &unverified)
	if cnt != 3000 || unverified != 0 {
		t.Fatalf("catalog files=%d unverified=%d", cnt, unverified)
	}
	// reservations released
	for _, d := range []core.Disk{d1, d2} {
		dd, _ := v.c.GetDisk(v.ctx, d.ID)
		if dd.ReservedBytes != 0 {
			t.Fatalf("%s reserved %d after job", dd.Name, dd.ReservedBytes)
		}
	}
	// no partial files left (NFR-I03)
	filepath.Walk(v.root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && bytes.Contains([]byte(p), []byte(".continuum-partial-")) {
			t.Errorf("leftover partial %s", p)
		}
		return nil
	})
	// VERIFY re-hash
	rep, err := v.e.Verify(v.ctx, "research-pool", "/", "")
	if err != nil || rep.OK != 3000 || len(rep.Mismatched) != 0 {
		t.Fatalf("verify %+v %v", rep, err)
	}
	// locate: lowest ordinal copy with physical path
	objs, err := v.c.Locate(v.ctx, j.VolumeID, "/research/dataset/folder00/file0000.bin")
	if err != nil || objs[0].Ordinal != 1 {
		t.Fatalf("locate %+v %v", objs, err)
	}
	if bad, _ := v.c.Audit.Verify(v.ctx); bad != 0 {
		t.Fatalf("audit chain broken at %d", bad)
	}
	// idempotency: same key → same job (FR-T22)
	req.IdempotencyKey = "k1"
	a, _ := v.e.Start(v.ctx, req)
	v.wait(a.ID)
	b, _ := v.e.Start(v.ctx, req)
	if a.ID != b.ID {
		t.Fatal("idempotency key must return the same job")
	}
}

// Pause keeps partial + reservation, resume continues from offset and verifies (FR-T07·T08).
func TestPauseResume(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d", 1<<30)
	v.volume(0, d)
	src := filepath.Join(v.root, "src", "big.bin")
	writeFile(t, src, 16*MiB)
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/big.bin", BwLimit: 8 * MiB})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	j, err = v.e.Pause(v.ctx, j.ID)
	if err != nil || j.State != JobPaused {
		t.Fatalf("pause: %v %+v", err, j)
	}
	part := partialName(filepath.Join(d.DataRoot, "big.bin"), j.ID)
	fi, err := os.Stat(part)
	if err != nil || fi.Size() == 0 || fi.Size() >= 16*MiB {
		t.Fatalf("partial should exist mid-way: %v", err)
	}
	if dd, _ := v.c.GetDisk(v.ctx, d.ID); dd.ReservedBytes != 16*MiB {
		t.Fatalf("reservation kept while paused: %d", dd.ReservedBytes)
	}
	// relaunch without bwlimit via resume
	j, err = v.e.Resume(v.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	j = v.wait(j.ID)
	if j.State != JobDone {
		t.Fatalf("resume → %s %s", j.State, j.Error)
	}
	want, _ := hashFile(src)
	got, _ := hashFile(filepath.Join(d.DataRoot, "big.bin"))
	if want != got {
		t.Fatal("resumed file differs from source")
	}
}

// Cancel removes the partial and releases reservation; source untouched.
func TestCancel(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d", 1<<30)
	v.volume(0, d)
	src := filepath.Join(v.root, "src", "big.bin")
	writeFile(t, src, 16*MiB)
	sum, _ := hashFile(src)
	j, _ := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/big.bin", BwLimit: 8 * MiB})
	time.Sleep(400 * time.Millisecond)
	j, err := v.e.Cancel(v.ctx, j.ID)
	if err != nil || j.State != JobCancelled {
		t.Fatalf("cancel: %v %+v", err, j)
	}
	if _, err := os.Stat(partialName(filepath.Join(d.DataRoot, "big.bin"), j.ID)); !os.IsNotExist(err) {
		t.Fatal("partial must be removed on cancel")
	}
	if dd, _ := v.c.GetDisk(v.ctx, d.ID); dd.ReservedBytes != 0 {
		t.Fatalf("reservation not released: %d", dd.ReservedBytes)
	}
	if s, _ := hashFile(src); s != sum {
		t.Fatal("source modified")
	}
}

// Restart recovery: an interrupted RUNNING job resumes on a new engine (NFR-A01).
func TestCrashRecovery(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d", 1<<30)
	v.volume(0, d)
	src := filepath.Join(v.root, "src", "big.bin")
	writeFile(t, src, 12*MiB)
	j, _ := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/big.bin", BwLimit: 8 * MiB})
	time.Sleep(400 * time.Millisecond)
	v.e.Shutdown() // simulate process stop
	v.c.DB.Exec(v.ctx, `UPDATE jobs SET state='RUNNING' WHERE job_id=?`, j.ID)
	v.c.DB.Exec(v.ctx, `UPDATE disks SET reserved_bytes=0`) // lost in crash → must be recomputed
	e2 := New(v.c)
	if err := e2.Recover(v.ctx); err != nil {
		t.Fatal(err)
	}
	e2.Wait(j.ID)
	j2, _ := e2.Get(v.ctx, j.ID)
	if j2.State != JobDone {
		t.Fatalf("recovered job %s %s", j2.State, j2.Error)
	}
	a, _ := hashFile(src)
	b, _ := hashFile(filepath.Join(d.DataRoot, "big.bin"))
	if a != b {
		t.Fatal("recovered copy differs")
	}
}

// Fault injection: target disk vanishes mid-copy → job PAUSED, source intact, resume after replug (NFR-I01, FR-D20).
func TestDiskRemovedPausesJob(t *testing.T) {
	v := newEnv(t)
	d := v.disk("usb", 1<<30)
	v.volume(0, d)
	src := filepath.Join(v.root, "src", "set")
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("f%d", i)), 2*MiB)
	}
	sums := map[string]string{}
	for i := 0; i < 4; i++ {
		sums[fmt.Sprint(i)], _ = hashFile(filepath.Join(src, fmt.Sprintf("f%d", i)))
	}
	diskDir := filepath.Dir(d.DataRoot)
	moved := diskDir + ".unplugged"
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/set", BwLimit: 4 * MiB, Transfers: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	os.Rename(diskDir, moved) // unplug mid-copy
	j = v.wait(j.ID)
	if j.State != JobPaused || j.Error == "" {
		t.Fatalf("want PAUSED (disk offline), got %s %q", j.State, j.Error)
	}
	for i := 0; i < 4; i++ { // source intact
		if s, _ := hashFile(filepath.Join(src, fmt.Sprintf("f%d", i))); s != sums[fmt.Sprint(i)] {
			t.Fatal("source modified")
		}
	}
	// a new copy to the offline disk is refused up front with code offline (exit 6)
	if _, err := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/other"}); core.CodeOf(err) != core.CodeOffline {
		t.Fatalf("want offline, got %v", err)
	}
	os.Rename(moved, diskDir) // replug (same marker)
	v.c.RefreshCapacity(v.ctx, d.ID)
	j, _ = v.e.Resume(v.ctx, j.ID)
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("after replug %s %s", j.State, j.Error)
	}
	for i := 0; i < 4; i++ {
		if s, _ := hashFile(filepath.Join(d.DataRoot, "set", fmt.Sprintf("f%d", i))); s != sums[fmt.Sprint(i)] {
			t.Fatalf("f%d differs after resume", i)
		}
	}
}

// PAUSED_NEED_SPACE → attach 3rd disk → resume places the rest (FR-P09·P11).
func TestNeedSpaceThenAddDisk(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 3*MiB), v.disk("d2", 3*MiB)
	vol := v.volume(0, d1, d2)
	src := filepath.Join(v.root, "src", "set")
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("f%d.bin", i)), MiB)
	}
	req := PlanRequest{Volume: "research-pool", Source: src, Destination: "/set"}
	if _, err := v.e.Start(v.ctx, req); core.CodeOf(err) != core.CodeInsufficientSpace {
		t.Fatalf("default on_insufficient=fail must refuse: %v", err)
	}
	req.OnInsufficient = "wait"
	j, err := v.e.Start(v.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if j = v.wait(j.ID); j.State != JobPausedNeedSpace {
		t.Fatalf("want PAUSED_NEED_SPACE, got %s", j.State)
	}
	d3 := v.disk("d3", 10*MiB)
	if err := v.c.AddDiskToVolume(v.ctx, vol.ID, d3.ID, 0); err != nil {
		t.Fatal(err)
	}
	j, _ = v.e.Resume(v.ctx, j.ID)
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("after adding disk: %s %s", j.State, j.Error)
	}
	if n, _ := countFiles(t, filepath.Join(filepath.Dir(d3.DataRoot), disk.DataRootName)); n == 0 {
		t.Fatal("3rd disk should receive the overflow")
	}
}

func TestConflictPolicies(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d", 1<<30)
	vol := v.volume(0, d)
	src := filepath.Join(v.root, "src", "a.txt")
	writeFile(t, src, 100)
	run := func(policy string) *Job {
		j, err := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/a.txt", OnConflict: policy})
		if err != nil {
			t.Fatal(err)
		}
		return v.wait(j.ID)
	}
	run("skip")
	j := run("skip")
	if j.Progress.FilesSkip != 1 {
		t.Fatalf("skip: %+v", j.Progress)
	}
	run("rename")
	if _, err := v.c.Locate(v.ctx, vol.ID, "/a (2).txt"); err != nil {
		t.Fatal("rename policy should create 'a (2).txt'")
	}
	writeFile(t, src, 200) // new content, newer mtime
	os.Chtimes(src, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	run("newer")
	objs, _ := v.c.Locate(v.ctx, vol.ID, "/a.txt")
	if objs[0].Size != 200 {
		t.Fatalf("newer should replace: size %d", objs[0].Size)
	}
	os.Chtimes(src, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if j := run("newer"); j.Progress.FilesSkip != 1 {
		t.Fatal("older source must be skipped")
	}
}

func TestMoveRenameMkdirDeleteTrash(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 1<<30), v.disk("d2", 1<<30)
	vol := v.volume(0, d1, d2)
	src := filepath.Join(v.root, "src", "batch7")
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("f%d", i)), 1000)
	}
	j, _ := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/import/batch7"})
	v.wait(j.ID)

	// same-disk move = rename
	j, err := v.e.Start(v.ctx, PlanRequest{Type: TypeMove, Volume: "research-pool", Source: "/import/batch7", Destination: "/archive/batch7"})
	if err != nil {
		t.Fatal(err)
	}
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("move %s %s", j.State, j.Error)
	}
	if _, err := os.Stat(filepath.Join(d1.DataRoot, "archive", "batch7", "f0")); err != nil {
		t.Fatal("file should be renamed on d1")
	}
	if objs, _ := v.c.Walk(v.ctx, vol.ID, "/import/batch7"); len(objs) != 0 {
		t.Fatalf("old logical path should be gone, %d left", len(objs))
	}
	// cross-disk move with forced ordinal 2 (copy → verify → delete)
	j, _ = v.e.Start(v.ctx, PlanRequest{Type: TypeMove, Volume: "research-pool", Source: "/archive/batch7", Destination: "/cold/batch7", ForcedOrdinal: 2})
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("cross move %s %s", j.State, j.Error)
	}
	if n, _ := countFiles(t, filepath.Join(d2.DataRoot, "cold")); n != 3 {
		t.Fatal("files should be on d2")
	}
	if _, err := os.Stat(filepath.Join(d1.DataRoot, "archive", "batch7", "f0")); !os.IsNotExist(err) {
		t.Fatal("source copy must be deleted after verified move")
	}
	// rename folder
	if err := v.e.Rename(v.ctx, "research-pool", "/cold/batch7", "/cold/batch-07"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/cold/batch-07/f1"); err != nil {
		t.Fatal(err)
	}
	// mkdir lazy vs ordinal
	if _, err := v.e.Mkdir(v.ctx, "research-pool", "/projects/2026", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d1.DataRoot, "projects")); !os.IsNotExist(err) {
		t.Fatal("lazy mkdir must not create a physical dir")
	}
	if _, err := v.e.Mkdir(v.ctx, "research-pool", "/projects/2027", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d2.DataRoot, "projects", "2027")); err != nil {
		t.Fatal("mkdir --ordinal 2 must create physically on d2")
	}
	// delete → trash → restore → delete → purge
	if _, err := v.e.Delete(v.ctx, "research-pool", "/cold", false, false); core.CodeOf(err) != core.CodeInvalid {
		t.Fatal("non-recursive delete of non-empty dir must fail")
	}
	res, err := v.e.Delete(v.ctx, "research-pool", "/cold/batch-07/f1", false, false)
	if err != nil || res.Files != 1 {
		t.Fatal(err)
	}
	tr, _ := v.e.Trash(v.ctx, "research-pool")
	if len(tr) != 1 {
		t.Fatalf("trash %d", len(tr))
	}
	if _, err := v.e.Restore(v.ctx, tr[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := v.c.Locate(v.ctx, vol.ID, "/cold/batch-07/f1"); err != nil {
		t.Fatal("restored file missing from catalog")
	}
	if _, err := v.e.Delete(v.ctx, "research-pool", "/cold", true, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := v.e.Purge(v.ctx, "research-pool", time.Nanosecond); n != 3 {
		t.Fatalf("purge %d", n)
	}
	if n, _ := countFiles(t, d2.DataRoot); n != 0 {
		t.Fatalf("d2 should be empty after purge, %d files", n)
	}
	// path traversal refused (FR-S04)
	if _, err := v.e.Mkdir(v.ctx, "research-pool", "/../../etc", 1); core.CodeOf(err) != core.CodeInvalid {
		t.Fatalf("traversal must be refused: %v", err)
	}
}

func TestVerifyDetectsTamper(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d", 1<<30)
	v.volume(0, d)
	src := filepath.Join(v.root, "src", "x.bin")
	writeFile(t, src, 4096)
	j, _ := v.e.Start(v.ctx, PlanRequest{Volume: "research-pool", Source: src, Destination: "/x.bin"})
	v.wait(j.ID)
	os.WriteFile(filepath.Join(d.DataRoot, "x.bin"), []byte("bit rot"), 0o644)
	rep, err := v.e.Verify(v.ctx, "research-pool", "/", "")
	if err != nil || len(rep.Mismatched) != 1 {
		t.Fatalf("tamper must be detected: %+v %v", rep, err)
	}
}
