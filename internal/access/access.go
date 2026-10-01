// Package access runs the SMB access point (FR-A01~A05, A08, A09): one Samba + mergerfs container
// that exposes every volume as one share (branches in ordinal order) plus one share per disk.
//
// Writes through the volume share follow mergerfs category.create=ff over the auto-place disks in
// ordinal order (≈ fill-in-order, file granularity, not tree-aware) and are cataloged by SCAN.
// Exact ordinal placement stays an app/CLI feature (FR-A05, documented).
package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"continuum/internal/audit"
	"continuum/internal/core"
)

const (
	keyEnabled  = "access.enabled"
	keyPort     = "access.port"
	keyBind     = "access.bind"
	keyPassword = "access.smb_password"
	User        = "continuum"
)

// Manager owns the access point container.
type Manager struct {
	C         *core.Core
	Dir       string // rendered config (bind-mounted read-only at /config)
	Image     string
	Container string
	// Docker runs the docker CLI (replaceable in tests).
	Docker func(ctx context.Context, args ...string) ([]byte, error)

	mu       sync.Mutex
	lastHash string
}

// New returns a manager using the docker CLI.
func New(c *core.Core, dir string) *Manager {
	return &Manager{C: c, Dir: dir, Image: "continuum-samba:local", Container: "continuum-smb", Docker: dockerCLI}
}

func dockerCLI(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("docker not found — install Docker to use the SMB access point")
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Share is one exported SMB share.
type Share struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // volume | disk
	Volume   string   `json:"volume"`
	Disk     string   `json:"disk,omitempty"`
	Ordinal  int      `json:"ordinal,omitempty"`
	ReadOnly bool     `json:"read_only"`
	URL      string   `json:"url"`
	Branches []string `json:"branches,omitempty"` // volume share: disk=RW|NC in ordinal order
}

// Status is the access point state (FR-A08).
type Status struct {
	Enabled      bool    `json:"enabled"`
	Running      bool    `json:"running"`
	ImagePresent bool    `json:"image_present"`
	Image        string  `json:"image"`
	Bind         string  `json:"bind"`
	Port         int     `json:"port"`
	User         string  `json:"user"`
	Password     string  `json:"password,omitempty"` // only returned by Credentials
	Clients      int     `json:"clients"`
	Shares       []Share `json:"shares"`
	Error        string  `json:"error,omitempty"`
}

// Config is the rendered container configuration (pure; unit-tested).
type Config struct {
	SmbConf string
	Start   string
	Mounts  []string // host:container bind mounts (data roots)
	Shares  []Share
}

// Render builds smb.conf + start.sh for the volumes (only local-node disks with a reachable data root).
func Render(vols []core.Volume, localNode string, bind string, port int, exists func(string) bool) Config {
	var cfg Config
	host := bind
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}
	url := func(name string) string {
		if port == 445 {
			return fmt.Sprintf("smb://%s/%s", host, name)
		}
		return fmt.Sprintf("smb://%s:%d/%s", host, port, name)
	}
	var conf strings.Builder
	conf.WriteString(`[global]
   server role = standalone server
   server min protocol = SMB2_10
   security = user
   map to guest = never
   passdb backend = tdbsam
   load printers = no
   printing = bsd
   printcap name = /dev/null
   disable spoolss = yes
   disable netbios = yes
   smb ports = 445
   log level = 1
   vfs objects = catia fruit streams_xattr
   fruit:metadata = stream
   fruit:model = MacSamba
   fruit:posix_rename = yes
   fruit:veto_appledouble = no
   fruit:wipe_intentionally_left_blank_rfork = yes
   fruit:delete_empty_adfiles = yes
   ea support = yes
   store dos attributes = yes
   veto files = /.trash/volume.json/*.continuum-partial-*/.probe-*/
   delete veto files = no
`)
	var start strings.Builder
	start.WriteString(`#!/bin/sh
# rendered by the Continuum Controller — do not edit
set -e
PW=$(cat /config/secret)
# the SMB user writes as the owner of the data roots (Docker Desktop shows bind mounts as root;
# native Linux shows the host user), so files on the disks keep their normal owner
REF=$(ls -d /disks/* 2>/dev/null | head -1)
OUID=$(stat -c %u "$REF" 2>/dev/null || echo "${PUID:-1000}")
OGID=$(stat -c %g "$REF" 2>/dev/null || echo "${PGID:-1000}")
# backends without xattrs (Docker Desktop virtiofs, exFAT/FAT disks): drop fruit/streams so macOS
# falls back to AppleDouble "._" files exactly like on exFAT (SCAN ignores them) instead of failing writes
MFX=""
if python3 -c 'import os,sys; os.setxattr(sys.argv[1], "user.continuum", b"1"); os.removexattr(sys.argv[1], "user.continuum")' "$REF" 2>/dev/null; then
  cp /config/smb.conf /etc/samba/smb.conf
else
  MFX=",xattr=nosys"
  sed -e 's/vfs objects = catia fruit streams_xattr/vfs objects = catia/' -e 's/ea support = yes/ea support = no/' -e 's/store dos attributes = yes/store dos attributes = no/' \
    /config/smb.conf > /etc/samba/smb.conf
  echo "continuum: data roots have no xattr support — Finder metadata stored as AppleDouble files"
fi
getent group ` + User + ` >/dev/null || groupadd -o -g "$OGID" ` + User + `
id ` + User + ` >/dev/null 2>&1 || useradd -o -M -s /usr/sbin/nologin -u "$OUID" -g ` + User + ` ` + User + `
printf '%s\n%s\n' "$PW" "$PW" | smbpasswd -s -a ` + User + ` >/dev/null
mkdir -p /pool
`)
	mounted := map[string]bool{}
	for _, v := range vols {
		if v.AccessMode == "blocked" {
			continue
		}
		ro := v.AccessMode != "full-direct" // read-centric (default): read-only shares, writes through the app
		disks := append([]core.Disk(nil), v.Disks...)
		sort.Slice(disks, func(i, j int) bool { return disks[i].Ordinal < disks[j].Ordinal })
		var branches []string
		var branchLabels []string
		for _, d := range disks {
			if d.NodeID != localNode || !exists(d.DataRoot) {
				continue
			}
			switch d.State {
			case core.StateOnline, core.StateDraining, core.StateReadOnly:
			default:
				continue
			}
			cp := "/disks/" + d.ID
			if !mounted[d.ID] {
				cfg.Mounts = append(cfg.Mounts, d.DataRoot+":"+cp)
				mounted[d.ID] = true
			}
			mode := "NC" // existing files stay writable in place; no new files here
			if d.State == core.StateOnline && d.Writable && core.AutoPlace(d.Role) {
				mode = "RW"
			}
			if d.State == core.StateReadOnly || !d.Writable {
				mode = "RO"
			}
			branches = append(branches, cp+"="+mode)
			branchLabels = append(branchLabels, fmt.Sprintf("%d:%s=%s", d.Ordinal, d.Name, mode))
			name := fmt.Sprintf("%s-%d", v.Slug, d.Ordinal)
			cfg.Shares = append(cfg.Shares, Share{Name: name, Kind: "disk", Volume: v.Slug, Disk: d.Name, Ordinal: d.Ordinal, ReadOnly: ro || mode == "RO", URL: url(name)})
			writeShare(&conf, name, cp, ro || mode == "RO", "disk "+d.Name+" (ordinal "+strconv.Itoa(d.Ordinal)+") of "+v.Name)
		}
		if len(branches) == 0 {
			continue
		}
		pool := "/pool/" + v.Slug
		minFree := v.MinFreeBytes
		if minFree <= 0 {
			minFree = 1 << 20
		}
		fmt.Fprintf(&start, "mkdir -p %s\nmergerfs -o allow_other,use_ino,cache.files=off,dropcacheonclose=true,category.create=ff,minfreespace=%d,fsname=%s$MFX %s %s\n",
			pool, minFree, v.Slug, strings.Join(branches, ":"), pool)
		cfg.Shares = append(cfg.Shares, Share{Name: v.Slug, Kind: "volume", Volume: v.Slug, ReadOnly: ro, URL: url(v.Slug), Branches: branchLabels})
		writeShare(&conf, v.Slug, pool, ro, "Continuum volume "+v.Name)
	}
	start.WriteString("exec smbd --foreground --no-process-group --debug-stdout\n")
	cfg.SmbConf, cfg.Start = conf.String(), start.String()
	sort.SliceStable(cfg.Shares, func(i, j int) bool { // volume share first, then its disks
		if cfg.Shares[i].Volume != cfg.Shares[j].Volume {
			return cfg.Shares[i].Volume < cfg.Shares[j].Volume
		}
		return cfg.Shares[i].Kind == "volume" && cfg.Shares[j].Kind != "volume"
	})
	return cfg
}

func writeShare(b *strings.Builder, name, path string, ro bool, comment string) {
	fmt.Fprintf(b, "\n[%s]\n   comment = %s\n   path = %s\n   read only = %s\n   valid users = %s\n   force user = %s\n   force group = %s\n   browseable = yes\n",
		name, comment, path, map[bool]string{true: "yes", false: "no"}[ro], User, User, User)
}

func (m *Manager) settings(ctx context.Context) (enabled bool, bind string, port int) {
	enabled = m.C.Setting(ctx, keyEnabled, "false") == "true"
	bind = m.C.Setting(ctx, keyBind, "127.0.0.1")
	port, _ = strconv.Atoi(m.C.Setting(ctx, keyPort, "4445"))
	if port <= 0 {
		port = 4445
	}
	return
}

func randomPassword() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

func (m *Manager) render(ctx context.Context) (Config, string, error) {
	vols, err := m.C.ListVolumes(ctx)
	if err != nil {
		return Config{}, "", err
	}
	_, bind, port := m.settings(ctx)
	cfg := Render(vols, m.C.LocalNode.ID, bind, port, func(p string) bool { _, err := os.Stat(p); return err == nil })
	h := sha256.New()
	fmt.Fprint(h, cfg.SmbConf, cfg.Start, strings.Join(cfg.Mounts, "|"), bind, port, m.Image)
	return cfg, hex.EncodeToString(h.Sum(nil)), nil
}

// Enable stores the setting and (re)creates the container.
func (m *Manager) Enable(ctx context.Context, bind string, port int) (*Status, error) {
	if bind != "" {
		if bind != "127.0.0.1" && bind != "0.0.0.0" && !strings.Contains(bind, ".") && !strings.Contains(bind, ":") {
			return nil, core.Errf(core.CodeInvalid, "bind must be an IP address")
		}
		m.C.SetSetting(ctx, keyBind, bind)
	}
	if port != 0 {
		if port < 1 || port > 65535 {
			return nil, core.Errf(core.CodeInvalid, "port must be 1-65535")
		}
		m.C.SetSetting(ctx, keyPort, strconv.Itoa(port))
	}
	m.C.SetSetting(ctx, keyEnabled, "true")
	if err := m.apply(ctx, true); err != nil {
		return m.Status(ctx), err
	}
	m.C.Audit.Append(ctx, audit.Entry{Action: "access.enable", Target: m.Container, Result: "ok"})
	return m.Status(ctx), nil
}

// Disable removes the container (data on the disks is untouched).
func (m *Manager) Disable(ctx context.Context) (*Status, error) {
	m.C.SetSetting(ctx, keyEnabled, "false")
	m.mu.Lock()
	m.Docker(ctx, "rm", "-f", m.Container)
	m.lastHash = ""
	m.mu.Unlock()
	m.C.Audit.Append(ctx, audit.Entry{Action: "access.disable", Target: m.Container, Result: "ok"})
	return m.Status(ctx), nil
}

// Reload re-renders and recreates the container (FR-D11 "접근점 reload").
func (m *Manager) Reload(ctx context.Context) (*Status, error) {
	enabled, _, _ := m.settings(ctx)
	if !enabled {
		return nil, core.Errf(core.CodeInvalid, "access point is disabled")
	}
	return m.Status(ctx), m.apply(ctx, true)
}

// Sync recreates the container when the rendered config changed (volume/disk/order/state edits).
func (m *Manager) Sync(ctx context.Context) error {
	if enabled, _, _ := m.settings(ctx); !enabled {
		return nil
	}
	return m.apply(ctx, false)
}

func (m *Manager) apply(ctx context.Context, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, hash, err := m.render(ctx)
	if err != nil {
		return err
	}
	if !force && hash == m.lastHash && m.running(ctx) {
		return nil
	}
	if _, err := m.Docker(ctx, "image", "inspect", m.Image); err != nil {
		return core.Errf(core.CodeInvalid, "image %s missing — run `make local-build` (docker build -t %s deploy/samba)", m.Image, m.Image)
	}
	pw, err := m.C.EnsureSetting(ctx, keyPassword, randomPassword)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	for name, body := range map[string]string{"smb.conf": cfg.SmbConf, "start.sh": cfg.Start, "secret": pw} {
		if err := os.WriteFile(filepath.Join(m.Dir, name), []byte(body), 0o600); err != nil {
			return err
		}
	}
	_, bind, port := m.settings(ctx)
	m.Docker(ctx, "rm", "-f", m.Container)
	args := []string{"run", "-d", "--name", m.Container, "--restart", "unless-stopped",
		"--device", "/dev/fuse", "--cap-add", "SYS_ADMIN", "--security-opt", "apparmor:unconfined",
		"-p", fmt.Sprintf("%s:%d:445", bind, port),
		"-e", "PUID=" + strconv.Itoa(os.Getuid()), "-e", "PGID=" + strconv.Itoa(os.Getgid()),
		"-v", m.Dir + ":/config:ro",
		"--label", "continuum.controller=" + m.C.ControllerID}
	for _, mt := range cfg.Mounts {
		args = append(args, "-v", mt)
	}
	args = append(args, m.Image)
	if _, err := m.Docker(ctx, args...); err != nil {
		m.lastHash = ""
		m.C.Audit.Emit(ctx, audit.Event{Kind: "access.failed"}, map[string]any{"error": err.Error()})
		return core.Errf(core.CodeOffline, "%v", err)
	}
	m.lastHash = hash
	m.C.Audit.Emit(ctx, audit.Event{Kind: "access.reloaded"}, map[string]any{"shares": len(cfg.Shares), "port": port})
	return nil
}

func (m *Manager) running(ctx context.Context) bool {
	out, err := m.Docker(ctx, "inspect", "-f", "{{.State.Running}}", m.Container)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Status reports configuration, container state and connected clients.
func (m *Manager) Status(ctx context.Context) *Status {
	enabled, bind, port := m.settings(ctx)
	s := &Status{Enabled: enabled, Bind: bind, Port: port, User: User, Image: m.Image, Shares: []Share{}}
	if cfg, _, err := m.render(ctx); err == nil {
		s.Shares = cfg.Shares
	}
	if _, err := m.Docker(ctx, "image", "inspect", m.Image); err == nil {
		s.ImagePresent = true
	} else if strings.Contains(err.Error(), "docker not found") {
		s.Error = err.Error()
		return s
	}
	s.Running = m.running(ctx)
	if s.Running {
		if out, err := m.Docker(ctx, "exec", m.Container, "smbstatus", "-b", "--json"); err == nil {
			var st struct {
				Sessions map[string]any `json:"sessions"`
			}
			if json.Unmarshal(out, &st) == nil {
				s.Clients = len(st.Sessions)
			}
		}
	} else if enabled {
		if out, err := m.Docker(ctx, "logs", "--tail", "5", m.Container); err == nil {
			s.Error = "container not running: " + strings.TrimSpace(string(out))
		}
	}
	return s
}

// Credentials returns the status including the SMB password (shown on explicit request only).
func (m *Manager) Credentials(ctx context.Context) (*Status, error) {
	pw, err := m.C.EnsureSetting(ctx, keyPassword, randomPassword)
	if err != nil {
		return nil, err
	}
	s := m.Status(ctx)
	s.Password = pw
	return s, nil
}
