package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DataRootName is the Continuum-owned directory on each disk (PRD §7.7).
const DataRootName = ".continuum"

// MarkerName is the disk marker file inside the data root (FR-D05).
const MarkerName = "volume.json"

// Marker is <mount>/.continuum/volume.json (PRD §13.2).
type Marker struct {
	Schema          int    `json:"schema"`
	ControllerID    string `json:"controller_id"`
	VolumeID        string `json:"volume_id"`
	VolumeSlug      string `json:"volume_slug"`
	DiskID          string `json:"disk_id"`
	FilesystemUUID  string `json:"filesystem_uuid"`
	OrdinalSnapshot int    `json:"ordinal_snapshot"`
	Role            string `json:"role"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// ReadMarker reads the marker from a data root.
func ReadMarker(dataRoot string) (*Marker, error) {
	b, err := os.ReadFile(filepath.Join(dataRoot, MarkerName))
	if err != nil {
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("invalid marker: %w", err)
	}
	return &m, nil
}

// WriteMarker writes the marker atomically (temp + fsync + rename).
func WriteMarker(dataRoot string, m Marker) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(dataRoot, MarkerName+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(tmp, filepath.Join(dataRoot, MarkerName))
}

// Check is one pre-attach check result.
type Check struct {
	No     int    `json:"no"`
	Name   string `json:"name"`
	Status string `json:"status"` // pass | warn | fail
	Detail string `json:"detail,omitempty"`
}

// Report is the preflight report (FR-D04).
type Report struct {
	Path         string   `json:"path"`
	DataRoot     string   `json:"data_root"`
	Checks       []Check  `json:"checks"`
	OK           bool     `json:"ok"`
	Capacity     Capacity `json:"capacity"`
	Capabilities []string `json:"capabilities"`
	CaseInsens   bool     `json:"case_insensitive"`
	SuggestRole  string   `json:"suggest_role,omitempty"`
	Marker       *Marker  `json:"existing_marker,omitempty"`
}

// PreflightInput configures Preflight.
type PreflightInput struct {
	Path          string
	MinFreeBytes  uint64
	ControllerID  string
	VolumeID      string // target volume ("" = not yet decided)
	NetworkBacked bool
	Stat          StatFunc
}

// Preflight runs the 10 checks of PRD §10.3. It creates the data root if missing.
func Preflight(in PreflightInput) Report {
	root := filepath.Join(in.Path, DataRootName)
	r := Report{Path: in.Path, DataRoot: root}
	add := func(no int, name, status, detail string) {
		r.Checks = append(r.Checks, Check{No: no, Name: name, Status: status, Detail: detail})
	}

	// 1 접근 가능
	if fi, err := os.Stat(in.Path); err != nil || !fi.IsDir() {
		add(1, "access", "fail", fmt.Sprintf("path not accessible: %v", err))
		r.OK = false
		return r
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		add(1, "access", "fail", "cannot create data root: "+err.Error())
		return r
	}
	add(1, "access", "pass", root)

	// 2 쓰기 권한
	probe := filepath.Join(root, ".probe-"+fmt.Sprint(time.Now().UnixNano()))
	if err := writeSync(probe, []byte("continuum")); err != nil {
		add(2, "write", "fail", err.Error())
	} else {
		os.Remove(probe)
		add(2, "write", "pass", "")
	}

	// 3 FS 종류·UUID
	fsType := detectFSType(in.Path)
	r.Capabilities = fsCapabilities(fsType)
	if fsType == "" {
		add(3, "filesystem", "warn", "filesystem type unknown — marker identity used")
	} else {
		add(3, "filesystem", "pass", fsType)
	}

	// 4 용량 · 5 예약 유효성
	c, err := in.Stat(in.Path)
	if err != nil {
		add(4, "capacity", "fail", err.Error())
	} else {
		r.Capacity = c
		if c.Available < in.MinFreeBytes {
			add(4, "capacity", "warn", fmt.Sprintf("available %d < min_free %d", c.Available, in.MinFreeBytes))
		} else {
			add(4, "capacity", "pass", fmt.Sprintf("total %d, available %d", c.Total, c.Available))
		}
		if c.Total > 0 && in.MinFreeBytes >= c.Total/2 {
			add(5, "reserve", "fail", "min_free must be < 50% of total")
		} else {
			add(5, "reserve", "pass", "")
		}
	}

	// 6 data root 충돌
	if m, err := ReadMarker(root); err == nil {
		r.Marker = m
		switch {
		case in.ControllerID != "" && m.ControllerID != "" && m.ControllerID != in.ControllerID:
			add(6, "data-root", "fail", "disk belongs to another controller ("+m.ControllerID+") — use volume import")
		case in.VolumeID != "" && m.VolumeID != "" && m.VolumeID != in.VolumeID:
			add(6, "data-root", "fail", "disk already belongs to volume "+m.VolumeSlug)
		default:
			add(6, "data-root", "pass", "existing marker "+m.DiskID+" (re-attach)")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		add(6, "data-root", "pass", "")
	} else {
		add(6, "data-root", "fail", err.Error())
	}

	// 7 대소문자 처리
	a, b := filepath.Join(root, ".case-a"), filepath.Join(root, ".CASE-A")
	if err := writeSync(a, []byte("a")); err == nil {
		_, err := os.Stat(b)
		r.CaseInsens = err == nil
		os.Remove(a)
		if r.CaseInsens {
			add(7, "case", "warn", "case-insensitive filesystem — name collision rules applied")
		} else {
			add(7, "case", "pass", "case-sensitive")
		}
	} else {
		add(7, "case", "fail", err.Error())
	}

	// 8 최대 이름 길이
	long := filepath.Join(root, strings.Repeat("n", 255))
	if err := writeSync(long, nil); err != nil {
		add(8, "name-length", "warn", "255-byte names not supported")
	} else {
		os.Remove(long)
		add(8, "name-length", "pass", "255")
	}

	// 9 링크·xattr
	var missing []string
	target := filepath.Join(root, ".link-target")
	if err := writeSync(target, []byte("x")); err == nil {
		if err := os.Symlink(target, target+".sym"); err != nil {
			missing = append(missing, "symlink")
		}
		if err := os.Link(target, target+".hard"); err != nil {
			missing = append(missing, "hardlink")
		}
		os.Remove(target + ".sym")
		os.Remove(target + ".hard")
		os.Remove(target)
	}
	if len(missing) > 0 {
		add(9, "links", "warn", "unsupported: "+strings.Join(missing, ","))
	} else {
		add(9, "links", "pass", "")
	}

	// 10 네트워크 지연
	start := time.Now()
	lat := filepath.Join(root, ".latency")
	_ = writeSync(lat, []byte("x"))
	os.Remove(lat)
	rtt := time.Since(start)
	if in.NetworkBacked && rtt > 50*time.Millisecond {
		add(10, "latency", "warn", fmt.Sprintf("write latency %s — semi-permanent role suggested", rtt.Round(time.Millisecond)))
		r.SuggestRole = "semi-permanent"
	} else {
		add(10, "latency", "pass", rtt.Round(time.Microsecond).String())
	}

	r.OK = true
	for _, ch := range r.Checks {
		if ch.Status == "fail" {
			r.OK = false
		}
	}
	return r
}

func writeSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// detectFSType finds the filesystem of the mount containing path.
func detectFSType(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	mp, fs := mountOf(abs)
	_ = mp
	return fs
}

// mountOf returns the longest mount point prefix of abs and its fstype.
func mountOf(abs string) (string, string) {
	parts, err := partitions()
	if err != nil {
		return "", ""
	}
	best, fs := "", ""
	for _, p := range parts {
		mp := p.Mountpoint
		if runtime.GOOS == "windows" {
			mp = strings.ToLower(mp)
		}
		cmp := abs
		if runtime.GOOS == "windows" {
			cmp = strings.ToLower(abs)
		}
		if (cmp == mp || strings.HasPrefix(cmp, strings.TrimSuffix(mp, string(filepath.Separator))+string(filepath.Separator)) || mp == "/") && len(mp) > len(best) {
			best, fs = p.Mountpoint, p.Fstype
		}
	}
	return best, fs
}

// MountOf is exported for callers that need the mount point of a folder.
func MountOf(path string) (mount, fstype string) {
	abs, _ := filepath.Abs(path)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return mountOf(abs)
}
