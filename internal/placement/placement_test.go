package placement

import (
	"fmt"
	"math"
	"testing"
)

const GiB = int64(1) << 30

// R05 dataset: 30 GB = 10 folders × 300 files (PRD R05, D12).
func r05Items() []Item {
	var items []Item
	per := 30 * GiB / 3000
	for f := 0; f < 10; f++ {
		for i := 0; i < 300; i++ {
			items = append(items, Item{Path: fmt.Sprintf("folder%02d/file%04d.bin", f, i), Size: per})
		}
	}
	return items
}

func r05Disks() []Disk {
	return []Disk{
		{ID: "d1", Name: "disk-01", Ordinal: 1, State: "ONLINE", Writable: true, AutoPlace: true, TotalBytes: 25 * GiB, EffectiveFree: 25 * GiB, MinFree: 1 * GiB},
		{ID: "d2", Name: "disk-02", Ordinal: 2, State: "ONLINE", Writable: true, AutoPlace: true, TotalBytes: 15 * GiB, EffectiveFree: 15 * GiB, MinFree: 1 * GiB},
	}
}

func approx(got, want int64) bool { return math.Abs(float64(got-want)) <= float64(GiB)/2 }

// FR-P08: 25/15 GB disks, 1 GB reserve, 30 GB (10 folders, 3,000 files) → ≈ 24 / 6 GB.
func TestFRP08SpilloverReproduction(t *testing.T) {
	for _, pol := range []string{FillInOrder, TopdirAffinity} {
		p := Compute(r05Disks(), r05Items(), 10, Options{Policy: pol, Spillover: true})
		if !p.Sufficient || len(p.Placements) != 2 {
			t.Fatalf("%s: plan %+v", pol, p)
		}
		b1, b2 := p.Placements[0].Bytes, p.Placements[1].Bytes
		if !approx(b1, 24*GiB) || !approx(b2, 6*GiB) || b1+b2 != p.Bytes {
			t.Fatalf("%s: got %.2f / %.2f GiB, want ≈ 24 / 6", pol, float64(b1)/float64(GiB), float64(b2)/float64(GiB))
		}
		if p.Files != 3000 || p.Placements[0].Files+p.Placements[1].Files != 3000 {
			t.Fatalf("%s: files %d", pol, p.Files)
		}
		// reserve respected
		for _, pl := range p.Placements {
			if pl.RemainingAboveReserve < 0 {
				t.Fatalf("%s: reserve violated on %s", pol, pl.Name)
			}
		}
		t.Logf("%s: disk-01 %.2f GiB (%d files) · disk-02 %.2f GiB (%d files)", pol,
			float64(b1)/float64(GiB), p.Placements[0].Files, float64(b2)/float64(GiB), p.Placements[1].Files)
	}
}

// topdir-affinity keeps each top-level folder on a single disk when it fits.
func TestTopdirKeepsFoldersTogether(t *testing.T) {
	p := Compute(r05Disks(), r05Items(), 10, Options{Policy: TopdirAffinity, Spillover: true})
	where := map[string]map[string]bool{}
	for _, a := range p.Assignments {
		top := a.Path[:8]
		if where[top] == nil {
			where[top] = map[string]bool{}
		}
		where[top][a.DiskID] = true
	}
	for top, ds := range where {
		if len(ds) != 1 {
			t.Fatalf("folder %s split across %d disks", top, len(ds))
		}
	}
}

// FR-P09 / FR-P11: pool out of space, then a 3rd disk continues the placement.
func TestPoolOutOfSpaceThenAddDisk(t *testing.T) {
	items := r05Items()
	items = append(items, Item{Path: "extra/big.bin", Size: 10 * GiB})
	p := Compute(r05Disks(), items, 11, Options{Policy: FillInOrder, Spillover: true})
	if p.Sufficient || p.Error != ErrPoolOutOfSpace || p.Unplaced == 0 {
		t.Fatalf("expected PoolOutOfSpace, got %+v", p.Error)
	}
	disks := append(r05Disks(), Disk{ID: "d3", Name: "disk-03", Ordinal: 3, State: "ONLINE", Writable: true, AutoPlace: true, TotalBytes: 20 * GiB, EffectiveFree: 20 * GiB, MinFree: GiB})
	p = Compute(disks, items, 11, Options{Policy: FillInOrder, Spillover: true})
	if !p.Sufficient || len(p.Placements) != 3 {
		t.Fatalf("3rd disk should absorb the rest: %+v", p)
	}
}

// R04: forced ordinal — no spillover, fails if the target is too small.
func TestForcedOrdinal(t *testing.T) {
	p := Compute(r05Disks(), r05Items()[:10], 1, Options{ForcedOrdinal: 2, Spillover: true})
	if !p.Sufficient || len(p.Placements) != 1 || p.Placements[0].DiskID != "d2" {
		t.Fatalf("forced to ordinal 2: %+v", p.Placements)
	}
	p = Compute(r05Disks(), r05Items(), 10, Options{ForcedOrdinal: 2, Spillover: true})
	if p.Sufficient || p.Error != ErrInsufficientOnForced {
		t.Fatalf("want InsufficientSpaceOnForcedDisk, got %s", p.Error)
	}
	p = Compute(r05Disks(), r05Items()[:1], 1, Options{ForcedOrdinal: 9})
	if p.Error != ErrForcedUnavailable {
		t.Fatalf("want ForcedTargetUnavailable, got %s", p.Error)
	}
}

// R06 start ordinal + FR-V17 strict mode.
func TestStartOrdinalAndStrict(t *testing.T) {
	p := Compute(r05Disks(), r05Items()[:10], 1, Options{StartOrdinal: 2, Spillover: true})
	if len(p.Placements) != 1 || p.Placements[0].Ordinal != 2 {
		t.Fatalf("start ordinal 2: %+v", p.Placements)
	}
	p = Compute(r05Disks(), r05Items(), 10, Options{Spillover: false})
	if p.Sufficient || p.Error != ErrPoolOutOfSpace || len(p.Placements) != 1 {
		t.Fatalf("strict mode must not spill: %+v", p)
	}
}

func TestWholeTreeOnly(t *testing.T) {
	p := Compute(r05Disks(), r05Items(), 10, Options{Policy: WholeTree, Spillover: true})
	if p.Error != ErrNoSingleDiskCanHoldTree {
		t.Fatalf("30 GB tree cannot fit a single disk: %s", p.Error)
	}
	p = Compute(r05Disks(), r05Items()[:300], 1, Options{Policy: WholeTree, Spillover: true})
	if !p.Sufficient || len(p.Placements) != 1 {
		t.Fatalf("3 GB tree fits disk-01: %+v", p)
	}
}

// Portable / archive / offline disks are excluded from automatic placement (DP08).
func TestExclusions(t *testing.T) {
	disks := append(r05Disks(),
		Disk{ID: "p", Name: "mac-ext", Ordinal: 3, State: "ONLINE", Writable: true, Role: "portable", AutoPlace: false, TotalBytes: 100 * GiB, EffectiveFree: 100 * GiB},
		Disk{ID: "o", Name: "nas", Ordinal: 4, State: "OFFLINE", Writable: true, AutoPlace: true, TotalBytes: 100 * GiB, EffectiveFree: 100 * GiB},
	)
	p := Compute(disks, r05Items()[:1], 1, Options{Spillover: true})
	if len(p.Excluded) != 2 || p.Excluded[0].Reason != "role=portable" || p.Excluded[1].Reason != "state=OFFLINE" {
		t.Fatalf("exclusions %+v", p.Excluded)
	}
	// manual target may still pick the portable disk
	p = Compute(disks, r05Items()[:1], 1, Options{ForcedOrdinal: 3})
	if !p.Sufficient || p.Placements[0].DiskID != "p" {
		t.Fatalf("forced to portable: %+v", p)
	}
}

func TestMostFreeAndBalanced(t *testing.T) {
	p := Compute(r05Disks(), r05Items()[:1], 1, Options{Policy: MostFree, Spillover: true})
	if p.Placements[0].DiskID != "d1" {
		t.Fatal("most-free should pick the 25 GB disk")
	}
	disks := r05Disks()
	disks[0].EffectiveFree = 5 * GiB // 20% free vs 100%
	p = Compute(disks, r05Items()[:1], 1, Options{Policy: Balanced, Spillover: true})
	if p.Placements[0].DiskID != "d2" {
		t.Fatal("balanced should pick the emptier disk")
	}
}

func TestNameWarnings(t *testing.T) {
	disks := []Disk{{ID: "w", Ordinal: 1, State: "ONLINE", Writable: true, AutoPlace: true, TotalBytes: 100 * GiB, EffectiveFree: 100 * GiB, FSType: "ntfs", CaseInsensitive: true}}
	items := []Item{{Path: "a/b:c.txt", Size: 1}, {Path: "a/CON.txt", Size: 1}, {Path: "a/Readme.md", Size: 1}, {Path: "a/README.md", Size: 1}}
	p := Compute(disks, items, 1, Options{Spillover: true})
	codes := map[string]bool{}
	for _, w := range p.Warnings {
		codes[w.Code] = true
	}
	for _, c := range []string{"NAME_RULE", "NAME_RESERVED", "CASE_COLLISION"} {
		if !codes[c] {
			t.Errorf("missing warning %s in %+v", c, p.Warnings)
		}
	}
	if SafeName("a:b?.txt") != "a_b_.txt" {
		t.Fatal(SafeName("a:b?.txt"))
	}
}
