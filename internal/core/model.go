// Package core holds the Continuum domain model and registry services:
// nodes, disks (FR-D), volumes (FR-V) and the catalog (FR-K).
package core

import "strings"

// Disk states (FR-D06, D09).
const (
	StateOnline   = "ONLINE"
	StateDraining = "DRAINING"
	StateReadOnly = "READ_ONLY"
	StateOffline  = "OFFLINE"
	StateFailed   = "FAILED"
	StateDisabled = "DISABLED"
)

// Roles (FR-D07, D34).
const (
	RoleAlwaysOn      = "always-on"
	RoleSemiPermanent = "semi-permanent"
	RolePortable      = "portable"
	RoleImport        = "import"
	RoleArchive       = "archive"
)

var validRoles = map[string]bool{RoleAlwaysOn: true, RoleSemiPermanent: true, RolePortable: true, RoleImport: true, RoleArchive: true}

// AutoPlace reports whether a role takes part in automatic placement (PRD §10.4).
func AutoPlace(role string) bool { return role == RoleAlwaysOn || role == RoleSemiPermanent }

// Placement policies (FR-V15, FR-P05).
const (
	PolicyFillInOrder    = "fill-in-order"
	PolicyTopdirAffinity = "topdir-affinity"
	PolicyWholeTree      = "whole-tree-only"
	PolicyMostFree       = "most-free"
	PolicyBalanced       = "balanced"
)

var validPolicies = map[string]bool{PolicyFillInOrder: true, PolicyTopdirAffinity: true, PolicyWholeTree: true, PolicyMostFree: true, PolicyBalanced: true}

// ValidPolicy reports whether p is a known placement policy.
func ValidPolicy(p string) bool { return validPolicies[p] }

type Node struct {
	ID            string `json:"node_id"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version,omitempty"`
	AgentVersion  string `json:"agent_version,omitempty"`
	State         string `json:"state"`
	LastHeartbeat string `json:"last_heartbeat,omitempty"`
	DiskCount     int    `json:"disk_count"`
	CreatedAt     string `json:"created_at"`
	Remote        bool   `json:"remote"`
	Arch          string `json:"arch,omitempty"`
	CertNotAfter  string `json:"cert_not_after,omitempty"`
	Protocol      int    `json:"protocol,omitempty"`
}

type Disk struct {
	ID             string   `json:"disk_id"`
	VolumeID       string   `json:"volume_id,omitempty"`
	NodeID         string   `json:"node_id"`
	NodeName       string   `json:"node_name,omitempty"`
	Name           string   `json:"name"`
	ProviderType   string   `json:"provider_type"`
	FilesystemUUID string   `json:"filesystem_uuid"`
	NativeDiskID   string   `json:"native_disk_id,omitempty"`
	VolumeLabel    string   `json:"volume_label,omitempty"`
	FilesystemType string   `json:"filesystem_type,omitempty"`
	MountPath      string   `json:"mount_path"`
	DataRoot       string   `json:"data_root"`
	Ordinal        int      `json:"ordinal,omitempty"`
	Role           string   `json:"role"`
	State          string   `json:"state"`
	Writable       bool     `json:"writable"`
	TotalBytes     int64    `json:"total_bytes"`
	FreeBytes      int64    `json:"free_bytes"`
	AvailableBytes int64    `json:"available_bytes"`
	ReservedBytes  int64    `json:"reserved_bytes"`
	MinFreeBytes   *int64   `json:"min_free_bytes,omitempty"`
	BusType        string   `json:"bus_type,omitempty"`
	Removable      bool     `json:"removable"`
	NetworkBacked  bool     `json:"network_backed"`
	Health         string   `json:"health"`
	Capabilities   []string `json:"capabilities,omitempty"`
	LastSeen       string   `json:"last_seen,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`

	// Derived
	EffectiveFree    int64  `json:"effective_free_bytes"`
	EffectiveMinFree int64  `json:"effective_min_free_bytes"`
	UsedPct          int    `json:"used_pct"`
	Warning          string `json:"warning,omitempty"` // capacity threshold (85/95%) or portable
}

type Volume struct {
	ID                 string `json:"volume_id"`
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	MountPath          string `json:"mount_path,omitempty"`
	PlacementPolicy    string `json:"placement_policy"`
	MinFreeBytes       int64  `json:"min_free_bytes"`
	AllowSpillover     bool   `json:"allow_spillover"`
	AccessMode         string `json:"access_mode"`
	TrashRetentionDays int    `json:"trash_retention_days"`
	ScanIntervalMin    int    `json:"scan_interval_min"`
	OrphanPolicy       string `json:"orphan_policy"`
	LastScanAt         string `json:"last_scan_at,omitempty"`
	Revision           int64  `json:"revision"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`

	Disks    []Disk   `json:"disks"`
	Summary  Summary  `json:"summary"`
	Warnings []string `json:"warnings,omitempty"`
}

// Summary is the "N disks · usable · free" line (FR-V10):
// usable = Σ(total − min_free), free = Σ(effective_free).
type Summary struct {
	DiskCount   int   `json:"disk_count"`
	OnlineCount int   `json:"online_count"`
	TotalBytes  int64 `json:"total_bytes"`
	UsableBytes int64 `json:"usable_bytes"`
	FreeBytes   int64 `json:"free_bytes"`
	ObjectCount int64 `json:"object_count"`
	StoredBytes int64 `json:"stored_bytes"`
}

// derive fills computed disk fields given the volume default min_free.
func (d *Disk) derive(volMinFree int64) {
	d.EffectiveMinFree = volMinFree
	if d.MinFreeBytes != nil {
		d.EffectiveMinFree = *d.MinFreeBytes
	}
	d.EffectiveFree = d.AvailableBytes - d.ReservedBytes
	if d.EffectiveFree < 0 {
		d.EffectiveFree = 0
	}
	if d.TotalBytes > 0 {
		d.UsedPct = int(100 - (d.FreeBytes*100)/d.TotalBytes)
	}
	switch {
	case d.UsedPct >= 95:
		d.Warning = "capacity-critical"
	case d.UsedPct >= 85:
		d.Warning = "capacity-high"
	case d.VolumeID != "" && !AutoPlace(d.Role) && (d.Role == RolePortable || d.Role == RoleImport):
		d.Warning = "portable-in-volume"
	}
}

// Summarize computes FR-V10 over the member disks.
func Summarize(disks []Disk) Summary {
	var s Summary
	for _, d := range disks {
		s.DiskCount++
		if d.State == StateOnline {
			s.OnlineCount++
		}
		s.TotalBytes += d.TotalBytes
		u := d.TotalBytes - d.EffectiveMinFree
		if u > 0 {
			s.UsableBytes += u
		}
		s.FreeBytes += d.EffectiveFree
	}
	return s
}

// Slugify makes a filesystem-safe slug (FR-V13).
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
