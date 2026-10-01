package hub

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"continuum/internal/core"
	"continuum/internal/transfer"
)

func wait(t *testing.T, e *transfer.Engine, id string) *transfer.Job {
	t.Helper()
	e.Wait(id)
	j, err := e.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// E20: copy into a remote disk, move remote→local and local→remote (copy→verify→delete), scan/verify/drain
// on the remote node, and auto-resume after the node comes back (FR-N05).
func TestCrossNodeTransfers(t *testing.T) {
	r := newRig(t)
	agentDir := t.TempDir()
	stop, node := r.startAgent(agentDir)
	e := transfer.New(r.c)
	e.Remote = r.h
	e.RetryBackoff = []time.Duration{10 * time.Millisecond}
	t.Cleanup(e.Shutdown)
	r.h.OnNodeOnline = func(string) { e.ResumeOffline(context.Background()) }

	zero := int64(0)
	localDir, remoteDir := t.TempDir(), t.TempDir()
	ld, err := r.c.Attach(r.ctx, core.AttachRequest{Path: localDir, Name: "mac-1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	rd, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: node, Path: remoteDir, Name: "lin-1", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond) // first heartbeat with the new watch list → capacity
	vol, err := r.c.CreateVolume(r.ctx, core.CreateVolumeRequest{Name: "mixed", MinFreeBytes: &zero, DiskIDs: []string{ld.Disk.ID, rd.Disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src")
	for i := 0; i < 6; i++ {
		os.MkdirAll(src, 0o755)
		os.WriteFile(filepath.Join(src, fmt.Sprintf("f%d.bin", i)), make([]byte, 200_000+i), 0o644)
	}

	// 1) COPY controller → remote disk (forced ordinal 2)
	j, err := e.Start(r.ctx, transfer.PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/data", ForcedOrdinal: 2})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("copy to remote: %s %s", j.State, j.Error)
	}
	rroot := filepath.Join(remoteDir, ".continuum")
	if _, err := os.Stat(filepath.Join(rroot, "data", "f5.bin")); err != nil {
		t.Fatal("file must land on the agent's disk")
	}
	objs, _ := r.c.Locate(r.ctx, vol.ID, "/data/f3.bin")
	if len(objs) != 1 || objs[0].DiskID != rd.Disk.ID || objs[0].Checksum == "" {
		t.Fatalf("catalog: %+v", objs)
	}

	// 2) verify + full scan run on the node
	if rep, err := e.Verify(r.ctx, vol.Slug, "/", ""); err != nil || rep.Checked != 6 || len(rep.Mismatched)+len(rep.Missing) != 0 {
		t.Fatalf("verify remote: %v %+v", err, rep)
	}
	os.WriteFile(filepath.Join(rroot, "data", "direct.txt"), []byte("added on the node"), 0o644)
	if rep, err := e.Scan(r.ctx, vol.Slug, transfer.ScanOptions{Full: true}); err != nil || len(rep.Added) != 1 {
		t.Fatalf("scan remote: %v %+v", err, rep)
	}

	// 3) MOVE remote → local (target ordinal 1): copy → verify → delete on the node
	j, err = e.Start(r.ctx, transfer.PlanRequest{Type: transfer.TypeMove, Volume: vol.Slug, Source: "/data/f0.bin", Destination: "/moved/f0.bin", ForcedOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("move remote→local: %s %s", j.State, j.Error)
	}
	if _, err := os.Stat(filepath.Join(localDir, ".continuum", "moved", "f0.bin")); err != nil {
		t.Fatal("moved file must be on the local disk")
	}
	if _, err := os.Stat(filepath.Join(rroot, "data", "f0.bin")); !os.IsNotExist(err) {
		t.Fatal("source on the node must be deleted after verify")
	}

	// 4) DRAIN the remote disk → everything lands on mac-1, lin-1 leaves the volume
	j, err = e.Start(r.ctx, transfer.PlanRequest{Type: transfer.TypeDrain, Volume: vol.Slug, Source: rd.Disk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(t, e, j.ID); j.State != transfer.JobDone {
		t.Fatalf("drain remote: %s %s", j.State, j.Error)
	}
	if got, _ := r.c.GetDisk(r.ctx, rd.Disk.ID); got.State != core.StateDisabled || got.VolumeID != "" {
		t.Fatalf("drained remote disk: %s %q", got.State, got.VolumeID)
	}
	if rep, _ := e.Verify(r.ctx, vol.Slug, "/", ""); rep.Checked != 7 || len(rep.Mismatched)+len(rep.Missing) != 0 {
		t.Fatalf("verify after drain: %+v", rep)
	}

	// 5) node lost mid-copy → PAUSED (offline); node back → auto-resume → DONE
	r2 := t.TempDir()
	if _, err := r.c.Enable(r.ctx, rd.Disk.ID); err != nil {
		t.Fatal(err)
	}
	rd2, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: node, Path: r2, Name: "lin-2", MinFreeBytes: &zero, VolumeID: vol.ID})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	big := filepath.Join(t.TempDir(), "big")
	os.MkdirAll(big, 0o755)
	for i := 0; i < 4; i++ {
		os.WriteFile(filepath.Join(big, fmt.Sprintf("b%d.bin", i)), make([]byte, 2<<20), 0o644)
	}
	j, err = e.Start(r.ctx, transfer.PlanRequest{Volume: vol.Slug, Source: big + "/", Destination: "/big", ForcedOrdinal: 2, BwLimit: 2 << 20, Transfers: 1})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	stop() // node disappears
	if j = wait(t, e, j.ID); j.State != transfer.JobPaused {
		t.Fatalf("node lost: want PAUSED, got %s %s", j.State, j.Error)
	}
	cfgDir := agentDir
	actx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	go runAgent(actx, t, cfgDir)
	for i := 0; i < 200; i++ {
		if j, _ = e.Get(r.ctx, j.ID); j.State == transfer.JobDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if j.State != transfer.JobDone {
		t.Fatalf("auto-resume after reconnect: %s %s", j.State, j.Error)
	}
	if _, err := os.Stat(filepath.Join(r2, ".continuum", "big", "b3.bin")); err != nil {
		t.Fatal("resumed job must finish on lin-2")
	}
	_ = rd2
}

// E25: wipe runs on the disk's node; refused while in a volume or without the exact name.
func TestRemoteWipe(t *testing.T) {
	r := newRig(t)
	stop, node := r.startAgent(t.TempDir())
	defer stop()
	e := transfer.New(r.c)
	e.Remote = r.h
	t.Cleanup(e.Shutdown)
	dir := t.TempDir()
	zero := int64(0)
	res, err := r.c.Attach(r.ctx, core.AttachRequest{NodeID: node, Path: dir, Name: "old-hdd", MinFreeBytes: &zero})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	os.MkdirAll(filepath.Join(dir, ".continuum", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, ".continuum", "x", "secret.bin"), make([]byte, 100_000), 0o644)
	os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("not ours"), 0o644)
	vol, _ := r.c.CreateVolume(r.ctx, core.CreateVolumeRequest{Name: "tmp", MinFreeBytes: &zero, DiskIDs: []string{res.Disk.ID}})
	if _, err := e.Wipe(r.ctx, res.Disk.ID, "old-hdd", 1); core.CodeOf(err) != core.CodeConflict {
		t.Fatalf("in a volume must be refused: %v", err)
	}
	r.c.DeleteVolume(r.ctx, vol.ID)
	if _, err := e.Wipe(r.ctx, res.Disk.ID, "OLD-HDD", 1); core.CodeOf(err) != core.CodeInvalid {
		t.Fatalf("wrong confirmation must be refused: %v", err)
	}
	w, err := e.Wipe(r.ctx, res.Disk.ID, "old-hdd", 2)
	if err != nil || w.Files != 2 {
		t.Fatalf("wipe: %v %+v", err, w)
	}
	if _, err := os.Stat(filepath.Join(dir, ".continuum")); !os.IsNotExist(err) {
		t.Fatal("data root must be removed on the node")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "outside.txt")); string(b) != "not ours" {
		t.Fatal("files outside the data root must be untouched")
	}
	if _, err := r.c.GetDisk(r.ctx, res.Disk.ID); err == nil {
		t.Fatal("wiped disk must be unregistered")
	}
}
