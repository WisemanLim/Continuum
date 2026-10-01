package transfer

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"continuum/internal/core"
)

func seed(t *testing.T, v *env, vol core.Volume, n int, size int64, forced int) {
	t.Helper()
	src := filepath.Join(v.root, fmt.Sprintf("seed-%d", forced))
	for i := 0; i < n; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("f%02d.bin", i)), size)
	}
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: fmt.Sprintf("/s%d", forced), ForcedOrdinal: forced})
	if err != nil {
		t.Fatal(err)
	}
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("seed: %s %s", j.State, j.Error)
	}
}

// FR-D10: DRAIN moves everything off a disk (copy → verify → delete), then the disk leaves the volume as DISABLED.
func TestDrainRemovesDisk(t *testing.T) {
	v := newEnv(t)
	d1, d2, d3 := v.disk("d1", 20*MiB), v.disk("d2", 20*MiB), v.disk("d3", 20*MiB)
	vol := v.volume(0, d1, d2, d3)
	seed(t, v, vol, 5, MiB, 2) // 5 MiB on d2
	seed(t, v, vol, 2, MiB, 1)

	pl, err := v.e.Plan(v.ctx, PlanRequest{Type: TypeDrain, Volume: vol.Slug, Source: d2.ID, StartOrdinal: 3})
	if err != nil || pl.Files != 5 || !pl.Sufficient || len(pl.Placements) != 1 || pl.Placements[0].DiskID != d3.ID {
		t.Fatalf("drain plan (start ordinal 3): %v %+v", err, pl.Plan)
	}
	j, err := v.e.Start(v.ctx, PlanRequest{Type: TypeDrain, Volume: vol.Slug, Source: d2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.c.GetDisk(v.ctx, d2.ID); got.State != core.StateDraining && got.State != core.StateDisabled {
		t.Fatalf("disk must be DRAINING while the job runs: %s", got.State)
	}
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("drain: %s %s", j.State, j.Error)
	}
	got, _ := v.c.GetDisk(v.ctx, d2.ID)
	if got.State != core.StateDisabled || got.VolumeID != "" {
		t.Fatalf("drained disk: state=%s volume=%q", got.State, got.VolumeID)
	}
	if n, _ := countFiles(t, d2.DataRoot); n != 0 {
		t.Fatalf("%d files left on drained disk", n)
	}
	vv, _ := v.c.GetVolume(v.ctx, vol.ID)
	if len(vv.Disks) != 2 || vv.Disks[1].ID != d3.ID || vv.Disks[1].Ordinal != 2 {
		t.Fatalf("volume disks after drain: %+v", vv.Disks)
	}
	for i := 0; i < 5; i++ {
		objs, err := v.c.Locate(v.ctx, vol.ID, fmt.Sprintf("/s2/f%02d.bin", i))
		if err != nil || len(objs) != 1 || objs[0].DiskID == d2.ID || objs[0].Checksum == "" {
			t.Fatalf("file %d after drain: %v %+v", i, err, objs)
		}
	}
	rep, _ := v.e.Verify(v.ctx, vol.Slug, "/", "")
	if len(rep.Mismatched)+len(rep.Missing) != 0 {
		t.Fatalf("verify after drain: %+v", rep)
	}
}

// Not enough room elsewhere → refused, disk back ONLINE; cancel → ONLINE.
func TestDrainRefusedAndCancel(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 20*MiB), v.disk("d2", 4*MiB)
	vol := v.volume(0, d1, d2)
	seed(t, v, vol, 6, MiB, 1)
	if _, err := v.e.Start(v.ctx, PlanRequest{Type: TypeDrain, Volume: vol.Slug, Source: d1.ID}); core.CodeOf(err) != core.CodeInsufficientSpace {
		t.Fatalf("expected insufficient_space, got %v", err)
	}
	if got, _ := v.c.GetDisk(v.ctx, d1.ID); got.State != core.StateOnline {
		t.Fatalf("refused drain must restore ONLINE: %s", got.State)
	}
	d3 := v.disk("d3", 20*MiB)
	v.c.AddDiskToVolume(v.ctx, vol.ID, d3.ID, 0)
	j, err := v.e.Start(v.ctx, PlanRequest{Type: TypeDrain, Volume: vol.Slug, Source: d1.ID, BwLimit: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.e.Cancel(v.ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	v.wait(j.ID)
	if got, _ := v.c.GetDisk(v.ctx, d1.ID); got.State != core.StateOnline || got.VolumeID != vol.ID {
		t.Fatalf("cancelled drain: %s %q", got.State, got.VolumeID)
	}
}

// FR-P10: after reordering, REBALANCE re-places existing data by fill-in-order; files already right stay.
func TestRebalance(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 20*MiB), v.disk("d2", 20*MiB)
	vol := v.volume(0, d1, d2)
	seed(t, v, vol, 4, MiB, 2) // everything forced onto d2
	pl, err := v.e.Plan(v.ctx, PlanRequest{Type: TypeRebalance, Volume: vol.Slug})
	if err != nil || pl.Files != 4 || pl.Placements[0].DiskID != d1.ID {
		t.Fatalf("rebalance plan: %v %+v", err, pl.Plan)
	}
	j, err := v.e.Start(v.ctx, PlanRequest{Type: TypeRebalance, Volume: vol.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if j = v.wait(j.ID); j.State != JobDone {
		t.Fatalf("rebalance: %s %s", j.State, j.Error)
	}
	if n, _ := countFiles(t, d1.DataRoot); n != 4 {
		t.Fatalf("d1 files: %d", n)
	}
	// nothing left to move
	pl, _ = v.e.Plan(v.ctx, PlanRequest{Type: TypeRebalance, Volume: vol.Slug})
	if pl.Files != 0 {
		t.Fatalf("second rebalance should be a no-op: %d", pl.Files)
	}
	os.RemoveAll(filepath.Join(v.root, "seed-2"))
}

// FR-N08 central mode: a job held by another worker's live lease is not started by Recover; once the
// lease expires (worker crashed) another worker takes it over and finishes it.
func TestJobLeaseTakeover(t *testing.T) {
	v := newEnv(t)
	d1 := v.disk("d1", 50*MiB)
	vol := v.volume(0, d1)
	src := filepath.Join(v.root, "lease-src")
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("l%d.bin", i)), MiB)
	}
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/lease", BwLimit: 512 << 10})
	if err != nil {
		t.Fatal(err)
	}
	v.e.Pause(v.ctx, j.ID)
	v.wait(j.ID)
	// pretend a crashed worker left the job RUNNING with a live lease
	v.c.DB.Exec(v.ctx, `UPDATE jobs SET state=?, claimed_by='wrk_dead', lease_until=? WHERE job_id=?`, JobRunning, leaseTS(time.Now().Add(time.Hour)), j.ID)
	other := New(v.c)
	t.Cleanup(other.Shutdown)
	if err := other.Recover(v.ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got, _ := other.Get(v.ctx, j.ID); got.State != JobRunning || other.isRunning(j.ID) {
		t.Fatalf("a live lease must keep the job away from other workers: %s running=%v", got.State, other.isRunning(j.ID))
	}
	v.c.DB.Exec(v.ctx, `UPDATE jobs SET lease_until=? WHERE job_id=?`, leaseTS(time.Now().Add(-time.Second)), j.ID)
	if err := other.Recover(v.ctx); err != nil {
		t.Fatal(err)
	}
	other.Wait(j.ID)
	got, _ := other.Get(v.ctx, j.ID)
	if got.State != JobDone {
		t.Fatalf("takeover: %s %s", got.State, got.Error)
	}
	var claimed sql.NullString
	v.c.DB.QueryRow(v.ctx, `SELECT claimed_by FROM jobs WHERE job_id=?`, j.ID).Scan(&claimed)
	if claimed.Valid {
		t.Fatalf("finished job must release its lease: %v", claimed.String)
	}
}

// FR-P10 time windows: a job outside its window waits (QUEUED), starts when the window opens, pauses
// when it closes and resumes the next time it opens.
func TestTimeWindow(t *testing.T) {
	if !inWindow("22:00-06:00", time.Date(2026, 1, 1, 23, 30, 0, 0, time.Local)) || inWindow("22:00-06:00", time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)) ||
		!inWindow("09:00-17:00", time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)) || inWindow("09:00-17:00", time.Date(2026, 1, 1, 17, 0, 0, 0, time.Local)) {
		t.Fatal("inWindow")
	}
	v := newEnv(t)
	d1 := v.disk("d1", 100*MiB)
	vol := v.volume(0, d1)
	src := filepath.Join(v.root, "win-src")
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(src, fmt.Sprintf("w%d.bin", i)), MiB)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	var mu sync.Mutex
	v.e.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	set := func(t time.Time) { mu.Lock(); now = t; mu.Unlock() }
	if _, err := v.e.Start(v.ctx, PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/w", Window: "25:00-01:00"}); core.CodeOf(err) != core.CodeInvalid {
		t.Fatalf("bad window: %v", err)
	}
	j, err := v.e.Start(v.ctx, PlanRequest{Volume: vol.Slug, Source: src + "/", Destination: "/w", Window: "22:00-06:00", BwLimit: 1 << 20, Transfers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if j.State != JobQueued || !strings.Contains(j.Error, "waiting for time window") {
		t.Fatalf("outside window: %s %q", j.State, j.Error)
	}
	if v.e.WindowDue(v.ctx, now) != 0 {
		t.Fatal("still outside")
	}
	set(time.Date(2026, 1, 1, 22, 0, 0, 0, time.Local))
	if v.e.WindowDue(v.ctx, v.e.Now()) != 1 {
		t.Fatal("window opened: job must start")
	}
	time.Sleep(1500 * time.Millisecond)
	set(time.Date(2026, 1, 2, 6, 30, 0, 0, time.Local)) // window closes mid-job
	got := v.wait(j.ID)
	if got.State != JobPaused || !strings.Contains(got.Error, "waiting for time window") {
		t.Fatalf("window closed: %s %q", got.State, got.Error)
	}
	set(time.Date(2026, 1, 2, 23, 0, 0, 0, time.Local))
	v.e.WindowDue(v.ctx, v.e.Now())
	if got = v.wait(j.ID); got.State != JobDone {
		t.Fatalf("resumed in the next window: %s %q", got.State, got.Error)
	}
}
