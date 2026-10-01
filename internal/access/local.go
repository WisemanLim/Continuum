package access

import (
	"fmt"
	"sort"
	"strings"

	"continuum/internal/core"
)

// LocalMount renders a Linux mergerfs mount of a volume's local disks (FR-A04 "로컬 전용은 mergerfs"):
// read union in ordinal order; new files go to the first auto-place disk with room (category.create=ff).
type LocalMount struct {
	Command     string `json:"command"`      // one-shot mount
	Fstab       string `json:"fstab"`        // /etc/fstab line
	SystemdUnit string `json:"systemd_unit"` // <escaped>.mount unit
	UnitName    string `json:"unit_name"`
}

// systemdEscapePath mimics `systemd-escape --path` for the unit name.
func systemdEscapePath(p string) string {
	p = strings.Trim(p, "/")
	var b strings.Builder
	for i, r := range p {
		switch {
		case r == '/':
			b.WriteByte('-')
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || (r == '.' && i > 0):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, `\x%02x`, r)
		}
	}
	return b.String()
}

// RenderLocalMount builds the mount for local disks of v (node = this host).
func RenderLocalMount(v core.Volume, node, mountpoint string) (LocalMount, error) {
	ds := append([]core.Disk(nil), v.Disks...)
	sort.Slice(ds, func(i, j int) bool { return ds[i].Ordinal < ds[j].Ordinal })
	var branches []string
	for _, d := range ds {
		if d.NodeID != node || d.State == core.StateDisabled {
			continue
		}
		mode := "NC"
		if d.Writable && core.AutoPlace(d.Role) {
			mode = "RW"
		}
		if !d.Writable {
			mode = "RO"
		}
		branches = append(branches, d.DataRoot+"="+mode)
	}
	if len(branches) == 0 {
		return LocalMount{}, core.Errf(core.CodeInvalid, "volume %s has no disk on this node", v.Slug)
	}
	minFree := v.MinFreeBytes
	if minFree <= 0 {
		minFree = 1 << 20
	}
	opts := fmt.Sprintf("allow_other,use_ino,cache.files=off,dropcacheonclose=true,category.create=ff,minfreespace=%d,fsname=continuum-%s", minFree, v.Slug)
	src := strings.Join(branches, ":")
	unit := systemdEscapePath(mountpoint) + ".mount"
	return LocalMount{
		Command:  fmt.Sprintf("mkdir -p %s && mergerfs -o %s %s %s", mountpoint, opts, src, mountpoint),
		Fstab:    fmt.Sprintf("%s  %s  fuse.mergerfs  %s,nofail  0 0", src, mountpoint, opts),
		UnitName: unit,
		SystemdUnit: fmt.Sprintf(`[Unit]
Description=Continuum volume %s (mergerfs, ordinal order)
After=local-fs.target

[Mount]
What=%s
Where=%s
Type=fuse.mergerfs
Options=%s,nofail

[Install]
WantedBy=multi-user.target
`, v.Slug, src, mountpoint, opts),
	}, nil
}

// RenderAutoconnect returns a macOS LaunchAgent that (re)mounts an SMB share at login, every 5 minutes and on
// network changes (FR-A02). Credentials come from the login keychain (`continuum access autoconnect` stores them).
func RenderAutoconnect(label, smbURL string) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	script := fmt.Sprintf(`try
	mount volume "%s"
end try`, smbURL)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/osascript</string>
    <string>-e</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>300</integer>
  <key>WatchPaths</key>
  <array><string>/Library/Preferences/SystemConfiguration</string></array>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
`, esc(label), esc(script))
}
