package transfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/nodefs"
	"continuum/internal/store"
)

// Mkdir creates a logical directory (FR-T13). ordinal > 0 also creates it physically on that disk now;
// otherwise it is created lazily when the first file lands.
func (e *Engine) Mkdir(ctx context.Context, volume, logical string, ordinal int) (core.Object, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return core.Object{}, err
	}
	lp, err := core.CleanLogical(logical)
	if err != nil {
		return core.Object{}, err
	}
	if lp == "/" {
		return core.Object{}, core.Errf(core.CodeInvalid, "cannot create root")
	}
	if objs, err := e.C.Locate(ctx, v.ID, lp); err == nil {
		for _, o := range objs {
			if o.Type == "file" {
				return core.Object{}, core.Errf(core.CodeConflict, "%s exists as a file", lp)
			}
		}
	}
	if ordinal > 0 {
		var target *core.Disk
		for i := range v.Disks {
			if v.Disks[i].Ordinal == ordinal {
				target = &v.Disks[i]
			}
		}
		if target == nil {
			return core.Object{}, core.Errf(core.CodeNotFound, "no disk with ordinal %d", ordinal)
		}
		p, err := core.PhysicalPath(target.DataRoot, lp)
		if err != nil {
			return core.Object{}, err
		}
		if err := e.fsOf(*target).MkdirAll(ctx, p); err != nil {
			return core.Object{}, err
		}
	}
	if err := e.C.EnsureDirs(ctx, v.ID, lp); err != nil {
		return core.Object{}, err
	}
	if ordinal == 0 { // logical-only until a file lands: SCAN must not prune it
		e.C.DB.Exec(ctx, `UPDATE objects SET state='virtual' WHERE volume_id=? AND logical_path=? AND object_type='dir'`, v.ID, lp)
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: "mkdir", Target: lp, VolumeID: v.ID, Result: "ok"})
	objs, err := e.C.Locate(ctx, v.ID, lp)
	if err != nil {
		return core.Object{}, err
	}
	return objs[0], nil
}

// Rename renames a file or folder on every disk that holds it; rolls back on failure (FR-T14).
func (e *Engine) Rename(ctx context.Context, volume, from, to string) error {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return err
	}
	src, err := core.CleanLogical(from)
	if err != nil {
		return err
	}
	dst, err := core.CleanLogical(to)
	if err != nil {
		return err
	}
	if src == "/" || dst == "/" || src == dst || strings.HasPrefix(dst, src+"/") {
		return core.Errf(core.CodeInvalid, "invalid rename %s → %s", src, dst)
	}
	if objs, _ := e.C.Walk(ctx, v.ID, dst); len(objs) > 0 {
		return core.Errf(core.CodeConflict, "%s already exists", dst)
	}
	objs, err := e.C.Walk(ctx, v.ID, src)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return core.Errf(core.CodeNotFound, "%s not found", src)
	}
	for _, o := range objs {
		if o.State == "offline" {
			return core.Errf(core.CodeOffline, "%s is on an offline disk", o.LogicalPath)
		}
	}
	// physical renames: one per disk holding src (top-level path only; children move with it)
	type done struct {
		fs       nodefs.FS
		from, to string
	}
	var applied []done
	rollback := func() {
		for i := len(applied) - 1; i >= 0; i-- {
			applied[i].fs.Rename(ctx, applied[i].to, applied[i].from)
		}
	}
	for _, d := range v.Disks {
		p, err := core.PhysicalPath(d.DataRoot, src)
		if err != nil {
			return err
		}
		dfs := e.fsOf(d)
		if _, err := dfs.Stat(ctx, p); err != nil {
			continue
		}
		q, err := core.PhysicalPath(d.DataRoot, dst)
		if err != nil {
			rollback()
			return err
		}
		if err := dfs.MkdirAll(ctx, filepath.Dir(q)); err != nil {
			rollback()
			return err
		}
		if err := dfs.Rename(ctx, p, q); err != nil {
			rollback()
			return core.Errf(core.CodeConflict, "rename on %s failed: %v", d.Name, err)
		}
		applied = append(applied, done{dfs, p, q})
	}
	now := store.Now()
	err = e.C.DB.Tx(ctx, func(tx *store.Tx) error {
		for _, o := range objs {
			np := dst + strings.TrimPrefix(o.LogicalPath, src)
			rel := ""
			if o.Type == "file" {
				rel = strings.TrimPrefix(np, "/")
			}
			if _, err := tx.Exec(ctx, `UPDATE objects SET logical_path=?, parent_path=?, name=?, physical_rel_path=?, updated_at=? WHERE object_id=?`,
				np, path.Dir(np), path.Base(np), nullIfEmpty(rel), now, o.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		rollback()
		return err
	}
	if err := e.C.EnsureDirs(ctx, v.ID, path.Dir(dst)); err != nil {
		return err
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: "rename", Target: dst, Source: src, VolumeID: v.ID, Result: "ok"})
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DeleteResult summarises a delete.
type DeleteResult struct {
	Files     int      `json:"files"`
	Bytes     int64    `json:"bytes"`
	Permanent bool     `json:"permanent"`
	Tombs     []string `json:"tombstones,omitempty"`
}

// Delete removes a logical file/folder (FR-T12). Default: move to the per-disk volume trash
// (<data_root>/.trash/<yyyy-mm>/<tomb_id>/) with a tombstone; permanent=true unlinks.
func (e *Engine) Delete(ctx context.Context, volume, logical string, recursive, permanent bool) (*DeleteResult, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return nil, err
	}
	lp, err := core.CleanLogical(logical)
	if err != nil {
		return nil, err
	}
	if lp == "/" {
		return nil, core.Errf(core.CodeInvalid, "refusing to delete the volume root")
	}
	objs, err := e.C.Walk(ctx, v.ID, lp)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, core.Errf(core.CodeNotFound, "%s not found", lp)
	}
	isDir := false
	for _, o := range objs {
		if o.LogicalPath == lp && o.Type == "dir" {
			isDir = true
		}
	}
	if isDir && len(objs) > 1 && !recursive {
		return nil, core.Errf(core.CodeInvalid, "%s is a non-empty directory — use recursive", lp)
	}
	res := &DeleteResult{Permanent: permanent}
	month := time.Now().UTC().Format("2006-01")
	purgeAfter := time.Now().UTC().AddDate(0, 0, v.TrashRetentionDays).Format(time.RFC3339Nano)
	for _, o := range objs {
		if o.Type != "file" {
			continue
		}
		if o.State == "offline" {
			return res, core.Errf(core.CodeOffline, "%s is on an offline disk", o.LogicalPath)
		}
		d, err := e.C.GetDisk(ctx, o.DiskID)
		if err != nil {
			return res, err
		}
		dfs := e.fsOf(d)
		if permanent {
			if err := dfs.Remove(ctx, o.PhysicalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
		} else {
			tomb := store.NewID("tmb")
			rel := filepath.Join(".trash", month, tomb, filepath.Base(o.PhysicalPath))
			dest := filepath.Join(d.DataRoot, rel)
			if err := dfs.MkdirAll(ctx, filepath.Dir(dest)); err != nil {
				return res, err
			}
			if err := dfs.Rename(ctx, o.PhysicalPath, dest); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
			if _, err := e.C.DB.Exec(ctx, `INSERT INTO tombstones (tomb_id, volume_id, logical_path, disk_id, trash_rel_path, size_bytes, checksum, reason, deleted_by, deleted_at, purge_after)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`, tomb, v.ID, o.LogicalPath, d.ID, filepath.ToSlash(rel), o.Size, nullIfEmpty(o.Checksum), "unlink", "local", store.Now(), purgeAfter); err != nil {
				return res, err
			}
			res.Tombs = append(res.Tombs, tomb)
		}
		nodefs.RemoveEmptyParents(ctx, dfs, filepath.Dir(o.PhysicalPath), d.DataRoot)
		e.C.DeleteObject(ctx, o.ID)
		res.Files++
		res.Bytes += o.Size
	}
	for _, o := range objs {
		if o.Type == "dir" {
			e.C.DeleteObject(ctx, o.ID)
		}
	}
	// remove now-empty physical directories on every disk
	for _, d := range v.Disks {
		if d.State == core.StateOffline {
			continue
		}
		if p, err := core.PhysicalPath(d.DataRoot, lp); err == nil {
			dfs := e.fsOf(d)
			dfs.Remove(ctx, p)
			nodefs.RemoveEmptyParents(ctx, dfs, filepath.Dir(p), d.DataRoot)
		}
	}
	action := "delete.trash"
	if permanent {
		action = "delete.permanent"
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: action, Target: lp, VolumeID: v.ID, Result: fmt.Sprintf("%d files", res.Files)})
	return res, nil
}

// Tomb is a trash entry (CTM-TRF-030).
type Tomb struct {
	ID          string `json:"tomb_id"`
	LogicalPath string `json:"logical_path"`
	DiskID      string `json:"disk_id"`
	Size        int64  `json:"size"`
	DeletedAt   string `json:"deleted_at"`
	PurgeAfter  string `json:"purge_after"`
	Reason      string `json:"reason"`
	trashRel    string
	checksum    string
}

func (e *Engine) tombs(ctx context.Context, where string, args ...any) ([]Tomb, error) {
	rows, err := e.C.DB.Query(ctx, `SELECT tomb_id, logical_path, COALESCE(disk_id,''), size_bytes, deleted_at, COALESCE(purge_after,''), reason,
		COALESCE(trash_rel_path,''), COALESCE(checksum,'') FROM tombstones WHERE `+where+` ORDER BY deleted_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tomb{}
	for rows.Next() {
		var t Tomb
		if err := rows.Scan(&t.ID, &t.LogicalPath, &t.DiskID, &t.Size, &t.DeletedAt, &t.PurgeAfter, &t.Reason, &t.trashRel, &t.checksum); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Trash lists tombstones of a volume.
func (e *Engine) Trash(ctx context.Context, volume string) ([]Tomb, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return nil, err
	}
	return e.tombs(ctx, `volume_id = ? AND reason = 'unlink'`, v.ID)
}

// Restore moves a trashed file back to its logical path (trash restore <id>).
func (e *Engine) Restore(ctx context.Context, tombID string) (core.Object, error) {
	ts, err := e.tombs(ctx, `tomb_id = ?`, tombID)
	if err != nil {
		return core.Object{}, err
	}
	if len(ts) == 0 {
		return core.Object{}, core.Errf(core.CodeNotFound, "trash entry %q not found", tombID)
	}
	t := ts[0]
	var volID string
	e.C.DB.QueryRow(ctx, `SELECT volume_id FROM tombstones WHERE tomb_id=?`, tombID).Scan(&volID)
	d, err := e.C.GetDisk(ctx, t.DiskID)
	if err != nil {
		return core.Object{}, err
	}
	if objs, err := e.C.Locate(ctx, volID, t.LogicalPath); err == nil && len(objs) > 0 {
		return core.Object{}, core.Errf(core.CodeConflict, "%s exists again — rename it before restoring", t.LogicalPath)
	}
	src := filepath.Join(d.DataRoot, filepath.FromSlash(t.trashRel))
	dst, err := core.PhysicalPath(d.DataRoot, t.LogicalPath)
	if err != nil {
		return core.Object{}, err
	}
	dfs := e.fsOf(d)
	fi, err := dfs.Stat(ctx, src)
	if err != nil {
		return core.Object{}, core.Errf(core.CodeNotFound, "trashed file missing: %v", err)
	}
	if err := dfs.MkdirAll(ctx, filepath.Dir(dst)); err != nil {
		return core.Object{}, err
	}
	if err := dfs.Rename(ctx, src, dst); err != nil {
		return core.Object{}, err
	}
	nodefs.RemoveEmptyParents(ctx, dfs, filepath.Dir(src), d.DataRoot)
	o := core.Object{VolumeID: volID, LogicalPath: t.LogicalPath, ParentPath: path.Dir(t.LogicalPath), Name: path.Base(t.LogicalPath),
		DiskID: d.ID, PhysicalRelPath: strings.TrimPrefix(t.LogicalPath, "/"), Size: fi.Size, MtimeNs: fi.MtimeNs,
		Mode: int64(os.FileMode(fi.Mode).Perm()), Checksum: t.checksum}
	if t.checksum != "" {
		o.ChecksumAlgo = "blake3"
	}
	if err := e.C.UpsertFile(ctx, o); err != nil {
		return o, err
	}
	e.C.DB.Exec(ctx, `DELETE FROM tombstones WHERE tomb_id=?`, tombID)
	e.C.Audit.Append(ctx, audit.Entry{Action: "trash.restore", Target: t.LogicalPath, VolumeID: volID, Result: "ok"})
	objs, _ := e.C.Locate(ctx, volID, t.LogicalPath)
	if len(objs) > 0 {
		return objs[0], nil
	}
	return o, nil
}

// Purge permanently deletes trash entries older than olderThan (0 = past their purge_after).
func (e *Engine) Purge(ctx context.Context, volume string, olderThan time.Duration) (int, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return 0, err
	}
	var ts []Tomb
	if olderThan > 0 {
		ts, err = e.tombs(ctx, `volume_id = ? AND reason='unlink' AND deleted_at < ?`, v.ID, time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano))
	} else {
		ts, err = e.tombs(ctx, `volume_id = ? AND reason='unlink' AND purge_after < ?`, v.ID, store.Now())
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range ts {
		d, err := e.C.GetDisk(ctx, t.DiskID)
		if err == nil {
			p := filepath.Join(d.DataRoot, filepath.FromSlash(t.trashRel))
			dfs := e.fsOf(d)
			dfs.Remove(ctx, p)
			nodefs.RemoveEmptyParents(ctx, dfs, filepath.Dir(p), d.DataRoot)
		}
		e.C.DB.Exec(ctx, `DELETE FROM tombstones WHERE tomb_id=?`, t.ID)
		n++
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: "trash.purge", VolumeID: v.ID, Result: fmt.Sprintf("%d entries", n)})
	return n, nil
}

// VerifyReport is the VERIFY result (FR-T03, §11.8). Mismatches are reported, never auto-fixed.
type VerifyReport struct {
	Checked    int           `json:"checked"`
	OK         int           `json:"ok"`
	Mismatched []VerifyIssue `json:"mismatched"`
	Missing    []VerifyIssue `json:"missing"`
	Unhashed   int           `json:"unhashed"`
}

type VerifyIssue struct {
	LogicalPath string `json:"logical_path"`
	Disk        string `json:"disk"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
}

// Verify re-hashes catalog files under logical (or all files of one disk if diskRef != "").
func (e *Engine) Verify(ctx context.Context, volume, logical, diskRef string) (*VerifyReport, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return nil, err
	}
	if logical == "" {
		logical = "/"
	}
	objs, err := e.C.Walk(ctx, v.ID, logical)
	if err != nil {
		return nil, err
	}
	diskID := ""
	if diskRef != "" {
		d, err := e.C.GetDisk(ctx, diskRef)
		if err != nil {
			return nil, err
		}
		diskID = d.ID
	}
	r := &VerifyReport{Mismatched: []VerifyIssue{}, Missing: []VerifyIssue{}}
	now := store.Now()
	for _, o := range objs {
		if o.Type != "file" || (diskID != "" && o.DiskID != diskID) || o.State == "offline" {
			continue
		}
		r.Checked++
		if o.Checksum == "" {
			r.Unhashed++
			continue
		}
		var sum string
		od, err := e.C.GetDisk(ctx, o.DiskID)
		if err == nil {
			sum, err = e.fsOf(od).Hash(ctx, o.PhysicalPath) // hashed on the node that holds the data
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			r.Missing = append(r.Missing, VerifyIssue{LogicalPath: o.LogicalPath, Disk: o.DiskName})
			e.C.DB.Exec(ctx, `UPDATE objects SET state='missing', updated_at=? WHERE object_id=?`, now, o.ID)
		case err != nil:
			return r, err
		case sum != o.Checksum:
			r.Mismatched = append(r.Mismatched, VerifyIssue{LogicalPath: o.LogicalPath, Disk: o.DiskName, Expected: o.Checksum, Actual: sum})
			e.C.DB.Exec(ctx, `UPDATE objects SET state='modified', updated_at=? WHERE object_id=?`, now, o.ID)
		default:
			r.OK++
			e.C.DB.Exec(ctx, `UPDATE objects SET verified_at=?, state='ok' WHERE object_id=?`, now, o.ID)
		}
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: "verify", Target: logical, VolumeID: v.ID,
		Result: fmt.Sprintf("checked=%d ok=%d mismatched=%d missing=%d", r.Checked, r.OK, len(r.Mismatched), len(r.Missing))})
	return r, nil
}
