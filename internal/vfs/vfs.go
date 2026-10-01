//go:build !windows && !(darwin && cgo && fuset)

// Package vfs mounts a volume as one filesystem (FR-A06, Phase 4: Linux FUSE first).
//
// Reads come from the catalog's copy with the lowest ordinal (FR-A07). New files are placed by the volume's
// placement policy (fill-in-order, topdir-affinity, …) — exact ordinal placement for every application,
// which the SMB access point cannot offer — and written straight to that disk; the catalog is updated when
// the file is closed. Rename and delete reuse the engine (every disk, rollback, trash). Disks on other
// nodes are listed but not readable through the mount (use the app, CLI or SMB on that node).
package vfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	gofs "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/transfer"
)

type node struct {
	gofs.Inode
	fs *FS
}

var (
	_ gofs.NodeLookuper   = (*node)(nil)
	_ gofs.NodeReaddirer  = (*node)(nil)
	_ gofs.NodeGetattrer  = (*node)(nil)
	_ gofs.NodeOpener     = (*node)(nil)
	_ gofs.NodeCreater    = (*node)(nil)
	_ gofs.NodeMkdirer    = (*node)(nil)
	_ gofs.NodeUnlinker   = (*node)(nil)
	_ gofs.NodeRmdirer    = (*node)(nil)
	_ gofs.NodeRenamer    = (*node)(nil)
	_ gofs.NodeSetattrer  = (*node)(nil)
	_ gofs.NodeStatfser   = (*node)(nil)
	_ gofs.NodeSymlinker  = (*node)(nil)
	_ gofs.NodeReadlinker = (*node)(nil)
	_ gofs.NodeSetxattrer = (*node)(nil)
	_ gofs.NodeGetxattrer = (*node)(nil)
)

// Extended attributes / ACLs are not stored (the catalog has no place for them): ENOTSUP lets tools like
// `cp -a` and Finder fall back quietly instead of failing.
func (n *node) Setxattr(ctx context.Context, attr string, data []byte, flags uint32) syscall.Errno {
	return syscall.ENOTSUP
}

func (n *node) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	return 0, syscall.ENODATA
}

func (n *node) logical() string { return "/" + n.Path(nil) }

func join(dir, name string) string { return path.Join(dir, name) }

func fill(out *fuse.Attr, o core.Object, phys os.FileInfo) {
	if o.Type == "dir" {
		out.Mode = syscall.S_IFDIR | 0o755
		out.Nlink = 2
	} else if phys != nil && phys.Mode()&os.ModeSymlink != 0 {
		out.Mode = syscall.S_IFLNK | 0o777
		out.Nlink = 1
	} else {
		out.Mode = syscall.S_IFREG | uint32(os.FileMode(o.Mode).Perm())
		if out.Mode&0o777 == 0 {
			out.Mode |= 0o644
		}
		out.Size = uint64(o.Size)
		out.Nlink = 1
	}
	if phys != nil && phys.Mode()&os.ModeSymlink == 0 { // the file on disk is the truth while it is being written
		out.Size = uint64(phys.Size())
		t := phys.ModTime()
		out.SetTimes(&t, &t, &t)
	} else if o.MtimeNs > 0 {
		t := time.Unix(0, o.MtimeNs)
		out.SetTimes(&t, &t, &t)
	}
	out.Uid, out.Gid = uint32(os.Getuid()), uint32(os.Getgid())
}

func (n *node) child(ctx context.Context, o core.Object, out *fuse.EntryOut) *gofs.Inode {
	var fi os.FileInfo
	if o.Type == "file" {
		if p, err := n.fs.physical(ctx, o); err == nil {
			fi, _ = os.Lstat(p)
		}
	}
	fill(&out.Attr, o, fi)
	out.SetEntryTimeout(time.Second)
	out.SetAttrTimeout(time.Second)
	mode := uint32(syscall.S_IFREG)
	if o.Type == "dir" {
		mode = syscall.S_IFDIR
	} else if fi != nil && fi.Mode()&os.ModeSymlink != 0 {
		mode = syscall.S_IFLNK
	}
	return n.NewInode(ctx, &node{fs: n.fs}, gofs.StableAttr{Mode: mode})
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofs.Inode, syscall.Errno) {
	o, ok, err := n.fs.pick(ctx, join(n.logical(), name))
	if err != nil {
		return nil, toErrno(err)
	}
	if !ok {
		return nil, syscall.ENOENT
	}
	return n.child(ctx, o, out), 0
}

func (n *node) Readdir(ctx context.Context) (gofs.DirStream, syscall.Errno) {
	objs, err := n.fs.C.ListDir(ctx, n.fs.Volume, n.logical(), "")
	if err != nil {
		return nil, toErrno(err)
	}
	seen := map[string]bool{}
	var ents []fuse.DirEntry
	for _, o := range objs {
		if seen[o.Name] {
			continue // same name on several disks: one entry (lowest ordinal, FR-A07)
		}
		seen[o.Name] = true
		mode := uint32(syscall.S_IFREG)
		if o.Type == "dir" {
			mode = syscall.S_IFDIR
		}
		ents = append(ents, fuse.DirEntry{Name: o.Name, Mode: mode})
	}
	return gofs.NewListDirStream(ents), 0
}

func (n *node) Getattr(ctx context.Context, fh gofs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if n.IsRoot() {
		out.Mode = syscall.S_IFDIR | 0o755
		out.Uid, out.Gid = uint32(os.Getuid()), uint32(os.Getgid())
		return 0
	}
	if h, ok := fh.(*handle); ok { // open file: ask the descriptor
		return h.Getattr(ctx, out)
	}
	o, ok, err := n.fs.pick(ctx, n.logical())
	if err != nil || !ok {
		return syscall.ENOENT
	}
	var fi os.FileInfo
	if o.Type == "file" {
		if p, err := n.fs.physical(ctx, o); err == nil {
			fi, _ = os.Lstat(p)
		}
	}
	fill(&out.Attr, o, fi)
	out.SetTimeout(time.Second)
	return 0
}

// Symlink stores the link on the disk chosen by the placement policy (like a new file).
func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*gofs.Inode, syscall.Errno) {
	if n.fs.ReadOnly {
		return nil, syscall.EROFS
	}
	lp := join(n.logical(), name)
	if _, ok, _ := n.fs.pick(ctx, lp); ok {
		return nil, syscall.EEXIST
	}
	d, err := n.fs.place(ctx, lp)
	if err != nil {
		return nil, toErrno(err)
	}
	p, err := core.PhysicalPath(d.DataRoot, lp)
	if err != nil {
		return nil, syscall.EINVAL
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.Symlink(target, p); err != nil {
		return nil, toErrno(err)
	}
	n.fs.commit(ctx, lp, d.ID, p)
	return n.child(ctx, core.Object{Type: "file", LogicalPath: lp, DiskID: d.ID}, out), 0
}

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	o, ok, err := n.fs.pick(ctx, n.logical())
	if err != nil || !ok {
		return nil, syscall.ENOENT
	}
	p, err := n.fs.physical(ctx, o)
	if err != nil {
		return nil, toErrno(err)
	}
	t, err := os.Readlink(p)
	if err != nil {
		return nil, toErrno(err)
	}
	return []byte(t), 0
}

func (n *node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	v, err := n.fs.C.GetVolume(ctx, n.fs.Volume)
	if err != nil {
		return toErrno(err)
	}
	const bs = 4096
	out.Bsize, out.Frsize = bs, bs
	out.Blocks = uint64(v.Summary.UsableBytes) / bs
	out.Bfree = uint64(v.Summary.FreeBytes) / bs
	out.Bavail = out.Bfree
	out.NameLen = 255
	return 0
}

// handle is an open file on one disk; on release the catalog learns the new size/mtime.
type handle struct {
	*gofs.LoopbackFile
	fs      *FS
	logical string
	diskID  string
	rel     string
	path    string
	written bool
}

var (
	_ gofs.FileReleaser = (*handle)(nil)
	_ gofs.FileFlusher  = (*handle)(nil)
)

func (h *handle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.written = true
	return h.LoopbackFile.Write(ctx, data, off)
}

func (h *handle) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	h.written = true
	return h.LoopbackFile.Setattr(ctx, in, out)
}

// Flush runs synchronously in close(2) (RELEASE arrives later): commit here so the catalog is current
// when the writer's close returns.
func (h *handle) Flush(ctx context.Context) syscall.Errno {
	errno := h.LoopbackFile.Flush(ctx)
	if h.written {
		h.fs.commit(context.Background(), h.logical, h.diskID, h.path)
	}
	return errno
}

func (h *handle) Release(ctx context.Context) syscall.Errno {
	errno := h.LoopbackFile.Release(ctx)
	if h.written {
		h.fs.commit(context.Background(), h.logical, h.diskID, h.path)
	}
	return errno
}

func (n *node) Open(ctx context.Context, flags uint32) (gofs.FileHandle, uint32, syscall.Errno) {
	writing := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0
	if writing && n.fs.ReadOnly {
		return nil, 0, syscall.EROFS
	}
	lp := n.logical()
	o, ok, err := n.fs.pick(ctx, lp)
	if err != nil || !ok {
		return nil, 0, syscall.ENOENT
	}
	p, err := n.fs.physical(ctx, o)
	if err != nil {
		return nil, 0, toErrno(err)
	}
	fd, err := os.OpenFile(p, int(flags)&^syscall.O_CREAT, 0)
	if err != nil {
		return nil, 0, toErrno(err)
	}
	h := &handle{LoopbackFile: gofs.NewLoopbackFileFromOS(fd), fs: n.fs, logical: lp, diskID: o.DiskID, path: p, written: flags&syscall.O_TRUNC != 0}
	return h, 0, 0
}

func (n *node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*gofs.Inode, gofs.FileHandle, uint32, syscall.Errno) {
	if n.fs.ReadOnly {
		return nil, nil, 0, syscall.EROFS
	}
	lp := join(n.logical(), name)
	if _, ok, _ := n.fs.pick(ctx, lp); ok {
		return nil, nil, 0, syscall.EEXIST
	}
	d, err := n.fs.place(ctx, lp)
	if err != nil {
		return nil, nil, 0, toErrno(err)
	}
	p, err := core.PhysicalPath(d.DataRoot, lp)
	if err != nil {
		return nil, nil, 0, syscall.EINVAL
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, nil, 0, toErrno(err)
	}
	fd, err := os.OpenFile(p, int(flags)|os.O_CREATE|os.O_EXCL, os.FileMode(mode).Perm())
	if err != nil {
		return nil, nil, 0, toErrno(err)
	}
	n.fs.commit(ctx, lp, d.ID, p) // visible immediately (size 0), finalized on release
	n.fs.C.Audit.Append(ctx, audit.Entry{Action: "vfs.create", Target: lp, VolumeID: n.fs.Volume, Destination: p, Result: "disk " + d.Name})
	fi, _ := fd.Stat()
	o := core.Object{Type: "file", Mode: int64(mode)}
	fill(&out.Attr, o, fi)
	ino := n.NewInode(ctx, &node{fs: n.fs}, gofs.StableAttr{Mode: syscall.S_IFREG})
	return ino, &handle{LoopbackFile: gofs.NewLoopbackFileFromOS(fd), fs: n.fs, logical: lp, diskID: d.ID, path: p, written: true}, 0, 0
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*gofs.Inode, syscall.Errno) {
	if n.fs.ReadOnly {
		return nil, syscall.EROFS
	}
	lp := join(n.logical(), name)
	if _, ok, _ := n.fs.pick(ctx, lp); ok {
		return nil, syscall.EEXIST
	}
	if _, err := n.fs.E.Mkdir(ctx, n.fs.Volume, lp, 0); err != nil { // logical until a file lands (FR-T13)
		return nil, toErrno(err)
	}
	return n.child(ctx, core.Object{Type: "dir", LogicalPath: lp}, out), 0
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	if n.fs.ReadOnly {
		return syscall.EROFS
	}
	_, err := n.fs.E.Delete(ctx, n.fs.Volume, join(n.logical(), name), false, false) // → volume trash
	return toErrno(err)
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	if n.fs.ReadOnly {
		return syscall.EROFS
	}
	lp := join(n.logical(), name)
	objs, err := n.fs.C.ListDir(ctx, n.fs.Volume, lp, "")
	if err != nil {
		return toErrno(err)
	}
	if len(objs) > 0 {
		return syscall.ENOTEMPTY
	}
	_, err = n.fs.E.Delete(ctx, n.fs.Volume, lp, true, false)
	return toErrno(err)
}

func (n *node) Rename(ctx context.Context, name string, newParent gofs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if n.fs.ReadOnly {
		return syscall.EROFS
	}
	from := join(n.logical(), name)
	to := join("/"+newParent.EmbeddedInode().Path(nil), newName)
	if _, ok, _ := n.fs.pick(ctx, to); ok { // POSIX rename replaces the target (editors save this way)
		if flags&1 != 0 { // RENAME_NOREPLACE
			return syscall.EEXIST
		}
		if _, err := n.fs.E.Delete(ctx, n.fs.Volume, to, true, false); err != nil {
			return toErrno(err)
		}
	}
	return toErrno(n.fs.E.Rename(ctx, n.fs.Volume, from, to))
}

func (n *node) Setattr(ctx context.Context, fh gofs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if h, ok := fh.(*handle); ok {
		return h.Setattr(ctx, in, out)
	}
	if n.fs.ReadOnly {
		return syscall.EROFS
	}
	o, ok, err := n.fs.pick(ctx, n.logical())
	if err != nil || !ok {
		return syscall.ENOENT
	}
	if o.Type == "dir" {
		return n.Getattr(ctx, nil, out)
	}
	p, err := n.fs.physical(ctx, o)
	if err != nil {
		return toErrno(err)
	}
	if sz, ok := in.GetSize(); ok {
		if err := os.Truncate(p, int64(sz)); err != nil {
			return toErrno(err)
		}
	}
	if m, ok := in.GetMode(); ok {
		os.Chmod(p, os.FileMode(m).Perm())
	}
	if mt, ok := in.GetMTime(); ok {
		os.Chtimes(p, time.Now(), mt)
	}
	n.fs.commit(ctx, o.LogicalPath, o.DiskID, p)
	return n.Getattr(ctx, nil, out)
}

// macFUSEMount is the helper go-fuse runs on macOS (macFUSE ≥ 4; FSKit backend from 5.4 on macOS 26).
const macFUSEMount = "/Library/Filesystems/macfuse.fs/Contents/Resources/mount_macfuse"

// mountTimeout bounds a macOS mount: when mount_macfuse fails before handing over the FUSE fd (extension not
// approved / not loaded), go-fuse waits on the socket forever. Linux fails fast and needs no bound.
var mountTimeout = 15 * time.Second

func mountWithTimeout(dir string, root gofs.InodeEmbedder, opts *gofs.Options) (*fuse.Server, error) {
	if runtime.GOOS != "darwin" {
		return gofs.Mount(dir, root, opts)
	}
	type res struct {
		srv *fuse.Server
		err error
	}
	ch := make(chan res, 1)
	go func() { // on timeout this goroutine stays blocked in go-fuse; one leak per failed attempt
		srv, err := gofs.Mount(dir, root, opts)
		ch <- res{srv, err}
	}()
	select {
	case r := <-ch:
		return r.srv, r.err
	case <-time.After(mountTimeout):
		return nil, errors.New("macFUSE did not start the file system — approve the macFUSE system extension " +
			"(System Settings → Privacy & Security, then restart) or enable it under General → Login Items & Extensions → File System Extensions")
	}
}

// Available reports whether this host can mount (Linux /dev/fuse, macOS macFUSE).
func Available() error {
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/dev/fuse"); err != nil {
			return errors.New("no /dev/fuse — install fuse3 (container: --device /dev/fuse --cap-add SYS_ADMIN)")
		}
	case "darwin":
		if _, err := os.Stat(macFUSEMount); err != nil {
			return errors.New("macFUSE is not installed — https://macfuse.github.io (optional dependency, FR-R07); the SMB access point works without it")
		}
	default:
		return fmt.Errorf("native VFS is not supported on %s — use the SMB access point", runtime.GOOS)
	}
	return nil
}

// Mount mounts the volume at dir (Linux FUSE; macFUSE where installed).
func Mount(c *core.Core, e *transfer.Engine, volume, dir string, readOnly bool) (*FS, error) {
	v, err := c.GetVolume(context.Background(), volume)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f := &FS{C: c, E: e, Volume: v.ID, ReadOnly: readOnly}
	root := &node{fs: f}
	opts := &gofs.Options{MountOptions: fuse.MountOptions{FsName: "continuum:" + v.Slug, Name: "continuum", DirectMount: true, AllowOther: os.Getuid() == 0}}
	if readOnly {
		opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	}
	if runtime.GOOS == "darwin" { // Finder shows the volume name; no ._ AppleDouble files on the disks
		opts.MountOptions.Options = append(opts.MountOptions.Options, "volname="+strings.ReplaceAll(v.Name, ",", " "), "noappledouble", "noapplexattr")
	}
	sec := time.Second
	opts.EntryTimeout, opts.AttrTimeout, opts.NegativeTimeout = &sec, &sec, &sec
	srv, err := mountWithTimeout(dir, root, opts)
	if err != nil {
		return nil, core.Errf(core.CodeInvalid, "mount %s: %v (needs FUSE: Linux /dev/fuse, or macFUSE)", dir, err)
	}
	f.be = gofuseBackend{srv}
	c.Audit.Append(context.Background(), audit.Entry{Action: "vfs.mount", Target: dir, VolumeID: v.ID, Result: map[bool]string{true: "ro", false: "rw"}[readOnly]})
	return f, nil
}

type gofuseBackend struct{ srv *fuse.Server }

func (b gofuseBackend) unmount() error { return b.srv.Unmount() }
func (b gofuseBackend) wait()          { b.srv.Wait() }

// xattrPassThrough: this backend refuses extended attributes (ENOTSUP).
const xattrPassThrough = false
