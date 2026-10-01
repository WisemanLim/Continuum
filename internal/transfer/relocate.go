package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/placement"
	"continuum/internal/store"
)

// planRelocate builds DRAIN / REBALANCE plans (FR-D10, FR-P10).
//
//	DRAIN:     req.Source = disk; every file on it is placed on the remaining disks (start ordinal honoured).
//	REBALANCE: req.Source = logical scope (default "/"); placement is recomputed from scratch as if the scope
//	           were empty, and only files whose target differs from their current disk are moved.
//
// Files touched by another active job are left out (PRD §11.8).
func (e *Engine) planRelocate(ctx context.Context, req PlanRequest, v core.Volume, pr *PlanResult) (*PlanResult, error) {
	busy := e.busyPaths(ctx, v.ID)
	policy := v.PlacementPolicy
	if req.Policy != "" {
		if !core.ValidPolicy(req.Policy) {
			return nil, core.Errf(core.CodeInvalid, "unknown placement policy %q", req.Policy)
		}
		policy = req.Policy
	}
	minFree := v.MinFreeBytes
	if req.MinFreeBytes != nil {
		minFree = *req.MinFreeBytes
	}
	pr.MinFree, pr.Spillover = minFree, true
	cands := placementDisks(v, minFree, req.MinFreeBytes != nil)

	var objs []core.Object
	switch req.Type {
	case TypeDrain:
		d, err := e.C.GetDisk(ctx, req.Source)
		if err != nil {
			return nil, err
		}
		if d.VolumeID != v.ID {
			return nil, core.Errf(core.CodeInvalid, "disk %s is not in volume %s", d.Name, v.Slug)
		}
		pr.Source, pr.Destination = d.ID, "/"
		all, err := e.C.Walk(ctx, v.ID, "/")
		if err != nil {
			return nil, err
		}
		for _, o := range all {
			if o.Type == "file" && o.DiskID == d.ID {
				objs = append(objs, o)
			}
		}
		for i := range cands {
			if cands[i].ID == d.ID {
				cands[i].State = core.StateDraining // never a target
			}
		}
	case TypeRebalance:
		scope := req.Source
		if scope == "" {
			scope = "/"
		}
		scope, err := core.CleanLogical(scope)
		if err != nil {
			return nil, err
		}
		pr.Source, pr.Destination = scope, scope
		all, err := e.C.Walk(ctx, v.ID, scope)
		if err != nil {
			return nil, err
		}
		back := map[string]int64{} // simulate an empty scope: its bytes count as free on their disk
		for _, o := range all {
			if o.Type == "file" && o.State == "ok" && !busy[o.LogicalPath] {
				objs = append(objs, o)
				back[o.DiskID] += o.Size
			}
		}
		for i := range cands {
			cands[i].EffectiveFree += back[cands[i].ID]
		}
	}

	var items []placement.Item
	byPath := map[string]core.Object{}
	for _, o := range objs {
		if busy[o.LogicalPath] {
			continue
		}
		if o.State != "ok" && req.Type == TypeDrain {
			return nil, core.Errf(core.CodeOffline, "%s is %s — resolve it before draining", o.LogicalPath, o.State)
		}
		key := strings.TrimPrefix(o.LogicalPath, "/")
		byPath[key] = o
		items = append(items, placement.Item{Path: key, Size: o.Size})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	plan := placement.Compute(cands, items, 0, placement.Options{Policy: policy, StartOrdinal: req.StartOrdinal, Spillover: true})

	ord := map[string]int{}
	for _, d := range v.Disks {
		ord[d.ID] = d.Ordinal
	}
	var moves []placement.Assignment
	for _, a := range plan.Assignments {
		if o := byPath[a.Path]; a.DiskID != o.DiskID {
			moves = append(moves, a)
		}
	}
	// lower targets first: moving data toward low ordinals frees the high ones it may need next
	sort.SliceStable(moves, func(i, j int) bool { return ord[moves[i].DiskID] < ord[moves[j].DiskID] })
	plan.Assignments = moves
	plan.Files, plan.Bytes = len(moves), 0
	for _, a := range moves {
		plan.Bytes += a.Size
		o := byPath[a.Path]
		pr.items = append(pr.items, planItem{logical: o.LogicalPath, source: o.PhysicalPath, srcDisk: o.DiskID, objID: o.ID, size: o.Size,
			mode: os.FileMode(o.Mode), mtimeNs: o.MtimeNs})
	}
	if req.Type == TypeDrain { // unplaced files still have to leave the disk
		assigned := map[string]bool{}
		for _, a := range moves {
			assigned[a.Path] = true
		}
		for key, o := range byPath {
			if !assigned[key] {
				pr.items = append(pr.items, planItem{logical: o.LogicalPath, source: o.PhysicalPath, srcDisk: o.DiskID, objID: o.ID, size: o.Size,
					mode: os.FileMode(o.Mode), mtimeNs: o.MtimeNs})
			}
		}
		plan.Files = len(pr.items)
	}
	// remaining after = today's free above reserve − incoming + outgoing (the simulation above is not reality)
	delta := map[string]int64{}
	for _, a := range moves {
		delta[a.DiskID] -= a.Size
		delta[byPath[a.Path].DiskID] += a.Size
	}
	plan.Placements = summarize(v, moves)
	for i, p := range plan.Placements {
		for _, d := range v.Disks {
			if d.ID == p.DiskID {
				plan.Placements[i].RemainingAboveReserve = d.EffectiveFree + delta[d.ID]
			}
		}
	}
	pr.Plan = plan
	return pr, nil
}

// afterRelocate runs when a DRAIN job ends: DONE with nothing left → the disk leaves the volume and
// becomes DISABLED (FR-D10 steps 8-9); otherwise (cancel/fail/pause-terminal) it returns to ONLINE.
func (e *Engine) afterRelocate(ctx context.Context, jobID, state string) {
	var typ, src, vol string
	if e.C.DB.QueryRow(ctx, `SELECT job_type, COALESCE(source,''), COALESCE(volume_id,'') FROM jobs WHERE job_id=?`, jobID).Scan(&typ, &src, &vol) != nil || typ != TypeDrain {
		return
	}
	d, err := e.C.GetDisk(ctx, src)
	if err != nil {
		return
	}
	var files int
	e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE disk_id=? AND object_type='file'`, d.ID).Scan(&files)
	if state != JobDone || files > 0 {
		if d.State == core.StateDraining {
			e.C.DB.Exec(ctx, `UPDATE disks SET state=?, updated_at=? WHERE disk_id=?`, core.StateOnline, store.Now(), d.ID)
		}
		return
	}
	e.C.DB.Exec(ctx, `DELETE FROM objects WHERE disk_id=? AND object_type='dir'`, d.ID)
	v, err := e.C.GetVolume(ctx, vol)
	if err != nil {
		return
	}
	var keep []string
	for _, x := range v.Disks {
		if x.ID != d.ID {
			keep = append(keep, x.ID)
		}
	}
	if len(keep) > 0 {
		if _, err := e.C.SetDisks(ctx, v.ID, core.SetDisksRequest{DiskIDs: keep}); err != nil {
			e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.drain_failed", VolumeID: v.ID, DiskID: d.ID}, map[string]any{"error": err.Error()})
			return
		}
	}
	e.C.DB.Exec(ctx, `UPDATE disks SET state=?, writable=0, updated_at=? WHERE disk_id=?`, core.StateDisabled, store.Now(), d.ID)
	e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.drained", VolumeID: v.ID, DiskID: d.ID, JobID: jobID}, nil)
	e.C.Audit.Append(ctx, audit.Entry{Action: "disk.drain", Target: d.Name, VolumeID: v.ID, Result: "removed from volume"})
}

// ResumeOffline resumes jobs that paused because a disk went offline once all their disks are back
// (FR-N05 "복귀 시 자동 재개"). Called when an agent node reconnects.
func (e *Engine) ResumeOffline(ctx context.Context) int {
	rows, err := e.C.DB.Query(ctx, `SELECT job_id FROM jobs WHERE state=? AND COALESCE(error,'') LIKE '%offline%'`, JobPaused)
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		var offline int
		e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM job_items i JOIN disks d ON d.disk_id=i.disk_id
			WHERE i.job_id=? AND i.state IN ('pending','running') AND d.state<>?`, id, core.StateOnline).Scan(&offline)
		if offline > 0 {
			continue
		}
		if _, err := e.Resume(ctx, id); err == nil {
			n++
			e.C.Audit.Emit(ctx, audit.Event{Kind: "job.auto_resumed", JobID: id}, map[string]any{"reason": "disks back online"})
		}
	}
	return n
}

// parseWindow parses "HH:MM-HH:MM" (local time, may wrap midnight).
func parseWindow(w string) (start, end int, err error) {
	var h1, m1, h2, m2 int
	if _, err := fmt.Sscanf(strings.TrimSpace(w), "%d:%d-%d:%d", &h1, &m1, &h2, &m2); err != nil || h1 > 23 || h2 > 23 || m1 > 59 || m2 > 59 || h1 < 0 || h2 < 0 || m1 < 0 || m2 < 0 {
		return 0, 0, core.Errf(core.CodeInvalid, "window must look like 22:00-06:00 (got %q)", w)
	}
	if h1*60+m1 == h2*60+m2 {
		return 0, 0, core.Errf(core.CodeInvalid, "window start and end must differ")
	}
	return h1*60 + m1, h2*60 + m2, nil
}

func inWindow(w string, now time.Time) bool {
	s, e, err := parseWindow(w)
	if err != nil {
		return true
	}
	m := now.Hour()*60 + now.Minute()
	if s < e {
		return m >= s && m < e
	}
	return m >= s || m < e // wraps midnight
}

func windowMsg(w string) string { return "waiting for time window " + w + " — starts automatically" }

// WindowDue starts or resumes jobs whose time window has opened (called from the Background tick).
func (e *Engine) WindowDue(ctx context.Context, now time.Time) int {
	rows, err := e.C.DB.Query(ctx, `SELECT job_id, state, COALESCE(params_json,'') FROM jobs WHERE state IN (?, ?) AND COALESCE(error,'') LIKE 'waiting for time window%'`, JobQueued, JobPaused)
	if err != nil {
		return 0
	}
	type due struct{ id, state string }
	var ds []due
	for rows.Next() {
		var id, st, params string
		rows.Scan(&id, &st, &params)
		var req PlanRequest
		json.Unmarshal([]byte(params), &req)
		if req.Window != "" && inWindow(req.Window, now) {
			ds = append(ds, due{id, st})
		}
	}
	rows.Close()
	for _, d := range ds {
		e.C.DB.Exec(ctx, `UPDATE jobs SET error=NULL WHERE job_id=?`, d.id)
		if d.state == JobQueued {
			e.launch(d.id)
		} else {
			e.Resume(ctx, d.id)
		}
	}
	return len(ds)
}
