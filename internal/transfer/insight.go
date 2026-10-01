package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"continuum/internal/agentproto"
	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/nodefs"
)

// ---- Usage analysis (FR-D15, catalog based) ----

// UsageNode is a folder in the usage tree (treemap input).
type UsageNode struct {
	Name     string       `json:"name"`
	Path     string       `json:"path"`
	Bytes    int64        `json:"bytes"`
	Files    int          `json:"files"`
	Children []*UsageNode `json:"children,omitempty"`
}

// UsageFile is one entry of the top-N largest files.
type UsageFile struct {
	LogicalPath string `json:"logical_path"`
	Disk        string `json:"disk"`
	Size        int64  `json:"size"`
}

// UsageType aggregates by file extension.
type UsageType struct {
	Ext   string `json:"ext"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// Usage is the usage analysis of a disk or a whole volume.
type Usage struct {
	Bytes int64       `json:"bytes"`
	Files int         `json:"files"`
	Tree  *UsageNode  `json:"tree"`
	Top   []UsageFile `json:"top_files"`
	Types []UsageType `json:"types"`
}

// Usage aggregates cataloged files of one disk (diskRef) or of a volume (volume) — depth-limited tree.
func (e *Engine) Usage(ctx context.Context, volume, diskRef string, topN, depth int) (*Usage, error) {
	if topN <= 0 {
		topN = 20
	}
	if depth <= 0 {
		depth = 3
	}
	q := `SELECT o.logical_path, o.size_bytes, COALESCE(d.name,'') FROM objects o LEFT JOIN disks d ON d.disk_id=o.disk_id WHERE o.object_type='file'`
	var args []any
	if diskRef != "" {
		d, err := e.C.GetDisk(ctx, diskRef)
		if err != nil {
			return nil, err
		}
		q, args = q+` AND o.disk_id=?`, append(args, d.ID)
	}
	if volume != "" {
		v, err := e.C.GetVolume(ctx, volume)
		if err != nil {
			return nil, err
		}
		q, args = q+` AND o.volume_id=?`, append(args, v.ID)
	}
	rows, err := e.C.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	u := &Usage{Tree: &UsageNode{Name: "/", Path: "/"}, Top: []UsageFile{}, Types: []UsageType{}}
	types := map[string]*UsageType{}
	for rows.Next() {
		var f UsageFile
		if err := rows.Scan(&f.LogicalPath, &f.Size, &f.Disk); err != nil {
			return nil, err
		}
		u.Bytes += f.Size
		u.Files++
		n := u.Tree
		n.Bytes += f.Size
		n.Files++
		parts := strings.Split(strings.Trim(path.Dir(f.LogicalPath), "/"), "/")
		for i, p := range parts {
			if p == "" || i >= depth {
				break
			}
			var child *UsageNode
			for _, c := range n.Children {
				if c.Name == p {
					child = c
				}
			}
			if child == nil {
				child = &UsageNode{Name: p, Path: "/" + strings.Join(parts[:i+1], "/")}
				n.Children = append(n.Children, child)
			}
			child.Bytes += f.Size
			child.Files++
			n = child
		}
		ext := strings.ToLower(path.Ext(f.LogicalPath))
		if ext == "" {
			ext = "(none)"
		}
		t := types[ext]
		if t == nil {
			t = &UsageType{Ext: ext}
			types[ext] = t
		}
		t.Bytes += f.Size
		t.Files++
		u.Top = append(u.Top, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(u.Top, func(i, j int) bool { return u.Top[i].Size > u.Top[j].Size })
	if len(u.Top) > topN {
		u.Top = u.Top[:topN]
	}
	for _, t := range types {
		u.Types = append(u.Types, *t)
	}
	sort.Slice(u.Types, func(i, j int) bool { return u.Types[i].Bytes > u.Types[j].Bytes })
	var sortTree func(n *UsageNode)
	sortTree = func(n *UsageNode) {
		sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Bytes > n.Children[j].Bytes })
		for _, c := range n.Children {
			sortTree(c)
		}
	}
	sortTree(u.Tree)
	return u, nil
}

// ---- Duplicate finder (FR-D16): size → partial hash → full BLAKE3 ----

// DupeFile is one copy inside a duplicate group.
type DupeFile struct {
	LogicalPath string `json:"logical_path"`
	Disk        string `json:"disk"`
	Ordinal     int    `json:"ordinal"`
	Physical    string `json:"physical_path"`
}

// DupeGroup is a set of files with identical content.
type DupeGroup struct {
	Size     int64      `json:"size"`
	Checksum string     `json:"checksum"`
	Files    []DupeFile `json:"files"`
	Wasted   int64      `json:"wasted_bytes"` // size × (copies − 1)
}

// DupeReport lists duplicate groups of a volume (optionally one disk).
type DupeReport struct {
	Groups   []DupeGroup `json:"groups"`
	Wasted   int64       `json:"wasted_bytes"`
	Hashed   int         `json:"files_hashed"`
	Scanned  int         `json:"files_scanned"`
	MinBytes int64       `json:"min_size"`
}

const partialChunk = 64 << 10

// Dupes finds identical files (different logical paths, any disk). Deleting is left to the user (trash).
func (e *Engine) Dupes(ctx context.Context, volume, diskRef string, minSize int64) (*DupeReport, error) {
	v, err := e.C.GetVolume(ctx, volume)
	if err != nil {
		return nil, err
	}
	if minSize <= 0 {
		minSize = 1
	}
	q := `SELECT o.object_id, o.logical_path, o.size_bytes, COALESCE(o.checksum,''), d.name, COALESCE(d.ordinal,0), d.data_root, o.physical_rel_path, d.state, d.disk_id
		FROM objects o JOIN disks d ON d.disk_id=o.disk_id
		WHERE o.volume_id=? AND o.object_type='file' AND o.size_bytes>=?`
	args := []any{v.ID, minSize}
	if diskRef != "" {
		d, err := e.C.GetDisk(ctx, diskRef)
		if err != nil {
			return nil, err
		}
		q, args = q+` AND o.disk_id=?`, append(args, d.ID)
	}
	rows, err := e.C.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type cand struct {
		id, sum, disk string
		size          int64
		f             DupeFile
	}
	bySize := map[int64][]*cand{}
	rep := &DupeReport{Groups: []DupeGroup{}, MinBytes: minSize}
	for rows.Next() {
		c := &cand{}
		var root, rel, state string
		if err := rows.Scan(&c.id, &c.f.LogicalPath, &c.size, &c.sum, &c.f.Disk, &c.f.Ordinal, &root, &rel, &state, &c.disk); err != nil {
			rows.Close()
			return nil, err
		}
		if state == core.StateOffline || state == core.StateDisabled {
			continue
		}
		c.f.Physical, _ = core.PhysicalPath(root, "/"+rel)
		bySize[c.size] = append(bySize[c.size], c)
		rep.Scanned++
	}
	rows.Close()
	fss := map[string]nodefs.FS{}
	fsFor := func(diskID string) nodefs.FS {
		if f, ok := fss[diskID]; ok {
			return f
		}
		f := nodefs.FS(nodefs.Local{})
		if d, err := e.C.GetDisk(ctx, diskID); err == nil {
			f = e.fsOf(d)
		}
		fss[diskID] = f
		return f
	}
	for size, cs := range bySize { // stage 1: size
		if len(cs) < 2 {
			continue
		}
		byPart := map[string][]*cand{}
		for _, c := range cs { // stage 2: partial hash (head + tail)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if ph, err := fsFor(c.disk).PartialHash(ctx, c.f.Physical); err == nil {
				byPart[ph] = append(byPart[ph], c)
			}
		}
		for _, pcs := range byPart {
			if len(pcs) < 2 {
				continue
			}
			byFull := map[string][]*cand{}
			for _, c := range pcs { // stage 3: full BLAKE3 (reuses verified catalog checksum)
				if c.sum == "" {
					s, err := fsFor(c.disk).Hash(ctx, c.f.Physical)
					if err != nil {
						continue
					}
					c.sum = s
					rep.Hashed++
					e.C.DB.Exec(ctx, `UPDATE objects SET checksum=?, checksum_algo='blake3' WHERE object_id=?`, s, c.id)
				}
				byFull[c.sum] = append(byFull[c.sum], c)
			}
			for sum, fcs := range byFull {
				paths := map[string]bool{}
				for _, c := range fcs {
					paths[c.f.LogicalPath] = true
				}
				if len(paths) < 2 { // same logical path on several disks = conflict, not a duplicate (Conflict Center)
					continue
				}
				g := DupeGroup{Size: size, Checksum: sum, Wasted: size * int64(len(fcs)-1)}
				for _, c := range fcs {
					g.Files = append(g.Files, c.f)
				}
				sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].LogicalPath < g.Files[j].LogicalPath })
				rep.Groups = append(rep.Groups, g)
				rep.Wasted += g.Wasted
			}
		}
	}
	sort.Slice(rep.Groups, func(i, j int) bool { return rep.Groups[i].Wasted > rep.Groups[j].Wasted })
	return rep, nil
}

// ---- SMART cache + benchmark history (FR-D13·D14) ----

// Smart returns the cached SMART result of a disk, refreshing it when refresh is set or nothing is cached.
func (e *Engine) Smart(ctx context.Context, diskRef string, refresh bool) (*disk.Smart, error) {
	d, err := e.C.GetDisk(ctx, diskRef)
	if err != nil {
		return nil, err
	}
	var raw string
	e.C.DB.QueryRow(ctx, `SELECT COALESCE(smart_json,'') FROM disks WHERE disk_id=?`, d.ID).Scan(&raw)
	if raw != "" && !refresh {
		var s disk.Smart
		if json.Unmarshal([]byte(raw), &s) == nil {
			return &s, nil
		}
	}
	if d.State == core.StateOffline {
		return nil, core.Errf(core.CodeOffline, "disk %s is offline", d.Name)
	}
	var s disk.Smart
	if e.Remote != nil && e.C.IsRemote(d) {
		if err := e.Remote.Call(ctx, d.NodeID, agentproto.OpSmart, agentproto.PathArgs{Path: d.MountPath}, &s); err != nil {
			return nil, core.Errf(core.CodeOffline, "node %s: %v", d.NodeName, err)
		}
	} else {
		s = disk.ReadSmart(ctx, d.MountPath)
	}
	b, _ := json.Marshal(s)
	e.C.DB.Exec(ctx, `UPDATE disks SET smart_json=? WHERE disk_id=?`, string(b), d.ID)
	e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.smart", DiskID: d.ID, VolumeID: d.VolumeID}, // history (temperature trend)
		map[string]any{"status": s.Status, "passed": s.Passed, "temperature_c": s.Temperature, "source": s.Source})
	if s.Passed != nil && !*s.Passed {
		e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.smart_warning", DiskID: d.ID, VolumeID: d.VolumeID}, map[string]any{"status": s.Status})
	}
	return &s, nil
}

// Bench runs a benchmark on an ONLINE disk with enough free space and stores the result card as an event.
func (e *Engine) Bench(ctx context.Context, diskRef string, size int64) (*disk.Bench, error) {
	d, err := e.C.GetDisk(ctx, diskRef)
	if err != nil {
		return nil, err
	}
	if d.State != core.StateOnline {
		return nil, core.Errf(core.CodeOffline, "benchmark needs an ONLINE disk (%s is %s)", d.Name, d.State)
	}
	if size <= 0 {
		size = 256 << 20
	}
	if d.EffectiveFree < size*2 {
		return nil, core.Errf(core.CodeInsufficientSpace, "benchmark needs %d bytes free above the reserve", size*2)
	}
	var r disk.Bench
	if e.Remote != nil && e.C.IsRemote(d) {
		err = e.Remote.Call(ctx, d.NodeID, agentproto.OpBench, agentproto.BenchArgs{Path: d.DataRoot, Size: size}, &r)
	} else {
		r, err = disk.RunBench(ctx, d.DataRoot, size)
	}
	if err != nil {
		return nil, err
	}
	e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.bench", DiskID: d.ID, VolumeID: d.VolumeID}, r)
	return &r, nil
}

// BenchHistory returns stored benchmark cards of a disk (newest first).
func (e *Engine) BenchHistory(ctx context.Context, diskRef string) ([]disk.Bench, error) {
	d, err := e.C.GetDisk(ctx, diskRef)
	if err != nil {
		return nil, err
	}
	rows, err := e.C.DB.Query(ctx, `SELECT COALESCE(payload_json,'') FROM events WHERE kind='disk.bench' AND disk_id=? ORDER BY ts DESC LIMIT 20`, d.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []disk.Bench{}
	for rows.Next() {
		var p string
		rows.Scan(&p)
		var b disk.Bench
		if json.Unmarshal([]byte(p), &b) == nil {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}

// WipeResult reports a secure delete.
type WipeResult struct {
	Disk   string `json:"disk"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Passes int    `json:"passes"`
}

// Wipe overwrites and removes the disk's Continuum data root, then unregisters the disk (FR-D22, FR-S05).
// Only for disks outside any volume (drain first); confirm must equal the disk name. Data outside the
// data root is never touched; full-device erase (ATA/NVMe sanitize) stays with OS tools.
func (e *Engine) Wipe(ctx context.Context, diskRef, confirm string, passes int) (*WipeResult, error) {
	d, err := e.C.GetDisk(ctx, diskRef)
	if err != nil {
		return nil, err
	}
	if confirm != d.Name {
		return nil, core.Errf(core.CodeInvalid, "confirmation must be the disk name %q", d.Name)
	}
	if d.VolumeID != "" {
		return nil, core.Errf(core.CodeConflict, "disk %s is in a volume — drain it first", d.Name)
	}
	if d.State == core.StateOffline {
		return nil, core.Errf(core.CodeOffline, "disk %s is offline", d.Name)
	}
	if passes <= 0 {
		passes = 1
	}
	var r agentproto.WipeResult
	if e.Remote != nil && e.C.IsRemote(d) {
		err = e.Remote.Call(ctx, d.NodeID, agentproto.OpWipe, agentproto.WipeArgs{Path: d.DataRoot, Passes: passes}, &r)
	} else {
		r.Files, r.Bytes, err = disk.Wipe(ctx, d.DataRoot, passes)
	}
	if err != nil {
		return nil, err
	}
	e.C.DB.Exec(ctx, `DELETE FROM objects WHERE disk_id=?`, d.ID)
	if err := e.C.Unregister(ctx, d.ID); err != nil {
		return nil, err
	}
	e.C.Audit.Append(ctx, audit.Entry{Action: "disk.wipe", Target: d.Name, Result: fmt.Sprintf("%d files, %d bytes, %d passes", r.Files, r.Bytes, passes)})
	e.C.Audit.Emit(ctx, audit.Event{Kind: "disk.wiped", DiskID: d.ID}, map[string]any{"files": r.Files, "bytes": r.Bytes, "passes": passes})
	return &WipeResult{Disk: d.Name, Files: r.Files, Bytes: r.Bytes, Passes: passes}, nil
}

// SmartPoint is one SMART snapshot (temperature trend, FR-D13).
type SmartPoint struct {
	At          string `json:"at"`
	Status      string `json:"status"`
	Passed      *bool  `json:"passed,omitempty"`
	Temperature *int   `json:"temperature_c,omitempty"`
}

// SmartHistory returns up to limit snapshots, oldest first.
func (e *Engine) SmartHistory(ctx context.Context, diskRef string, limit int) ([]SmartPoint, error) {
	d, err := e.C.GetDisk(ctx, diskRef)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 168 // a week of hourly samples
	}
	rows, err := e.C.DB.Query(ctx, `SELECT ts, COALESCE(payload_json,'') FROM events WHERE kind='disk.smart' AND disk_id=? ORDER BY ts DESC LIMIT ?`, d.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SmartPoint{}
	for rows.Next() {
		var ts, p string
		rows.Scan(&ts, &p)
		pt := SmartPoint{At: ts}
		json.Unmarshal([]byte(p), &pt)
		pt.At = ts
		out = append([]SmartPoint{pt}, out...)
	}
	return out, rows.Err()
}

// SmartAll refreshes SMART for every ONLINE disk (hourly from the Background loop).
func (e *Engine) SmartAll(ctx context.Context) {
	ds, err := e.C.ListDisks(ctx, core.DiskFilter{})
	if err != nil {
		return
	}
	for _, d := range ds {
		if d.State == core.StateOnline {
			e.Smart(ctx, d.ID, true)
		}
	}
}
