package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"continuum/internal/disk"
	"continuum/internal/store"
)

const diskCols = `d.disk_id, COALESCE(d.volume_id,''), d.node_id, COALESCE(n.hostname,''), d.name, d.provider_type, d.filesystem_uuid,
	COALESCE(d.native_disk_id,''), COALESCE(d.volume_label,''), COALESCE(d.filesystem_type,''), d.mount_path, d.data_root,
	COALESCE(d.ordinal,0), d.role, d.state, d.writable, d.total_bytes, d.free_bytes, d.available_bytes, d.reserved_bytes,
	d.min_free_bytes, COALESCE(d.bus_type,''), d.removable, d.network_backed, d.health, COALESCE(d.capabilities_json,''),
	COALESCE(d.last_seen,''), d.created_at, d.updated_at, COALESCE(v.min_free_bytes, 1073741824)`

const diskFrom = ` FROM disks d LEFT JOIN nodes n ON n.node_id = d.node_id LEFT JOIN volumes v ON v.volume_id = d.volume_id `

type scanner interface{ Scan(dest ...any) error }

func scanDisk(s scanner) (Disk, error) {
	var d Disk
	var writable, removable, network int
	var minFree sql.NullInt64
	var caps string
	var volMinFree int64
	err := s.Scan(&d.ID, &d.VolumeID, &d.NodeID, &d.NodeName, &d.Name, &d.ProviderType, &d.FilesystemUUID,
		&d.NativeDiskID, &d.VolumeLabel, &d.FilesystemType, &d.MountPath, &d.DataRoot,
		&d.Ordinal, &d.Role, &d.State, &writable, &d.TotalBytes, &d.FreeBytes, &d.AvailableBytes, &d.ReservedBytes,
		&minFree, &d.BusType, &removable, &network, &d.Health, &caps, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt, &volMinFree)
	if err != nil {
		return d, err
	}
	d.Writable, d.Removable, d.NetworkBacked = writable == 1, removable == 1, network == 1
	if minFree.Valid {
		v := minFree.Int64
		d.MinFreeBytes = &v
	}
	if caps != "" {
		_ = json.Unmarshal([]byte(caps), &d.Capabilities)
	}
	d.derive(volMinFree)
	return d, nil
}

// DiskFilter selects disks for ListDisks.
type DiskFilter struct {
	VolumeID   string // "" = all
	Unassigned bool   // only disks not in any volume (Create Volume › Available storage, FR-V02)
}

func (c *Core) ListDisks(ctx context.Context, f DiskFilter) ([]Disk, error) {
	q := `SELECT ` + diskCols + diskFrom
	var args []any
	switch {
	case f.VolumeID != "":
		q += `WHERE d.volume_id = ? ORDER BY d.ordinal`
		args = append(args, f.VolumeID)
	case f.Unassigned:
		q += `WHERE d.volume_id IS NULL ORDER BY d.name`
	default:
		q += `ORDER BY d.volume_id, d.ordinal, d.name`
	}
	rows, err := c.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Disk{}
	for rows.Next() {
		d, err := scanDisk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDisk finds a disk by id or name.
func (c *Core) GetDisk(ctx context.Context, ref string) (Disk, error) {
	d, err := scanDisk(c.DB.QueryRow(ctx, `SELECT `+diskCols+diskFrom+`WHERE d.disk_id = ? OR d.name = ?`, ref, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return d, Errf(CodeNotFound, "disk %q not found", ref)
	}
	return d, err
}

// DiscoveredDisk annotates a discovery result with its registration.
type DiscoveredDisk struct {
	disk.Descriptor
	DiskID   string `json:"disk_id,omitempty"`
	VolumeID string `json:"volume_id,omitempty"`
}

// Discover lists local volumes (FR-D01) and marks registered ones.
func (c *Core) Discover(ctx context.Context) ([]DiscoveredDisk, error) {
	ds, err := disk.Discover(ctx, c.LocalNode.ID, c.Stat)
	if err != nil {
		return nil, err
	}
	reg, err := c.ListDisks(ctx, DiskFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveredDisk, 0, len(ds))
	for _, d := range ds {
		dd := DiscoveredDisk{Descriptor: d}
		for _, r := range reg {
			if r.MountPath == d.MountPath || (r.FilesystemUUID == d.FilesystemUUID && filepath.Clean(r.MountPath) == filepath.Clean(d.MountPath)) {
				dd.DiskID, dd.VolumeID = r.ID, r.VolumeID
			}
		}
		out = append(out, dd)
	}
	return out, nil
}

// AttachRequest registers a local path (disk mount root or folder) as a Disk (FR-D03, D11).
type AttachRequest struct {
	Path         string `json:"path"`
	Name         string `json:"name,omitempty"`
	Role         string `json:"role,omitempty"`
	MinFreeBytes *int64 `json:"min_free_bytes,omitempty"`
	VolumeID     string `json:"volume_id,omitempty"` // optional: append to volume
	Ordinal      int    `json:"ordinal,omitempty"`   // optional: insert position (1-based)
	Adopt        bool   `json:"adopt,omitempty"`     // accept a marker from another controller (volume import, FR-V22)
	NodeID       string `json:"node_id,omitempty"`   // agent node owning the path (default: this host)
}

// AttachResult returns the disk and its preflight report.
type AttachResult struct {
	Disk      Disk        `json:"disk"`
	Preflight disk.Report `json:"preflight"`
	Reattach  bool        `json:"reattached"`
}

// Preflight runs the 10 checks without registering (FR-D04).
func (c *Core) Preflight(ctx context.Context, path string, minFree int64, volumeID string) disk.Report {
	return c.preflight(path, minFree, volumeID, false)
}

func (c *Core) preflight(path string, minFree int64, volumeID string, adopt bool) disk.Report {
	_, fstype := disk.MountOf(path)
	ctl := c.ControllerID
	if adopt {
		ctl = ""
	}
	return disk.Preflight(disk.PreflightInput{
		Path: path, MinFreeBytes: uint64(max64(minFree, 0)), ControllerID: ctl, VolumeID: volumeID,
		NetworkBacked: isNetworkFS(fstype), Stat: c.Stat,
	})
}

func (c *Core) Attach(ctx context.Context, req AttachRequest) (*AttachResult, error) {
	if req.Path == "" {
		return nil, Errf(CodeInvalid, "path required")
	}
	remote := req.NodeID != "" && req.NodeID != c.LocalNode.ID
	var abs string
	if remote { // the agent's OS resolves its own paths
		if c.Remote == nil {
			return nil, Errf(CodeInvalid, "no agent hub configured")
		}
		if !strings.HasPrefix(req.Path, "/") && !(len(req.Path) > 2 && req.Path[1] == ':') {
			return nil, Errf(CodeInvalid, "path on node must be absolute")
		}
		abs = strings.TrimRight(req.Path, "/")
		if abs == "" {
			abs = "/"
		}
	} else {
		var err error
		if abs, err = filepath.Abs(req.Path); err != nil {
			return nil, Errf(CodeInvalid, "bad path: %v", err)
		}
	}
	node := c.LocalNode.ID
	if remote {
		node = req.NodeID
	}
	if req.Role == "" {
		req.Role = RoleAlwaysOn
	}
	if !validRoles[req.Role] {
		return nil, Errf(CodeInvalid, "unknown role %q", req.Role)
	}
	minFree := int64(1 << 30)
	if req.MinFreeBytes != nil {
		minFree = *req.MinFreeBytes
	}
	var rep disk.Report
	var identity string
	var meta disk.Descriptor
	if remote {
		ctl := c.ControllerID
		if req.Adopt {
			ctl = ""
		}
		var err error
		if rep, identity, meta, err = c.Remote.Probe(ctx, node, abs, uint64(max64(minFree, 0)), ctl, req.VolumeID); err != nil {
			return nil, Errf(CodeOffline, "node %s: %v", node, err)
		}
	} else {
		rep = c.preflight(abs, minFree, req.VolumeID, req.Adopt)
	}
	if !rep.OK {
		e := Errf(CodeInvalid, "preflight failed for %s", abs)
		e.Info = map[string]any{"preflight": rep}
		return &AttachResult{Preflight: rep}, e
	}

	// Re-attach by marker (DP01: identity is metadata, not path).
	if rep.Marker != nil && rep.Marker.DiskID != "" {
		if d, err := c.GetDisk(ctx, rep.Marker.DiskID); err == nil {
			now := store.Now()
			if _, err := c.DB.Exec(ctx, `UPDATE disks SET mount_path=?, data_root=?, state=?, last_seen=?, updated_at=? WHERE disk_id=?`,
				abs, rep.DataRoot, StateOnline, now, now, d.ID); err != nil {
				return nil, err
			}
			c.RefreshCapacity(ctx, d.ID)
			d, _ = c.GetDisk(ctx, d.ID)
			c.audit(ctx, "disk.reattach", d.ID, d.VolumeID, "ok")
			return &AttachResult{Disk: d, Preflight: rep, Reattach: true}, nil
		}
	}

	if !remote {
		identity, meta = c.identityFor(ctx, abs)
	}
	name := req.Name
	if name == "" {
		name = meta.VolumeLabel
		if name == "" || meta.MountPath != abs {
			name = path.Base(filepath.ToSlash(abs))
		}
	}
	caps, _ := json.Marshal(rep.Capabilities)
	now := store.Now()
	d := Disk{ID: store.NewID("dsk")}
	_, err := c.DB.Exec(ctx, `INSERT INTO disks (disk_id, node_id, name, provider_type, filesystem_uuid, native_disk_id, volume_label, filesystem_type,
		mount_path, data_root, role, state, writable, total_bytes, free_bytes, available_bytes, min_free_bytes, bus_type, removable,
		network_backed, health, capabilities_json, last_seen, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, node, name, "local-fs", identity, meta.NativeDiskID, meta.VolumeLabel, meta.FilesystemType,
		abs, rep.DataRoot, req.Role, StateOnline, boolInt(req.Role != RoleArchive), int64(rep.Capacity.Total), int64(rep.Capacity.Free),
		int64(rep.Capacity.Available), nullInt(req.MinFreeBytes), meta.BusType, boolInt(meta.Removable), boolInt(meta.NetworkBacked),
		"ONLINE", string(caps), now, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, Errf(CodeConflict, "%s is already registered", abs)
		}
		return nil, err
	}
	if err := c.writeMarker(ctx, d.ID); err != nil {
		return nil, err
	}
	if remote {
		c.Remote.RefreshWatch(ctx, node)
	}
	if req.VolumeID != "" {
		if err := c.AddDiskToVolume(ctx, req.VolumeID, d.ID, req.Ordinal); err != nil {
			return nil, err
		}
	}
	d, err = c.GetDisk(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	c.audit(ctx, "disk.attach", d.ID, d.VolumeID, "ok")
	c.event(ctx, "disk.attached", d.VolumeID, d.ID, "", map[string]any{"path": abs, "role": d.Role})
	return &AttachResult{Disk: d, Preflight: rep}, nil
}

// identityFor: mount root → filesystem UUID; folder → path identity (FR-D02).
func (c *Core) identityFor(ctx context.Context, abs string) (string, disk.Descriptor) {
	mount, _ := disk.MountOf(abs)
	var meta disk.Descriptor
	if ds, err := disk.Discover(ctx, c.LocalNode.ID, c.Stat); err == nil {
		for _, d := range ds {
			if d.MountPath == mount {
				meta = d
			}
		}
	}
	if mount == abs && meta.FilesystemUUID != "" {
		return meta.FilesystemUUID, meta
	}
	return disk.PathIdentity(abs), meta
}

// writeMarker (re)writes <data_root>/volume.json from the registry (FR-D05).
func (c *Core) writeMarker(ctx context.Context, diskID string) error {
	d, err := c.GetDisk(ctx, diskID)
	if err != nil {
		return err
	}
	m := disk.Marker{Schema: 1, ControllerID: c.ControllerID, DiskID: d.ID, FilesystemUUID: d.FilesystemUUID,
		OrdinalSnapshot: d.Ordinal, Role: d.Role, CreatedAt: d.CreatedAt, UpdatedAt: store.Now()}
	if d.VolumeID != "" {
		m.VolumeID = d.VolumeID
		_ = c.DB.QueryRow(ctx, `SELECT slug FROM volumes WHERE volume_id = ?`, d.VolumeID).Scan(&m.VolumeSlug)
	}
	if c.IsRemote(d) {
		if c.Remote == nil {
			return Errf(CodeOffline, "disk %s is on node %s but no agent hub is configured", d.Name, d.NodeID)
		}
		return c.Remote.WriteMarker(ctx, d.NodeID, d.DataRoot, m)
	}
	if err := os.MkdirAll(d.DataRoot, 0o755); err != nil {
		return err
	}
	return disk.WriteMarker(d.DataRoot, m)
}

// RefreshCapacity re-reads statfs; missing path → OFFLINE, back → ONLINE (FR-D12, D20).
func (c *Core) RefreshCapacity(ctx context.Context, diskID string) {
	d, err := c.GetDisk(ctx, diskID)
	if err != nil {
		return
	}
	now := store.Now()
	var cp disk.Capacity
	exists := false
	if c.IsRemote(d) {
		if c.Remote == nil {
			return
		}
		var known bool
		if cp, exists, known = c.Remote.Capacity(d.NodeID, d.MountPath, d.DataRoot); !known {
			return // no heartbeat yet
		}
	} else {
		exists = disk.Exists(d.DataRoot)
	}
	if !exists {
		if d.State != StateOffline && d.State != StateDisabled {
			_, _ = c.DB.Exec(ctx, `UPDATE disks SET state=?, updated_at=? WHERE disk_id=?`, StateOffline, now, d.ID)
			c.event(ctx, "disk.offline", d.VolumeID, d.ID, "", map[string]any{"path": d.MountPath})
		}
		return
	}
	if !c.IsRemote(d) {
		var err error
		if cp, err = c.Stat(d.MountPath); err != nil {
			return
		}
	}
	state := d.State
	if state == StateOffline {
		state = StateOnline
		c.event(ctx, "disk.online", d.VolumeID, d.ID, "", nil)
	}
	_, _ = c.DB.Exec(ctx, `UPDATE disks SET total_bytes=?, free_bytes=?, available_bytes=?, state=?, last_seen=?, updated_at=? WHERE disk_id=?`,
		int64(cp.Total), int64(cp.Free), int64(cp.Available), state, now, now, d.ID)
	if nd, err := c.GetDisk(ctx, d.ID); err == nil && nd.Warning != d.Warning && strings.HasPrefix(nd.Warning, "capacity-") {
		c.event(ctx, "disk.capacity", d.VolumeID, d.ID, "", map[string]any{"level": nd.Warning, "used_pct": nd.UsedPct}) // 85/95% crossed (FR-D12)
	}
}

// RefreshAll refreshes every registered disk.
func (c *Core) RefreshAll(ctx context.Context) {
	ds, err := c.ListDisks(ctx, DiskFilter{})
	if err != nil {
		return
	}
	for _, d := range ds {
		c.RefreshCapacity(ctx, d.ID)
	}
}

// SetRole changes a disk role (FR-D07).
func (c *Core) SetRole(ctx context.Context, ref, role string) (Disk, error) {
	if !validRoles[role] {
		return Disk{}, Errf(CodeInvalid, "unknown role %q", role)
	}
	d, err := c.GetDisk(ctx, ref)
	if err != nil {
		return d, err
	}
	_, err = c.DB.Exec(ctx, `UPDATE disks SET role=?, writable=?, updated_at=? WHERE disk_id=?`, role, boolInt(role != RoleArchive), store.Now(), d.ID)
	if err != nil {
		return d, err
	}
	_ = c.writeMarker(ctx, d.ID)
	c.bumpRevision(ctx, d.VolumeID)
	c.audit(ctx, "disk.role", d.ID, d.VolumeID, role)
	return c.GetDisk(ctx, d.ID)
}

// SetMinFree overrides the per-disk reserve (FR-V16). nil clears the override.
func (c *Core) SetMinFree(ctx context.Context, ref string, bytes *int64) (Disk, error) {
	d, err := c.GetDisk(ctx, ref)
	if err != nil {
		return d, err
	}
	if _, err := c.DB.Exec(ctx, `UPDATE disks SET min_free_bytes=?, updated_at=? WHERE disk_id=?`, nullInt(bytes), store.Now(), d.ID); err != nil {
		return d, err
	}
	c.bumpRevision(ctx, d.VolumeID)
	return c.GetDisk(ctx, d.ID)
}

// Detach marks a disk DISABLED, keeping data and catalog (FR-D09). Objects become "offline".
func (c *Core) Detach(ctx context.Context, ref string) (Disk, error) {
	d, err := c.GetDisk(ctx, ref)
	if err != nil {
		return d, err
	}
	if n := c.activeJobsOnDisk(ctx, d.ID); n > 0 {
		return d, Errf(CodeConflict, "disk %s has %d running job(s) — pause or cancel first", d.Name, n)
	}
	now := store.Now()
	err = c.DB.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE disks SET state=?, writable=0, updated_at=? WHERE disk_id=?`, StateDisabled, now, d.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE objects SET state='offline', updated_at=? WHERE disk_id=? AND state='ok'`, now, d.ID)
		return err
	})
	if err != nil {
		return d, err
	}
	c.bumpRevision(ctx, d.VolumeID)
	c.audit(ctx, "disk.detach", d.ID, d.VolumeID, "ok")
	c.event(ctx, "disk.detached", d.VolumeID, d.ID, "", nil)
	return c.GetDisk(ctx, d.ID)
}

// Enable brings a DISABLED disk back online.
func (c *Core) Enable(ctx context.Context, ref string) (Disk, error) {
	d, err := c.GetDisk(ctx, ref)
	if err != nil {
		return d, err
	}
	now := store.Now()
	err = c.DB.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE disks SET state=?, writable=?, updated_at=? WHERE disk_id=?`, StateOnline, boolInt(d.Role != RoleArchive), now, d.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE objects SET state='ok', updated_at=? WHERE disk_id=? AND state='offline'`, now, d.ID)
		return err
	})
	if err != nil {
		return d, err
	}
	c.RefreshCapacity(ctx, d.ID)
	c.bumpRevision(ctx, d.VolumeID)
	c.audit(ctx, "disk.enable", d.ID, d.VolumeID, "ok")
	return c.GetDisk(ctx, d.ID)
}

// Unregister removes a disk that holds no catalog objects. Data on disk is untouched.
func (c *Core) Unregister(ctx context.Context, ref string) error {
	d, err := c.GetDisk(ctx, ref)
	if err != nil {
		return err
	}
	var n int
	if err := c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM objects WHERE disk_id = ?`, d.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return Errf(CodeConflict, "disk %s still holds %d catalog object(s) — drain it first", d.Name, n)
	}
	if _, err := c.DB.Exec(ctx, `DELETE FROM disks WHERE disk_id = ?`, d.ID); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(d.DataRoot, disk.MarkerName))
	c.bumpRevision(ctx, d.VolumeID)
	c.audit(ctx, "disk.unregister", d.ID, d.VolumeID, "ok")
	return nil
}

func (c *Core) activeJobsOnDisk(ctx context.Context, diskID string) int {
	var n int
	_ = c.DB.QueryRow(ctx, `SELECT COUNT(DISTINCT j.job_id) FROM jobs j JOIN job_items i ON i.job_id = j.job_id
		WHERE i.disk_id = ? AND j.state IN ('RUNNING','VERIFYING','COMMITTING')`, diskID).Scan(&n)
	return n
}

func isNetworkFS(fs string) bool { return disk.IsNetworkFS(fs) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
