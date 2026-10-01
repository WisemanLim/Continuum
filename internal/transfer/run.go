package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/nodefs"
	"continuum/internal/store"
	"continuum/internal/tracing"
)

var errDiskOffline = errors.New("target disk offline")

// launch starts the job worker if not already running.
func (e *Engine) launch(id string) {
	e.mu.Lock()
	if e.running[id] != nil {
		e.mu.Unlock()
		return
	}
	if !e.claim(context.Background(), id) {
		e.mu.Unlock()
		log.Printf("transfer: job %s is held by another controller worker — not starting it here", id)
		return
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	rc := &runCtl{cancel: cancel, prog: &Progress{JobID: id, State: JobQueued, Placements: map[string]*DiskProgress{}}, done: make(chan struct{}), started: time.Now()}
	e.running[id] = rc
	e.mu.Unlock()
	// a resumed/retried/recovered DRAIN keeps its disk closed to new writes
	e.C.DB.Exec(context.Background(), `UPDATE disks SET state=? WHERE state=? AND disk_id=(SELECT source FROM jobs WHERE job_id=? AND job_type=?)`,
		core.StateDraining, core.StateOnline, id, TypeDrain)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer close(rc.done)
		e.run(ctx, id, rc)
		e.unclaim(context.Background(), id)
		e.mu.Lock()
		delete(e.running, id)
		e.mu.Unlock()
	}()
}

type workItem struct {
	JobItem
	meta itemMeta
}

func (e *Engine) run(ctx context.Context, id string, rc *runCtl) {
	bg := context.Background()
	j, err := e.Get(bg, id)
	if err != nil {
		log.Printf("transfer: %s: %v", id, err)
		return
	}
	var req PlanRequest
	_ = json.Unmarshal(j.Params, &req)
	_, span := tracing.Start(bg, "job "+j.Type, tracing.KindInternal) // one trace per run (OTLP when configured)
	span.Set("continuum.job.id", id)
	span.Set("continuum.job.type", j.Type)
	span.Set("continuum.volume.id", j.VolumeID)
	defer span.End()
	now := store.Now()
	e.C.DB.Exec(bg, `UPDATE jobs SET state=?, started_at=COALESCE(started_at, ?), error=NULL WHERE job_id=?`, JobRunning, now, id)
	e.C.Audit.Emit(bg, audit.Event{Kind: "job.started", VolumeID: j.VolumeID, JobID: id}, nil)

	all, _ := e.Items(bg, id, "")
	v, _ := e.C.GetVolume(bg, j.VolumeID)
	disks := map[string]core.Disk{}
	for _, d := range v.Disks {
		disks[d.ID] = d
	}
	// totals are computed locally, then published under rc.mu (snapshot() reads concurrently from Get/SSE)
	var total, done, skipped, bytesTotal, bytesDone int64
	placed := map[string]*DiskProgress{}
	var todo []workItem
	for _, it := range all {
		if it.State == itemCancelled {
			continue
		}
		total++
		bytesTotal += it.Size
		switch it.State {
		case itemDone:
			done++
			bytesDone += it.Size
			dp := placed[it.DiskID]
			if dp == nil {
				dp = &DiskProgress{Name: disks[it.DiskID].Name}
				placed[it.DiskID] = dp
			}
			dp.Files++
			dp.Bytes += it.Size
		case itemSkipped:
			skipped++
			bytesDone += it.Size
		case itemPending:
			var m itemMeta
			var s string
			e.C.DB.QueryRow(bg, `SELECT COALESCE(checksum,'') FROM job_items WHERE job_id=? AND item_no=?`, id, it.No).Scan(&s)
			json.Unmarshal([]byte(s), &m)
			todo = append(todo, workItem{JobItem: it, meta: m})
		}
	}
	rc.mu.Lock()
	rc.started = time.Now()
	rc.prog.State = JobRunning
	rc.prog.FilesTotal, rc.prog.FilesDone, rc.prog.FilesSkip, rc.prog.BytesTotal = total, done, skipped, bytesTotal
	rc.prog.Placements = placed
	rc.mu.Unlock()
	rc.bytes.Store(bytesDone)

	// periodic persistence + publish (1 s)
	stopTick := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		lastRenew := time.Now()
		for {
			select {
			case <-t.C:
				snap := rc.snapshot()
				e.saveProgress(bg, id, snap)
				e.publish(*snap)
				if req.Window != "" && !inWindow(req.Window, e.Now()) {
					rc.cancel(errWindow) // the scheduler resumes it when the window opens
				}
				if time.Since(lastRenew) > e.LeaseTTL/3 { // keep the lease while the job runs
					e.claim(bg, id)
					lastRenew = time.Now()
				}
			case <-stopTick:
				return
			}
		}
	}()

	workers := e.Workers
	if req.Transfers > 0 {
		workers = req.Transfers
	}
	sems := map[string]chan struct{}{}
	for d := range disks {
		sems[d] = make(chan struct{}, max(e.PerDisk, 1))
	}
	queue := make(chan workItem)
	var wg sync.WaitGroup
	var pauseReason string
	var reasonMu sync.Mutex
	setReason := func(r string) {
		reasonMu.Lock()
		if pauseReason == "" {
			pauseReason = r
		}
		reasonMu.Unlock()
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range queue {
				if context.Cause(ctx) != nil {
					continue
				}
				sem := sems[it.DiskID]
				if sem == nil {
					sem = make(chan struct{}, 1)
				}
				sem <- struct{}{}
				e.processItem(ctx, j, req, disks, it, rc, setReason)
				<-sem
			}
		}()
	}
	for _, it := range todo {
		if context.Cause(ctx) != nil {
			break
		}
		queue <- it
	}
	close(queue)
	wg.Wait()
	close(stopTick)

	// final state
	cause := context.Cause(ctx)
	final, msg := JobDone, ""
	switch {
	case errors.Is(cause, errWindow):
		final, msg = JobPaused, windowMsg(req.Window)
		e.C.DB.Exec(bg, `UPDATE job_items SET state=? WHERE job_id=? AND state=?`, itemPending, id, itemRunning)
	case errors.Is(cause, errPaused):
		final, msg = JobPaused, "paused"
		e.C.DB.Exec(bg, `UPDATE job_items SET state=? WHERE job_id=? AND state=?`, itemPending, id, itemRunning)
	case errors.Is(cause, errCancelled):
		e.cleanupPartials(bg, id)
		final, msg = JobCancelled, "cancelled by user"
	case pauseReason != "":
		final, msg = JobPaused, pauseReason
		e.C.DB.Exec(bg, `UPDATE job_items SET state=? WHERE job_id=? AND state=?`, itemPending, id, itemRunning)
	default:
		var unplaced, failed int
		e.C.DB.QueryRow(bg, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND state=?`, id, itemUnplaced).Scan(&unplaced)
		e.C.DB.QueryRow(bg, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND state=?`, id, itemFailed).Scan(&failed)
		switch {
		case unplaced > 0:
			final, msg = JobPausedNeedSpace, fmt.Sprintf("%d file(s) need space — attach a disk and resume", unplaced)
		case failed > 0:
			final, msg = JobFailed, fmt.Sprintf("%d file(s) failed", failed)
		}
		if moveLike(j.Type) {
			e.pruneMovedDirs(bg, j)
		}
	}
	rc.mu.Lock()
	rc.prog.State = final
	rc.prog.Reason = msg
	rc.mu.Unlock()
	snap := rc.snapshot()
	span.Set("continuum.job.state", final)
	span.Set("continuum.job.files_done", snap.FilesDone)
	span.Set("continuum.job.bytes_done", snap.BytesDone)
	if final == JobFailed {
		span.Fail(msg)
	}
	e.saveProgress(bg, id, snap)
	e.finish(bg, id, final, msg)
	e.publish(*snap)
}

func (rc *runCtl) addPlacement(disk, name string, size int64) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	dp := rc.prog.Placements[disk]
	if dp == nil {
		dp = &DiskProgress{Name: name}
		rc.prog.Placements[disk] = dp
	}
	dp.Files++
	dp.Bytes += size
}

func (e *Engine) saveProgress(ctx context.Context, id string, p *Progress) {
	b, _ := json.Marshal(p)
	e.C.DB.Exec(ctx, `UPDATE jobs SET progress_json=? WHERE job_id=?`, string(b), id)
}

// finish writes the job state; terminal states release leftover reservations.
func (e *Engine) finish(ctx context.Context, id, state, msg string) {
	terminal := state == JobDone || state == JobFailed || state == JobCancelled
	e.C.DB.Tx(ctx, func(tx *store.Tx) error {
		if terminal { // leftover reservations are released by recomputeReservations below
			tx.Exec(ctx, `UPDATE job_items SET state=? WHERE job_id=? AND state IN ('pending','running')`, itemCancelled, id)
		}
		var fin any
		if terminal {
			fin = store.Now()
		}
		ecount := 0
		tx.QueryRow(ctx, `SELECT COUNT(*) FROM job_items WHERE job_id=? AND state=?`, id, itemFailed).Scan(&ecount)
		_, err := tx.Exec(ctx, `UPDATE jobs SET state=?, error=?, errors_count=?, finished_at=? WHERE job_id=?`, state, nullIf(msg), ecount, fin, id)
		return err
	})
	if terminal {
		e.recomputeReservations(ctx)
		e.afterRelocate(ctx, id, state)
	}
	var vol string
	e.C.DB.QueryRow(ctx, `SELECT COALESCE(volume_id,'') FROM jobs WHERE job_id=?`, id).Scan(&vol)
	e.C.Audit.Emit(ctx, audit.Event{Kind: "job." + strings.ToLower(state), VolumeID: vol, JobID: id}, map[string]any{"message": msg})
	e.C.Audit.Append(ctx, audit.Entry{Action: "job.finish", Target: id, VolumeID: vol, Result: state})
}

// cleanupPartials removes partial files of unfinished items.
func (e *Engine) cleanupPartials(ctx context.Context, id string) {
	items, _ := e.Items(ctx, id, "")
	for _, it := range items {
		if it.State == itemDone || it.State == itemSkipped || it.DiskID == "" {
			continue
		}
		d, err := e.C.GetDisk(ctx, it.DiskID)
		if err != nil {
			continue
		}
		if final, err := core.PhysicalPath(d.DataRoot, it.LogicalPath); err == nil {
			e.fsOf(d).Remove(ctx, partialName(final, id))
			e.fsOf(d).Remove(ctx, partialName(final, id)+".w") // resumable direct-transfer partial
		}
	}
}

func (e *Engine) setItem(ctx context.Context, jobID string, no int, state, errMsg, checksum string) {
	if checksum != "" {
		e.C.DB.Exec(ctx, `UPDATE job_items SET state=?, error=?, checksum=? WHERE job_id=? AND item_no=?`, state, nullIf(errMsg), checksum, jobID, no)
		return
	}
	e.C.DB.Exec(ctx, `UPDATE job_items SET state=?, error=? WHERE job_id=? AND item_no=?`, state, nullIf(errMsg), jobID, no)
}

func (e *Engine) release(ctx context.Context, disk string, size int64) {
	if disk == "" || size == 0 {
		return
	}
	e.C.DB.Exec(ctx, `UPDATE disks SET reserved_bytes = CASE WHEN reserved_bytes > ? THEN reserved_bytes - ? ELSE 0 END WHERE disk_id=?`, size, size, disk)
}

// processItem runs one item with automatic retries (FR-T08: 3 retries, backoff 1·4·16 s).
func (e *Engine) processItem(ctx context.Context, j *Job, req PlanRequest, disks map[string]core.Disk, it workItem, rc *runCtl, pause func(string)) {
	bg := context.Background()
	e.setItem(bg, j.ID, it.No, itemRunning, "", "")
	rc.mu.Lock()
	rc.prog.Current = it.LogicalPath
	rc.mu.Unlock()
	sameDiskMove := moveLike(j.Type) && it.meta.SrcDisk == it.DiskID

	var written int64
	prog := func(delta, offset int64) {
		written += delta
		rc.bytes.Add(delta)
		if offset-it.Offset >= 8<<20 { // persist resume offset every 8 MiB (FR-T07)
			it.Offset = offset
			e.C.DB.Exec(bg, `UPDATE job_items SET offset_bytes=? WHERE job_id=? AND item_no=?`, offset, j.ID, it.No)
		}
	}
	var lastErr error
	for attempt := 0; attempt <= len(e.RetryBackoff); attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(e.RetryBackoff[attempt-1]):
			case <-ctx.Done():
			}
		}
		if context.Cause(ctx) != nil {
			rc.bytes.Add(-written)
			return // paused/cancelled: item stays running → reset by run()
		}
		res, skipped, err := e.execItem(ctx, j, req, disks, it, prog)
		if err == nil {
			rc.mu.Lock()
			if skipped {
				rc.prog.FilesSkip++
			} else {
				rc.prog.FilesDone++
			}
			rc.mu.Unlock()
			rc.bytes.Add(it.Size - written)
			if skipped {
				e.setItem(bg, j.ID, it.No, itemSkipped, res, "")
			} else {
				rc.addPlacement(it.DiskID, disks[it.DiskID].Name, it.Size)
				e.setItem(bg, j.ID, it.No, itemDone, "", res)
			}
			if !sameDiskMove {
				e.release(bg, it.DiskID, it.Size)
			}
			return
		}
		rc.bytes.Add(-written)
		written = 0
		lastErr = err
		if d, ok := disks[it.DiskID]; ok && !nodefs.Exists(bg, e.fsOf(d), d.DataRoot) && !errors.Is(err, errPaused) && !errors.Is(err, errCancelled) {
			err = errDiskOffline // unplugged mid-copy (FR-D20)
			e.C.RefreshCapacity(bg, d.ID)
		}
		switch {
		case errors.Is(err, errPaused), errors.Is(err, errCancelled):
			return
		case errors.Is(err, errDiskOffline):
			pause(fmt.Sprintf("disk %s offline — reconnect and resume", disks[it.DiskID].Name))
			return
		case errors.Is(err, errNoSpace):
			// FR-P11: give the item back to the planner; job ends PAUSED_NEED_SPACE unless re-placed on resume
			e.C.DB.Exec(bg, `UPDATE job_items SET state=?, disk_id=NULL, offset_bytes=0 WHERE job_id=? AND item_no=?`, itemUnplaced, j.ID, it.No)
			e.release(bg, it.DiskID, it.Size)
			return
		case errors.Is(err, ErrVerify), errors.Is(err, os.ErrNotExist) && attempt < len(e.RetryBackoff):
			continue
		}
		if core.CodeOf(err) != "" {
			break // domain errors are not retried
		}
	}
	rc.mu.Lock()
	rc.prog.FilesFail++
	rc.mu.Unlock()
	e.setItem(bg, j.ID, it.No, itemFailed, lastErr.Error(), "")
	if !sameDiskMove {
		e.release(bg, it.DiskID, it.Size)
	}
	e.C.Audit.Append(bg, audit.Entry{Action: strings.ToLower(j.Type), Target: it.LogicalPath, VolumeID: j.VolumeID, Source: it.Source, Result: "failed: " + lastErr.Error()})
}

// execItem performs one COPY/MOVE item. Returns (checksum|skip-reason, skipped, error).
func (e *Engine) execItem(ctx context.Context, j *Job, req PlanRequest, disks map[string]core.Disk, it workItem, prog progressFn) (string, bool, error) {
	bg := context.Background()
	d, ok := disks[it.DiskID]
	if !ok {
		var err error
		if d, err = e.C.GetDisk(bg, it.DiskID); err != nil {
			return "", false, err
		}
	}
	dfs := e.fsOf(d)
	if !nodefs.Exists(bg, dfs, d.DataRoot) {
		e.C.RefreshCapacity(bg, d.ID)
		return "", false, errDiskOffline
	}
	logical := it.LogicalPath

	// conflict handling (FR-T10)
	existing, _ := e.C.Locate(bg, j.VolumeID, logical)
	var others []core.Object
	for _, o := range existing {
		if o.Type == "file" && o.ID != it.meta.ObjID {
			others = append(others, o)
		}
	}
	final, err := core.PhysicalPath(d.DataRoot, logical)
	if err != nil {
		return "", false, err
	}
	_, statErr := dfs.Stat(bg, final)
	orphan := statErr == nil && len(others) == 0 && !(moveLike(j.Type) && it.Source == final)
	if len(others) > 0 || orphan {
		switch req.OnConflict {
		case "skip":
			return "exists", true, nil
		case "newer":
			var newest int64
			for _, o := range others {
				if o.MtimeNs > newest {
					newest = o.MtimeNs
				}
			}
			if orphan {
				if fi, err := os.Stat(final); err == nil {
					newest = fi.ModTime().UnixNano()
				}
			}
			if it.meta.MtimeNs <= newest {
				return "not newer", true, nil
			}
		case "rename":
			logical = e.freeName(bg, j.VolumeID, dfs, d.DataRoot, logical)
			if final, err = core.PhysicalPath(d.DataRoot, logical); err != nil {
				return "", false, err
			}
			others = nil
			e.C.DB.Exec(bg, `UPDATE job_items SET logical_path=? WHERE job_id=? AND item_no=?`, logical, j.ID, it.No)
		}
	}

	parent, name := path.Dir(logical), path.Base(logical)
	obj := core.Object{VolumeID: j.VolumeID, LogicalPath: logical, ParentPath: parent, Name: name, DiskID: d.ID,
		PhysicalRelPath: strings.TrimPrefix(logical, "/"), Size: it.Size, MtimeNs: it.meta.MtimeNs, Mode: int64(it.meta.Mode)}
	verify := req.Verify != "none"

	switch {
	case it.meta.Link != "":
		dfs.MkdirAll(bg, filepath.Dir(final))
		dfs.Remove(bg, final)
		if err := dfs.Symlink(bg, it.meta.Link, final); err != nil {
			return "", false, err
		}
	case moveLike(j.Type) && it.meta.SrcDisk == d.ID:
		// same-disk move = rename, no data rewrite (PRD §11.3)
		if err := dfs.MkdirAll(bg, filepath.Dir(final)); err != nil {
			return "", false, err
		}
		if err := dfs.Rename(bg, it.Source, final); err != nil {
			return "", false, err
		}
		if dfs.Local() {
			syncDir(filepath.Dir(final))
		}
		if err := e.C.EnsureDirs(bg, j.VolumeID, parent); err != nil {
			return "", false, err
		}
		if _, err := e.C.DB.Exec(bg, `UPDATE objects SET logical_path=?, parent_path=?, name=?, physical_rel_path=?, updated_at=? WHERE object_id=?`,
			logical, parent, name, obj.PhysicalRelPath, store.Now(), it.meta.ObjID); err != nil {
			return "", false, err
		}
		prog(it.Size, it.Size)
		nodefs.RemoveEmptyParents(bg, dfs, filepath.Dir(it.Source), d.DataRoot)
		e.C.Audit.Append(bg, audit.Entry{Action: "move", Target: logical, VolumeID: j.VolumeID, Source: it.Source, Destination: final, Result: "ok"})
		e.dropOthers(bg, others, d.ID, final)
		return "renamed", false, nil
	default:
		res, err := e.copyFile(ctx, e.srcFS(bg, j, it), it.Source, dfs, final, j.ID, verify, req.BwLimit, os.FileMode(it.meta.Mode), time.Unix(0, it.meta.MtimeNs), prog)
		if err != nil {
			return "", false, err
		}
		obj.Checksum = res.Checksum
		if verify {
			obj.ChecksumAlgo = "blake3"
			obj.VerifiedAt = store.Now()
		}
	}
	if err := e.C.UpsertFile(bg, obj); err != nil { // step 12: catalog commit
		return "", false, err
	}
	if moveLike(j.Type) { // cross-disk move: copy → verify → delete source (3 steps)
		if sd, err := e.C.GetDisk(bg, it.meta.SrcDisk); err == nil {
			sfs := e.fsOf(sd)
			sfs.Remove(bg, it.Source)
			nodefs.RemoveEmptyParents(bg, sfs, filepath.Dir(it.Source), sd.DataRoot)
		}
		e.C.DeleteObject(bg, it.meta.ObjID)
	}
	e.dropOthers(bg, others, d.ID, final)
	e.C.Audit.Append(bg, audit.Entry{Action: strings.ToLower(j.Type), Target: logical, VolumeID: j.VolumeID, Source: it.Source, Destination: final,
		Checksum: obj.Checksum, Result: "ok"}) // step 14
	return obj.Checksum, false, nil
}

// dropOthers removes replaced copies (on_conflict=replace) except the new file.
func (e *Engine) dropOthers(ctx context.Context, others []core.Object, keepDisk, keepPath string) {
	for _, o := range others {
		if o.DiskID == keepDisk {
			continue // same disk: already overwritten by rename; row updated by upsert
		}
		if o.PhysicalPath != "" && o.PhysicalPath != keepPath {
			if od, err := e.C.GetDisk(ctx, o.DiskID); err == nil {
				e.fsOf(od).Remove(ctx, o.PhysicalPath)
			}
		}
		e.C.DeleteObject(ctx, o.ID)
	}
}

// freeName returns "name (2).ext"… not present in catalog nor on disk (keep both).
func (e *Engine) freeName(ctx context.Context, volumeID string, fsys nodefs.FS, dataRoot, logical string) string {
	dir, base := path.Dir(logical), path.Base(logical)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for n := 2; ; n++ {
		cand := path.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
		if _, err := e.C.Locate(ctx, volumeID, cand); err == nil {
			continue
		}
		if p, err := core.PhysicalPath(dataRoot, cand); err == nil {
			if _, err := fsys.Stat(ctx, p); err == nil {
				continue
			}
		}
		return cand
	}
}

// pruneMovedDirs removes catalog dir objects under the MOVE source that no longer hold files.
func (e *Engine) pruneMovedDirs(ctx context.Context, j *Job) {
	src, err := core.CleanLogical(j.Source)
	if err != nil {
		return
	}
	objs, _ := e.C.Walk(ctx, j.VolumeID, src)
	files := map[string]bool{}
	for _, o := range objs {
		if o.Type == "file" {
			for p := o.ParentPath; p != "/" && p != "."; p = path.Dir(p) {
				files[p] = true
			}
		}
	}
	for _, o := range objs {
		if o.Type == "dir" && !files[o.LogicalPath] {
			e.C.DeleteObject(ctx, o.ID)
		}
	}
}

// srcFS is where an item's source lives: the Controller host for COPY, the source disk's node for moves.
func (e *Engine) srcFS(ctx context.Context, j *Job, it workItem) nodefs.FS {
	if moveLike(j.Type) && it.meta.SrcDisk != "" {
		if sd, err := e.C.GetDisk(ctx, it.meta.SrcDisk); err == nil {
			return e.fsOf(sd)
		}
	}
	return nodefs.Local{}
}
