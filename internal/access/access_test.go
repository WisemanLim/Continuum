package access

import (
	"strings"
	"testing"

	"continuum/internal/core"
)

func vol(slug, mode string, disks ...core.Disk) core.Volume {
	return core.Volume{Slug: slug, Name: slug, AccessMode: mode, MinFreeBytes: 1 << 30, Disks: disks}
}

func dk(id string, ord int, role, state string) core.Disk {
	return core.Disk{ID: id, Name: id, NodeID: "n1", Ordinal: ord, Role: role, State: state, Writable: true, DataRoot: "/data/" + id}
}

func TestRender(t *testing.T) {
	all := func(string) bool { return true }
	vols := []core.Volume{
		vol("lab", "full-direct", dk("d2", 2, core.RoleAlwaysOn, core.StateOnline), dk("d1", 1, core.RoleAlwaysOn, core.StateOnline),
			dk("p3", 3, core.RolePortable, core.StateOnline), dk("x4", 4, core.RoleAlwaysOn, core.StateOffline)),
		vol("arch", "read-centric", dk("a1", 1, core.RoleArchive, core.StateOnline)),
		vol("hidden", "blocked", dk("h1", 1, core.RoleAlwaysOn, core.StateOnline)),
	}
	vols[1].Disks[0].Writable = false
	cfg := Render(vols, "n1", "127.0.0.1", 4445, all)

	if !strings.Contains(cfg.Start, "/disks/d1=RW:/disks/d2=RW:/disks/p3=NC /pool/lab") {
		t.Fatalf("lab branches (ordinal order, portable = no create, offline skipped):\n%s", cfg.Start)
	}
	if !strings.Contains(cfg.Start, "category.create=ff,minfreespace=1073741824") {
		t.Fatal("create policy / minfreespace")
	}
	if strings.Contains(cfg.SmbConf, "[hidden") || strings.Contains(cfg.Start, "h1") {
		t.Fatal("blocked volume must not be exported")
	}
	if len(cfg.Mounts) != 4 || cfg.Mounts[0] != "/data/d1:/disks/d1" {
		t.Fatalf("mounts %v", cfg.Mounts)
	}
	want := map[string]bool{"lab": false, "lab-1": false, "lab-2": false, "lab-3": false, "arch": true, "arch-1": true}
	if len(cfg.Shares) != len(want) {
		t.Fatalf("shares %+v", cfg.Shares)
	}
	for _, s := range cfg.Shares {
		ro, ok := want[s.Name]
		if !ok || s.ReadOnly != ro {
			t.Fatalf("share %s ro=%v", s.Name, s.ReadOnly)
		}
	}
	if cfg.Shares[0].Name != "arch" || cfg.Shares[2].URL != "smb://127.0.0.1:4445/lab" {
		t.Fatalf("order/url: %+v", cfg.Shares)
	}
	for _, s := range []string{"server min protocol = SMB2_10", "vfs objects = catia fruit streams_xattr", "veto files = /.trash/volume.json/", "[lab]\n   comment = Continuum volume lab\n   path = /pool/lab\n   read only = no"} {
		if !strings.Contains(cfg.SmbConf, s) {
			t.Fatalf("smb.conf missing %q", s)
		}
	}
	// unreachable data root → skipped
	cfg = Render(vols[:1], "n1", "0.0.0.0", 445, func(p string) bool { return p != "/data/d1" })
	if strings.Contains(cfg.Start, "/disks/d1") || cfg.Shares[0].URL != "smb://localhost/lab" {
		t.Fatalf("missing root: %s %+v", cfg.Start, cfg.Shares)
	}
}

func TestLocalMountAndAutoconnect(t *testing.T) {
	v := vol("lab", "full-direct", dk("d1", 1, core.RoleAlwaysOn, core.StateOnline), dk("p2", 2, core.RolePortable, core.StateOnline), dk("x3", 3, core.RoleAlwaysOn, core.StateOnline))
	v.Disks[2].NodeID = "other"
	m, err := RenderLocalMount(v, "n1", "/mnt/continuum/lab")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Fstab, "/data/d1=RW:/data/p2=NC  /mnt/continuum/lab  fuse.mergerfs") || strings.Contains(m.Fstab, "x3") {
		t.Fatalf("fstab: %s", m.Fstab)
	}
	if m.UnitName != "mnt-continuum-lab.mount" || !strings.Contains(m.SystemdUnit, "Where=/mnt/continuum/lab") {
		t.Fatalf("unit %s:\n%s", m.UnitName, m.SystemdUnit)
	}
	if _, err := RenderLocalMount(v, "nobody", "/x"); err == nil {
		t.Fatal("no local disk must error")
	}
	p := RenderAutoconnect("com.continuum.smb.lab", "smb://continuum@nas:4445/lab")
	for _, want := range []string{`mount volume "smb://continuum@nas:4445/lab"`, "<key>StartInterval</key><integer>300</integer>", "SystemConfiguration"} {
		if !strings.Contains(p, want) {
			t.Fatalf("plist missing %q:\n%s", want, p)
		}
	}
}
