//go:build darwin && cgo && fuset

// macOS backend without a kernel extension: cgofuse + FUSE-T (libfuse-t, NFS/FSKit inside), built with
// `CGO_ENABLED=1 go build -tags fuset`. Same semantics as the go-fuse backend (vfs.go): lowest-ordinal reads,
// placement-policy writes, catalog commit on flush/close, rename/delete through the engine (trash).
package vfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/sys/unix"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/transfer"
)

// FUSETLib is the FUSE-T library loaded by the patched cgofuse (third_party/cgofuse).
const FUSETLib = "/usr/local/lib/libfuse-t.dylib"

// UnsafeOptIn must be set to mount with FUSE-T: on macOS 26.6 (25G83) mounting/unmounting through FUSE-T's
// NFS loopback repeatedly triggered a kernel panic in the macOS NFS client
// ("nfs_vinvalbuf2: ubc_msync failed!, error 22", nfs_bio.c:4260) — the machine reboots. The backend stays
// experimental and off until that is understood; the SMB access point is the supported macOS path.
const UnsafeOptIn = "CONTINUUM_VFS_FUSET"

// Available reports whether FUSE-T is installed and explicitly enabled.
func Available() error {
	if os.Getenv(UnsafeOptIn) != "i-accept-kernel-panic-risk" {
		return errors.New("FUSE-T VFS is disabled: it caused macOS kernel panics (NFS client, nfs_vinvalbuf2) in testing — use the SMB access point" +
			" (set " + UnsafeOptIn + "=i-accept-kernel-panic-risk only on a disposable test machine)")
	}
	if _, err := os.Stat(FUSETLib); err != nil {
		return errors.New("FUSE-T is not installed — https://www.fuse-t.org (optional dependency, FR-R07); the SMB access point works without it")
	}
	return nil
}

var mountTimeout = 15 * time.Second

type openFile struct {
	f       *os.File
	logical string
	diskID  string
	path    string
	written bool
}

type fusetFS struct {
	fuse.FileSystemBase
	fs    *FS
	mu    sync.Mutex
	files map[uint64]*openFile
	next  uint64
	ready chan struct{}
}

type fusetBackend struct {
	host *fuse.FileSystemHost
	done chan struct{}
}

func (b *fusetBackend) unmount() error {
	if !b.host.Unmount() {
		return errors.New("FUSE-T unmount failed (busy?)")
	}
	<-b.done
	return nil
}
func (b *fusetBackend) wait() { <-b.done }

// Mount mounts the volume at dir with FUSE-T.
func Mount(c *core.Core, e *transfer.Engine, volume, dir string, readOnly bool) (*FS, error) {
	if err := Available(); err != nil {
		return nil, core.Errf(core.CodeInvalid, "%v", err)
	}
	v, err := c.GetVolume(context.Background(), volume)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f := &FS{C: c, E: e, Volume: v.ID, ReadOnly: readOnly}
	impl := &fusetFS{fs: f, files: map[uint64]*openFile{}, ready: make(chan struct{})}
	host := fuse.NewFileSystemHost(impl)
	opts := []string{"-o", "fsname=continuum:" + v.Slug + ",volname=" + strings.ReplaceAll(v.Name, ",", " ") + ",namedattr,noappledouble"} // xattrs via NFS named attributes → no ._ files
	if readOnly {
		opts = append(opts, "-o", "ro")
	}
	be := &fusetBackend{host: host, done: make(chan struct{})}
	failed := make(chan struct{})
	go func() {
		ok := host.Mount(dir, opts) // runs until unmounted
		if !ok {
			close(failed)
		}
		close(be.done)
	}()
	select {
	case <-impl.ready:
		if !attached(dir, mountTimeout) { // FUSE-T calls Init before the NFS mount is attached to dir
			host.Unmount()
			return nil, core.Errf(core.CodeInvalid, "mount %s: FUSE-T started but the mount never appeared", dir)
		}
	case <-failed:
		return nil, core.Errf(core.CodeInvalid, "mount %s: FUSE-T refused the mount (see the controller log)", dir)
	case <-time.After(mountTimeout):
		host.Unmount()
		return nil, core.Errf(core.CodeInvalid, "mount %s: FUSE-T did not start within %s", dir, mountTimeout)
	}
	f.be = be
	c.Audit.Append(context.Background(), audit.Entry{Action: "vfs.mount", Target: dir, VolumeID: v.ID, Result: map[bool]string{true: "ro", false: "rw"}[readOnly] + " (FUSE-T)"})
	return f, nil
}

// attached waits until dir is a different filesystem than its parent.
func attached(dir string, max time.Duration) bool {
	var parent syscall.Statfs_t
	if syscall.Statfs(filepath.Dir(dir), &parent) != nil {
		return false
	}
	for end := time.Now().Add(max); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		var st syscall.Statfs_t
		if syscall.Statfs(dir, &st) == nil && st.Fsid != parent.Fsid {
			return true
		}
	}
	return false
}

func neg(err error) int { return -int(toErrno(err)) }

func (x *fusetFS) Init() {
	select {
	case <-x.ready:
	default:
		close(x.ready)
	}
}

func clean(p string) string { return path.Clean("/" + p) }

func (x *fusetFS) stat(o core.Object, phys os.FileInfo, st *fuse.Stat_t) {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	*st = fuse.Stat_t{Uid: uid, Gid: gid, Nlink: 1}
	switch {
	case o.Type == "dir":
		st.Mode, st.Nlink = fuse.S_IFDIR|0o755, 2
	case phys != nil && phys.Mode()&os.ModeSymlink != 0:
		st.Mode = fuse.S_IFLNK | 0o777
	default:
		perm := uint32(os.FileMode(o.Mode).Perm())
		if perm == 0 {
			perm = 0o644
		}
		st.Mode, st.Size = fuse.S_IFREG|perm, o.Size
	}
	t := time.Unix(0, o.MtimeNs)
	if phys != nil && phys.Mode()&os.ModeSymlink == 0 && o.Type != "dir" {
		st.Size, t = phys.Size(), phys.ModTime()
	}
	ts := fuse.NewTimespec(t)
	st.Atim, st.Mtim, st.Ctim, st.Birthtim = ts, ts, ts, ts
}

func (x *fusetFS) Getattr(p string, st *fuse.Stat_t, fh uint64) int {
	ctx := context.Background()
	if p = clean(p); p == "/" {
		x.stat(core.Object{Type: "dir"}, nil, st)
		return 0
	}
	x.mu.Lock()
	of := x.files[fh]
	x.mu.Unlock()
	if of != nil {
		if fi, err := of.f.Stat(); err == nil {
			x.stat(core.Object{Type: "file", Mode: int64(fi.Mode().Perm())}, fi, st)
			return 0
		}
	}
	o, ok, err := x.fs.pick(ctx, p)
	if err != nil {
		return neg(err)
	}
	if !ok {
		return -fuse.ENOENT
	}
	var fi os.FileInfo
	if o.Type == "file" {
		if pp, err := x.fs.physical(ctx, o); err == nil {
			fi, _ = os.Lstat(pp)
		}
	}
	x.stat(o, fi, st)
	return 0
}

func (x *fusetFS) Readdir(p string, fill func(name string, st *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	objs, err := x.fs.C.ListDir(context.Background(), x.fs.Volume, clean(p), "")
	if err != nil {
		return neg(err)
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	seen := map[string]bool{}
	for _, o := range objs {
		if seen[o.Name] {
			continue // FR-A07: one entry per name (lowest ordinal)
		}
		seen[o.Name] = true
		if !fill(o.Name, nil, 0) {
			break
		}
	}
	return 0
}

func (x *fusetFS) add(of *openFile) uint64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.next++
	x.files[x.next] = of
	return x.next
}

func (x *fusetFS) get(fh uint64) *openFile {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.files[fh]
}

func (x *fusetFS) Open(p string, flags int) (int, uint64) {
	ctx := context.Background()
	p = clean(p)
	writing := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0
	if writing && x.fs.ReadOnly {
		return -fuse.EROFS, ^uint64(0)
	}
	o, ok, err := x.fs.pick(ctx, p)
	if err != nil || !ok || o.Type == "dir" {
		return -fuse.ENOENT, ^uint64(0)
	}
	pp, err := x.fs.physical(ctx, o)
	if err != nil {
		return neg(err), ^uint64(0)
	}
	fd, err := os.OpenFile(pp, flags&^syscall.O_CREAT, 0)
	if err != nil {
		return neg(err), ^uint64(0)
	}
	return 0, x.add(&openFile{f: fd, logical: p, diskID: o.DiskID, path: pp, written: flags&syscall.O_TRUNC != 0})
}

func (x *fusetFS) Create(p string, flags int, mode uint32) (int, uint64) {
	ctx := context.Background()
	p = clean(p)
	if x.fs.ReadOnly {
		return -fuse.EROFS, ^uint64(0)
	}
	if _, ok, _ := x.fs.pick(ctx, p); ok {
		return -fuse.EEXIST, ^uint64(0)
	}
	d, err := x.fs.place(ctx, p)
	if err != nil {
		return neg(err), ^uint64(0)
	}
	pp, err := core.PhysicalPath(d.DataRoot, p)
	if err != nil {
		return -fuse.EINVAL, ^uint64(0)
	}
	if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
		return neg(err), ^uint64(0)
	}
	fd, err := os.OpenFile(pp, flags|os.O_CREATE|os.O_EXCL, os.FileMode(mode).Perm())
	if err != nil {
		return neg(err), ^uint64(0)
	}
	x.fs.commit(ctx, p, d.ID, pp)
	x.fs.C.Audit.Append(ctx, audit.Entry{Action: "vfs.create", Target: p, VolumeID: x.fs.Volume, Destination: pp, Result: "disk " + d.Name})
	return 0, x.add(&openFile{f: fd, logical: p, diskID: d.ID, path: pp, written: true})
}

func (x *fusetFS) Read(p string, buf []byte, ofst int64, fh uint64) int {
	of := x.get(fh)
	if of == nil {
		return -fuse.EBADF
	}
	n, err := of.f.ReadAt(buf, ofst)
	if err != nil && !errors.Is(err, io.EOF) {
		return neg(err)
	}
	return n
}

func (x *fusetFS) Write(p string, buf []byte, ofst int64, fh uint64) int {
	of := x.get(fh)
	if of == nil {
		return -fuse.EBADF
	}
	n, err := of.f.WriteAt(buf, ofst)
	if err != nil {
		return neg(err)
	}
	of.written = true
	return n
}

func (x *fusetFS) Truncate(p string, size int64, fh uint64) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	if of := x.get(fh); of != nil {
		if err := of.f.Truncate(size); err != nil {
			return neg(err)
		}
		of.written = true
		return 0
	}
	return x.onPhysical(p, func(pp string) error { return os.Truncate(pp, size) })
}

// onPhysical applies a metadata change to the served copy and refreshes the catalog.
func (x *fusetFS) onPhysical(p string, fn func(pp string) error) int {
	ctx := context.Background()
	o, ok, err := x.fs.pick(ctx, clean(p))
	if err != nil || !ok {
		return -fuse.ENOENT
	}
	if o.Type == "dir" {
		return 0
	}
	pp, err := x.fs.physical(ctx, o)
	if err != nil {
		return neg(err)
	}
	if err := fn(pp); err != nil {
		return neg(err)
	}
	x.fs.commit(ctx, o.LogicalPath, o.DiskID, pp)
	return 0
}

func (x *fusetFS) Chmod(p string, mode uint32) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	return x.onPhysical(p, func(pp string) error { return os.Chmod(pp, os.FileMode(mode).Perm()) })
}

func (x *fusetFS) Utimens(p string, tmsp []fuse.Timespec) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	mt := time.Now()
	if len(tmsp) > 1 {
		mt = tmsp[1].Time()
	}
	return x.onPhysical(p, func(pp string) error { return os.Chtimes(pp, time.Now(), mt) })
}

func (x *fusetFS) Chown(p string, uid, gid uint32) int { return 0 } // ownership is the controller user

func (x *fusetFS) finish(fh uint64, release bool) int {
	of := x.get(fh)
	if of == nil {
		return 0
	}
	var err error
	if release {
		x.mu.Lock()
		delete(x.files, fh)
		x.mu.Unlock()
		err = of.f.Close()
	} else {
		err = of.f.Sync()
	}
	if of.written {
		x.fs.commit(context.Background(), of.logical, of.diskID, of.path)
	}
	if err != nil {
		return neg(err)
	}
	return 0
}

func (x *fusetFS) Flush(p string, fh uint64) int                { return x.finish(fh, false) }
func (x *fusetFS) Release(p string, fh uint64) int              { return x.finish(fh, true) }
func (x *fusetFS) Fsync(p string, datasync bool, fh uint64) int { return x.finish(fh, false) }
func (x *fusetFS) Opendir(p string) (int, uint64)               { return 0, 0 }
func (x *fusetFS) Releasedir(p string, fh uint64) int           { return 0 }
func (x *fusetFS) Access(p string, mask uint32) int             { return 0 }

// Extended attributes (Finder tags, quarantine, resource forks) are stored on the served copy itself, so the
// macOS NFS client never falls back to "._name" AppleDouble files on the disks. Logical folders have no
// single physical copy: their attributes are accepted and not kept.
func (x *fusetFS) xattrPath(p string) (string, bool, int) {
	ctx := context.Background()
	o, ok, err := x.fs.pick(ctx, clean(p))
	if err != nil || !ok {
		return "", false, -fuse.ENOENT
	}
	if o.Type == "dir" {
		return "", true, 0
	}
	pp, err := x.fs.physical(ctx, o)
	if err != nil {
		return "", false, neg(err)
	}
	return pp, false, 0
}

func (x *fusetFS) Setxattr(p, name string, value []byte, flags int) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	pp, dir, rc := x.xattrPath(p)
	if rc != 0 || dir {
		return rc
	}
	return neg(unix.Lsetxattr(pp, name, value, flags))
}

func (x *fusetFS) Getxattr(p, name string) (int, []byte) {
	pp, dir, rc := x.xattrPath(p)
	if rc != 0 {
		return rc, nil
	}
	if dir {
		return -fuse.ENOATTR, nil
	}
	sz, err := unix.Lgetxattr(pp, name, nil)
	if err != nil {
		return neg(err), nil
	}
	buf := make([]byte, sz)
	n, err := unix.Lgetxattr(pp, name, buf)
	if err != nil {
		return neg(err), nil
	}
	return 0, buf[:n]
}

func (x *fusetFS) Removexattr(p, name string) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	pp, dir, rc := x.xattrPath(p)
	if rc != 0 || dir {
		return rc
	}
	return neg(unix.Lremovexattr(pp, name))
}

func (x *fusetFS) Listxattr(p string, fill func(name string) bool) int {
	pp, dir, rc := x.xattrPath(p)
	if rc != 0 || dir {
		return rc
	}
	sz, err := unix.Llistxattr(pp, nil)
	if err != nil || sz == 0 {
		return neg(err)
	}
	buf := make([]byte, sz)
	n, err := unix.Llistxattr(pp, buf)
	if err != nil {
		return neg(err)
	}
	for _, name := range strings.Split(strings.TrimRight(string(buf[:n]), "\x00"), "\x00") {
		if name != "" && !fill(name) {
			break
		}
	}
	return 0
}

func (x *fusetFS) Mkdir(p string, mode uint32) int {
	ctx := context.Background()
	p = clean(p)
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	if _, ok, _ := x.fs.pick(ctx, p); ok {
		return -fuse.EEXIST
	}
	if _, err := x.fs.E.Mkdir(ctx, x.fs.Volume, p, 0); err != nil {
		return neg(err)
	}
	return 0
}

func (x *fusetFS) Unlink(p string) int {
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	_, err := x.fs.E.Delete(context.Background(), x.fs.Volume, clean(p), false, false) // → volume trash
	return neg(err)
}

func (x *fusetFS) Rmdir(p string) int {
	ctx := context.Background()
	p = clean(p)
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	objs, err := x.fs.C.ListDir(ctx, x.fs.Volume, p, "")
	if err != nil {
		return neg(err)
	}
	if len(objs) > 0 {
		return -fuse.ENOTEMPTY
	}
	_, err = x.fs.E.Delete(ctx, x.fs.Volume, p, true, false)
	return neg(err)
}

func (x *fusetFS) Rename(from, to string) int {
	ctx := context.Background()
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	from, to = clean(from), clean(to)
	if _, ok, _ := x.fs.pick(ctx, to); ok { // POSIX rename replaces the target
		if _, err := x.fs.E.Delete(ctx, x.fs.Volume, to, true, false); err != nil {
			return neg(err)
		}
	}
	return neg(x.fs.E.Rename(ctx, x.fs.Volume, from, to))
}

func (x *fusetFS) Symlink(target, p string) int {
	ctx := context.Background()
	p = clean(p)
	if x.fs.ReadOnly {
		return -fuse.EROFS
	}
	if _, ok, _ := x.fs.pick(ctx, p); ok {
		return -fuse.EEXIST
	}
	d, err := x.fs.place(ctx, p)
	if err != nil {
		return neg(err)
	}
	pp, err := core.PhysicalPath(d.DataRoot, p)
	if err != nil {
		return -fuse.EINVAL
	}
	os.MkdirAll(filepath.Dir(pp), 0o755)
	if err := os.Symlink(target, pp); err != nil {
		return neg(err)
	}
	x.fs.commit(ctx, p, d.ID, pp)
	return 0
}

func (x *fusetFS) Readlink(p string) (int, string) {
	ctx := context.Background()
	o, ok, err := x.fs.pick(ctx, clean(p))
	if err != nil || !ok {
		return -fuse.ENOENT, ""
	}
	pp, err := x.fs.physical(ctx, o)
	if err != nil {
		return neg(err), ""
	}
	t, err := os.Readlink(pp)
	if err != nil {
		return neg(err), ""
	}
	return 0, t
}

func (x *fusetFS) Statfs(p string, st *fuse.Statfs_t) int {
	v, err := x.fs.C.GetVolume(context.Background(), x.fs.Volume)
	if err != nil {
		return neg(err)
	}
	const bs = 4096
	*st = fuse.Statfs_t{Bsize: bs, Frsize: bs, Blocks: uint64(v.Summary.UsableBytes) / bs, Bfree: uint64(v.Summary.FreeBytes) / bs,
		Bavail: uint64(v.Summary.FreeBytes) / bs, Namemax: 255}
	return 0
}

// xattrPassThrough: this backend stores extended attributes on the served copy.
const xattrPassThrough = true
