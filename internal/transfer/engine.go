// Package transfer is the Continuum transfer engine (FR-T01~T23, PRD §11):
// asynchronous jobs with reservation accounting, the 14-step atomic copy, resume,
// pause/cancel/retry, and the catalog-level operations (move, rename, mkdir, delete, trash, verify).
package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"continuum/internal/agentproto"
	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/nodefs"
	"continuum/internal/placement"
	"continuum/internal/store"
)

// Job types (FR-T01) and states (PRD §11 state machine).
const (
	TypeCopy      = "COPY"
	TypeMove      = "MOVE"
	TypeVerify    = "VERIFY"
	TypeDrain     = "DRAIN"     // FR-D10: move everything off one disk, then remove it from the volume
	TypeRebalance = "REBALANCE" // FR-P10: re-place existing data by the current order/policy

	JobQueued          = "QUEUED"
	JobRunning         = "RUNNING"
	JobPaused          = "PAUSED"
	JobPausedNeedSpace = "PAUSED_NEED_SPACE"
	JobDone            = "DONE"
	JobFailed          = "FAILED"
	JobCancelled       = "CANCELLED"

	itemPending   = "pending"
	itemRunning   = "running"
	itemDone      = "done"
	itemSkipped   = "skipped"
	itemFailed    = "failed"
	itemCancelled = "cancelled"
	itemUnplaced  = "unplaced"
)

// moveLike job types relocate cataloged files (same disk = rename, cross disk = copy → verify → delete).
func moveLike(t string) bool { return t == TypeMove || t == TypeDrain || t == TypeRebalance }

func activeState(s string) bool {
	return s == JobQueued || s == JobRunning || s == JobPaused || s == JobPausedNeedSpace
}

// PlanRequest describes a COPY (local source → volume) or MOVE (volume → volume).
type PlanRequest struct {
	Type           string   `json:"type"`
	Volume         string   `json:"volume"`
	Source         string   `json:"source"`      // COPY: local path · MOVE: logical path
	Destination    string   `json:"destination"` // logical path
	StartOrdinal   int      `json:"start_ordinal,omitempty"`
	ForcedOrdinal  int      `json:"forced_ordinal,omitempty"`
	Policy         string   `json:"placement_policy,omitempty"`
	Spillover      *bool    `json:"spillover,omitempty"`
	MinFreeBytes   *int64   `json:"min_free_bytes,omitempty"`
	Verify         string   `json:"verify,omitempty"`          // blake3 (default) | none
	OnConflict     string   `json:"on_conflict,omitempty"`     // skip (default) | replace | rename | newer
	OnInsufficient string   `json:"on_insufficient,omitempty"` // fail (default) | wait → PAUSED_NEED_SPACE
	Include        []string `json:"include,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
	BwLimit        int64    `json:"bwlimit_bps,omitempty"`
	Transfers      int      `json:"transfers,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Window         string   `json:"window,omitempty"` // "22:00-06:00": run only inside this local-time window (FR-P10)
}

// PlanResult is placement.Plan plus request context.
type PlanResult struct {
	placement.Plan
	Volume      string `json:"volume"`
	VolumeID    string `json:"volume_id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Existing    int    `json:"existing_paths"` // logical paths already in catalog (conflict policy applies)
	StartOrd    int    `json:"start_ordinal,omitempty"`
	ForcedOrd   int    `json:"forced_ordinal,omitempty"`
	Spillover   bool   `json:"spillover"`
	MinFree     int64  `json:"min_free_bytes"`

	items  []planItem
	volume core.Volume
}

type planItem struct {
	logical string
	source  string // local abs path (COPY) or physical path (MOVE)
	srcDisk string // MOVE: current disk
	objID   string // MOVE: object id being moved
	size    int64
	mode    os.FileMode
	mtimeNs int64
	link    string
	exists  bool // logical path already in catalog (conflict policy applies)
}

// Engine runs jobs. One Engine per Controller.
type Engine struct {
	C            *core.Core
	Workers      int // per job (default 4)
	PerDisk      int // concurrent items per disk (default 2, HDD seek contention)
	RetryBackoff []time.Duration

	mu      sync.Mutex
	running map[string]*runCtl
	subs    map[chan Progress]string // subscriber → job filter ("" = all)
	wg      sync.WaitGroup
	scanMu  sync.Mutex // one SCAN at a time (they walk whole disks)

	// WorkerID identifies this Controller process; LeaseTTL bounds how long a crashed worker keeps a job
	// (central mode: several Controllers may share one PostgreSQL, FR-N08).
	WorkerID string
	LeaseTTL time.Duration
	// Now is the clock for time windows (tests replace it).
	Now func() time.Time

	// NoDirect forces relaying node ↔ node copies through the Controller (CONTINUUM_DIRECT=off).
	NoDirect bool
	direct   atomic.Int64
	resumed  atomic.Int64

	// Remote gives the filesystem of agent nodes (nil = single machine).
	Remote interface {
		FS(node string) nodefs.FS
		Call(ctx context.Context, node, op string, args, out any) error
	}
}

// directPuller is implemented by the agent hub: node ↔ node copies without relaying bytes (D36).
type directPuller interface {
	Pull(ctx context.Context, srcNode, src, dstNode string, a agentproto.PullArgs) (agentproto.PullResult, error)
}

type noder interface{ Node() string }

// fsOf returns the filesystem holding a disk's data.
func (e *Engine) fsOf(d core.Disk) nodefs.FS {
	if e.Remote != nil && e.C.IsRemote(d) {
		return e.Remote.FS(d.NodeID)
	}
	return nodefs.Local{}
}

type runCtl struct {
	cancel  context.CancelCauseFunc
	prog    *Progress
	mu      sync.Mutex
	done    chan struct{}
	bytes   atomic.Int64 // bytes done (lock-free, updated per chunk)
	started time.Time
}

// New creates an engine; call Recover() at startup.
func New(c *core.Core) *Engine {
	return &Engine{C: c, Workers: 4, PerDisk: 2, RetryBackoff: []time.Duration{time.Second, 4 * time.Second, 16 * time.Second},
		running: map[string]*runCtl{}, subs: map[chan Progress]string{}, WorkerID: store.NewID("wrk"), LeaseTTL: 30 * time.Second, Now: time.Now}
}

// Plan scans the source and computes placement without reserving anything (FR-P07).
func (e *Engine) Plan(ctx context.Context, req PlanRequest) (*PlanResult, error) {
	if req.Type == "" {
		req.Type = TypeCopy
	}
	v, err := e.freshVolume(ctx, req.Volume)
	if err != nil {
		return nil, err
	}
	dest, err := core.CleanLogical(req.Destination)
	if err != nil {
		return nil, err
	}
	pr := &PlanResult{Volume: v.Slug, VolumeID: v.ID, Source: req.Source, Destination: dest, volume: v,
		StartOrd: req.StartOrdinal, ForcedOrd: req.ForcedOrdinal}
	var items []placement.Item
	var dirs int
	switch req.Type {
	case TypeCopy:
		src, n, base, err := scan(req.Source, req.Include, append(append([]string{}, defaultExcludes...), req.Exclude...))
		if err != nil {
			return nil, core.Errf(core.CodeInvalid, "source: %v", err)
		}
		dirs = n
		fi, _ := os.Stat(req.Source)
		single := fi != nil && !fi.IsDir()
		for _, s := range src {
			lp := path.Join(dest, s.Rel)
			if single {
				lp = dest
				if strings.HasSuffix(req.Destination, "/") || e.isDir(ctx, v.ID, dest) {
					lp = path.Join(dest, base)
				}
			}
			pi := planItem{logical: lp, source: s.Abs, size: s.Size, mode: s.Mode, mtimeNs: s.MtimeNs, link: s.Link}
			pi.exists = e.exists(ctx, v.ID, lp, "")
			pr.items = append(pr.items, pi)
			if pi.exists && skipPolicy(req.OnConflict) {
				continue // will be skipped: needs no space
			}
			items = append(items, placement.Item{Path: s.Rel, Size: s.Size})
		}
	case TypeMove:
		src, err := core.CleanLogical(req.Source)
		if err != nil {
			return nil, err
		}
		objs, err := e.C.Walk(ctx, v.ID, src)
		if err != nil {
			return nil, err
		}
		for _, o := range objs {
			if o.Type == "dir" {
				dirs++
				continue
			}
			if o.State != "ok" {
				return nil, core.Errf(core.CodeOffline, "%s is %s", o.LogicalPath, o.State)
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(o.LogicalPath, src), "/")
			lp := path.Join(dest, rel)
			if rel == "" {
				lp = dest
			}
			pi := planItem{logical: lp, source: o.PhysicalPath, srcDisk: o.DiskID, objID: o.ID, size: o.Size,
				mode: os.FileMode(o.Mode), mtimeNs: o.MtimeNs}
			pi.exists = e.exists(ctx, v.ID, lp, o.ID)
			pr.items = append(pr.items, pi)
			if pi.exists && skipPolicy(req.OnConflict) {
				continue
			}
			items = append(items, placement.Item{Path: strings.TrimPrefix(lp, "/"), Size: o.Size})
		}
		if len(pr.items) == 0 {
			return nil, core.Errf(core.CodeNotFound, "%s has no files", src)
		}
	case TypeDrain, TypeRebalance:
		return e.planRelocate(ctx, req, v, pr)
	default:
		return nil, core.Errf(core.CodeInvalid, "unsupported plan type %q", req.Type)
	}

	policy := v.PlacementPolicy
	if req.Policy != "" {
		if !core.ValidPolicy(req.Policy) {
			return nil, core.Errf(core.CodeInvalid, "unknown placement policy %q", req.Policy)
		}
		policy = req.Policy
	}
	spill := v.AllowSpillover
	if req.Spillover != nil {
		spill = *req.Spillover
	}
	pr.Spillover = spill
	minFree := v.MinFreeBytes
	if req.MinFreeBytes != nil {
		minFree = *req.MinFreeBytes
	}
	pr.MinFree = minFree
	cands := placementDisks(v, minFree, req.MinFreeBytes != nil)

	if req.Type == TypeMove && req.ForcedOrdinal == 0 {
		// volume-internal move keeps files on their current disk (rename only)
		pr.Plan = placement.Plan{Files: len(items), Dirs: dirs, Policy: "same-disk", Sufficient: true, Placements: []placement.DiskPlacement{},
			Excluded: []placement.Excluded{}, Warnings: []placement.Warning{}}
		for _, it := range pr.items {
			if it.exists && skipPolicy(req.OnConflict) {
				continue
			}
			pr.Bytes += it.size
			pr.Assignments = append(pr.Assignments, placement.Assignment{Path: strings.TrimPrefix(it.logical, "/"), Size: it.size, DiskID: it.srcDisk})
		}
		pr.Placements = summarize(v, pr.Assignments)
	} else {
		pr.Plan = placement.Compute(cands, items, dirs, placement.Options{Policy: policy, StartOrdinal: req.StartOrdinal,
			ForcedOrdinal: req.ForcedOrdinal, Spillover: spill})
	}
	for _, it := range pr.items {
		if it.exists {
			pr.Existing++
		}
	}
	return pr, nil
}

func skipPolicy(p string) bool { return p == "" || p == "skip" }

// exists reports whether a file other than selfID already occupies logical.
func (e *Engine) exists(ctx context.Context, volumeID, logical, selfID string) bool {
	objs, err := e.C.Locate(ctx, volumeID, logical)
	if err != nil {
		return false
	}
	for _, o := range objs {
		if o.ID != selfID && o.Type == "file" {
			return true
		}
	}
	return false
}

// freshVolume re-reads statfs of every member disk before planning (placement must not use stale capacity).
func (e *Engine) freshVolume(ctx context.Context, ref string) (core.Volume, error) {
	v, err := e.C.GetVolume(ctx, ref)
	if err != nil {
		return v, err
	}
	for _, d := range v.Disks {
		e.C.RefreshCapacity(ctx, d.ID)
	}
	return e.C.GetVolume(ctx, v.ID)
}

func (e *Engine) isDir(ctx context.Context, volumeID, lp string) bool {
	var n int
	_ = e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE volume_id=? AND logical_path=? AND object_type='dir'`, volumeID, lp).Scan(&n)
	return n > 0
}

func placementDisks(v core.Volume, minFree int64, override bool) []placement.Disk {
	var out []placement.Disk
	for _, d := range v.Disks {
		mf := d.EffectiveMinFree
		if override || d.MinFreeBytes == nil {
			mf = minFree
		}
		ci := false
		for _, c := range d.Capabilities {
			if c == "case-insensitive" {
				ci = true
			}
		}
		out = append(out, placement.Disk{ID: d.ID, Name: d.Name, Node: d.NodeName, Ordinal: d.Ordinal, State: d.State, Role: d.Role,
			Writable: d.Writable, AutoPlace: core.AutoPlace(d.Role), TotalBytes: d.TotalBytes, EffectiveFree: d.EffectiveFree,
			MinFree: mf, FSType: d.FilesystemType, CaseInsensitive: ci})
	}
	return out
}

func summarize(v core.Volume, as []placement.Assignment) []placement.DiskPlacement {
	by := map[string]*placement.DiskPlacement{}
	out := []placement.DiskPlacement{}
	for _, a := range as {
		p := by[a.DiskID]
		if p == nil {
			for _, d := range v.Disks {
				if d.ID == a.DiskID {
					p = &placement.DiskPlacement{Ordinal: d.Ordinal, DiskID: d.ID, Name: d.Name, Node: d.NodeName}
				}
			}
			if p == nil {
				continue
			}
			by[a.DiskID] = p
		}
		p.Files++
		p.Bytes += a.Size
	}
	for _, d := range v.Disks {
		if p := by[d.ID]; p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// Job is the API view of a job.
type Job struct {
	ID          string          `json:"job_id"`
	Type        string          `json:"type"`
	VolumeID    string          `json:"volume_id,omitempty"`
	State       string          `json:"state"`
	Source      string          `json:"source,omitempty"`
	Destination string          `json:"destination,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"`
	Plan        json.RawMessage `json:"plan,omitempty"`
	Progress    *Progress       `json:"progress,omitempty"`
	ErrorsCount int             `json:"errors_count"`
	Error       string          `json:"error,omitempty"`
	Attempt     int             `json:"attempt"`
	CreatedAt   string          `json:"created_at"`
	StartedAt   string          `json:"started_at,omitempty"`
	FinishedAt  string          `json:"finished_at,omitempty"`
}

// Progress is streamed to Transfers/CLI watch (FR-T09).
type Progress struct {
	JobID      string                   `json:"job_id"`
	State      string                   `json:"state"`
	FilesTotal int64                    `json:"files_total"`
	FilesDone  int64                    `json:"files_done"`
	FilesSkip  int64                    `json:"files_skipped"`
	FilesFail  int64                    `json:"files_failed"`
	BytesTotal int64                    `json:"bytes_total"`
	BytesDone  int64                    `json:"bytes_done"`
	RateBps    int64                    `json:"rate_bps"`
	ETASeconds int64                    `json:"eta_seconds"`
	Current    string                   `json:"current,omitempty"`
	Placements map[string]*DiskProgress `json:"placements"`
	Reason     string                   `json:"reason,omitempty"`
	UpdatedAt  string                   `json:"updated_at"`
}

type DiskProgress struct {
	Name  string `json:"name"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

// Start computes a fresh plan (re-reserving at execution time, FR-P07), persists the job
// and its items, reserves bytes on target disks and launches it. Idempotent by key (FR-T22).
func (e *Engine) Start(ctx context.Context, req PlanRequest) (*Job, error) {
	if req.IdempotencyKey != "" {
		var id string
		if err := e.C.DB.QueryRow(ctx, `SELECT job_id FROM jobs WHERE idempotency_key=?`, req.IdempotencyKey).Scan(&id); err == nil {
			return e.Get(ctx, id)
		}
	}
	if req.Type == "" {
		req.Type = TypeCopy
	}
	if req.Verify == "" {
		req.Verify = "blake3"
	}
	if req.OnConflict == "" {
		req.OnConflict = "skip"
		if req.Type == TypeDrain || req.Type == TypeRebalance {
			req.OnConflict = "rename" // never drop a copy while relocating
		}
	}
	switch req.OnConflict {
	case "skip", "replace", "rename", "newer":
	default:
		return nil, core.Errf(core.CodeInvalid, "on_conflict must be skip|replace|rename|newer")
	}
	var drained *core.Disk
	if req.Type == TypeDrain { // DRAINING first: blocks new writes to the disk (FR-D10 step 1-2)
		d, err := e.C.GetDisk(ctx, req.Source)
		if err != nil {
			return nil, err
		}
		if d.State != core.StateOnline && d.State != core.StateDraining {
			return nil, core.Errf(core.CodeOffline, "disk %s is %s — only an ONLINE disk can be drained", d.Name, d.State)
		}
		if _, err := e.C.DB.Exec(ctx, `UPDATE disks SET state=?, updated_at=? WHERE disk_id=?`, core.StateDraining, store.Now(), d.ID); err != nil {
			return nil, err
		}
		drained = &d
	}
	undrain := func() {
		if drained != nil {
			e.C.DB.Exec(ctx, `UPDATE disks SET state=?, updated_at=? WHERE disk_id=?`, core.StateOnline, store.Now(), drained.ID)
		}
	}
	if req.Window != "" {
		if _, _, err := parseWindow(req.Window); err != nil {
			undrain()
			return nil, err
		}
	}
	pr, err := e.Plan(ctx, req)
	if err != nil {
		undrain()
		return nil, err
	}
	if pr.Error == placement.ErrForcedUnavailable || pr.Error == placement.ErrNoCandidates {
		undrain()
		ce := core.Errf(core.CodeOffline, "%s: no online writable disk for this request", pr.Error)
		ce.Info = map[string]any{"plan": pr}
		return nil, ce
	}
	if !pr.Sufficient && req.OnInsufficient != "wait" {
		undrain()
		ce := core.Errf(core.CodeInsufficientSpace, "%s: %d file(s), %d byte(s) cannot be placed", pr.Error, pr.Unplaced, pr.UnplacedB)
		ce.Info = map[string]any{"plan": pr}
		return nil, ce
	}
	prefix := map[string]string{TypeCopy: "cpy", TypeMove: "mov", TypeDrain: "drn", TypeRebalance: "rbl"}[req.Type]
	id := store.NewID(prefix)
	params, _ := json.Marshal(req)
	planJSON, _ := json.Marshal(pr)
	now := store.Now()

	assigned := map[string]string{}
	for _, a := range pr.Assignments {
		assigned[a.Path] = a.DiskID
	}
	err = e.C.DB.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO jobs (job_id, job_type, volume_id, state, source, destination, params_json, plan_json, idempotency_key, requested_by, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`, id, req.Type, pr.VolumeID, JobQueued, req.Source, pr.Destination, string(params), string(planJSON),
			nullIf(req.IdempotencyKey), audit.ActorFrom(ctx), now); err != nil {
			return err
		}
		reserve := map[string]int64{}
		for i, it := range pr.items {
			key := strings.TrimPrefix(it.logical, "/")
			if req.Type == TypeCopy {
				key = relKey(pr, it)
			}
			disk := assigned[key]
			st := itemPending
			if it.exists && skipPolicy(req.OnConflict) {
				st, disk = itemSkipped, ""
			} else if disk == "" {
				st = itemUnplaced
			} else if !(moveLike(req.Type) && disk == it.srcDisk) {
				reserve[disk] += it.size // same-disk rename needs no space
			}
			meta, _ := json.Marshal(itemMeta{Mode: uint32(it.mode), MtimeNs: it.mtimeNs, Link: it.link, SrcDisk: it.srcDisk, ObjID: it.objID})
			if _, err := tx.Exec(ctx, `INSERT INTO job_items (job_id, item_no, logical_path, source_path, disk_id, size_bytes, state, checksum)
				VALUES (?,?,?,?,?,?,?,?)`, id, i+1, it.logical, it.source, nullIf(disk), it.size, st, string(meta)); err != nil {
				return err
			}
		}
		for d, b := range reserve { // step 3: reservation (FR-P02)
			if _, err := tx.Exec(ctx, `UPDATE disks SET reserved_bytes = reserved_bytes + ? WHERE disk_id = ?`, b, d); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		undrain()
		return nil, err
	}
	e.C.Audit.Emit(ctx, audit.Event{Kind: "job.queued", VolumeID: pr.VolumeID, JobID: id}, map[string]any{"type": req.Type, "files": pr.Files, "bytes": pr.Bytes})
	if req.Window != "" && !inWindow(req.Window, e.Now()) {
		e.C.DB.Exec(ctx, `UPDATE jobs SET error=? WHERE job_id=?`, windowMsg(req.Window), id) // WindowDue starts it
		return e.Get(ctx, id)
	}
	e.launch(id)
	return e.Get(ctx, id)
}

// relKey maps a COPY plan item back to the placement item path (source-relative).
func relKey(pr *PlanResult, it planItem) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(it.logical, pr.Destination), "/")
	if rel == "" || it.logical == pr.Destination {
		return path.Base(it.source)
	}
	return rel
}

// itemMeta is stored in job_items.checksum until the real checksum replaces it on completion.
type itemMeta struct {
	Mode    uint32 `json:"mode"`
	MtimeNs int64  `json:"mtime_ns"`
	Link    string `json:"link,omitempty"`
	SrcDisk string `json:"src_disk,omitempty"`
	ObjID   string `json:"obj_id,omitempty"`
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Get loads a job with live progress if running.
func (e *Engine) Get(ctx context.Context, id string) (*Job, error) {
	var j Job
	var params, plan, prog, errStr, started, finished sql.NullString
	err := e.C.DB.QueryRow(ctx, `SELECT job_id, job_type, COALESCE(volume_id,''), state, COALESCE(source,''), COALESCE(destination,''),
		params_json, plan_json, progress_json, errors_count, error, attempt, created_at, started_at, finished_at FROM jobs WHERE job_id=?`, id).
		Scan(&j.ID, &j.Type, &j.VolumeID, &j.State, &j.Source, &j.Destination, &params, &plan, &prog, &j.ErrorsCount, &errStr, &j.Attempt,
			&j.CreatedAt, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.Errf(core.CodeNotFound, "job %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	j.Params, j.Plan = raw(params), raw(plan)
	j.Error, j.StartedAt, j.FinishedAt = errStr.String, started.String, finished.String
	e.mu.Lock()
	rc := e.running[id]
	e.mu.Unlock()
	if rc != nil {
		j.Progress = rc.snapshot()
	} else if prog.Valid {
		var p Progress
		if json.Unmarshal([]byte(prog.String), &p) == nil {
			p.State = j.State
			j.Progress = &p
		}
	}
	return &j, nil
}

func raw(s sql.NullString) json.RawMessage {
	if !s.Valid || s.String == "" {
		return nil
	}
	return json.RawMessage(s.String)
}

// List returns jobs newest first, optionally filtered by state or volume.
func (e *Engine) List(ctx context.Context, state, volumeID string, limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT job_id FROM jobs WHERE 1=1`
	var args []any
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	if volumeID != "" {
		q += ` AND volume_id = ?`
		args = append(args, volumeID)
	}
	rows, err := e.C.DB.Query(ctx, q+` ORDER BY created_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*Job{}
	for _, id := range ids {
		j, err := e.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		j.Params, j.Plan = nil, nil
		out = append(out, j)
	}
	return out, nil
}

// JobItem is one file of a job (Transfers › job detail, error export FR-T21).
type JobItem struct {
	No          int    `json:"item_no"`
	LogicalPath string `json:"logical_path"`
	Source      string `json:"source"`
	DiskID      string `json:"disk_id,omitempty"`
	Size        int64  `json:"size"`
	Offset      int64  `json:"offset"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
}

// Items lists job items; state filter optional (e.g. "failed").
func (e *Engine) Items(ctx context.Context, id, state string) ([]JobItem, error) {
	q := `SELECT item_no, logical_path, COALESCE(source_path,''), COALESCE(disk_id,''), size_bytes, offset_bytes, state, COALESCE(error,'') FROM job_items WHERE job_id=?`
	args := []any{id}
	if state != "" {
		q += ` AND state=?`
		args = append(args, state)
	}
	rows, err := e.C.DB.Query(ctx, q+` ORDER BY item_no`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobItem{}
	for rows.Next() {
		var it JobItem
		if err := rows.Scan(&it.No, &it.LogicalPath, &it.Source, &it.DiskID, &it.Size, &it.Offset, &it.State, &it.Error); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Pause stops after the current chunk, keeping partial files and reservations (FR-T08).
func (e *Engine) Pause(ctx context.Context, id string) (*Job, error) {
	return e.stop(ctx, id, errPaused)
}

// Cancel removes partial files, releases reservations; finished files stay (FR-T08).
func (e *Engine) Cancel(ctx context.Context, id string) (*Job, error) {
	j, err := e.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !activeState(j.State) {
		return j, core.Errf(core.CodeConflict, "job is %s", j.State)
	}
	if e.isRunning(id) {
		return e.stop(ctx, id, errCancelled)
	}
	// not running (paused / need space): clean up directly
	e.cleanupPartials(ctx, id)
	e.finish(ctx, id, JobCancelled, "cancelled by user")
	return e.Get(ctx, id)
}

func (e *Engine) stop(ctx context.Context, id string, cause error) (*Job, error) {
	e.mu.Lock()
	rc := e.running[id]
	e.mu.Unlock()
	if rc == nil {
		j, err := e.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return j, core.Errf(core.CodeConflict, "job is %s (not running)", j.State)
	}
	rc.cancel(cause)
	<-rc.done
	return e.Get(ctx, id)
}

func (e *Engine) isRunning(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running[id] != nil
}

// Resume restarts a PAUSED or PAUSED_NEED_SPACE job; unplaced items are re-planned (FR-P09·P11).
func (e *Engine) Resume(ctx context.Context, id string) (*Job, error) {
	j, err := e.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.State != JobPaused && j.State != JobPausedNeedSpace && j.State != JobQueued {
		return j, core.Errf(core.CodeConflict, "job is %s", j.State)
	}
	if err := e.replaceUnplaced(ctx, j); err != nil {
		return j, err
	}
	e.launch(id)
	return e.Get(ctx, id)
}

// Retry re-queues failed items only (FR-T08 RETRY, FR-T21).
func (e *Engine) Retry(ctx context.Context, id string) (*Job, error) {
	j, err := e.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if activeState(j.State) {
		return j, core.Errf(core.CodeConflict, "job is %s", j.State)
	}
	err = e.C.DB.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(ctx, `SELECT disk_id, SUM(size_bytes) FROM job_items WHERE job_id=? AND state=? AND disk_id IS NOT NULL GROUP BY disk_id`, id, itemFailed)
		if err != nil {
			return err
		}
		res := map[string]int64{}
		for rows.Next() {
			var d string
			var b int64
			rows.Scan(&d, &b)
			res[d] = b
		}
		rows.Close()
		for d, b := range res {
			if _, err := tx.Exec(ctx, `UPDATE disks SET reserved_bytes = reserved_bytes + ? WHERE disk_id=?`, b, d); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE job_items SET state=?, error=NULL WHERE job_id=? AND state=?`, itemPending, id, itemFailed); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE jobs SET state=?, attempt=attempt+1, error=NULL, finished_at=NULL WHERE job_id=?`, JobQueued, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	e.launch(id)
	return e.Get(ctx, id)
}

// replaceUnplaced assigns disks to unplaced items against the current pool state.
func (e *Engine) replaceUnplaced(ctx context.Context, j *Job) error {
	items, err := e.Items(ctx, j.ID, itemUnplaced)
	if err != nil || len(items) == 0 {
		return err
	}
	var req PlanRequest
	_ = json.Unmarshal(j.Params, &req)
	v, err := e.freshVolume(ctx, j.VolumeID)
	if err != nil {
		return err
	}
	spill := v.AllowSpillover
	if req.Spillover != nil {
		spill = *req.Spillover
	}
	minFree := v.MinFreeBytes
	if req.MinFreeBytes != nil {
		minFree = *req.MinFreeBytes
	}
	policy := v.PlacementPolicy
	if req.Policy != "" {
		policy = req.Policy
	}
	var pitems []placement.Item
	for _, it := range items {
		pitems = append(pitems, placement.Item{Path: it.LogicalPath, Size: it.Size})
	}
	plan := placement.Compute(placementDisks(v, minFree, req.MinFreeBytes != nil), pitems, 0,
		placement.Options{Policy: policy, StartOrdinal: req.StartOrdinal, ForcedOrdinal: req.ForcedOrdinal, Spillover: spill})
	byPath := map[string]string{}
	for _, a := range plan.Assignments {
		byPath[a.Path] = a.DiskID
	}
	return e.C.DB.Tx(ctx, func(tx *store.Tx) error {
		for _, it := range items {
			d := byPath[it.LogicalPath]
			if d == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE job_items SET disk_id=?, state=? WHERE job_id=? AND item_no=?`, d, itemPending, j.ID, it.No); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE disks SET reserved_bytes = reserved_bytes + ? WHERE disk_id=?`, it.Size, d); err != nil {
				return err
			}
		}
		return nil
	})
}

// Recover re-queues jobs interrupted by a crash/restart and recomputes reservations (NFR-A01).
func (e *Engine) Recover(ctx context.Context) error {
	// only jobs nobody holds a live lease on: another Controller may be running the rest
	rows, err := e.C.DB.Query(ctx, `SELECT job_id FROM jobs WHERE state IN (?, ?) AND (claimed_by IS NULL OR lease_until < ?)`, JobRunning, JobQueued, leaseTS(time.Now()))
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := e.C.DB.Exec(ctx, `UPDATE job_items SET state=? WHERE job_id=? AND state=?`, itemPending, id, itemRunning); err != nil {
			return err
		}
	}
	if err := e.recomputeReservations(ctx); err != nil {
		return err
	}
	for _, id := range ids {
		log.Printf("transfer: resuming interrupted job %s", id)
		e.launch(id)
	}
	return nil
}

// leaseTS is a fixed-width UTC timestamp so leases compare correctly as strings on both databases.
func leaseTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// claim takes (or renews) the job lease for this worker; false if another live worker holds it.
func (e *Engine) claim(ctx context.Context, id string) bool {
	now := time.Now()
	res, err := e.C.DB.Exec(ctx, `UPDATE jobs SET claimed_by=?, lease_until=? WHERE job_id=? AND (claimed_by IS NULL OR claimed_by=? OR lease_until < ?)`,
		e.WorkerID, leaseTS(now.Add(e.LeaseTTL)), id, e.WorkerID, leaseTS(now))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (e *Engine) unclaim(ctx context.Context, id string) {
	e.C.DB.Exec(ctx, `UPDATE jobs SET claimed_by=NULL, lease_until=NULL WHERE job_id=? AND claimed_by=?`, id, e.WorkerID)
}

// recomputeReservations sets reserved_bytes = Σ size of pending/running items of active jobs.
func (e *Engine) recomputeReservations(ctx context.Context) error {
	if _, err := e.C.DB.Exec(ctx, `UPDATE disks SET reserved_bytes = 0`); err != nil {
		return err
	}
	_, err := e.C.DB.Exec(ctx, `UPDATE disks SET reserved_bytes = COALESCE((
		SELECT SUM(i.size_bytes) FROM job_items i JOIN jobs j ON j.job_id = i.job_id
		WHERE i.disk_id = disks.disk_id AND i.state IN ('pending','running')
		AND j.state IN ('QUEUED','RUNNING','PAUSED','PAUSED_NEED_SPACE')
		AND NOT (j.job_type = 'MOVE' AND i.checksum LIKE '%"src_disk":"' || i.disk_id || '"%')), 0)`)
	return err
}

// Wait blocks until a running job stops (tests, CLI --wait).
func (e *Engine) Wait(id string) {
	e.mu.Lock()
	rc := e.running[id]
	e.mu.Unlock()
	if rc != nil {
		<-rc.done
	}
}

// Shutdown pauses all running jobs (keeps partials) — used on Controller stop.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	var rcs []*runCtl
	for _, rc := range e.running {
		rcs = append(rcs, rc)
	}
	e.mu.Unlock()
	for _, rc := range rcs {
		rc.cancel(errPaused)
		<-rc.done
	}
}

// ---- progress / subscriptions ----

// Subscribe receives progress snapshots (jobID "" = all jobs). Call the returned func to stop.
func (e *Engine) Subscribe(jobID string) (<-chan Progress, func()) {
	ch := make(chan Progress, 64)
	e.mu.Lock()
	e.subs[ch] = jobID
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, ch)
		e.mu.Unlock()
	}
}

func (e *Engine) publish(p Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch, f := range e.subs {
		if f == "" || f == p.JobID {
			select {
			case ch <- p:
			default: // slow subscriber: drop (next snapshot supersedes)
			}
		}
	}
}

func (rc *runCtl) snapshot() *Progress {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	p := *rc.prog
	p.BytesDone = rc.bytes.Load()
	el := time.Since(rc.started).Seconds()
	if el > 0.5 {
		p.RateBps = int64(float64(p.BytesDone) / el)
	}
	if p.RateBps > 0 {
		p.ETASeconds = (p.BytesTotal - p.BytesDone) / p.RateBps
	}
	pl := map[string]*DiskProgress{}
	for k, v := range rc.prog.Placements {
		c := *v
		pl[k] = &c
	}
	p.Placements = pl
	p.UpdatedAt = store.Now()
	return &p
}
