// Package disk discovers local disks/volumes (FR-D01·D02), reads capacity (FR-D12)
// and runs the 10 pre-attach checks (FR-D04, PRD §10.3). It has no database dependency.
package disk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	gdisk "github.com/shirou/gopsutil/v4/disk"
)

// Descriptor is the 16-field DiskDescriptor (PRD §13.2, D43).
type Descriptor struct {
	NodeID         string   `json:"node_id"`
	NativeDiskID   string   `json:"native_disk_id"`
	FilesystemUUID string   `json:"filesystem_uuid"`
	VolumeLabel    string   `json:"volume_label"`
	FilesystemType string   `json:"filesystem_type"`
	MountPath      string   `json:"mount_path"`
	TotalBytes     uint64   `json:"total_bytes"`
	FreeBytes      uint64   `json:"free_bytes"`
	AvailableBytes uint64   `json:"available_bytes"`
	Mounted        bool     `json:"mounted"`
	Writable       bool     `json:"writable"`
	Removable      bool     `json:"removable"`
	NetworkBacked  bool     `json:"network_backed"`
	BusType        string   `json:"bus_type"`
	Health         string   `json:"health"`
	Capabilities   []string `json:"capabilities"`
}

// Capacity is a statfs snapshot.
type Capacity struct {
	Total     uint64 `json:"total_bytes"`
	Free      uint64 `json:"free_bytes"`
	Available uint64 `json:"available_bytes"`
}

// StatFunc reads capacity for a path. Tests replace it to simulate disk sizes (FR-P08).
type StatFunc func(path string) (Capacity, error)

// Stat is the real statfs implementation.
func Stat(path string) (Capacity, error) {
	u, err := gdisk.Usage(path)
	if err != nil {
		return Capacity{}, err
	}
	// gopsutil Free = blocks available to unprivileged users (Bavail) on unix.
	return Capacity{Total: u.Total, Free: u.Free, Available: u.Free}, nil
}

var pseudoFS = map[string]bool{
	"devfs": true, "autofs": true, "tmpfs": true, "devtmpfs": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true, "nullfs": true, "mqueue": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true, "bpf": true, "fusectl": true,
	"configfs": true, "hugetlbfs": true, "ramfs": true, "binfmt_misc": true, "efivarfs": true, "nsfs": true,
}

var networkFS = map[string]bool{"smbfs": true, "cifs": true, "nfs": true, "nfs4": true, "afpfs": true, "webdav": true, "fuse.sshfs": true}

// Discover lists user-visible mounted volumes on this node.
func Discover(ctx context.Context, nodeID string, stat StatFunc) ([]Descriptor, error) {
	parts, err := gdisk.PartitionsWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	meta := osMetadata(ctx)
	seen := map[string]bool{}
	out := []Descriptor{}
	for _, p := range parts {
		if pseudoFS[p.Fstype] || !userVisible(p.Mountpoint) || seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		d := Descriptor{
			NodeID: nodeID, MountPath: p.Mountpoint, FilesystemType: p.Fstype, Mounted: true,
			NativeDiskID: p.Device, Health: "ONLINE", NetworkBacked: networkFS[p.Fstype],
			Writable: !hasOpt(p.Opts, "ro") && !hasOpt(p.Opts, "rdonly") && !hasOpt(p.Opts, "read-only"),
		}
		if c, err := stat(p.Mountpoint); err == nil {
			d.TotalBytes, d.FreeBytes, d.AvailableBytes = c.Total, c.Free, c.Available
		}
		if m, ok := meta[p.Device]; ok {
			m.apply(&d)
		} else if m, ok := meta[p.Mountpoint]; ok {
			m.apply(&d)
		}
		if d.NetworkBacked && d.BusType == "" {
			d.BusType = strings.ToUpper(strings.TrimPrefix(p.Fstype, "fuse."))
		}
		if d.FilesystemUUID == "" {
			d.FilesystemUUID = PathIdentity(p.Mountpoint)
		}
		d.Capabilities = fsCapabilities(p.Fstype)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MountPath < out[j].MountPath })
	return out, nil
}

// PathIdentity is a stable fallback identity for folders / FS without UUID (FAT32, network shares).
func PathIdentity(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			abs = r
		}
	}
	h := sha256.Sum256([]byte(abs))
	return "path:" + hex.EncodeToString(h[:8])
}

func userVisible(mp string) bool {
	switch runtime.GOOS {
	case "darwin":
		return mp == "/" || strings.HasPrefix(mp, "/Volumes/")
	case "windows":
		return len(mp) >= 2 && mp[1] == ':'
	default:
		if mp == "/" || strings.HasPrefix(mp, "/mnt") || strings.HasPrefix(mp, "/media") ||
			strings.HasPrefix(mp, "/run/media") || strings.HasPrefix(mp, "/srv") || strings.HasPrefix(mp, "/home") ||
			strings.HasPrefix(mp, "/data") {
			return true
		}
		return false
	}
}

func hasOpt(opts []string, o string) bool {
	for _, x := range opts {
		if x == o {
			return true
		}
	}
	return false
}

func fsCapabilities(fstype string) []string {
	caps := map[string][]string{
		"apfs":  {"xattr", "acl", "sparse", "hardlink", "symlink", "reflink", "birthtime", "case-insensitive"},
		"hfs":   {"xattr", "acl", "hardlink", "symlink", "birthtime", "case-insensitive"},
		"ntfs":  {"acl", "sparse", "hardlink", "symlink", "birthtime", "case-insensitive", "max-path:32767"},
		"refs":  {"acl", "sparse", "reflink", "birthtime", "case-insensitive", "max-path:32767"},
		"exfat": {"case-insensitive", "birthtime"},
		"msdos": {"case-insensitive", "max-file:4294967295"},
		"vfat":  {"case-insensitive", "max-file:4294967295"},
		"fat32": {"case-insensitive", "max-file:4294967295"},
		"ext4":  {"xattr", "acl", "sparse", "hardlink", "symlink"},
		"xfs":   {"xattr", "acl", "sparse", "hardlink", "symlink", "reflink"},
		"btrfs": {"xattr", "acl", "sparse", "hardlink", "symlink", "reflink"},
		"zfs":   {"xattr", "acl", "sparse", "hardlink", "symlink"},
	}
	return caps[strings.ToLower(fstype)]
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
