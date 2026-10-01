// Package watch reflects direct edits on local disks in near real time (FR-K05): fsnotify watches every
// folder under each ONLINE local data root (inotify / FSEvents-kqueue / ReadDirectoryChangesW); a burst of
// events marks the disk dirty and, after a quiet period, runs an incremental SCAN of that disk only.
// Network filesystems and remote disks keep the periodic scan (polling).
package watch

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"continuum/internal/core"
	"continuum/internal/transfer"
)

// Watcher keeps one fsnotify watcher for all local data roots.
type Watcher struct {
	C        *core.Core
	E        *transfer.Engine
	Debounce time.Duration // quiet period before the scan (default 3 s)

	w      *fsnotify.Watcher
	mu     sync.Mutex
	roots  map[string]string    // data root → disk id
	dirty  map[string]time.Time // disk id → last event
	Scans  int                  // scans triggered (tests, metrics)
	closed bool
}

// New creates a watcher; call Run.
func New(c *core.Core, e *transfer.Engine) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{C: c, E: e, Debounce: 3 * time.Second, w: fw, roots: map[string]string{}, dirty: map[string]time.Time{}}, nil
}

func ignored(p string) bool {
	base := filepath.Base(p)
	return strings.Contains(p, string(filepath.Separator)+".trash") || strings.Contains(base, ".continuum-partial-") ||
		strings.HasPrefix(base, ".probe-") || base == ".DS_Store" || strings.HasPrefix(base, "._")
}

func (w *Watcher) addTree(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && ignored(p) {
				return filepath.SkipDir
			}
			w.w.Add(p)
		}
		return nil
	})
}

// Sync (re)aligns watches with the registry: local ONLINE disks in a volume, not network-backed.
func (w *Watcher) Sync(ctx context.Context) {
	ds, err := w.C.ListDisks(ctx, core.DiskFilter{})
	if err != nil {
		return
	}
	want := map[string]string{}
	for _, d := range ds {
		if d.VolumeID == "" || d.State != core.StateOnline || d.NetworkBacked || w.C.IsRemote(d) {
			continue
		}
		want[filepath.Clean(d.DataRoot)] = d.ID
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for root := range w.roots {
		if _, ok := want[root]; !ok {
			for _, p := range w.w.WatchList() {
				if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
					w.w.Remove(p)
				}
			}
			delete(w.roots, root)
		}
	}
	for root, id := range want {
		if _, ok := w.roots[root]; !ok {
			w.roots[root] = id
			w.addTree(root)
		}
	}
}

func (w *Watcher) diskOf(p string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	for root, id := range w.roots {
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
			return id
		}
	}
	return ""
}

// Run processes events until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	defer w.w.Close()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	sync := time.NewTicker(5 * time.Second) // pick up newly attached disks quickly
	defer sync.Stop()
	w.Sync(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			if ignored(ev.Name) || filepath.Base(ev.Name) == "volume.json" {
				continue
			}
			id := w.diskOf(ev.Name)
			if id == "" {
				continue
			}
			if ev.Has(fsnotify.Create) { // new folder: watch it too (fsnotify is not recursive)
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					w.addTree(ev.Name)
				}
			}
			w.mu.Lock()
			w.dirty[id] = time.Now()
			w.mu.Unlock()
		case err, ok := <-w.w.Errors:
			if !ok {
				return
			}
			log.Printf("watch: %v", err)
		case <-sync.C:
			w.Sync(ctx)
		case <-t.C:
			w.flush(ctx)
		}
	}
}

// flush scans disks that have been quiet for Debounce (skips disks a running job is writing to:
// the job commits its own files and the scan would only race with it).
func (w *Watcher) flush(ctx context.Context) {
	w.mu.Lock()
	var due []string
	for id, at := range w.dirty {
		if time.Since(at) >= w.Debounce {
			due = append(due, id)
		}
	}
	w.mu.Unlock()
	for _, id := range due {
		d, err := w.C.GetDisk(ctx, id)
		if err != nil || d.VolumeID == "" {
			w.clear(id)
			continue
		}
		if w.busy(ctx, id) {
			continue // retry after the job
		}
		w.clear(id)
		if _, err := w.E.Scan(ctx, d.VolumeID, transfer.ScanOptions{Disk: id}); err != nil {
			log.Printf("watch: scan %s: %v", d.Name, err)
		}
		w.mu.Lock()
		w.Scans++
		w.mu.Unlock()
	}
}

func (w *Watcher) clear(id string) {
	w.mu.Lock()
	delete(w.dirty, id)
	w.mu.Unlock()
}

func (w *Watcher) busy(ctx context.Context, diskID string) bool {
	var n int
	w.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM job_items i JOIN jobs j ON j.job_id=i.job_id
		WHERE i.disk_id=? AND j.state='RUNNING' AND i.state IN ('pending','running')`, diskID).Scan(&n)
	return n > 0
}

// Count returns the number of scans triggered so far.
func (w *Watcher) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Scans
}
