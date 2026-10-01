//go:build !windows

package vfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"

	"continuum/internal/core"
	"continuum/internal/placement"
	"continuum/internal/transfer"
)

// Shared by both backends: go-fuse (Linux /dev/fuse, macOS macFUSE; pure Go) and cgofuse + FUSE-T
// (macOS without a kernel extension; cgo, `-tags fuset`).

// FS is one mounted volume.
type FS struct {
	C        *core.Core
	E        *transfer.Engine
	Volume   string // volume id
	ReadOnly bool

	mu sync.Mutex
	be backend
}

// backend is the mounted filesystem of one implementation.
type backend interface {
	unmount() error
	wait()
}

// pick returns the object served for a logical path: lowest ordinal on an ONLINE local disk.
func (f *FS) pick(ctx context.Context, logical string) (core.Object, bool, error) {
	objs, err := f.C.Locate(ctx, f.Volume, logical)
	if err != nil {
		if core.CodeOf(err) == core.CodeNotFound {
			return core.Object{}, false, nil
		}
		return core.Object{}, false, err
	}
	var dir *core.Object
	for i, o := range objs {
		if o.Type == "dir" {
			dir = &objs[i]
			continue
		}
		return o, true, nil // Locate orders by ordinal
	}
	if dir != nil {
		return *dir, true, nil
	}
	return core.Object{}, false, nil
}

func (f *FS) disk(ctx context.Context, id string) (core.Disk, error) { return f.C.GetDisk(ctx, id) }

func toErrno(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, fs.ErrNotExist), core.CodeOf(err) == core.CodeNotFound:
		return syscall.ENOENT
	case errors.Is(err, fs.ErrExist), core.CodeOf(err) == core.CodeConflict:
		return syscall.EEXIST
	case core.CodeOf(err) == core.CodeOffline:
		return syscall.EIO
	case core.CodeOf(err) == core.CodeInsufficientSpace:
		return syscall.ENOSPC
	case core.CodeOf(err) == core.CodePermission:
		return syscall.EACCES
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return syscall.EIO
}

// physical returns the local path of an object (error for disks on other nodes / offline).
func (f *FS) physical(ctx context.Context, o core.Object) (string, error) {
	d, err := f.disk(ctx, o.DiskID)
	if err != nil {
		return "", err
	}
	if f.C.IsRemote(d) {
		return "", core.Errf(core.CodeOffline, "%s is on node %s", o.LogicalPath, d.NodeName)
	}
	if d.State == core.StateOffline || d.State == core.StateDisabled {
		return "", core.Errf(core.CodeOffline, "disk %s is %s", d.Name, d.State)
	}
	return core.PhysicalPath(d.DataRoot, o.LogicalPath)
}

// commit records a file written through the mount (checksum left for verify/full scan).
func (f *FS) commit(ctx context.Context, logical, diskID, p string) {
	fi, err := os.Lstat(p)
	if err != nil {
		return
	}
	f.C.EnsureDirs(ctx, f.Volume, path.Dir(logical))
	f.C.UpsertFile(ctx, core.Object{VolumeID: f.Volume, LogicalPath: logical, ParentPath: path.Dir(logical), Name: path.Base(logical), DiskID: diskID,
		PhysicalRelPath: strings.TrimPrefix(logical, "/"), Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano(), Mode: int64(fi.Mode().Perm())})
	// content changed: any stored checksum is stale
	f.C.DB.Exec(ctx, `UPDATE objects SET checksum=NULL, checksum_algo=NULL, verified_at=NULL WHERE volume_id=? AND logical_path=? AND disk_id=?`, f.Volume, logical, diskID)
}

// place chooses the disk for a new file with the volume's placement policy (exact ordinal semantics).
func (f *FS) place(ctx context.Context, logical string) (core.Disk, error) {
	v, err := f.C.GetVolume(ctx, f.Volume)
	if err != nil {
		return core.Disk{}, err
	}
	for _, d := range v.Disks { // fresh statfs: the mount may have just filled a disk
		if !f.C.IsRemote(d) {
			f.C.RefreshCapacity(ctx, d.ID)
		}
	}
	if v, err = f.C.GetVolume(ctx, f.Volume); err != nil {
		return core.Disk{}, err
	}
	var cands []placement.Disk
	local := map[string]core.Disk{}
	for _, d := range v.Disks {
		if f.C.IsRemote(d) {
			continue // the mount writes to this host's disks only
		}
		local[d.ID] = d
		cands = append(cands, placement.Disk{ID: d.ID, Name: d.Name, Ordinal: d.Ordinal, State: d.State, Role: d.Role, Writable: d.Writable,
			AutoPlace: core.AutoPlace(d.Role), TotalBytes: d.TotalBytes, EffectiveFree: d.EffectiveFree, MinFree: d.EffectiveMinFree, FSType: d.FilesystemType})
	}
	plan := placement.Compute(cands, []placement.Item{{Path: strings.TrimPrefix(logical, "/"), Size: 1}}, 0,
		placement.Options{Policy: v.PlacementPolicy, Spillover: v.AllowSpillover})
	if len(plan.Assignments) == 0 {
		return core.Disk{}, core.Errf(core.CodeInsufficientSpace, "no writable disk with free space above the reserve")
	}
	return local[plan.Assignments[0].DiskID], nil
}

// Unmount detaches the mount.
func (f *FS) Unmount() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.be == nil {
		return nil
	}
	err := f.be.unmount()
	if err == nil {
		f.be = nil
	}
	return err
}

// Wait blocks until the mount is gone.
func (f *FS) Wait() {
	f.mu.Lock()
	be := f.be
	f.mu.Unlock()
	if be != nil {
		be.wait()
	}
}
