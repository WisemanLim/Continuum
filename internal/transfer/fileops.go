package transfer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/blake3"

	"continuum/internal/agentproto"
	"continuum/internal/nodefs"
)

const chunk = 4 << 20 // 4 MiB stream chunk (PRD §11.2 step 6)

// PartialSuffix marks in-flight files; SCAN/access points must ignore them (NFR-I03).
func partialName(final, jobID string) string { return final + ".continuum-partial-" + jobID }

// errPaused / errCancelled are cancellation causes.
var (
	errPaused    = errors.New("paused")
	errCancelled = errors.New("cancelled")
	errWindow    = fmt.Errorf("%w: outside its time window", errPaused) // keeps partials like a pause
	errNoSpace   = nodefs.ErrNoSpace
)

// copyResult is the outcome of copyAtomic.
type copyResult struct {
	Checksum string
	Resumed  int64 // bytes skipped thanks to an existing partial
}

// progressFn reports bytes written since the last call and the current offset.
type progressFn func(delta, offset int64)

// copyAtomic implements steps 4–11 of the 14-step atomic copy (PRD §11.2):
// mkdir → partial (resume from offset) → chunk copy → fsync → verify BLAKE3 → metadata → atomic rename.
// On pause the partial file is kept for resume; on any other failure it is removed and the source is untouched.
func copyAtomic(ctx context.Context, src, final, jobID string, verify bool, bwlimit int64, mode fs.FileMode, mtime time.Time, prog progressFn) (copyResult, error) {
	var res copyResult
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return res, classify(err)
	}
	part := partialName(final, jobID)

	in, err := os.Open(src)
	if err != nil {
		return res, err
	}
	defer in.Close()
	sfi, err := in.Stat()
	if err != nil {
		return res, err
	}

	var offset int64
	if pfi, err := os.Stat(part); err == nil && pfi.Size() <= sfi.Size() {
		offset = pfi.Size()
	}
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return res, classify(err)
	}
	cleanup := func() { out.Close(); os.Remove(part) }
	if offset > 0 {
		if _, err := in.Seek(offset, io.SeekStart); err != nil {
			cleanup()
			return res, err
		}
		if _, err := out.Seek(offset, io.SeekStart); err != nil {
			cleanup()
			return res, err
		}
		res.Resumed = offset
		prog(offset, offset)
	} else if err := out.Truncate(0); err != nil {
		cleanup()
		return res, err
	}

	// source hash: streamed while copying when starting from zero
	srcHash := blake3.New()
	hashing := verify && offset == 0
	buf := make([]byte, chunk)
	start := time.Now()
	written := offset
	for {
		if err := context.Cause(ctx); err != nil {
			out.Sync()
			out.Close()
			if errors.Is(err, errPaused) {
				return res, errPaused // keep partial (FR-T07)
			}
			os.Remove(part)
			return res, err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if hashing {
				srcHash.Write(buf[:n])
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				cleanup()
				return res, classify(werr)
			}
			written += int64(n)
			prog(int64(n), written)
			if bwlimit > 0 { // token-bucket-ish throttle (FR-T17)
				want := time.Duration(float64(written-offset) / float64(bwlimit) * float64(time.Second))
				if d := want - time.Since(start); d > 0 {
					time.Sleep(d)
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			cleanup()
			return res, rerr
		}
	}
	if err := out.Sync(); err != nil { // step 10: fsync file
		cleanup()
		return res, classify(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return res, classify(err)
	}

	if verify { // steps 7–8: BLAKE3 source vs re-read target
		var sum string
		if hashing {
			sum = hex.EncodeToString(srcHash.Sum(nil))
		} else if sum, err = hashFile(src); err != nil {
			os.Remove(part)
			return res, err
		}
		got, err := hashFile(part)
		if err != nil {
			os.Remove(part)
			return res, err
		}
		if got != sum {
			os.Remove(part)
			return res, fmt.Errorf("%w: source %s target %s", ErrVerify, sum[:16], got[:16])
		}
		res.Checksum = sum
	}

	// step 9: metadata (mode + mtime); unsupported bits are ignored by the FS
	_ = os.Chmod(part, mode.Perm())
	_ = os.Chtimes(part, time.Now(), mtime)

	// step 11: atomic rename, then fsync the parent directory
	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return res, classify(err)
	}
	syncDir(filepath.Dir(final))
	return res, nil
}

// copyFile copies src (on srcFS) to final (on dstFS). Local→local keeps the resumable copyAtomic; any remote
// side streams through the Controller: the source BLAKE3 is computed while streaming and compared with the
// checksum the destination node re-read from its disk, and only then is the partial renamed (PRD §11.2).
func (e *Engine) copyFile(ctx context.Context, srcFS nodefs.FS, src string, dstFS nodefs.FS, final, jobID string, verify bool, bwlimit int64,
	mode fs.FileMode, mtime time.Time, prog progressFn) (copyResult, error) {
	if srcFS.Local() && dstFS.Local() {
		return copyAtomic(ctx, src, final, jobID, verify, bwlimit, mode, mtime, prog)
	}
	var res copyResult
	st, err := srcFS.Stat(ctx, src)
	if err != nil {
		return res, err
	}
	if done, r, err := e.copyDirect(ctx, srcFS, src, dstFS, final, jobID, verify, bwlimit, mode, mtime, st.Size, prog); done {
		return r, err
	}
	in, err := srcFS.Open(ctx, src)
	if err != nil {
		return res, err
	}
	defer in.Close()
	part := partialName(final, jobID)
	mr := &meterReader{ctx: ctx, r: in, h: blake3.New(), bw: bwlimit, prog: prog, start: time.Now()}
	pr, err := dstFS.Put(ctx, nodefs.PutArgs{Final: part, Partial: part + ".w", Size: st.Size, Mode: uint32(mode), MtimeNs: mtime.UnixNano(), Verify: verify}, mr)
	if mr.err != nil {
		dstFS.Remove(context.Background(), part)
		return res, mr.err // paused / cancelled
	}
	if err != nil {
		return res, nodefs.Classify(err)
	}
	sum := hex.EncodeToString(mr.h.Sum(nil))
	if verify && pr.Checksum != sum {
		dstFS.Remove(context.Background(), part)
		return res, fmt.Errorf("%w: source %s target %s", ErrVerify, sum[:16], pr.Checksum[:min(16, len(pr.Checksum))])
	}
	if err := dstFS.Rename(ctx, part, final); err != nil {
		dstFS.Remove(context.Background(), part)
		return res, nodefs.Classify(err)
	}
	if verify {
		res.Checksum = sum
	}
	return res, nil
}

// copyDirect runs a remote → remote copy on the agents (same node: local copy there; different nodes: the
// target agent pulls from the source agent over mTLS). done=false means "not possible, relay instead".
// The ".w" partial survives pause / disconnect so the next attempt resumes at its length.
func (e *Engine) copyDirect(ctx context.Context, srcFS nodefs.FS, src string, dstFS nodefs.FS, final, jobID string, verify bool, bwlimit int64,
	mode fs.FileMode, mtime time.Time, size int64, prog progressFn) (bool, copyResult, error) {
	var res copyResult
	p, ok := e.Remote.(directPuller)
	sn, ok1 := srcFS.(noder)
	dn, ok2 := dstFS.(noder)
	if e.NoDirect || !ok || !ok1 || !ok2 || srcFS.Local() || dstFS.Local() {
		return false, res, nil
	}
	part := partialName(final, jobID)
	var mu sync.Mutex
	var reported int64 // bytes already passed to prog (agent reports absolute file length)
	advance := func(done int64) {
		mu.Lock()
		defer mu.Unlock()
		if done > reported {
			prog(done-reported, done)
			reported = done
		}
	}
	pctx := agentproto.WithProgress(ctx, advance) // in-file progress while the agents copy (E35)
	pr, err := p.Pull(pctx, sn.Node(), src, dn.Node(), agentproto.PullArgs{Final: part, Partial: part + ".w", Size: size,
		Mode: uint32(mode), MtimeNs: mtime.UnixNano(), Verify: verify, BwLimit: bwlimit})
	if errors.Is(err, nodefs.ErrNoDirect) || errors.Is(err, nodefs.ErrPeerUnreachable) {
		log.Printf("transfer: direct %s → %s not possible (%v), relaying through the controller", sn.Node(), dn.Node(), err)
		return false, res, nil
	}
	if err != nil {
		if c := context.Cause(ctx); c != nil {
			return true, res, c // paused / cancelled: partial kept for resume
		}
		if strings.Contains(err.Error(), "verify failed") {
			return true, res, fmt.Errorf("%w: %v", ErrVerify, err)
		}
		return true, res, nodefs.Classify(err)
	}
	advance(size)
	if err := dstFS.Rename(ctx, part, final); err != nil {
		dstFS.Remove(context.Background(), part)
		return true, res, nodefs.Classify(err)
	}
	e.direct.Add(1)
	if pr.Resumed > 0 {
		e.resumed.Add(1)
	}
	if verify {
		res.Checksum = pr.SourceSum
	}
	return true, res, nil
}

// DirectStats reports direct node ↔ node copies and how many resumed from a partial (tests, metrics).
func (e *Engine) DirectStats() (direct, resumed int64) { return e.direct.Load(), e.resumed.Load() }

// meterReader adds progress, bandwidth limit (FR-T17), cancellation and the source hash to a stream.
type meterReader struct {
	ctx   context.Context
	r     io.Reader
	h     *blake3.Hasher
	bw    int64
	prog  progressFn
	start time.Time
	n     int64
	err   error
}

func (m *meterReader) Read(p []byte) (int, error) {
	if err := context.Cause(m.ctx); err != nil {
		m.err = err
		return 0, err
	}
	n, err := m.r.Read(p)
	if n > 0 {
		m.h.Write(p[:n])
		m.n += int64(n)
		m.prog(int64(n), m.n)
		if m.bw > 0 {
			want := time.Duration(float64(m.n) / float64(m.bw) * float64(time.Second))
			if d := want - time.Since(m.start); d > 0 {
				time.Sleep(d)
			}
		}
	}
	return n, err
}

// ErrVerify marks a checksum mismatch (CLI exit 4).
var ErrVerify = errors.New("checksum mismatch")

func hashFile(p string) (string, error) {
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

func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// classify maps ENOSPC to errNoSpace so the engine can re-place or pause (FR-P11).
func classify(err error) error { return nodefs.Classify(err) }
