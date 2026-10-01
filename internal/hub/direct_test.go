package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/agent"
	"continuum/internal/core"
	"continuum/internal/transfer"
)

// startPeerAgent enrolls an agent that also serves the direct agent ↔ agent endpoint on 127.0.0.1.
func (r *rig) startPeerAgent(dir string, peer bool) (context.CancelFunc, string) {
	r.t.Helper()
	tok, err := r.h.CreateToken(r.ctx, "peer", "tester", time.Hour)
	if err != nil {
		r.t.Fatal(err)
	}
	cfg, err := agent.Enroll(r.ctx, dir, r.url, tok.Token)
	if err != nil {
		r.t.Fatal(err)
	}
	actx, cancel := context.WithCancel(r.ctx)
	r.t.Cleanup(cancel) // runs before the server closes (LIFO), so its streams end
	a := agent.New(cfg)
	if peer {
		if _, err := a.PeerListen(actx, "127.0.0.1:0", "https://127.0.0.1:0"); err != nil {
			r.t.Fatal(err)
		}
	}
	go a.Run(actx)
	r.waitOnline(cfg.NodeID, true)
	return cancel, cfg.NodeID
}

// E32 (D36): drain between two agents — the target agent pulls from the source agent over mTLS with a
// CA-signed ticket (no bytes through the Controller), resumes from its partial after a pause, verifies BLAKE3.
func TestDirectAgentToAgent(t *testing.T) {
	r := newRig(t)
	_, nodeA := r.startPeerAgent(t.TempDir(), true)
	_, nodeB := r.startPeerAgent(t.TempDir(), true)
	e := transfer.New(r.c)
	e.Remote = r.h
	e.RetryBackoff = []time.Duration{10 * time.Millisecond}
	t.Cleanup(e.Shutdown)

	zero := int64(0)
	dirA, dirB := t.TempDir(), t.TempDir()
	da, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: nodeA, Path: dirA, Name: "a1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	db, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: nodeB, Path: dirB, Name: "b1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	vol, err := r.c.CreateVolume(r.ctx, core.CreateVolumeRequest{Name: "peers", MinFreeBytes: &zero, DiskIDs: []string{da.Disk.ID, db.Disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(src, 0o755)
	big := make([]byte, 6<<20)
	rand.Read(big)
	os.WriteFile(filepath.Join(src, "big.bin"), big, 0o644)
	for _, n := range []string{"s1.txt", "s2.txt", "s3.txt"} {
		os.WriteFile(filepath.Join(src, n), []byte("small "+n), 0o644)
	}
	j, err := e.Start(r.ctx, transfer.PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/d", ForcedOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("seed copy: %s %s", j.State, j.Error)
	}

	// drain a1 → b1, throttled so it can be paused mid-file
	j, err = e.Start(r.ctx, transfer.PlanRequest{Type: transfer.TypeDrain, Volume: vol.Slug, Source: da.Disk.ID, BwLimit: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	partial := ""
	for i := 0; i < 100 && partial == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		m, _ := filepath.Glob(filepath.Join(dirB, ".continuum", "d", "big.bin.continuum-partial-*.w"))
		if len(m) == 1 {
			if fi, _ := os.Stat(m[0]); fi != nil && fi.Size() > 1<<20 {
				partial = m[0]
			}
		}
	}
	if partial == "" {
		t.Fatal("direct partial never appeared on the target agent")
	}
	// E35: in-file progress from the agent reaches the job while big.bin is still copying
	var live int64
	for i := 0; i < 40 && live == 0; i++ {
		if jj, _ := e.Get(r.ctx, j.ID); jj != nil && jj.State == transfer.JobRunning && jj.Progress != nil && jj.Progress.BytesDone > 1<<20 {
			live = jj.Progress.BytesDone // > the 3 small files together: only the agent's in-file reports get here
		}
		time.Sleep(100 * time.Millisecond)
	}
	if live == 0 || live >= int64(len(big)) {
		t.Fatalf("in-file progress while copying: %d", live)
	}
	if _, err := e.Pause(r.ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	e.Wait(j.ID)
	time.Sleep(300 * time.Millisecond) // the agent stops the pull (OpCancel) and closes the partial
	fi, err := os.Stat(partial)
	if err != nil || fi.Size() == 0 || fi.Size() >= int64(len(big)) {
		t.Fatalf("partial after pause: %v %v", fi, err)
	}
	if _, err := e.Resume(r.ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("drain: %s %s", j.State, j.Error)
	}
	got, err := os.ReadFile(filepath.Join(dirB, ".continuum", "d", "big.bin"))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("big.bin on b1: %d bytes, %v", len(got), err)
	}
	if _, err := os.Stat(filepath.Join(dirA, ".continuum", "d", "big.bin")); !os.IsNotExist(err) {
		t.Fatal("drain must delete the source copy")
	}
	direct, resumed := e.DirectStats()
	if direct != 4 || resumed != 1 {
		t.Fatalf("direct=%d resumed=%d (want 4 direct, 1 resumed)", direct, resumed)
	}
	objs, _ := r.c.Locate(r.ctx, vol.ID, "/d/big.bin")
	if len(objs) != 1 || objs[0].DiskID != db.Disk.ID || objs[0].Checksum == "" {
		t.Fatalf("catalog: %+v", objs)
	}
	if m, _ := filepath.Glob(filepath.Join(dirB, ".continuum", "d", "*.continuum-partial-*")); len(m) != 0 {
		t.Fatalf("leftover partials: %v", m)
	}
}

// Without a peer endpoint on the source agent the copy falls back to relaying through the Controller.
func TestDirectFallsBackToRelay(t *testing.T) {
	r := newRig(t)
	_, nodeA := r.startPeerAgent(t.TempDir(), false)
	_, nodeB := r.startPeerAgent(t.TempDir(), true)
	e := transfer.New(r.c)
	e.Remote = r.h
	t.Cleanup(e.Shutdown)
	zero := int64(0)
	dirA, dirB := t.TempDir(), t.TempDir()
	da, _ := r.c.Attach(r.ctx, core.AttachRequest{NodeID: nodeA, Path: dirA, Name: "a1", MinFreeBytes: &zero})
	db, _ := r.c.Attach(r.ctx, core.AttachRequest{NodeID: nodeB, Path: dirB, Name: "b1", MinFreeBytes: &zero})
	time.Sleep(1200 * time.Millisecond)
	vol, err := r.c.CreateVolume(r.ctx, core.CreateVolumeRequest{Name: "relay", MinFreeBytes: &zero, DiskIDs: []string{da.Disk.ID, db.Disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "x.txt"), []byte("relayed"), 0o644)
	j, _ := e.Start(r.ctx, transfer.PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/r", ForcedOrdinal: 1})
	wait(t, e, j.ID)
	j, _ = e.Start(r.ctx, transfer.PlanRequest{Type: transfer.TypeDrain, Volume: vol.Slug, Source: da.Disk.ID})
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("drain: %s %s", j.State, j.Error)
	}
	if b, err := os.ReadFile(filepath.Join(dirB, ".continuum", "r", "x.txt")); err != nil || string(b) != "relayed" {
		t.Fatalf("relayed copy: %q %v", b, err)
	}
	if direct, _ := e.DirectStats(); direct != 0 {
		t.Fatalf("source has no peer endpoint: direct=%d", direct)
	}
}
