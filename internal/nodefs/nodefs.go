// Package nodefs abstracts the filesystem of the node that owns a disk (FR-N02, P3). The transfer engine
// talks to every disk through FS: Local runs on this host; the Controller's agent hub implements FS for
// remote nodes by sending agentproto commands. The agent executes those commands with Local, so both sides
// share one implementation of every operation (atomic put, BLAKE3, walk …).
package nodefs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/zeebo/blake3"

	"continuum/internal/agentproto"
	"continuum/internal/disk"
)

type (
	FileInfo  = agentproto.FileInfo
	WalkEntry = agentproto.WalkEntry
	PutArgs   = agentproto.PutArgs
	PutResult = agentproto.PutResult
)

// FS is the per-node filesystem used for disk data. Errors wrap fs.ErrNotExist / fs.ErrExist / ErrNoSpace
// so callers can use errors.Is regardless of where the disk is.
type FS interface {
	Local() bool
	Stat(ctx context.Context, p string) (FileInfo, error) // lstat semantics
	ReadDir(ctx context.Context, p string) ([]FileInfo, error)
	Walk(ctx context.Context, root string) ([]WalkEntry, error)
	MkdirAll(ctx context.Context, p string) error
	Remove(ctx context.Context, p string) error
	RemoveAll(ctx context.Context, p string) error
	Rename(ctx context.Context, from, to string) error
	Symlink(ctx context.Context, target, link string) error
	Chtimes(ctx context.Context, p string, mtimeNs int64) error
	Hash(ctx context.Context, p string) (string, error)
	PartialHash(ctx context.Context, p string) (string, error)
	StatFS(ctx context.Context, p string) (disk.Capacity, error)
	ReadFile(ctx context.Context, p string) ([]byte, error)
	WriteFile(ctx context.Context, p string, data []byte, mode fs.FileMode) error
	// Put streams r into args.Partial (fsync), optionally re-reads it for the checksum, applies mode/mtime and
	// renames to args.Final. The returned checksum is of the bytes on disk.
	Put(ctx context.Context, args PutArgs, r io.Reader) (PutResult, error)
	// Open streams a file's content.
	Open(ctx context.Context, p string) (io.ReadCloser, error)
}

// ErrNoSpace marks ENOSPC on any node.
var ErrNoSpace = errors.New("no space left on device")

// ErrNoDirect: the nodes cannot transfer directly (old agent / no peer endpoint) — relay through the Controller.
var ErrNoDirect = errors.New("direct transfer not available")

// ErrPeerUnreachable: a direct agent ↔ agent transfer could not reach the source agent (caller relays instead).
var ErrPeerUnreachable = errors.New("peer agent unreachable")

// Classify maps OS errors to the portable sentinels.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOSPC) {
		return ErrNoSpace
	}
	return err
}

// Kind names a portable error class for the wire.
func Kind(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "not_exist"
	case errors.Is(err, fs.ErrExist):
		return "exist"
	case errors.Is(err, ErrNoSpace), errors.Is(err, syscall.ENOSPC):
		return "no_space"
	case errors.Is(err, ErrPeerUnreachable):
		return "peer_unreachable"
	}
	return ""
}

// FromKind rebuilds an error that errors.Is understands.
func FromKind(kind, msg string) error {
	switch kind {
	case "not_exist":
		return fmt.Errorf("%s: %w", msg, fs.ErrNotExist)
	case "exist":
		return fmt.Errorf("%s: %w", msg, fs.ErrExist)
	case "no_space":
		return fmt.Errorf("%s: %w", msg, ErrNoSpace)
	case "peer_unreachable":
		return fmt.Errorf("%s: %w", msg, ErrPeerUnreachable)
	}
	return errors.New(msg)
}

// Local is this host's filesystem.
type Local struct{}

func (Local) Local() bool { return true }

func info(name string, fi fs.FileInfo, p string) FileInfo {
	out := FileInfo{Name: name, Size: fi.Size(), Mode: uint32(fi.Mode()), MtimeNs: fi.ModTime().UnixNano(), IsDir: fi.IsDir()}
	if fi.Mode()&fs.ModeSymlink != 0 {
		out.Link, _ = os.Readlink(p)
	}
	return out
}

func (Local) Stat(_ context.Context, p string) (FileInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return info(fi.Name(), fi, p), nil
}

func (Local) ReadDir(_ context.Context, p string) ([]FileInfo, error) {
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(ents))
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			out = append(out, info(e.Name(), fi, filepath.Join(p, e.Name())))
		}
	}
	return out, nil
}

func (Local) Walk(ctx context.Context, root string) ([]WalkEntry, error) {
	root = filepath.Clean(root)
	out := []WalkEntry{}
	err := filepath.WalkDir(root, func(p string, de fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // unreadable subtree: skip
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		fi, err := de.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, WalkEntry{Rel: filepath.ToSlash(rel), FileInfo: info(de.Name(), fi, p)})
		return nil
	})
	return out, err
}

func (Local) MkdirAll(_ context.Context, p string) error { return Classify(os.MkdirAll(p, 0o755)) }
func (Local) Remove(_ context.Context, p string) error   { return os.Remove(p) }
func (Local) RemoveAll(_ context.Context, p string) error {
	return os.RemoveAll(p)
}
func (Local) Rename(_ context.Context, from, to string) error { return Classify(os.Rename(from, to)) }
func (Local) Symlink(_ context.Context, target, link string) error {
	return os.Symlink(target, link)
}
func (Local) Chtimes(_ context.Context, p string, mtimeNs int64) error {
	return os.Chtimes(p, time.Now(), time.Unix(0, mtimeNs))
}
func (Local) Hash(_ context.Context, p string) (string, error) { return HashFile(p) }

// PartialChunk is the head/tail size used by PartialHash (duplicate finder stage 2).
const PartialChunk = 64 << 10

func (Local) PartialHash(_ context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	h := blake3.New()
	if _, err := io.CopyN(h, f, min(size, PartialChunk)); err != nil {
		return "", err
	}
	if size > 2*PartialChunk {
		if _, err := f.Seek(-PartialChunk, io.SeekEnd); err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (Local) StatFS(_ context.Context, p string) (disk.Capacity, error) { return disk.Stat(p) }
func (Local) ReadFile(_ context.Context, p string) ([]byte, error)      { return os.ReadFile(p) }

func (Local) WriteFile(_ context.Context, p string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Classify(err)
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return Classify(err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return Classify(err)
	}
	f.Sync()
	f.Close()
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return Classify(err)
	}
	SyncDir(filepath.Dir(p))
	return nil
}

func (Local) Open(_ context.Context, p string) (io.ReadCloser, error) { return os.Open(p) }

// Put: steps 4–11 of the atomic copy for a streamed source (PRD §11.2). The partial is removed on failure.
func (Local) Put(ctx context.Context, a PutArgs, r io.Reader) (PutResult, error) {
	var res PutResult
	if err := os.MkdirAll(filepath.Dir(a.Final), 0o755); err != nil {
		return res, Classify(err)
	}
	out, err := os.OpenFile(a.Partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return res, Classify(err)
	}
	h := blake3.New()
	n, err := io.CopyBuffer(io.MultiWriter(out, h), r, make([]byte, 4<<20))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(a.Partial)
		return res, Classify(err)
	}
	if a.Size >= 0 && n != a.Size {
		os.Remove(a.Partial)
		return res, fmt.Errorf("short transfer: %d of %d bytes", n, a.Size)
	}
	res.Bytes = n
	res.Checksum = hex.EncodeToString(h.Sum(nil))
	if a.Verify { // re-read what is on the disk, not what we meant to write
		if res.Checksum, err = HashFile(a.Partial); err != nil {
			os.Remove(a.Partial)
			return res, err
		}
	}
	_ = os.Chmod(a.Partial, fs.FileMode(a.Mode).Perm())
	if a.MtimeNs != 0 {
		_ = os.Chtimes(a.Partial, time.Now(), time.Unix(0, a.MtimeNs))
	}
	if err := ctx.Err(); err != nil {
		os.Remove(a.Partial)
		return res, err
	}
	if err := os.Rename(a.Partial, a.Final); err != nil {
		os.Remove(a.Partial)
		return res, Classify(err)
	}
	SyncDir(filepath.Dir(a.Final))
	return res, nil
}

// HashFile is the BLAKE3 of a local file.
func HashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SyncDir fsyncs a directory (no-op on Windows).
func SyncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// Exists reports whether p exists on fsys.
func Exists(ctx context.Context, fsys FS, p string) bool {
	_, err := fsys.Stat(ctx, p)
	return err == nil
}

// RemoveEmptyParents deletes empty directories from dir up to (not including) stop.
func RemoveEmptyParents(ctx context.Context, fsys FS, dir, stop string) {
	stop = filepath.Clean(stop)
	for d := filepath.Clean(dir); d != stop && len(d) > len(stop); d = filepath.Dir(d) {
		if err := fsys.Remove(ctx, d); err != nil {
			return
		}
	}
}
