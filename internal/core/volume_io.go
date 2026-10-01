package core

import (
	"context"

	"gopkg.in/yaml.v3"
)

// VolumeBundle is the portable volume configuration (FR-V22): disk identity + order + policy.
type VolumeBundle struct {
	Schema          int          `yaml:"schema" json:"schema"`
	ControllerID    string       `yaml:"controller_id" json:"controller_id"`
	Name            string       `yaml:"name" json:"name"`
	MountPath       string       `yaml:"mount_path,omitempty" json:"mount_path,omitempty"`
	PlacementPolicy string       `yaml:"placement_policy" json:"placement_policy"`
	MinFreeBytes    int64        `yaml:"min_free_bytes" json:"min_free_bytes"`
	AllowSpillover  bool         `yaml:"allow_spillover" json:"allow_spillover"`
	Disks           []BundleDisk `yaml:"disks" json:"disks"`
}

type BundleDisk struct {
	Ordinal        int    `yaml:"ordinal" json:"ordinal"`
	Name           string `yaml:"name" json:"name"`
	FilesystemUUID string `yaml:"filesystem_uuid" json:"filesystem_uuid"`
	ProviderType   string `yaml:"provider_type" json:"provider_type"`
	Node           string `yaml:"node" json:"node"`
	MountPath      string `yaml:"mount_path" json:"mount_path"`
	Role           string `yaml:"role" json:"role"`
	MinFreeBytes   *int64 `yaml:"min_free_bytes,omitempty" json:"min_free_bytes,omitempty"`
}

// ExportVolume renders the bundle as YAML.
func (c *Core) ExportVolume(ctx context.Context, ref string) ([]byte, error) {
	v, err := c.GetVolume(ctx, ref)
	if err != nil {
		return nil, err
	}
	b := VolumeBundle{Schema: 1, ControllerID: c.ControllerID, Name: v.Name, MountPath: v.MountPath,
		PlacementPolicy: v.PlacementPolicy, MinFreeBytes: v.MinFreeBytes, AllowSpillover: v.AllowSpillover}
	for _, d := range v.Disks {
		b.Disks = append(b.Disks, BundleDisk{Ordinal: d.Ordinal, Name: d.Name, FilesystemUUID: d.FilesystemUUID,
			ProviderType: d.ProviderType, Node: d.NodeName, MountPath: d.MountPath, Role: d.Role, MinFreeBytes: d.MinFreeBytes})
	}
	return yaml.Marshal(b)
}

// ImportVolume recreates a volume. Each disk is matched by filesystem identity among registered
// disks; unmatched disks are attached from their recorded mount path.
func (c *Core) ImportVolume(ctx context.Context, data []byte) (Volume, error) {
	var b VolumeBundle
	if err := yaml.Unmarshal(data, &b); err != nil {
		return Volume{}, Errf(CodeInvalid, "invalid bundle: %v", err)
	}
	if b.Name == "" || len(b.Disks) == 0 {
		return Volume{}, Errf(CodeInvalid, "bundle needs name and disks")
	}
	reg, err := c.ListDisks(ctx, DiskFilter{})
	if err != nil {
		return Volume{}, err
	}
	var ids []string
	for _, bd := range b.Disks {
		id := ""
		for _, d := range reg {
			if d.FilesystemUUID == bd.FilesystemUUID && d.ProviderType == bd.ProviderType {
				id = d.ID
			}
		}
		if id == "" {
			res, err := c.Attach(ctx, AttachRequest{Path: bd.MountPath, Name: bd.Name, Role: bd.Role, MinFreeBytes: bd.MinFreeBytes, Adopt: true})
			if err != nil {
				return Volume{}, Errf(CodeNotFound, "disk %s (%s) not available: %v", bd.Name, bd.MountPath, err)
			}
			id = res.Disk.ID
		}
		ids = append(ids, id)
	}
	spill := b.AllowSpillover
	minFree := b.MinFreeBytes
	return c.CreateVolume(ctx, CreateVolumeRequest{Name: b.Name, MountPath: b.MountPath, PlacementPolicy: b.PlacementPolicy,
		MinFreeBytes: &minFree, AllowSpillover: &spill, DiskIDs: ids})
}
