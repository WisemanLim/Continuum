// Package placement is the Continuum placement engine (FR-P01~P11, PRD §11.1).
// It is pure: callers pass disks (with effective free space) and items; it returns a plan.
package placement

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Policies (FR-P05).
const (
	FillInOrder    = "fill-in-order"   // file-spill: each file → first fitting ordinal
	TopdirAffinity = "topdir-affinity" // keep each top-level folder on one disk, else spill files
	WholeTree      = "whole-tree-only" // entire source on one disk or fail
	MostFree       = "most-free"
	Balanced       = "balanced"
)

// Disk is a placement candidate.
type Disk struct {
	ID              string `json:"disk_id"`
	Name            string `json:"name"`
	Node            string `json:"node"`
	Ordinal         int    `json:"ordinal"`
	State           string `json:"state"`
	Role            string `json:"role"`
	Writable        bool   `json:"writable"`
	AutoPlace       bool   `json:"auto_place"`
	TotalBytes      int64  `json:"total_bytes"`
	EffectiveFree   int64  `json:"effective_free_bytes"` // fs_available − reserved_bytes (FR-P02)
	MinFree         int64  `json:"min_free_bytes"`
	FSType          string `json:"filesystem_type,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

// Item is one source file (relative, slash-separated path).
type Item struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Options control placement (FR-P03, FR-V15~V17).
type Options struct {
	Policy        string `json:"policy"`
	StartOrdinal  int    `json:"start_ordinal,omitempty"`  // R06
	ForcedOrdinal int    `json:"forced_ordinal,omitempty"` // R04: no spillover
	Spillover     bool   `json:"spillover"`                // false = strict: first target only
	ThroughputBps int64  `json:"-"`                        // ETA estimate; default 150 MB/s
}

// Assignment maps an item to a disk.
type Assignment struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	DiskID string `json:"disk_id"`
}

// DiskPlacement is the per-disk plan line (§9.3).
type DiskPlacement struct {
	Ordinal               int    `json:"ordinal"`
	DiskID                string `json:"disk_id"`
	Name                  string `json:"name"`
	Node                  string `json:"node"`
	Files                 int    `json:"files"`
	Bytes                 int64  `json:"bytes"`
	RemainingAboveReserve int64  `json:"remaining_above_reserve"`
}

// Excluded explains why a disk is not a target.
type Excluded struct {
	Ordinal int    `json:"ordinal"`
	DiskID  string `json:"disk_id"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
}

// Warning is a non-fatal plan finding (name rules, FS limits).
type Warning struct {
	Code string `json:"code"`
	Path string `json:"path"`
	Fix  string `json:"fix,omitempty"`
	Msg  string `json:"message"`
}

// Plan is the PlanCopy result (FR-P07).
type Plan struct {
	Files       int             `json:"files"`
	Dirs        int             `json:"dirs"`
	Bytes       int64           `json:"bytes"`
	Policy      string          `json:"policy"`
	Placements  []DiskPlacement `json:"placements"`
	Excluded    []Excluded      `json:"excluded"`
	Sufficient  bool            `json:"sufficient"`
	Unplaced    int             `json:"unplaced_files"`
	UnplacedB   int64           `json:"unplaced_bytes"`
	Error       string          `json:"error,omitempty"` // PoolOutOfSpace | InsufficientSpaceOnForcedDisk | …
	ETASeconds  int64           `json:"eta_seconds"`
	Warnings    []Warning       `json:"warnings"`
	Assignments []Assignment    `json:"-"`
}

// Error names (§11.1 pseudocode).
const (
	ErrPoolOutOfSpace          = "PoolOutOfSpace"
	ErrForcedUnavailable       = "ForcedTargetUnavailable"
	ErrInsufficientOnForced    = "InsufficientSpaceOnForcedDisk"
	ErrNoSingleDiskCanHoldTree = "NoSingleDiskCanHoldTree"
	ErrNoCandidates            = "NoWritableDisk"
)

type state struct {
	disks []*cand
	opts  Options
}

type cand struct {
	Disk
	free int64 // simulated effective free during planning (reservations accumulate)
	pl   *DiskPlacement
}

func (c *cand) fits(size int64) bool { return c.free >= size+c.MinFree }

// Compute builds a plan. dirs = number of source directories (for display).
func Compute(disks []Disk, items []Item, dirs int, opt Options) Plan {
	if opt.Policy == "" {
		opt.Policy = FillInOrder
	}
	if opt.ThroughputBps <= 0 {
		opt.ThroughputBps = 150_000_000
	}
	p := Plan{Files: len(items), Dirs: dirs, Policy: opt.Policy, Placements: []DiskPlacement{}, Excluded: []Excluded{}, Warnings: []Warning{}}
	for _, it := range items {
		p.Bytes += it.Size
	}

	sorted := append([]Disk(nil), disks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	st := state{opts: opt}
	for _, d := range sorted {
		reason := ""
		switch {
		case d.State != "ONLINE":
			reason = "state=" + d.State
		case !d.Writable:
			reason = "read-only"
		case opt.ForcedOrdinal != 0 && d.Ordinal != opt.ForcedOrdinal:
			reason = fmt.Sprintf("forced ordinal %d", opt.ForcedOrdinal)
		case opt.ForcedOrdinal == 0 && !d.AutoPlace:
			reason = "role=" + d.Role
		case opt.ForcedOrdinal == 0 && opt.StartOrdinal != 0 && d.Ordinal < opt.StartOrdinal:
			reason = fmt.Sprintf("before start ordinal %d", opt.StartOrdinal)
		}
		if reason != "" {
			p.Excluded = append(p.Excluded, Excluded{Ordinal: d.Ordinal, DiskID: d.ID, Name: d.Name, Reason: reason})
			continue
		}
		c := &cand{Disk: d, free: d.EffectiveFree}
		st.disks = append(st.disks, c)
	}
	if !opt.Spillover && opt.ForcedOrdinal == 0 && len(st.disks) > 1 {
		for _, c := range st.disks[1:] {
			p.Excluded = append(p.Excluded, Excluded{Ordinal: c.Ordinal, DiskID: c.ID, Name: c.Name, Reason: "spillover disabled"})
		}
		st.disks = st.disks[:1]
	}
	if opt.ForcedOrdinal != 0 && len(st.disks) == 0 {
		p.Error = ErrForcedUnavailable
	} else if len(st.disks) == 0 {
		p.Error = ErrNoCandidates
	}

	if p.Error == "" {
		switch opt.Policy {
		case WholeTree:
			st.placeGroup(&p, items, true)
		case TopdirAffinity:
			for _, g := range groupTopLevel(items) {
				if !st.placeGroup(&p, g, false) {
					st.placeFiles(&p, g)
				}
			}
		default:
			st.placeFiles(&p, items)
		}
	}

	for _, c := range st.disks {
		if c.pl != nil {
			c.pl.RemainingAboveReserve = c.free - c.MinFree
			p.Placements = append(p.Placements, *c.pl)
		}
	}
	sort.Slice(p.Placements, func(i, j int) bool { return p.Placements[i].Ordinal < p.Placements[j].Ordinal })
	p.Sufficient = p.Error == "" && p.Unplaced == 0
	if p.Unplaced > 0 && p.Error == "" {
		p.Error = ErrPoolOutOfSpace
		if opt.ForcedOrdinal != 0 {
			p.Error = ErrInsufficientOnForced
		}
	}
	p.ETASeconds = p.Bytes / opt.ThroughputBps
	p.Warnings = append(p.Warnings, nameWarnings(items, st.disks, p.Assignments)...)
	return p
}

// placeGroup puts all items on one disk. whole=true records failure as NoSingleDiskCanHoldTree.
func (s *state) placeGroup(p *Plan, items []Item, whole bool) bool {
	var size int64
	for _, it := range items {
		size += it.Size
	}
	if c := s.choose(size); c != nil {
		for _, it := range items {
			s.assign(p, c, it)
		}
		return true
	}
	if whole {
		p.Error = ErrNoSingleDiskCanHoldTree
		p.Unplaced += len(items)
		p.UnplacedB += size
	}
	return false
}

func (s *state) placeFiles(p *Plan, items []Item) {
	for _, it := range items {
		if c := s.choose(it.Size); c != nil {
			s.assign(p, c, it)
		} else {
			p.Unplaced++
			p.UnplacedB += it.Size
		}
	}
}

// choose implements choose_target over the (already filtered, ordinal-sorted) candidates.
func (s *state) choose(size int64) *cand {
	switch s.opts.Policy {
	case MostFree:
		var best *cand
		for _, c := range s.disks {
			if c.fits(size) && (best == nil || c.free > best.free) {
				best = c
			}
		}
		return best
	case Balanced:
		var best *cand
		ratio := func(c *cand) float64 {
			if c.TotalBytes == 0 {
				return 0
			}
			return float64(c.free-size) / float64(c.TotalBytes)
		}
		for _, c := range s.disks {
			if c.fits(size) && (best == nil || ratio(c) > ratio(best)) {
				best = c
			}
		}
		return best
	default: // ordinal ASC, first fit
		for _, c := range s.disks {
			if c.fits(size) {
				return c
			}
		}
		return nil
	}
}

func (s *state) assign(p *Plan, c *cand, it Item) {
	c.free -= it.Size
	if c.pl == nil {
		c.pl = &DiskPlacement{Ordinal: c.Ordinal, DiskID: c.ID, Name: c.Name, Node: c.Node}
	}
	c.pl.Files++
	c.pl.Bytes += it.Size
	p.Assignments = append(p.Assignments, Assignment{Path: it.Path, Size: it.Size, DiskID: c.ID})
}

// groupTopLevel groups items by their first path segment (top-level folder, or the file itself).
func groupTopLevel(items []Item) [][]Item {
	idx := map[string]int{}
	var groups [][]Item
	for _, it := range items {
		top := it.Path
		if i := strings.IndexByte(it.Path, '/'); i >= 0 {
			top = it.Path[:i]
		}
		g, ok := idx[top]
		if !ok {
			g = len(groups)
			idx[top] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], it)
	}
	return groups
}

var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// nameWarnings applies target-FS name rules at plan time (FR-T06, PRD §11.5).
func nameWarnings(items []Item, disks []*cand, as []Assignment) []Warning {
	byID := map[string]*cand{}
	for _, c := range disks {
		byID[c.ID] = c
	}
	out := []Warning{}
	seenFold := map[string]map[string]string{} // disk → folded path → original
	for _, a := range as {
		c := byID[a.DiskID]
		if c == nil {
			continue
		}
		fs := strings.ToLower(c.FSType)
		ntfsLike := fs == "ntfs" || fs == "refs" || fs == "exfat" || fs == "msdos" || fs == "vfat" || fs == "fat32"
		for _, seg := range strings.Split(a.Path, "/") {
			if ntfsLike {
				if strings.ContainsAny(seg, `:*?"<>|\`) {
					out = append(out, Warning{Code: "NAME_RULE", Path: a.Path, Fix: SafeName(seg), Msg: "forbidden character for " + c.FSType})
					break
				}
				base := strings.ToUpper(strings.TrimSuffix(seg, path.Ext(seg)))
				if windowsReserved[base] {
					out = append(out, Warning{Code: "NAME_RESERVED", Path: a.Path, Fix: "_" + seg, Msg: "reserved device name"})
					break
				}
				if strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") {
					out = append(out, Warning{Code: "NAME_TRAILING", Path: a.Path, Fix: strings.TrimRight(seg, " ."), Msg: "trailing space or dot"})
					break
				}
			}
			if len(seg) > 255 {
				out = append(out, Warning{Code: "NAME_LENGTH", Path: a.Path, Msg: "name longer than 255 bytes"})
				break
			}
		}
		if (fs == "msdos" || fs == "vfat" || fs == "fat32") && a.Size >= 4<<30 {
			out = append(out, Warning{Code: "FAT32_4GB", Path: a.Path, Msg: "file ≥ 4 GiB cannot be stored on FAT32"})
		}
		if c.CaseInsensitive {
			m := seenFold[c.ID]
			if m == nil {
				m = map[string]string{}
				seenFold[c.ID] = m
			}
			k := strings.ToLower(norm.NFC.String(a.Path))
			if orig, ok := m[k]; ok && orig != a.Path {
				out = append(out, Warning{Code: "CASE_COLLISION", Path: a.Path, Msg: "collides with " + orig + " on case-insensitive filesystem"})
			}
			m[k] = a.Path
		}
	}
	return out
}

// SafeName replaces NTFS-forbidden characters with '_' and NFC-normalises (auto-rename rule).
func SafeName(s string) string {
	s = norm.NFC.String(s)
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:*?"<>|\`, r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
}
