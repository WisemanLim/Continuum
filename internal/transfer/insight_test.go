package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"continuum/internal/audit"
	"time"
)

// FR-D15 usage (tree/top/types) + FR-D16 duplicates (size → partial → full) across disks.
func TestUsageAndDupes(t *testing.T) {
	v := newEnv(t)
	d1, d2 := v.disk("d1", 1<<30), v.disk("d2", 1<<30)
	vol := v.volume(0, d1, d2)
	big := make([]byte, 300<<10)
	for i := range big {
		big[i] = byte(i)
	}
	os.MkdirAll(filepath.Join(d1.DataRoot, "photos", "2024"), 0o755)
	os.MkdirAll(filepath.Join(d2.DataRoot, "backup"), 0o755)
	os.WriteFile(filepath.Join(d1.DataRoot, "photos", "2024", "a.jpg"), big, 0o644)
	os.WriteFile(filepath.Join(d2.DataRoot, "backup", "a-copy.jpg"), big, 0o644)
	near := append([]byte{}, big...)
	near[len(near)/2] ^= 1 // same size, same head/tail, different middle → only full hash separates it
	os.WriteFile(filepath.Join(d2.DataRoot, "backup", "near.jpg"), near, 0o644)
	os.WriteFile(filepath.Join(d1.DataRoot, "notes.txt"), []byte("hello"), 0o644)
	if _, err := v.e.Scan(v.ctx, vol.Slug, ScanOptions{}); err != nil {
		t.Fatal(err)
	}

	u, err := v.e.Usage(v.ctx, vol.Slug, "", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if u.Files != 4 || len(u.Top) != 2 || u.Top[0].Size != int64(len(big)) || u.Types[0].Ext != ".jpg" || u.Types[0].Files != 3 {
		t.Fatalf("usage: %+v", u)
	}
	if len(u.Tree.Children) != 2 || u.Tree.Children[0].Name != "backup" || u.Tree.Children[1].Children[0].Path != "/photos/2024" {
		t.Fatalf("tree: %+v %+v", *u.Tree.Children[0], *u.Tree.Children[1])
	}
	ud, _ := v.e.Usage(v.ctx, "", d1.ID, 10, 3)
	if ud.Files != 2 {
		t.Fatalf("disk usage files: %d", ud.Files)
	}

	rep, err := v.e.Dupes(v.ctx, vol.Slug, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 1 || len(rep.Groups[0].Files) != 2 || rep.Wasted != int64(len(big)) {
		t.Fatalf("dupes: %+v", rep)
	}
	if rep, _ := v.e.Dupes(v.ctx, vol.Slug, d1.ID, 0); len(rep.Groups) != 0 {
		t.Fatal("single-disk scope must not see the copy on d2")
	}
}

func TestSmartCacheAndBench(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d1", 1<<30)
	s, err := v.e.Smart(v.ctx, d.ID, false)
	if err != nil || s.CheckedAt == "" {
		t.Fatal(err)
	}
	s2, _ := v.e.Smart(v.ctx, d.ID, false)
	if s2.CheckedAt != s.CheckedAt {
		t.Fatal("second call must come from the smart_json cache")
	}
	b, err := v.e.Bench(v.ctx, d.ID, 4<<20)
	if err != nil || b.SeqWriteMBs <= 0 {
		t.Fatalf("bench %v %+v", err, b)
	}
	h, _ := v.e.BenchHistory(v.ctx, d.ID)
	if len(h) != 1 {
		t.Fatalf("history: %d", len(h))
	}
	if _, err := v.e.Bench(v.ctx, d.ID, 1<<40); err == nil {
		t.Fatal("bench larger than free space must be refused")
	}
}

func TestSmartHistory(t *testing.T) {
	v := newEnv(t)
	d := v.disk("d1", 1<<30)
	for _, c := range []int{31, 33, 35} {
		temp := c
		v.c.Audit.Emit(v.ctx, audit.Event{Kind: "disk.smart", DiskID: d.ID}, map[string]any{"status": "PASSED", "temperature_c": &temp})
		time.Sleep(2 * time.Millisecond)
	}
	h, err := v.e.SmartHistory(v.ctx, d.ID, 0)
	if err != nil || len(h) != 3 || *h[0].Temperature != 31 || *h[2].Temperature != 35 || h[2].Status != "PASSED" {
		t.Fatalf("history (oldest first): %v %+v", err, h)
	}
	v.e.Smart(v.ctx, d.ID, true) // a real refresh appends a snapshot
	if h, _ = v.e.SmartHistory(v.ctx, d.ID, 0); len(h) != 4 {
		t.Fatalf("after refresh: %d", len(h))
	}
}
