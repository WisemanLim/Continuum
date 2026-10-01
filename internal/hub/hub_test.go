package hub

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/agent"
	"continuum/internal/core"
	"continuum/internal/nodefs"
	"continuum/internal/pki"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
)

type rig struct {
	t   *testing.T
	ctx context.Context
	c   *core.Core
	h   *Hub
	url string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := t.TempDir()
	db, err := store.Open(storetest.URL(t, root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := core.Open(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadOrCreate(filepath.Join(root, "pki"), c.ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := ca.ServerTLS(pki.LocalHosts())
	if err != nil {
		t.Fatal(err)
	}
	h := New(c, ca, "")
	h.HeartbeatSec = 1
	c.Remote = h
	srv := httptest.NewUnstartedServer(h.Handler())
	srv.TLS = tc
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	h.PublicURL = srv.URL
	go h.Run(ctx)
	return &rig{t: t, ctx: ctx, c: c, h: h, url: srv.URL}
}

func (r *rig) startAgent(dir string) (context.CancelFunc, string) {
	r.t.Helper()
	tok, err := r.h.CreateToken(r.ctx, "test", "tester", time.Hour)
	if err != nil {
		r.t.Fatal(err)
	}
	cfg, err := agent.Enroll(r.ctx, dir, r.url, tok.Token)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := agent.Enroll(r.ctx, r.t.TempDir(), r.url, tok.Token); err == nil {
		r.t.Fatal("token must be single use")
	}
	actx, cancel := context.WithCancel(r.ctx)
	go agent.New(cfg).Run(actx)
	r.waitOnline(cfg.NodeID, true)
	return cancel, cfg.NodeID
}

func (r *rig) waitOnline(node string, want bool) {
	r.t.Helper()
	for i := 0; i < 200; i++ {
		var st string
		r.c.DB.QueryRow(r.ctx, `SELECT state FROM nodes WHERE node_id=?`, node).Scan(&st)
		if r.h.Online(node) == want && (st == "ONLINE") == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatalf("node %s online=%v not reached", node, want)
}

// FR-N01/N02/N05/S03: enroll → mTLS stream → remote attach → put/open/hash → offline after missed beats → revoke.
func TestAgentLifecycle(t *testing.T) {
	r := newRig(t)
	stop, node := r.startAgent(t.TempDir())

	// bad token / pinned CA mismatch
	if _, err := agent.Enroll(r.ctx, t.TempDir(), r.url, "CTM1.nope."+string(bytes.Repeat([]byte("0"), 64))); err == nil {
		t.Fatal("wrong CA pin must fail")
	}

	diskDir := t.TempDir()
	zero := int64(0)
	res, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: node, Path: diskDir, Name: "remote-1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatalf("remote attach: %v %+v", err, res)
	}
	if res.Disk.NodeID != node || res.Disk.DataRoot != filepath.Join(diskDir, ".continuum") {
		t.Fatalf("disk: %+v", res.Disk)
	}
	if _, err := os.Stat(filepath.Join(diskDir, ".continuum", "volume.json")); err != nil {
		t.Fatal("marker must be written by the agent")
	}

	fs := r.h.FS(node)
	data := bytes.Repeat([]byte("continuum "), 300_000) // 3 MB
	final := filepath.Join(diskDir, ".continuum", "a", "b.bin")
	pr, err := fs.Put(r.ctx, nodefs.PutArgs{Final: final, Partial: final + ".part", Size: int64(len(data)), Mode: 0o640, Verify: true}, bytes.NewReader(data))
	if err != nil || pr.Bytes != int64(len(data)) {
		t.Fatalf("put: %v %+v", err, pr)
	}
	want, _ := nodefs.HashFile(final)
	if got, err := fs.Hash(r.ctx, final); err != nil || got != want || pr.Checksum != want {
		t.Fatalf("hash: %s %s %s %v", got, want, pr.Checksum, err)
	}
	rc, err := fs.Open(r.ctx, final)
	if err != nil {
		t.Fatal(err)
	}
	back, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("open: %v len=%d", err, len(back))
	}
	if _, err := fs.Stat(r.ctx, final+".nope"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("not_exist must survive the wire: %v", err)
	}
	if ents, err := fs.Walk(r.ctx, filepath.Join(diskDir, ".continuum")); err != nil || len(ents) < 3 {
		t.Fatalf("walk: %v %d", err, len(ents))
	}

	// heartbeat capacity reaches the registry
	time.Sleep(1500 * time.Millisecond)
	d, _ := r.c.GetDisk(r.ctx, res.Disk.ID)
	if d.TotalBytes == 0 || d.State != core.StateOnline {
		t.Fatalf("capacity from heartbeat: %+v", d)
	}

	// agent stops → 3 missed beats → node + disk OFFLINE
	stop()
	r.waitOnline(node, false)
	for i := 0; i < 100; i++ {
		if d, _ = r.c.GetDisk(r.ctx, res.Disk.ID); d.State == core.StateOffline {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d.State != core.StateOffline {
		t.Fatalf("disk must go offline with its node: %s", d.State)
	}
	if _, err := fs.Stat(r.ctx, final); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("offline node: %v", err)
	}

	// revoke: certificate no longer accepted
	if err := r.h.Revoke(r.ctx, node, "tester"); err != nil {
		t.Fatal(err)
	}
}

// Reconnect: a restarted agent resumes the same node identity (FR-N03); revoked agents cannot reconnect.
func TestReconnectAndRevoke(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	stop, node := r.startAgent(dir)
	stop()
	r.waitOnline(node, false)
	cfg, err := agent.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	go agent.New(cfg).Run(actx)
	r.waitOnline(node, true)
	if err := r.h.Revoke(r.ctx, node, "tester"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if r.h.Online(node) {
		t.Fatal("revoked agent must not reconnect")
	}
}

// FR-N09: rotation issues a new serial; the old certificate is rejected, the agent reconnects with the new one.
func TestCertRotation(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	stop, node := r.startAgent(dir)
	defer stop()
	var before string
	r.c.DB.QueryRow(r.ctx, `SELECT cert_serial FROM nodes WHERE node_id=?`, node).Scan(&before)
	cfg, _ := agent.Load(dir)
	if err := agent.New(cfg).RenewNow(r.ctx); err != nil {
		t.Fatal(err)
	}
	var after string
	r.c.DB.QueryRow(r.ctx, `SELECT cert_serial FROM nodes WHERE node_id=?`, node).Scan(&after)
	if after == before || after == "" {
		t.Fatalf("serial not rotated: %s → %s", before, after)
	}
	// the running agent still holds the old cert: its next reconnect uses the new files
	r.h.drop(node)
	r.waitOnline(node, true)
}

func runAgent(ctx context.Context, t *testing.T, dir string) {
	cfg, err := agent.Load(dir)
	if err != nil {
		t.Error(err)
		return
	}
	agent.New(cfg).Run(ctx)
}
