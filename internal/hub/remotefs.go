package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"continuum/internal/agentproto"
	"continuum/internal/disk"
	"continuum/internal/nodefs"
)

func remoteErr(r *agentproto.Result) error { return nodefs.FromKind(r.Kind, r.Error) }

// FS returns the filesystem of a remote node.
func (h *Hub) FS(node string) nodefs.FS { return &remoteFS{h: h, node: node} }

type remoteFS struct {
	h    *Hub
	node string
}

func (r *remoteFS) Local() bool { return false }

// Node is the node id behind this filesystem (direct transfers).
func (r *remoteFS) Node() string { return r.node }

func (r *remoteFS) call(ctx context.Context, op string, args, out any) error {
	return r.h.Call(ctx, r.node, op, args, out)
}

func (r *remoteFS) Stat(ctx context.Context, p string) (nodefs.FileInfo, error) {
	var fi nodefs.FileInfo
	err := r.call(ctx, agentproto.OpStat, agentproto.PathArgs{Path: p}, &fi)
	return fi, err
}

func (r *remoteFS) ReadDir(ctx context.Context, p string) ([]nodefs.FileInfo, error) {
	var out []nodefs.FileInfo
	err := r.call(ctx, agentproto.OpReadDir, agentproto.PathArgs{Path: p}, &out)
	return out, err
}

func (r *remoteFS) Walk(ctx context.Context, root string) ([]nodefs.WalkEntry, error) {
	var out []nodefs.WalkEntry
	err := r.call(ctx, agentproto.OpWalk, agentproto.PathArgs{Path: root}, &out)
	return out, err
}

func (r *remoteFS) MkdirAll(ctx context.Context, p string) error {
	return r.call(ctx, agentproto.OpMkdirAll, agentproto.PathArgs{Path: p}, nil)
}
func (r *remoteFS) Remove(ctx context.Context, p string) error {
	return r.call(ctx, agentproto.OpRemove, agentproto.PathArgs{Path: p}, nil)
}
func (r *remoteFS) RemoveAll(ctx context.Context, p string) error {
	return r.call(ctx, agentproto.OpRemoveAll, agentproto.PathArgs{Path: p}, nil)
}
func (r *remoteFS) Rename(ctx context.Context, from, to string) error {
	return r.call(ctx, agentproto.OpRename, agentproto.RenameArgs{From: from, To: to}, nil)
}
func (r *remoteFS) Symlink(ctx context.Context, target, link string) error {
	return r.call(ctx, agentproto.OpSymlink, agentproto.RenameArgs{From: target, To: link}, nil)
}
func (r *remoteFS) Chtimes(ctx context.Context, p string, mtimeNs int64) error {
	return r.call(ctx, agentproto.OpChtimes, agentproto.ChtimesArgs{Path: p, MtimeNs: mtimeNs}, nil)
}

func (r *remoteFS) Hash(ctx context.Context, p string) (string, error) {
	var hr agentproto.HashResult
	err := r.call(ctx, agentproto.OpHash, agentproto.PathArgs{Path: p}, &hr)
	return hr.Sum, err
}

func (r *remoteFS) PartialHash(ctx context.Context, p string) (string, error) {
	var hr agentproto.HashResult
	err := r.call(ctx, agentproto.OpPartial, agentproto.PathArgs{Path: p}, &hr)
	return hr.Sum, err
}

func (r *remoteFS) StatFS(ctx context.Context, p string) (disk.Capacity, error) {
	var c disk.Capacity
	err := r.call(ctx, agentproto.OpStatFS, agentproto.PathArgs{Path: p}, &c)
	return c, err
}

func (r *remoteFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	var b []byte
	err := r.call(ctx, agentproto.OpReadFile, agentproto.PathArgs{Path: p}, &b)
	return b, err
}

func (r *remoteFS) WriteFile(ctx context.Context, p string, data []byte, mode fs.FileMode) error {
	return r.call(ctx, agentproto.OpWriteFile, agentproto.WriteFileArgs{Path: p, Data: data, Mode: uint32(mode)}, nil)
}

// Put relays src to the agent: the agent GETs the blob while the controller streams it.
func (r *remoteFS) Put(ctx context.Context, a nodefs.PutArgs, src io.Reader) (nodefs.PutResult, error) {
	var res nodefs.PutResult
	a.BlobID = r.h.newBlob(r.node, src, nil)
	defer r.h.dropBlob(a.BlobID)
	err := r.call(ctx, agentproto.OpPut, a, &res)
	return res, err
}

// Open asks the agent to upload the file; the returned reader yields the bytes as they arrive.
func (r *remoteFS) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	if _, err := r.Stat(ctx, p); err != nil { // fail fast (not_exist) before starting an upload
		return nil, err
	}
	pr, pw := io.Pipe()
	id := r.h.newBlob(r.node, nil, pw)
	done := make(chan error, 1)
	go func() {
		err := r.call(ctx, agentproto.OpGet, agentproto.GetArgs{BlobID: id, Path: p}, nil)
		if err != nil {
			pw.CloseWithError(err)
		}
		r.h.dropBlob(id)
		done <- err
	}()
	return &remoteReader{pr: pr, done: done}, nil
}

type remoteReader struct {
	pr   *io.PipeReader
	done chan error
	err  error
	got  bool
}

func (rr *remoteReader) Read(p []byte) (int, error) {
	n, err := rr.pr.Read(p)
	if errors.Is(err, io.EOF) && !rr.got { // EOF is only final once the agent reported success
		rr.got = true
		if rr.err = <-rr.done; rr.err != nil {
			return n, fmt.Errorf("remote read: %w", rr.err)
		}
	}
	return n, err
}

func (rr *remoteReader) Close() error { return rr.pr.Close() }
