package disk

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ---- SMART (FR-D13) ----

// SmartAttr is one SMART attribute row (ATA) or NVMe health field.
type SmartAttr struct {
	ID     int    `json:"id,omitempty"`
	Name   string `json:"name"`
	Value  int64  `json:"value"`
	Worst  int64  `json:"worst,omitempty"`
	Thresh int64  `json:"thresh,omitempty"`
	Raw    string `json:"raw,omitempty"`
	Failed bool   `json:"failed,omitempty"`
}

// Smart is the health summary of the physical device behind a mount.
type Smart struct {
	Available   bool        `json:"available"`
	Source      string      `json:"source"` // smartctl | diskutil | powershell | none
	Status      string      `json:"status"` // PASSED | FAILING | Verified | Not Supported | Unknown
	Passed      *bool       `json:"passed,omitempty"`
	Temperature *int        `json:"temperature_c,omitempty"`
	PowerOnH    *int64      `json:"power_on_hours,omitempty"`
	Model       string      `json:"model,omitempty"`
	Serial      string      `json:"serial,omitempty"`
	Device      string      `json:"device,omitempty"`
	Attributes  []SmartAttr `json:"attributes"`
	Note        string      `json:"note,omitempty"`
	CheckedAt   string      `json:"checked_at"`
}

// ReadSmart queries SMART for the device holding mountPath: smartctl when installed (all OS), else
// diskutil (macOS; USB bridges report "Not Supported") or Get-PhysicalDisk (Windows).
func ReadSmart(ctx context.Context, mountPath string) Smart {
	s := Smart{Source: "none", Status: "Unknown", Attributes: []SmartAttr{}, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if mp, _ := MountOf(mountPath); mp != "" { // a folder-based disk → the OS volume that holds it
		mountPath = mp
	}
	dev := deviceFor(ctx, mountPath)
	s.Device = dev
	if bin, err := exec.LookPath("smartctl"); err == nil && dev != "" {
		if out, err := run(ctx, bin, "-a", "-j", dev); len(out) > 0 { // smartctl exits non-zero on warnings but still prints JSON
			if p, perr := ParseSmartctl(out); perr == nil {
				p.Device, p.CheckedAt = dev, s.CheckedAt
				return p
			} else if err != nil {
				s.Note = "smartctl: " + err.Error()
			}
		}
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "sh", "-c", `diskutil info -plist "$1" | plutil -convert json -o - -`, "sh", mountPath).Output()
		if err == nil {
			var info struct {
				SMARTStatus string `json:"SMARTStatus"`
				MediaName   string `json:"MediaName"`
			}
			if json.Unmarshal(out, &info) == nil && info.SMARTStatus != "" {
				s.Source, s.Status, s.Model = "diskutil", info.SMARTStatus, info.MediaName
				s.Available = info.SMARTStatus != "Not Supported"
				if s.Available {
					ok := info.SMARTStatus == "Verified"
					s.Passed = &ok
				}
				if !s.Available {
					s.Note = "SMART not available through this USB bridge (macOS needs a third-party driver) — install smartmontools for more"
				} else if s.Note == "" {
					s.Note = "diskutil reports status only; install smartmontools (smartctl) for attributes and temperature"
				}
			}
		}
	case "windows":
		if len(mountPath) >= 2 && mountPath[1] == ':' {
			ps := fmt.Sprintf(`Get-Partition -DriveLetter %c | Get-Disk | Select-Object FriendlyName,SerialNumber,HealthStatus | ConvertTo-Json`, mountPath[0])
			if out, err := run(ctx, "powershell", "-NoProfile", "-Command", ps); err == nil {
				var info struct{ FriendlyName, SerialNumber, HealthStatus string }
				if json.Unmarshal(out, &info) == nil && info.HealthStatus != "" {
					ok := info.HealthStatus == "Healthy"
					s.Source, s.Status, s.Model, s.Serial, s.Passed, s.Available = "powershell", info.HealthStatus, info.FriendlyName, strings.TrimSpace(info.SerialNumber), &ok, true
					s.Note = "Windows reports health only; install smartmontools (smartctl) for attributes"
				}
			}
		}
	}
	if s.Source == "none" && s.Note == "" {
		s.Note = "SMART not available — install smartmontools (smartctl)"
	}
	return s
}

// ParseSmartctl parses `smartctl -a -j` (ATA and NVMe).
func ParseSmartctl(b []byte) (Smart, error) {
	var j struct {
		Device struct {
			Name string `json:"name"`
		} `json:"device"`
		ModelName   string `json:"model_name"`
		Serial      string `json:"serial_number"`
		SmartStatus *struct {
			Passed bool `json:"passed"`
		} `json:"smart_status"`
		Temperature struct {
			Current *int `json:"current"`
		} `json:"temperature"`
		PowerOnTime struct {
			Hours *int64 `json:"hours"`
		} `json:"power_on_time"`
		ATA struct {
			Table []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				Value      int64  `json:"value"`
				Worst      int64  `json:"worst"`
				Thresh     int64  `json:"thresh"`
				WhenFailed string `json:"when_failed"`
				Raw        struct {
					String string `json:"string"`
				} `json:"raw"`
			} `json:"table"`
		} `json:"ata_smart_attributes"`
		NVMe map[string]any `json:"nvme_smart_health_information_log"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return Smart{}, err
	}
	if j.SmartStatus == nil && len(j.ATA.Table) == 0 && j.NVMe == nil {
		return Smart{}, errors.New("no SMART data in smartctl output")
	}
	s := Smart{Available: true, Source: "smartctl", Status: "Unknown", Model: j.ModelName, Serial: j.Serial, Device: j.Device.Name,
		Temperature: j.Temperature.Current, PowerOnH: j.PowerOnTime.Hours, Attributes: []SmartAttr{}}
	if j.SmartStatus != nil {
		p := j.SmartStatus.Passed
		s.Passed = &p
		s.Status = map[bool]string{true: "PASSED", false: "FAILING"}[p]
	}
	for _, a := range j.ATA.Table {
		s.Attributes = append(s.Attributes, SmartAttr{ID: a.ID, Name: a.Name, Value: a.Value, Worst: a.Worst, Thresh: a.Thresh, Raw: a.Raw.String,
			Failed: a.WhenFailed != "" && a.WhenFailed != "-"})
	}
	for k, v := range j.NVMe {
		if f, ok := v.(float64); ok {
			s.Attributes = append(s.Attributes, SmartAttr{Name: k, Value: int64(f)})
		}
	}
	return s, nil
}

// deviceFor maps a mount point to its device node (smartctl input).
func deviceFor(ctx context.Context, mountPath string) string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "sh", "-c", `diskutil info -plist "$1" | plutil -convert json -o - -`, "sh", mountPath).Output()
		if err == nil {
			var info struct{ ParentWholeDisk string }
			if json.Unmarshal(out, &info) == nil && info.ParentWholeDisk != "" {
				return "/dev/" + info.ParentWholeDisk
			}
		}
	case "linux":
		if out, err := run(ctx, "findmnt", "-no", "SOURCE", "--target", mountPath); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "windows":
		if len(mountPath) >= 2 && mountPath[1] == ':' {
			return mountPath[:2]
		}
	}
	return ""
}

// ---- Benchmark (FR-D14) ----

// Bench is one benchmark result card.
type Bench struct {
	SizeBytes    int64   `json:"size_bytes"`
	SeqWriteMBs  float64 `json:"seq_write_mb_s"`
	SeqReadMBs   float64 `json:"seq_read_mb_s"`
	RandReadIOPS float64 `json:"rand_read_4k_iops"`
	CacheBypass  bool    `json:"cache_bypass"` // false: read numbers may include the OS page cache
	DurationMs   int64   `json:"duration_ms"`
	At           string  `json:"at"`
}

// RunBench writes a temp file of size bytes inside dataRoot (fsync), reads it back sequentially and
// issues 4 KiB random reads; the file is always removed.
func RunBench(ctx context.Context, dataRoot string, size int64) (Bench, error) {
	start := time.Now()
	r := Bench{SizeBytes: size, At: start.UTC().Format(time.RFC3339)}
	p := filepath.Join(dataRoot, fmt.Sprintf(".probe-bench-%d", start.UnixNano()))
	defer os.Remove(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o600)
	if err != nil {
		return r, err
	}
	defer f.Close()
	r.CacheBypass = noCache(f)
	buf := make([]byte, 1<<20)
	rand.Read(buf)
	t := time.Now()
	for n := int64(0); n < size; n += int64(len(buf)) {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if _, err := f.Write(buf[:min(int64(len(buf)), size-n)]); err != nil {
			return r, classify(err)
		}
	}
	if err := f.Sync(); err != nil {
		return r, err
	}
	r.SeqWriteMBs = mbs(size, time.Since(t))
	dropCache(f)
	f.Seek(0, io.SeekStart)
	t = time.Now()
	if _, err := io.CopyBuffer(io.Discard, f, buf); err != nil {
		return r, err
	}
	r.SeqReadMBs = mbs(size, time.Since(t))
	dropCache(f)
	blocks := size / 4096
	ops, small := 0, make([]byte, 4096)
	t = time.Now()
	for time.Since(t) < 2*time.Second && ops < 20000 && blocks > 0 {
		if _, err := f.ReadAt(small, mrand.Int64N(blocks)*4096); err != nil {
			return r, err
		}
		ops++
	}
	if el := time.Since(t).Seconds(); el > 0 {
		r.RandReadIOPS = float64(ops) / el
	}
	r.DurationMs = time.Since(start).Milliseconds()
	return r, nil
}

func mbs(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / 1e6 / d.Seconds()
}

func classify(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "no space") {
		return fmt.Errorf("not enough free space for the benchmark: %w", err)
	}
	return err
}

// ---- Open in Finder / Explorer (FR-D27) ----

// Reveal opens dir in the OS file manager of the machine running the Controller.
func Reveal(dir string) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("not a folder: %s", dir)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}
