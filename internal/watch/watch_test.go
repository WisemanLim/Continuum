package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/core"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
	"continuum/internal/transfer"
)

// FR-K05: a file added/removed directly on a disk reaches the catalog within seconds (no manual scan).
func TestWatcherReflectsDirectEdits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := store.Open(storetest.URL(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := core.Open(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := transfer.New(c)
	defer e.Shutdown()
	zero := int64(0)
	dir := filepath.Join(root, "disk")
	os.MkdirAll(dir, 0o755)
	res, err := c.Attach(ctx, core.AttachRequest{Path: dir, Name: "w1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	vol, _ := c.CreateVolume(ctx, core.CreateVolumeRequest{Name: "watched", MinFreeBytes: &zero, DiskIDs: []string{res.Disk.ID}})
	w, err := New(c, e)
	if err != nil {
		t.Fatal(err)
	}
	w.Debounce = 300 * time.Millisecond
	go w.Run(ctx)
	time.Sleep(300 * time.Millisecond)

	dr := res.Disk.DataRoot
	os.MkdirAll(filepath.Join(dr, "new", "deep"), 0o755) // new folders get watched too
	time.Sleep(200 * time.Millisecond)
	os.WriteFile(filepath.Join(dr, "new", "deep", "a.txt"), []byte("hello"), 0o644)
	wait := func(cond func() bool, what string) {
		t.Helper()
		for i := 0; i < 60; i++ {
			if cond() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timeout: %s", what)
	}
	wait(func() bool { _, err := c.Locate(ctx, vol.ID, "/new/deep/a.txt"); return err == nil }, "added file cataloged")
	os.Remove(filepath.Join(dr, "new", "deep", "a.txt"))
	wait(func() bool { _, err := c.Locate(ctx, vol.ID, "/new/deep/a.txt"); return err != nil }, "deleted file leaves the catalog")
	wait(func() bool { return w.Count() >= 2 }, "two scans counted") // the counter moves right after the scan commits
	// partial files and the trash do not trigger scans
	before := w.Count()
	os.WriteFile(filepath.Join(dr, "x.continuum-partial-job_1"), []byte("x"), 0o644)
	time.Sleep(800 * time.Millisecond)
	if w.Count() != before {
		t.Fatal("partial files must be ignored")
	}
}
