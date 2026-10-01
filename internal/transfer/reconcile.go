package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/nodefs"
	"continuum/internal/store"
)

// ScanOptions control SCAN (FR-K03, PRD §11.6).
type ScanOptions struct {
	Full        bool   `json:"full"`         // re-hash every file (integrity check) instead of size/mtime only
	Disk        string `json:"disk"`         // limit to one disk (id or name)
	FixPartials bool   `json:"fix_partials"` // delete stale partial files of dead jobs
}

// ScanReport summarises one SCAN.
type ScanReport struct {
	Volume        string      `json:"volume"`
	Mode          string      `json:"mode"` // incremental | full
	Disks         int         `json:"disks"`
	Files         int         `json:"files_seen"`
	Added         []ScanEntry `json:"added"`    // orphan → catalog (or review)
	Removed       []ScanEntry `json:"removed"`  // catalog entry without a file (external delete)
	Modified      []ScanEntry `json:"modified"` // size/mtime changed → metadata updated
	Corrupt       []ScanEntry `json:"corrupt"`  // full mode: same size/mtime but different hash
	Partials      []ScanEntry `json:"partials"` // stale partial files (dead jobs)
	PartialsFixed int         `json:"partials_removed"`
	DirsRemoved   int         `json:"dirs_removed"`
	Conflicts     int         `json:"conflicts_open"`
	Skipped       []string    `json:"skipped_disks,omitempty"` // offline disks
	DurationMs    int64       `json:"duration_ms"`
}

// ScanEntry is one change found by SCAN.
type ScanEntry struct {
	LogicalPath string `json:"logical_path"`
	Disk        string `json:"disk"`
	Size        int64  `json:"size,omitempty"`
	Note        string `json:"note,omitempty"`
}

var partialRe = regexp.MustCompile(`\.continuum-partial-([a-z]+_[0-9a-z]+)$`)

// Scan reconciles the catalog with what is physically on the disks (direct edits in Finder/Explorer,
// writes through the SMB access point, deletes) — DP09. Offline disks are skipped, never marked deleted.
func (e *Engine) Scan(ctx context.Context, volume string, opt ScanOptions) (*ScanReport, error) {
	e.scanMu.Lock()
	defer e.scanMu.Unlock()
	start := time.Now()
	v, err := e.freshVolume(ctx, volume)
	if err != nil {
		return nil, err
	}
	rep := &ScanReport{Volume: v.Slug, Mode: "incremental", Added: []ScanEntry{}, Removed: []ScanEntry{}, Modified: []ScanEntry{},
		Corrupt: []ScanEntry{}, Partials: []ScanEntry{}}
	if opt.Full {
		rep.Mode = "full"
	}
	onlyDisk := ""
	if opt.Disk != "" {
		d, err := e.C.GetDisk(ctx, opt.Disk)
		if err != nil {
			return nil, err
		}
		onlyDisk = d.ID
	}
	activeJobs := e.activeJobIDs(ctx)
	busy := e.busyPaths(ctx, v.ID)
	seenDirs := map[string]bool{}
	for _, d := range v.Disks {
		if onlyDisk != "" && d.ID != onlyDisk {
			continue
		}
		if d.State == core.StateOffline || d.State == core.StateDisabled || !nodefs.Exists(ctx, e.fsOf(d), d.DataRoot) {
			rep.Skipped = append(rep.Skipped, d.Name)
			continue
		}
		rep.Disks++
		if err := e.scanDisk(ctx, v, d, opt, rep, activeJobs, busy, seenDirs); err != nil {
			return rep, err
		}
	}
	if onlyDisk == "" && len(rep.Skipped) == 0 { // every disk seen: folders deleted outside Continuum leave the catalog
		rep.DirsRemoved = e.pruneDirs(ctx, v.ID, seenDirs)
	}
	n, err := e.DetectConflicts(ctx, v.ID, opt.Full)
	if err != nil {
		return rep, err
	}
	rep.Conflicts = n
	rep.DurationMs = time.Since(start).Milliseconds()
	e.C.DB.Exec(ctx, `UPDATE volumes SET last_scan_at=? WHERE volume_id=?`, store.Now(), v.ID)
	changes := len(rep.Added) + len(rep.Removed) + len(rep.Modified) + len(rep.Corrupt)
	e.C.Audit.Emit(ctx, audit.Event{Kind: "scan.completed", VolumeID: v.ID}, map[string]any{"mode": rep.Mode, "added": len(rep.Added),
		"removed": len(rep.Removed), "modified": len(rep.Modified), "corrupt": len(rep.Corrupt), "conflicts": n})
	if changes > 0 {
		e.C.Audit.Append(ctx, audit.Entry{Action: "scan", Target: v.Slug, VolumeID: v.ID, Result: rep.summary()})
	}
	return rep, nil
}

func (r *ScanReport) summary() string {
	return "added=" + itoa(len(r.Added)) + " removed=" + itoa(len(r.Removed)) + " modified=" + itoa(len(r.Modified)) + " corrupt=" + itoa(len(r.Corrupt))
}

func itoa(n int) string { return strconv.Itoa(n) }

func (e *Engine) scanDisk(ctx context.Context, v core.Volume, d core.Disk, opt ScanOptions, rep *ScanReport, activeJobs map[string]bool, busy map[string]bool, seenDirs map[string]bool) error {
	// catalog state for this disk
	known := map[string]core.Object{} // physical_rel_path → object
	rows, err := e.C.DB.Query(ctx, `SELECT object_id, logical_path, COALESCE(physical_rel_path,''), size_bytes, COALESCE(checksum,''), COALESCE(mtime_ns,0), state
		FROM objects WHERE disk_id=? AND object_type='file'`, d.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var o core.Object
		rows.Scan(&o.ID, &o.LogicalPath, &o.PhysicalRelPath, &o.Size, &o.Checksum, &o.MtimeNs, &o.State)
		known[o.PhysicalRelPath] = o
	}
	rows.Close()

	seen := map[string]bool{}
	dirs := map[string]bool{}
	root := filepath.Clean(d.DataRoot)
	dfs := e.fsOf(d)
	entries, err := dfs.Walk(ctx, root) // on the disk's node; an unreachable node aborts the scan (nothing is tombstoned)
	if err != nil {
		return err
	}
	hash := func(p string) (string, error) { return dfs.Hash(ctx, p) }
	for _, en := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := norm.NFC.String(en.Rel)
		if isInternal(rel) || strings.HasPrefix(rel, ".trash/") {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(en.Rel))
		if en.IsDir {
			dirs["/"+rel] = true
			continue
		}
		base := path.Base(rel)
		if rel == disk.MarkerName || strings.HasPrefix(base, ".probe-") || base == ".DS_Store" || base == "Thumbs.db" || strings.HasPrefix(base, "._") {
			continue
		}
		if m := partialRe.FindStringSubmatch(base); m != nil { // NFR-I03: never treat partials as files
			if !activeJobs[m[1]] {
				rep.Partials = append(rep.Partials, ScanEntry{LogicalPath: "/" + rel, Disk: d.Name, Note: "job " + m[1] + " not active"})
				if opt.FixPartials && dfs.Remove(ctx, p) == nil {
					rep.PartialsFixed++
				}
			}
			continue
		}
		mode := fs.FileMode(en.Mode)
		if !(mode.IsRegular() || mode&fs.ModeSymlink != 0) {
			continue
		}
		rep.Files++
		seen[rel] = true
		logical := "/" + rel
		if busy[logical] {
			continue // being written by a running job — the job commits it
		}
		o, ok := known[rel]
		mtime, size := en.MtimeNs, en.Size
		switch {
		case !ok: // orphan: file added directly on the disk (or through the SMB landing disk)
			obj := core.Object{VolumeID: v.ID, LogicalPath: logical, ParentPath: path.Dir(logical), Name: path.Base(logical), DiskID: d.ID,
				PhysicalRelPath: rel, Size: size, MtimeNs: mtime, Mode: int64(mode.Perm())}
			if opt.Full && mode.IsRegular() {
				if sum, err := hash(p); err == nil {
					obj.Checksum, obj.ChecksumAlgo, obj.VerifiedAt = sum, "blake3", store.Now()
				}
			}
			if err := e.C.UpsertFile(ctx, obj); err != nil {
				return err
			}
			note := "added to catalog"
			if v.OrphanPolicy == "review" {
				e.C.DB.Exec(ctx, `UPDATE objects SET state='review' WHERE volume_id=? AND logical_path=? AND disk_id=?`, v.ID, logical, d.ID)
				note = "needs review"
			}
			rep.Added = append(rep.Added, ScanEntry{LogicalPath: logical, Disk: d.Name, Size: size, Note: note})
		case o.Size != size || (o.MtimeNs != 0 && o.MtimeNs != mtime):
			sum, algo, ver := any(nil), any(nil), any(nil)
			if opt.Full && mode.IsRegular() {
				if s, err := hash(p); err == nil {
					sum, algo, ver = s, "blake3", store.Now()
				}
			}
			e.C.DB.Exec(ctx, `UPDATE objects SET size_bytes=?, mtime_ns=?, checksum=?, checksum_algo=?, verified_at=?, state='ok', updated_at=? WHERE object_id=?`,
				size, mtime, sum, algo, ver, store.Now(), o.ID)
			rep.Modified = append(rep.Modified, ScanEntry{LogicalPath: o.LogicalPath, Disk: d.Name, Size: size, Note: "edited outside Continuum"})
		case opt.Full && o.Checksum != "" && mode.IsRegular():
			if s, err := hash(p); err == nil && s != o.Checksum { // bit rot / silent change (PRD §11.6 변경)
				e.C.DB.Exec(ctx, `UPDATE objects SET state='corrupt', updated_at=? WHERE object_id=?`, store.Now(), o.ID)
				rep.Corrupt = append(rep.Corrupt, ScanEntry{LogicalPath: o.LogicalPath, Disk: d.Name, Note: "checksum differs without size/mtime change"})
			} else if err == nil && o.State == "corrupt" {
				e.C.DB.Exec(ctx, `UPDATE objects SET state='ok', verified_at=? WHERE object_id=?`, store.Now(), o.ID)
			}
		case o.State == "offline" || o.State == "missing":
			e.C.DB.Exec(ctx, `UPDATE objects SET state='ok', updated_at=? WHERE object_id=?`, store.Now(), o.ID)
		}
	}
	// catalog entries whose file is gone while the disk is ONLINE → external delete (tombstone, PRD §11.6)
	for rel, o := range known {
		if seen[rel] || busy[o.LogicalPath] {
			continue
		}
		e.C.DB.Exec(ctx, `INSERT INTO tombstones (tomb_id, volume_id, logical_path, disk_id, size_bytes, checksum, reason, deleted_by, deleted_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, store.NewID("tmb"), v.ID, o.LogicalPath, d.ID, o.Size, nullIf(o.Checksum), "external_delete", "scan", store.Now())
		e.C.DeleteObject(ctx, o.ID)
		rep.Removed = append(rep.Removed, ScanEntry{LogicalPath: o.LogicalPath, Disk: d.Name, Size: o.Size, Note: "deleted outside Continuum"})
	}
	// folders created directly (e.g. in Finder) become visible logical folders
	for dir := range dirs {
		e.C.EnsureDirs(ctx, v.ID, dir)
		seenDirs[dir] = true
	}
	sort.Slice(rep.Added, func(i, j int) bool { return rep.Added[i].LogicalPath < rep.Added[j].LogicalPath })
	return nil
}

// pruneDirs deletes catalog folders that exist on no disk and hold no files.
func (e *Engine) pruneDirs(ctx context.Context, volumeID string, seen map[string]bool) int {
	rows, err := e.C.DB.Query(ctx, `SELECT object_id, logical_path FROM objects WHERE volume_id=? AND object_type='dir' AND state<>'virtual'`, volumeID)
	if err != nil {
		return 0
	}
	type dir struct{ id, p string }
	var gone []dir
	for rows.Next() {
		var d dir
		rows.Scan(&d.id, &d.p)
		if !seen[d.p] && d.p != "/" {
			gone = append(gone, d)
		}
	}
	rows.Close()
	n := 0
	for _, d := range gone {
		var files int
		e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE volume_id=? AND (object_type='file' OR state='virtual') AND logical_path LIKE ? ESCAPE '\'`,
			volumeID, likePrefix(d.p)).Scan(&files)
		if files == 0 && e.C.DeleteObject(ctx, d.id) == nil {
			n++
		}
	}
	return n
}

func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.TrimSuffix(p, "/")) + "/%"
}

func isInternal(rel string) bool {
	return rel == ".trash" || strings.HasPrefix(rel, ".trash/")
}

func (e *Engine) activeJobIDs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := e.C.DB.Query(ctx, `SELECT job_id FROM jobs WHERE state IN ('QUEUED','RUNNING','PAUSED','PAUSED_NEED_SPACE')`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out[id] = true
	}
	return out
}

// busyPaths are logical paths of unfinished items in active jobs ("열린/복사 중 파일", PRD §11.8).
func (e *Engine) busyPaths(ctx context.Context, volumeID string) map[string]bool {
	out := map[string]bool{}
	rows, err := e.C.DB.Query(ctx, `SELECT i.logical_path FROM job_items i JOIN jobs j ON j.job_id=i.job_id
		WHERE j.volume_id=? AND j.state IN ('QUEUED','RUNNING','PAUSED','PAUSED_NEED_SPACE') AND i.state IN ('pending','running','unplaced')`, volumeID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out[p] = true
	}
	return out
}

// ---- Conflict Center (FR-K04, G15, A07) ----

// Candidate is one physical copy of a conflicted logical path.
type Candidate struct {
	ObjectID string `json:"object_id"`
	DiskID   string `json:"disk_id"`
	Disk     string `json:"disk"`
	Ordinal  int    `json:"ordinal"`
	Size     int64  `json:"size"`
	MtimeNs  int64  `json:"mtime_ns"`
	Checksum string `json:"checksum,omitempty"`
	Physical string `json:"physical_path"`
}

// Conflict is a logical path present on more than one disk.
type Conflict struct {
	ID          string      `json:"conflict_id"`
	VolumeID    string      `json:"volume_id"`
	LogicalPath string      `json:"logical_path"`
	Kind        string      `json:"kind"` // duplicate (identical) | conflict (differs)
	Candidates  []Candidate `json:"candidates"`
	State       string      `json:"state"`
	Resolution  string      `json:"resolution,omitempty"`
	CreatedAt   string      `json:"created_at"`
	ResolvedAt  string      `json:"resolved_at,omitempty"`
}

// DetectConflicts classifies logical paths stored on several disks. Nothing is deleted automatically.
// Returns the number of open conflicts.
func (e *Engine) DetectConflicts(ctx context.Context, volumeID string, hashMissing bool) (int, error) {
	rows, err := e.C.DB.Query(ctx, `SELECT o.logical_path FROM objects o WHERE o.volume_id=? AND o.object_type='file'
		GROUP BY o.logical_path HAVING COUNT(DISTINCT o.disk_id) > 1`, volumeID)
	if err != nil {
		return 0, err
	}
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		paths = append(paths, p)
	}
	rows.Close()
	current := map[string]bool{}
	for _, lp := range paths {
		current[lp] = true
		objs, err := e.C.Locate(ctx, volumeID, lp) // lowest ordinal first
		if err != nil {
			continue
		}
		var cands []Candidate
		for _, o := range objs {
			if o.Type != "file" {
				continue
			}
			sum := o.Checksum
			if sum == "" { // conflicts are rare: hash the involved copies to classify reliably
				if od, err := e.C.GetDisk(ctx, o.DiskID); err == nil {
					if s, err := e.fsOf(od).Hash(ctx, o.PhysicalPath); err == nil {
						sum = s
						e.C.DB.Exec(ctx, `UPDATE objects SET checksum=?, checksum_algo='blake3' WHERE object_id=?`, s, o.ID)
					}
				}
			}
			cands = append(cands, Candidate{ObjectID: o.ID, DiskID: o.DiskID, Disk: o.DiskName, Ordinal: o.Ordinal, Size: o.Size, MtimeNs: o.MtimeNs,
				Checksum: sum, Physical: o.PhysicalPath})
		}
		kind := "duplicate"
		for _, c := range cands[1:] {
			if c.Size != cands[0].Size || c.Checksum != cands[0].Checksum || c.Checksum == "" {
				kind = "conflict"
			}
		}
		cj, _ := json.Marshal(cands)
		var id string
		err = e.C.DB.QueryRow(ctx, `SELECT conflict_id FROM conflicts WHERE volume_id=? AND logical_path=? AND state='open'`, volumeID, lp).Scan(&id)
		if err != nil {
			e.C.DB.Exec(ctx, `INSERT INTO conflicts (conflict_id, volume_id, logical_path, kind, candidates_json, state, created_at) VALUES (?,?,?,?,?,'open',?)`,
				store.NewID("cfl"), volumeID, lp, kind, string(cj), store.Now())
			e.C.Audit.Emit(ctx, audit.Event{Kind: "conflict.detected", VolumeID: volumeID}, map[string]any{"path": lp, "kind": kind, "copies": len(cands)})
		} else {
			e.C.DB.Exec(ctx, `UPDATE conflicts SET kind=?, candidates_json=? WHERE conflict_id=?`, kind, string(cj), id)
		}
		// read priority: lowest ordinal is visible; others are flagged (FR-A07)
		for i, c := range cands {
			st := kind
			if i == 0 && kind == "duplicate" {
				st = "ok"
			}
			e.C.DB.Exec(ctx, `UPDATE objects SET state=? WHERE object_id=? AND state IN ('ok','duplicate','conflict')`, st, c.ObjectID)
		}
	}
	// conflicts that disappeared (a copy was deleted by hand) resolve themselves
	open, _ := e.Conflicts(ctx, volumeID, "open")
	for _, c := range open {
		if !current[c.LogicalPath] {
			e.C.DB.Exec(ctx, `UPDATE conflicts SET state='resolved', resolution='auto: single copy left', resolved_by='scan', resolved_at=? WHERE conflict_id=?`, store.Now(), c.ID)
			e.C.DB.Exec(ctx, `UPDATE objects SET state='ok' WHERE volume_id=? AND logical_path=? AND state IN ('duplicate','conflict')`, volumeID, c.LogicalPath)
		}
	}
	var n int
	e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM conflicts WHERE volume_id=? AND state='open'`, volumeID).Scan(&n)
	return n, nil
}

// Conflicts lists conflicts of a volume (state "" = all).
func (e *Engine) Conflicts(ctx context.Context, volumeID, state string) ([]Conflict, error) {
	q := `SELECT conflict_id, volume_id, logical_path, kind, candidates_json, state, COALESCE(resolution,''), created_at, COALESCE(resolved_at,'') FROM conflicts WHERE volume_id=?`
	args := []any{volumeID}
	if state != "" {
		q += ` AND state=?`
		args = append(args, state)
	}
	rows, err := e.C.DB.Query(ctx, q+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conflict{}
	for rows.Next() {
		var c Conflict
		var cj string
		if err := rows.Scan(&c.ID, &c.VolumeID, &c.LogicalPath, &c.Kind, &cj, &c.State, &c.Resolution, &c.CreatedAt, &c.ResolvedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(cj), &c.Candidates)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveConflict applies a user decision: keep = "lowest" | "newest" | "both" | <disk id or name>.
// Removed copies go to the volume trash (never unlinked), "both" renames the others to "name (disk).ext".
func (e *Engine) ResolveConflict(ctx context.Context, id, keep string) (*Conflict, error) {
	var volID string
	if err := e.C.DB.QueryRow(ctx, `SELECT volume_id FROM conflicts WHERE conflict_id=? AND state='open'`, id).Scan(&volID); err != nil {
		return nil, core.Errf(core.CodeNotFound, "open conflict %q not found", id)
	}
	all, err := e.Conflicts(ctx, volID, "open")
	if err != nil {
		return nil, err
	}
	var c *Conflict
	for i := range all {
		if all[i].ID == id {
			c = &all[i]
		}
	}
	if c == nil || len(c.Candidates) < 2 {
		return nil, core.Errf(core.CodeNotFound, "conflict %q has no candidates", id)
	}
	winner := -1
	switch keep {
	case "lowest", "":
		winner = 0
	case "newest":
		winner = 0
		for i, cand := range c.Candidates {
			if cand.MtimeNs > c.Candidates[winner].MtimeNs {
				winner = i
			}
		}
	case "both":
	default:
		for i, cand := range c.Candidates {
			if cand.DiskID == keep || cand.Disk == keep {
				winner = i
			}
		}
		if winner < 0 {
			return nil, core.Errf(core.CodeInvalid, "keep must be lowest|newest|both|<disk> (got %q)", keep)
		}
	}
	v, err := e.C.GetVolume(ctx, volID)
	if err != nil {
		return nil, err
	}
	month := time.Now().UTC().Format("2006-01")
	for i, cand := range c.Candidates {
		if keep == "both" {
			if i == 0 {
				continue
			}
			dir, base := path.Dir(c.LogicalPath), path.Base(c.LogicalPath)
			ext := path.Ext(base)
			np := path.Join(dir, strings.TrimSuffix(base, ext)+" ("+cand.Disk+")"+ext)
			d, err := e.C.GetDisk(ctx, cand.DiskID)
			if err != nil {
				return nil, err
			}
			dst, err := core.PhysicalPath(d.DataRoot, np)
			if err != nil {
				return nil, err
			}
			if err := e.fsOf(d).Rename(ctx, cand.Physical, dst); err != nil {
				return nil, err
			}
			e.C.DB.Exec(ctx, `UPDATE objects SET logical_path=?, name=?, physical_rel_path=?, state='ok', updated_at=? WHERE object_id=?`,
				np, path.Base(np), strings.TrimPrefix(np, "/"), store.Now(), cand.ObjectID)
			continue
		}
		if i == winner {
			continue
		}
		d, err := e.C.GetDisk(ctx, cand.DiskID)
		if err != nil {
			return nil, err
		}
		tomb := store.NewID("tmb")
		rel := filepath.Join(".trash", month, tomb, filepath.Base(cand.Physical))
		dest := filepath.Join(d.DataRoot, rel)
		dfs := e.fsOf(d)
		dfs.MkdirAll(ctx, filepath.Dir(dest))
		if err := dfs.Rename(ctx, cand.Physical, dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		purge := time.Now().UTC().AddDate(0, 0, v.TrashRetentionDays).Format(time.RFC3339Nano)
		e.C.DB.Exec(ctx, `INSERT INTO tombstones (tomb_id, volume_id, logical_path, disk_id, trash_rel_path, size_bytes, checksum, reason, deleted_by, deleted_at, purge_after)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`, tomb, volID, c.LogicalPath, d.ID, filepath.ToSlash(rel), cand.Size, nullIf(cand.Checksum), "unlink", "conflict-resolve", store.Now(), purge)
		e.C.DeleteObject(ctx, cand.ObjectID)
		nodefs.RemoveEmptyParents(ctx, dfs, filepath.Dir(cand.Physical), d.DataRoot)
	}
	e.C.DB.Exec(ctx, `UPDATE objects SET state='ok' WHERE volume_id=? AND logical_path=?`, volID, c.LogicalPath)
	e.C.DB.Exec(ctx, `UPDATE conflicts SET state='resolved', resolution=?, resolved_by='local', resolved_at=? WHERE conflict_id=?`, "keep "+keep, store.Now(), id)
	e.C.Audit.Append(ctx, audit.Entry{Action: "conflict.resolve", Target: c.LogicalPath, VolumeID: volID, Result: "keep " + keep})
	c.State, c.Resolution = "resolved", "keep "+keep
	return c, nil
}

// ResolveDuplicates resolves every open *duplicate* (identical copies) keeping the lowest ordinal.
func (e *Engine) ResolveDuplicates(ctx context.Context, volume string) (int, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return 0, err
	}
	all, err := e.Conflicts(ctx, v.ID, "open")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range all {
		if c.Kind == "duplicate" {
			if _, err := e.ResolveConflict(ctx, c.ID, "lowest"); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// ScanDue runs an incremental SCAN for every volume whose scan_interval_min elapsed (periodic
// reconcile; covers direct edits and SMB writes without a filesystem watcher).
func (e *Engine) ScanDue(ctx context.Context, now time.Time) {
	vols, err := e.C.ListVolumes(ctx)
	if err != nil {
		return
	}
	for _, v := range vols {
		every := v.ScanIntervalMin
		if v.AccessMode == "full-direct" && every > 5 { // FR-A09: direct writes (SMB/Finder) are expected → scan often
			every = 5
		}
		if every <= 0 {
			continue
		}
		if last, err := time.Parse(time.RFC3339Nano, v.LastScanAt); err == nil && now.Sub(last) < time.Duration(every)*time.Minute {
			continue
		}
		e.Scan(ctx, v.ID, ScanOptions{})
	}
}

// OpenConflicts counts open conflicts across all volumes (Overview tile).
func (e *Engine) OpenConflicts(ctx context.Context) int {
	var n int
	e.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM conflicts WHERE state='open'`).Scan(&n)
	return n
}
