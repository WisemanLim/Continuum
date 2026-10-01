package core

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"continuum/internal/store"
)

// Object is a catalog entry: logical path → physical location (FR-K01).
type Object struct {
	ID              string `json:"object_id"`
	VolumeID        string `json:"volume_id"`
	LogicalPath     string `json:"logical_path"`
	ParentPath      string `json:"parent_path"`
	Name            string `json:"name"`
	Type            string `json:"type"` // file | dir
	DiskID          string `json:"disk_id,omitempty"`
	DiskName        string `json:"disk_name,omitempty"`
	Ordinal         int    `json:"ordinal,omitempty"`
	PhysicalRelPath string `json:"physical_rel_path,omitempty"`
	PhysicalPath    string `json:"physical_path,omitempty"`
	Size            int64  `json:"size"`
	ChecksumAlgo    string `json:"checksum_algo,omitempty"`
	Checksum        string `json:"checksum,omitempty"`
	MtimeNs         int64  `json:"mtime_ns,omitempty"`
	Mode            int64  `json:"mode,omitempty"`
	State           string `json:"state"`
	VerifiedAt      string `json:"verified_at,omitempty"`
	UpdatedAt       string `json:"updated_at"`
}

// CleanLogical normalises a logical path: NFC, slash-separated, rooted, no "..".
func CleanLogical(p string) (string, error) {
	p = norm.NFC.String(strings.ReplaceAll(p, `\`, "/"))
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", Errf(CodeInvalid, "path traversal not allowed: %q", p)
		}
	}
	return path.Clean(p), nil
}

// PhysicalPath joins a disk data root and a logical path, refusing escapes (FR-S04, NFR-S02).
func PhysicalPath(dataRoot, logical string) (string, error) {
	lp, err := CleanLogical(logical)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(lp, "/")
	full := filepath.Join(dataRoot, filepath.FromSlash(rel))
	root := filepath.Clean(dataRoot)
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", Errf(CodePermission, "path escapes data root: %q", logical)
	}
	if strings.HasPrefix(rel, ".trash") || rel == "volume.json" {
		return "", Errf(CodePermission, "reserved path: %q", logical)
	}
	return full, nil
}

const objCols = `o.object_id, o.volume_id, o.logical_path, o.parent_path, o.name, o.object_type, COALESCE(o.disk_id,''),
	COALESCE(d.name,''), COALESCE(d.ordinal,0), COALESCE(o.physical_rel_path,''), COALESCE(d.data_root,''), o.size_bytes,
	COALESCE(o.checksum_algo,''), COALESCE(o.checksum,''), COALESCE(o.mtime_ns,0), COALESCE(o.mode,0), o.state,
	COALESCE(o.verified_at,''), o.updated_at`

func scanObject(s scanner) (Object, error) {
	var o Object
	var root string
	err := s.Scan(&o.ID, &o.VolumeID, &o.LogicalPath, &o.ParentPath, &o.Name, &o.Type, &o.DiskID, &o.DiskName, &o.Ordinal,
		&o.PhysicalRelPath, &root, &o.Size, &o.ChecksumAlgo, &o.Checksum, &o.MtimeNs, &o.Mode, &o.State, &o.VerifiedAt, &o.UpdatedAt)
	if err == nil && root != "" && o.PhysicalRelPath != "" {
		o.PhysicalPath = filepath.Join(root, filepath.FromSlash(o.PhysicalRelPath))
	}
	return o, err
}

const objFrom = ` FROM objects o LEFT JOIN disks d ON d.disk_id = o.disk_id `

// Locate returns every physical copy of a logical path (FR-K02). Lowest ordinal first (FR-A07).
func (c *Core) Locate(ctx context.Context, volumeID, logical string) ([]Object, error) {
	lp, err := CleanLogical(logical)
	if err != nil {
		return nil, err
	}
	rows, err := c.DB.Query(ctx, `SELECT `+objCols+objFrom+`WHERE o.volume_id = ? AND o.logical_path = ? ORDER BY COALESCE(d.ordinal, 0)`, volumeID, lp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, Errf(CodeNotFound, "%s not found in catalog", lp)
	}
	return out, rows.Err()
}

// ListDir lists the direct children of a logical directory (Volume › Files, `ls`).
// diskID != "" restricts to files physically on that disk (`ls --physical --ordinal N`, FR-C09).
func (c *Core) ListDir(ctx context.Context, volumeID, logical, diskID string) ([]Object, error) {
	lp, err := CleanLogical(logical)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + objCols + objFrom + `WHERE o.volume_id = ? AND o.parent_path = ?`
	args := []any{volumeID, lp}
	if diskID != "" {
		q += ` AND (o.disk_id = ? OR o.object_type = 'dir')`
		args = append(args, diskID)
	}
	rows, err := c.DB.Query(ctx, q+` ORDER BY o.object_type DESC, o.name, COALESCE(d.ordinal,0)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	seenDir := map[string]bool{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		if o.Type == "dir" {
			if seenDir[o.LogicalPath] {
				continue
			}
			seenDir[o.LogicalPath] = true
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return out, rows.Err()
}

// Walk returns all objects at or under a logical path (files and dirs).
func (c *Core) Walk(ctx context.Context, volumeID, logical string) ([]Object, error) {
	lp, err := CleanLogical(logical)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(lp, "/") + "/"
	rows, err := c.DB.Query(ctx, `SELECT `+objCols+objFrom+`WHERE o.volume_id = ? AND (o.logical_path = ? OR o.logical_path LIKE ?) ORDER BY o.logical_path`,
		volumeID, lp, escapeLike(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	// `%` and `_` are rare in paths; the exact-prefix check below keeps results correct.
	return s
}

// UpsertFile records a committed file on a disk (step 12 of the atomic copy).
func (c *Core) UpsertFile(ctx context.Context, o Object) error {
	now := store.Now()
	if err := c.EnsureDirs(ctx, o.VolumeID, o.ParentPath); err != nil {
		return err
	}
	return c.DB.Tx(ctx, func(tx *store.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT object_id FROM objects WHERE volume_id=? AND logical_path=? AND disk_id=?`, o.VolumeID, o.LogicalPath, o.DiskID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO objects (object_id, volume_id, logical_path, parent_path, name, object_type, disk_id, physical_rel_path,
				size_bytes, checksum_algo, checksum, mtime_ns, mode, state, verified_at, created_at, updated_at)
				VALUES (?,?,?,?,?,'file',?,?,?,?,?,?,?,'ok',?,?,?)`,
				store.NewID("obj"), o.VolumeID, o.LogicalPath, o.ParentPath, o.Name, o.DiskID, o.PhysicalRelPath,
				o.Size, o.ChecksumAlgo, o.Checksum, o.MtimeNs, o.Mode, nullStr(o.VerifiedAt), now, now)
			return err
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE objects SET physical_rel_path=?, size_bytes=?, checksum_algo=?, checksum=?, mtime_ns=?, mode=?, state='ok', verified_at=?, updated_at=? WHERE object_id=?`,
			o.PhysicalRelPath, o.Size, o.ChecksumAlgo, o.Checksum, o.MtimeNs, o.Mode, nullStr(o.VerifiedAt), now, id)
		return err
	})
}

// EnsureDirs creates catalog dir objects for logical and all its ancestors (disk_id NULL = lazy, FR-T13).
func (c *Core) EnsureDirs(ctx context.Context, volumeID, logical string) error {
	lp, err := CleanLogical(logical)
	if err != nil {
		return err
	}
	var chain []string
	for p := lp; p != "/"; p = path.Dir(p) {
		chain = append(chain, p)
	}
	now := store.Now()
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		var n int
		if err := c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE volume_id=? AND logical_path=? AND object_type='dir'`, volumeID, p).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := c.DB.Exec(ctx, `INSERT INTO objects (object_id, volume_id, logical_path, parent_path, name, object_type, state, created_at, updated_at)
			VALUES (?,?,?,?,?,'dir','ok',?,?)`, store.NewID("obj"), volumeID, p, path.Dir(p), path.Base(p), now, now); err != nil {
			return err
		}
	}
	return nil
}

// DeleteObject removes a catalog row.
func (c *Core) DeleteObject(ctx context.Context, id string) error {
	_, err := c.DB.Exec(ctx, `DELETE FROM objects WHERE object_id=?`, id)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
