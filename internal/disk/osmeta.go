package disk

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// meta carries OS-specific identity fields keyed by device or mount point (PRD §10.2).
type meta struct {
	UUID      string
	Label     string
	Bus       string
	Removable bool
	Serial    string
}

func (m meta) apply(d *Descriptor) {
	if m.UUID != "" {
		d.FilesystemUUID = m.UUID
	}
	if m.Label != "" {
		d.VolumeLabel = m.Label
	}
	if m.Bus != "" {
		d.BusType = strings.ToUpper(m.Bus)
	}
	d.Removable = d.Removable || m.Removable
	if m.Serial != "" {
		d.NativeDiskID = m.Serial
	}
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

var metaCache struct {
	sync.Mutex
	at time.Time
	m  map[string]meta
}

// osMetadata returns identity metadata for all volumes. Failures yield an empty map
// (discovery still works; identity falls back to PathIdentity). Cached briefly: the OS
// tools are slow (diskutil can take seconds) and discovery/attach call this repeatedly.
func osMetadata(ctx context.Context) map[string]meta {
	metaCache.Lock()
	defer metaCache.Unlock()
	if metaCache.m != nil && time.Since(metaCache.at) < 30*time.Second {
		return metaCache.m
	}
	m := map[string]meta{}
	switch runtime.GOOS {
	case "darwin":
		m = darwinMeta(ctx)
	case "linux":
		m = linuxMeta(ctx)
	case "windows":
		m = windowsMeta(ctx)
	}
	metaCache.m, metaCache.at = m, time.Now()
	return m
}

// darwin: diskutil info -plist <mount> → plutil → JSON.
func darwinMeta(ctx context.Context) map[string]meta {
	out := map[string]meta{}
	parts, err := exec.CommandContext(ctx, "mount").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(parts)), "\n") {
		// "<dev> on <mountpoint> (<fstype>, ...)"
		on, lp := strings.Index(line, " on "), strings.LastIndex(line, " (")
		if on < 0 || lp < on {
			continue
		}
		mp, fstype := line[on+4:lp], strings.SplitN(line[lp+2:], ",", 2)[0]
		if mp != "/" && !strings.HasPrefix(mp, "/Volumes/") {
			continue
		}
		switch strings.TrimSuffix(fstype, ")") {
		case "smbfs", "nfs", "afpfs", "webdav", "ftp": // network shares: diskutil has nothing useful and may block for seconds
			continue
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", `diskutil info -plist "$1" | plutil -convert json -o - -`, "sh", mp)
		b, err := cmd.Output()
		if err != nil {
			continue
		}
		var info struct {
			VolumeUUID  string `json:"VolumeUUID"`
			VolumeName  string `json:"VolumeName"`
			BusProtocol string `json:"BusProtocol"`
			Removable   bool   `json:"RemovableMediaOrExternalDevice"`
			Internal    bool   `json:"Internal"`
			DeviceNode  string `json:"DeviceNode"`
			DiskUUID    string `json:"DiskUUID"`
		}
		if json.Unmarshal(b, &info) != nil {
			continue
		}
		m := meta{UUID: info.VolumeUUID, Label: info.VolumeName, Bus: info.BusProtocol, Removable: info.Removable || !info.Internal, Serial: info.DiskUUID}
		out[mp] = m
		if info.DeviceNode != "" {
			out[info.DeviceNode] = m
		}
	}
	return out
}

// linux: lsblk --json.
func linuxMeta(ctx context.Context) map[string]meta {
	out := map[string]meta{}
	b, err := run(ctx, "lsblk", "--json", "-o", "PATH,UUID,LABEL,TRAN,RM,SERIAL,MOUNTPOINT")
	if err != nil {
		return out
	}
	type dev struct {
		Path       string `json:"path"`
		UUID       string `json:"uuid"`
		Label      string `json:"label"`
		Tran       string `json:"tran"`
		RM         any    `json:"rm"`
		Serial     string `json:"serial"`
		Mountpoint string `json:"mountpoint"`
		Children   []dev  `json:"children"`
	}
	var doc struct {
		Blockdevices []dev `json:"blockdevices"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return out
	}
	var walk func(ds []dev, tran, serial string, rm bool)
	walk = func(ds []dev, tran, serial string, rm bool) {
		for _, d := range ds {
			t, s := d.Tran, d.Serial
			if t == "" {
				t = tran
			}
			if s == "" {
				s = serial
			}
			r := rm || d.RM == true || d.RM == "1"
			m := meta{UUID: d.UUID, Label: d.Label, Bus: t, Removable: r, Serial: s}
			out[d.Path] = m
			if d.Mountpoint != "" {
				out[d.Mountpoint] = m
			}
			walk(d.Children, t, s, r)
		}
	}
	walk(doc.Blockdevices, "", "", false)
	return out
}

// windows: Get-Volume + Get-Partition/Get-Disk (drive letter → Volume GUID, bus).
func windowsMeta(ctx context.Context) map[string]meta {
	out := map[string]meta{}
	script := `Get-Volume | Where-Object DriveLetter | ForEach-Object {
  $p = Get-Partition -DriveLetter $_.DriveLetter -ErrorAction SilentlyContinue
  $d = if ($p) { Get-Disk -Number $p.DiskNumber -ErrorAction SilentlyContinue }
  [pscustomobject]@{ L=[string]$_.DriveLetter; U=$_.UniqueId; N=$_.FileSystemLabel; B=[string]$d.BusType; R=($_.DriveType -eq 'Removable'); S=$d.SerialNumber }
} | ConvertTo-Json -Compress`
	b, err := run(ctx, "powershell", "-NoProfile", "-Command", script)
	if err != nil {
		return out
	}
	var rows []struct {
		L, U, N, B, S string
		R             bool
	}
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "{") {
		s = "[" + s + "]"
	}
	if json.Unmarshal([]byte(s), &rows) != nil {
		return out
	}
	for _, r := range rows {
		out[r.L+":"] = meta{UUID: r.U, Label: r.N, Bus: r.B, Removable: r.R, Serial: strings.TrimSpace(r.S)}
		out[r.L+`:\`] = out[r.L+":"]
	}
	return out
}
