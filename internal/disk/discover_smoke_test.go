package disk

import (
	"context"
	"testing"
)

func TestDiscoverSmoke(t *testing.T) {
	ds, err := Discover(context.Background(), "nod_test", Stat)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) == 0 {
		t.Fatal("no volumes discovered (expected at least /)")
	}
	for _, d := range ds {
		t.Logf("%s fs=%s uuid=%s label=%q bus=%s rm=%v net=%v total=%d avail=%d", d.MountPath, d.FilesystemType, d.FilesystemUUID, d.VolumeLabel, d.BusType, d.Removable, d.NetworkBacked, d.TotalBytes, d.AvailableBytes)
		if d.FilesystemUUID == "" {
			t.Errorf("%s: empty identity", d.MountPath)
		}
	}
}
