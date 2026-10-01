package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"continuum/internal/store"
)

const volCols = `volume_id, name, slug, COALESCE(mount_path,''), placement_policy, min_free_bytes, allow_spillover, access_mode,
	trash_retention_days, scan_interval_min, revision, created_at, updated_at, orphan_policy, COALESCE(last_scan_at,'')`

func scanVolume(s scanner) (Volume, error) {
	var v Volume
	var spill int
	err := s.Scan(&v.ID, &v.Name, &v.Slug, &v.MountPath, &v.PlacementPolicy, &v.MinFreeBytes, &spill, &v.AccessMode,
		&v.TrashRetentionDays, &v.ScanIntervalMin, &v.Revision, &v.CreatedAt, &v.UpdatedAt, &v.OrphanPolicy, &v.LastScanAt)
	v.AllowSpillover = spill == 1
	return v, err
}

// CreateVolumeRequest is the single-transaction CreateVolume (FR-V01, V11).
type CreateVolumeRequest struct {
	Name            string   `json:"name"`
	MountPath       string   `json:"mount_path,omitempty"`
	PlacementPolicy string   `json:"placement_policy,omitempty"`
	MinFreeBytes    *int64   `json:"min_free_bytes,omitempty"`
	AllowSpillover  *bool    `json:"allow_spillover,omitempty"`
	DiskIDs         []string `json:"disk_ids"` // ordered: index 0 = ordinal 1
}

func (c *Core) CreateVolume(ctx context.Context, req CreateVolumeRequest) (Volume, error) {
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return Volume{}, Errf(CodeInvalid, "volume name must be 1–64 characters")
	}
	slug := Slugify(name)
	if slug == "" { // non-ASCII name (e.g. 한글) → vr001-style id (FR-V13)
		var n int
		_ = c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM volumes`).Scan(&n)
		slug = fmt.Sprintf("vr%03d", n+1)
	}
	if len(req.DiskIDs) == 0 {
		return Volume{}, Errf(CodeInvalid, "at least one disk is required")
	}
	policy := req.PlacementPolicy
	if policy == "" {
		policy = PolicyFillInOrder
	}
	if !validPolicies[policy] {
		return Volume{}, Errf(CodeInvalid, "unknown placement policy %q", policy)
	}
	minFree := int64(100 << 30) // UI default 100 GB (U30); CLI/API may override
	if req.MinFreeBytes != nil {
		minFree = *req.MinFreeBytes
	}
	if minFree < 0 {
		return Volume{}, Errf(CodeInvalid, "min_free_bytes must be >= 0")
	}
	spill := true
	if req.AllowSpillover != nil {
		spill = *req.AllowSpillover
	}
	mount := req.MountPath
	if mount == "" {
		mount = defaultMountPath(slug)
	}
	id := store.NewID("vol")
	now := store.Now()
	var diskIDs []string
	err := c.DB.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM volumes WHERE name = ? OR slug = ?`, name, slug).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return Errf(CodeConflict, "volume name %q already exists", name)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO volumes (volume_id, name, slug, mount_path, placement_policy, min_free_bytes, allow_spillover, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, id, name, slug, mount, policy, minFree, boolInt(spill), now, now); err != nil {
			return err
		}
		for i, ref := range req.DiskIDs {
			did, vol, err := diskRef(ctx, tx, ref)
			if err != nil {
				return err
			}
			if vol != "" {
				return Errf(CodeConflict, "disk %s already belongs to another volume", ref)
			}
			if _, err := tx.Exec(ctx, `UPDATE disks SET volume_id=?, ordinal=?, updated_at=? WHERE disk_id=?`, id, i+1, now, did); err != nil {
				return err
			}
			diskIDs = append(diskIDs, did)
		}
		return nil
	})
	if err != nil {
		return Volume{}, err
	}
	for _, did := range diskIDs {
		_ = c.writeMarker(ctx, did)
	}
	c.audit(ctx, "volume.create", id, id, "ok")
	c.event(ctx, "volume.created", id, "", "", map[string]any{"name": name, "disks": len(diskIDs)})
	return c.GetVolume(ctx, id)
}

func diskRef(ctx context.Context, tx *store.Tx, ref string) (id, volume string, err error) {
	var vol sql.NullString
	err = tx.QueryRow(ctx, `SELECT disk_id, volume_id FROM disks WHERE disk_id = ? OR name = ?`, ref, ref).Scan(&id, &vol)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", Errf(CodeNotFound, "disk %q not found", ref)
	}
	return id, vol.String, err
}

func defaultMountPath(slug string) string {
	switch goos {
	case "darwin":
		return "/Volumes/" + slug
	case "windows":
		return `\\controller\` + slug
	default:
		return "/mnt/" + slug
	}
}

// ListVolumes returns volumes with disks + summary.
func (c *Core) ListVolumes(ctx context.Context) ([]Volume, error) {
	rows, err := c.DB.Query(ctx, `SELECT `+volCols+` FROM volumes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var vs []Volume
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		vs = append(vs, v)
	}
	rows.Close()
	out := make([]Volume, 0, len(vs))
	for _, v := range vs {
		if err := c.fillVolume(ctx, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetVolume finds by id, slug or name.
func (c *Core) GetVolume(ctx context.Context, ref string) (Volume, error) {
	v, err := scanVolume(c.DB.QueryRow(ctx, `SELECT `+volCols+` FROM volumes WHERE volume_id = ? OR slug = ? OR name = ?`, ref, ref, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return v, Errf(CodeNotFound, "volume %q not found", ref)
	}
	if err != nil {
		return v, err
	}
	return v, c.fillVolume(ctx, &v)
}

func (c *Core) fillVolume(ctx context.Context, v *Volume) error {
	ds, err := c.ListDisks(ctx, DiskFilter{VolumeID: v.ID})
	if err != nil {
		return err
	}
	v.Disks = ds
	v.Summary = Summarize(ds)
	_ = c.DB.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes),0) FROM objects WHERE volume_id = ? AND object_type = 'file'`, v.ID).
		Scan(&v.Summary.ObjectCount, &v.Summary.StoredBytes)
	v.Warnings = nil
	shared := map[string][]string{} // folders on the same physical volume share free space
	for _, d := range ds {
		if d.NativeDiskID != "" {
			shared[d.NativeDiskID] = append(shared[d.NativeDiskID], d.Name)
		}
	}
	for _, names := range shared {
		if len(names) > 1 {
			v.Warnings = append(v.Warnings, fmt.Sprintf("disks %s are folders on the same physical volume — they share free space", strings.Join(names, ", ")))
		}
	}
	for _, d := range ds {
		if d.Role == RolePortable || d.Role == RoleImport {
			v.Warnings = append(v.Warnings, fmt.Sprintf("disk %s has role %s — excluded from automatic placement (FR-V09)", d.Name, d.Role))
		}
		if d.State != StateOnline {
			v.Warnings = append(v.Warnings, fmt.Sprintf("disk %s is %s", d.Name, d.State))
		}
	}
	return nil
}

// UpdateVolumeRequest patches configuration (FR-V12~V17) with optimistic locking (FR-V20).
type UpdateVolumeRequest struct {
	ExpectedRevision   int64   `json:"expected_revision"`
	Name               *string `json:"name,omitempty"`
	MountPath          *string `json:"mount_path,omitempty"`
	PlacementPolicy    *string `json:"placement_policy,omitempty"`
	MinFreeBytes       *int64  `json:"min_free_bytes,omitempty"`
	AllowSpillover     *bool   `json:"allow_spillover,omitempty"`
	AccessMode         *string `json:"access_mode,omitempty"`
	TrashRetentionDays *int    `json:"trash_retention_days,omitempty"`
	ScanIntervalMin    *int    `json:"scan_interval_min,omitempty"`
	OrphanPolicy       *string `json:"orphan_policy,omitempty"` // auto | review (FR-K03)
}

func (c *Core) UpdateVolume(ctx context.Context, ref string, req UpdateVolumeRequest) (Volume, error) {
	v, err := c.GetVolume(ctx, ref)
	if err != nil {
		return v, err
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != v.Revision {
		return v, revisionErr(v.Revision)
	}
	sets, args := []string{}, []any{}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if l := utf8.RuneCountInString(n); l < 1 || l > 64 {
			return v, Errf(CodeInvalid, "volume name must be 1–64 characters")
		}
		sets, args = append(sets, "name=?"), append(args, n)
		if s := Slugify(n); s != "" {
			sets, args = append(sets, "slug=?"), append(args, s)
		}
	}
	if req.MountPath != nil {
		sets, args = append(sets, "mount_path=?"), append(args, *req.MountPath)
	}
	if req.PlacementPolicy != nil {
		if !validPolicies[*req.PlacementPolicy] {
			return v, Errf(CodeInvalid, "unknown placement policy %q", *req.PlacementPolicy)
		}
		sets, args = append(sets, "placement_policy=?"), append(args, *req.PlacementPolicy)
	}
	if req.MinFreeBytes != nil {
		if *req.MinFreeBytes < 0 {
			return v, Errf(CodeInvalid, "min_free_bytes must be >= 0")
		}
		sets, args = append(sets, "min_free_bytes=?"), append(args, *req.MinFreeBytes)
	}
	if req.AllowSpillover != nil {
		sets, args = append(sets, "allow_spillover=?"), append(args, boolInt(*req.AllowSpillover))
	}
	if req.AccessMode != nil {
		switch *req.AccessMode {
		case "read-centric", "full-direct", "blocked":
		default:
			return v, Errf(CodeInvalid, "access_mode must be read-centric|full-direct|blocked")
		}
		sets, args = append(sets, "access_mode=?"), append(args, *req.AccessMode)
	}
	if req.TrashRetentionDays != nil {
		sets, args = append(sets, "trash_retention_days=?"), append(args, *req.TrashRetentionDays)
	}
	if req.ScanIntervalMin != nil {
		if *req.ScanIntervalMin < 1 {
			return v, Errf(CodeInvalid, "scan_interval_min must be >= 1")
		}
		sets, args = append(sets, "scan_interval_min=?"), append(args, *req.ScanIntervalMin)
	}
	if req.OrphanPolicy != nil {
		if *req.OrphanPolicy != "auto" && *req.OrphanPolicy != "review" {
			return v, Errf(CodeInvalid, "orphan_policy must be auto|review")
		}
		sets, args = append(sets, "orphan_policy=?"), append(args, *req.OrphanPolicy)
	}
	if len(sets) == 0 {
		return v, nil
	}
	sets = append(sets, "revision=revision+1", "updated_at=?")
	args = append(args, store.Now(), v.ID, v.Revision)
	res, err := c.DB.Exec(ctx, `UPDATE volumes SET `+strings.Join(sets, ", ")+` WHERE volume_id=? AND revision=?`, args...)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return v, Errf(CodeConflict, "volume name already exists")
		}
		return v, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		cur, _ := c.GetVolume(ctx, v.ID)
		return cur, revisionErr(cur.Revision)
	}
	c.audit(ctx, "volume.update", v.ID, v.ID, "ok")
	return c.GetVolume(ctx, v.ID)
}

func revisionErr(current int64) error {
	e := Errf(CodeRevisionMismatch, "volume was changed by another session (current revision %d) — reload", current)
	e.Info = map[string]any{"current_revision": current}
	return e
}

// SetDisksRequest replaces the ordered membership (ReorderDisks, FR-V19·D08, D42).
// New disks are added; omitted disks are removed only if they hold no catalog objects.
type SetDisksRequest struct {
	DiskIDs          []string `json:"disk_ids"`
	ExpectedRevision int64    `json:"expected_revision"`
}

func (c *Core) SetDisks(ctx context.Context, ref string, req SetDisksRequest) (Volume, error) {
	v, err := c.GetVolume(ctx, ref)
	if err != nil {
		return v, err
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != v.Revision {
		return v, revisionErr(v.Revision)
	}
	if len(req.DiskIDs) == 0 {
		return v, Errf(CodeInvalid, "a volume needs at least one disk")
	}
	now := store.Now()
	var touched []string
	err = c.DB.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.Exec(ctx, `UPDATE volumes SET revision=revision+1, updated_at=? WHERE volume_id=? AND revision=?`, now, v.ID, v.Revision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return revisionErr(v.Revision + 1)
		}
		keep := map[string]bool{}
		var ordered []string
		for _, ref := range req.DiskIDs {
			did, vol, err := diskRef(ctx, tx, ref)
			if err != nil {
				return err
			}
			if vol != "" && vol != v.ID {
				return Errf(CodeConflict, "disk %s belongs to another volume", ref)
			}
			if keep[did] {
				return Errf(CodeInvalid, "disk %s listed twice", ref)
			}
			keep[did] = true
			ordered = append(ordered, did)
		}
		for _, d := range v.Disks {
			if keep[d.ID] {
				continue
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE disk_id = ?`, d.ID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return Errf(CodeConflict, "disk %s holds %d object(s) — drain before removing it from the volume", d.Name, n)
			}
			if _, err := tx.Exec(ctx, `UPDATE disks SET volume_id=NULL, ordinal=NULL, updated_at=? WHERE disk_id=?`, now, d.ID); err != nil {
				return err
			}
			touched = append(touched, d.ID)
		}
		// clear ordinals first so UNIQUE(volume_id, ordinal) never collides mid-update
		if _, err := tx.Exec(ctx, `UPDATE disks SET ordinal=NULL WHERE volume_id=?`, v.ID); err != nil {
			return err
		}
		for i, did := range ordered {
			if _, err := tx.Exec(ctx, `UPDATE disks SET volume_id=?, ordinal=?, updated_at=? WHERE disk_id=?`, v.ID, i+1, now, did); err != nil {
				return err
			}
			touched = append(touched, did)
		}
		return nil
	})
	if err != nil {
		return v, err
	}
	for _, did := range touched {
		_ = c.writeMarker(ctx, did)
	}
	c.audit(ctx, "volume.reorder", v.ID, v.ID, strings.Join(req.DiskIDs, ","))
	c.event(ctx, "volume.reordered", v.ID, "", "", map[string]any{"disk_ids": req.DiskIDs})
	return c.GetVolume(ctx, v.ID)
}

// AddDiskToVolume inserts a disk at ordinal pos (0 = append).
func (c *Core) AddDiskToVolume(ctx context.Context, volRef, diskRef string, pos int) error {
	v, err := c.GetVolume(ctx, volRef)
	if err != nil {
		return err
	}
	d, err := c.GetDisk(ctx, diskRef)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(v.Disks)+1)
	for _, x := range v.Disks {
		ids = append(ids, x.ID)
	}
	if pos <= 0 || pos > len(ids) {
		ids = append(ids, d.ID)
	} else {
		ids = append(ids[:pos-1], append([]string{d.ID}, ids[pos-1:]...)...)
	}
	_, err = c.SetDisks(ctx, v.ID, SetDisksRequest{DiskIDs: ids, ExpectedRevision: v.Revision})
	return err
}

// DeleteVolume removes the registry entry only; files stay on the disks (FR-V19).
func (c *Core) DeleteVolume(ctx context.Context, ref string) error {
	v, err := c.GetVolume(ctx, ref)
	if err != nil {
		return err
	}
	var running int
	_ = c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE volume_id=? AND state IN ('QUEUED','PLANNING','RESERVED','RUNNING','PAUSED','VERIFYING','COMMITTING','PAUSED_NEED_SPACE')`, v.ID).Scan(&running)
	if running > 0 {
		return Errf(CodeConflict, "volume has %d active job(s)", running)
	}
	err = c.DB.Tx(ctx, func(tx *store.Tx) error {
		for _, q := range []string{
			`DELETE FROM objects WHERE volume_id=?`,
			`DELETE FROM tombstones WHERE volume_id=?`,
			`UPDATE disks SET volume_id=NULL, ordinal=NULL WHERE volume_id=?`,
			`DELETE FROM volumes WHERE volume_id=?`,
		} {
			if _, err := tx.Exec(ctx, q, v.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range v.Disks {
		_ = c.writeMarker(ctx, d.ID)
	}
	c.audit(ctx, "volume.delete", v.ID, v.ID, "ok")
	c.event(ctx, "volume.deleted", v.ID, "", "", map[string]any{"name": v.Name})
	return nil
}

func (c *Core) bumpRevision(ctx context.Context, volumeID string) {
	if volumeID != "" {
		_, _ = c.DB.Exec(ctx, `UPDATE volumes SET revision=revision+1, updated_at=? WHERE volume_id=?`, store.Now(), volumeID)
	}
}
