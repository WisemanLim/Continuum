package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"runtime"

	"github.com/shirou/gopsutil/v4/host"

	"continuum/internal/audit"
	"continuum/internal/disk"
	"continuum/internal/secrets"
	"continuum/internal/store"
	"continuum/internal/version"
)

// Core wires the registry services around one database.
type Core struct {
	DB           *store.DB
	Audit        *audit.Log
	Stat         disk.StatFunc
	ControllerID string
	LocalNode    Node
	// Remote reaches disks on agent nodes (nil = single-machine mode, FR-N07).
	Remote Remote
	// Secrets keeps credentials out of the database (FR-S06); nil = settings table.
	Secrets secrets.Store
}

// secretKeys are settings stored in the OS keychain / secret file instead of the database.
var secretKeys = map[string]bool{"access.smb_password": true, "notify.webhook_url": true, "oidc.client_secret": true, "notify.smtp_password": true, "ldap.bind_password": true}

// IsSecret reports whether a setting key is kept in the secret store.
func IsSecret(key string) bool { return secretKeys[key] }

// secret reads a secret, moving a legacy plaintext value out of the settings table on first use.
func (c *Core) secret(ctx context.Context, key string) (string, bool) {
	if v, err := c.Secrets.Get(key); err == nil {
		return v, true
	}
	var v string
	if err := c.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err == nil {
		if c.Secrets.Set(key, v) == nil {
			c.DB.Exec(ctx, `DELETE FROM settings WHERE key = ?`, key)
		}
		return v, true
	}
	return "", false
}

// Remote is implemented by the agent hub (P3).
type Remote interface {
	Probe(ctx context.Context, node, path string, minFree uint64, controllerID, volumeID string) (disk.Report, string, disk.Descriptor, error)
	WriteMarker(ctx context.Context, node, dataRoot string, m disk.Marker) error
	Capacity(node, mountPath, dataRoot string) (c disk.Capacity, exists, known bool)
	Discover(ctx context.Context, node string) ([]disk.Descriptor, error)
	RefreshWatch(ctx context.Context, node string)
}

// IsRemote reports whether a disk lives on an agent node.
func (c *Core) IsRemote(d Disk) bool { return d.NodeID != "" && d.NodeID != c.LocalNode.ID }

// SetNodeDisksOffline marks every disk of a node OFFLINE (node lost, FR-N05).
func (c *Core) SetNodeDisksOffline(ctx context.Context, node string) {
	ds, err := c.ListDisks(ctx, DiskFilter{})
	if err != nil {
		return
	}
	for _, d := range ds {
		if d.NodeID == node && d.State != StateOffline && d.State != StateDisabled {
			c.DB.Exec(ctx, `UPDATE disks SET state=?, updated_at=? WHERE disk_id=?`, StateOffline, store.Now(), d.ID)
			c.event(ctx, "disk.offline", d.VolumeID, d.ID, "", map[string]any{"path": d.MountPath, "node": node})
		}
	}
}

// Open migrates the DB and ensures controller id + local node (M1 embedded agent).
func Open(ctx context.Context, db *store.DB, stat disk.StatFunc) (*Core, error) {
	if _, err := db.Migrate(ctx); err != nil {
		return nil, err
	}
	if stat == nil {
		stat = disk.Stat
	}
	c := &Core{DB: db, Audit: audit.New(db), Stat: stat}
	var err error
	if c.ControllerID, err = c.setting(ctx, "controller_id", func() string { return store.NewID("ctl") }); err != nil {
		return nil, err
	}
	nodeID, err := c.setting(ctx, "local_node_id", func() string { return store.NewID("nod") })
	if err != nil {
		return nil, err
	}
	if err := c.upsertLocalNode(ctx, nodeID); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Core) setting(ctx context.Context, key string, gen func() string) (string, error) {
	var v string
	err := c.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	v = gen()
	_, err = c.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, v)
	return v, err
}

func (c *Core) upsertLocalNode(ctx context.Context, id string) error {
	hostname, _ := os.Hostname()
	osVer := ""
	if hi, err := host.InfoWithContext(ctx); err == nil {
		osVer = hi.Platform + " " + hi.PlatformVersion
	}
	now := store.Now()
	var n int
	if err := c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM nodes WHERE node_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	var err error
	if n == 0 {
		_, err = c.DB.Exec(ctx, `INSERT INTO nodes (node_id, hostname, os, os_version, agent_version, state, last_heartbeat, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, id, hostname, runtime.GOOS, osVer, version.Version, "ONLINE", now, now, now)
	} else {
		_, err = c.DB.Exec(ctx, `UPDATE nodes SET hostname=?, os=?, os_version=?, agent_version=?, state='ONLINE', last_heartbeat=?, updated_at=? WHERE node_id=?`,
			hostname, runtime.GOOS, osVer, version.Version, now, now, id)
	}
	if err != nil {
		return err
	}
	c.LocalNode = Node{ID: id, Hostname: hostname, OS: runtime.GOOS, OSVersion: osVer, AgentVersion: version.Version, State: "ONLINE", LastHeartbeat: now}
	return nil
}

// Heartbeat refreshes the local node timestamp.
func (c *Core) Heartbeat(ctx context.Context) {
	now := store.Now()
	_, _ = c.DB.Exec(ctx, `UPDATE nodes SET last_heartbeat=?, updated_at=? WHERE node_id=?`, now, now, c.LocalNode.ID)
}

// Nodes lists nodes with their disk counts.
func (c *Core) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := c.DB.Query(ctx, `SELECT n.node_id, n.hostname, n.os, COALESCE(n.os_version,''), COALESCE(n.agent_version,''), n.state,
		COALESCE(n.last_heartbeat,''), n.created_at, (SELECT COUNT(*) FROM disks d WHERE d.node_id = n.node_id),
		COALESCE(n.arch,''), COALESCE(n.cert_not_after,''), COALESCE(n.protocol,0)
		FROM nodes n ORDER BY n.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Hostname, &n.OS, &n.OSVersion, &n.AgentVersion, &n.State, &n.LastHeartbeat, &n.CreatedAt, &n.DiskCount,
			&n.Arch, &n.CertNotAfter, &n.Protocol); err != nil {
			return nil, err
		}
		n.Remote = n.ID != c.LocalNode.ID
		out = append(out, n)
	}
	return out, rows.Err()
}

func (c *Core) audit(ctx context.Context, action, target, volumeID, result string) {
	_, _ = c.Audit.Append(ctx, audit.Entry{Action: action, Target: target, NodeID: c.LocalNode.ID, VolumeID: volumeID, Result: result})
}

func (c *Core) event(ctx context.Context, kind, volumeID, diskID, jobID string, payload any) {
	_ = c.Audit.Emit(ctx, audit.Event{Kind: kind, NodeID: c.LocalNode.ID, VolumeID: volumeID, DiskID: diskID, JobID: jobID}, payload)
}

// Setting returns a stored setting or def.
func (c *Core) Setting(ctx context.Context, key, def string) string {
	if c.Secrets != nil && secretKeys[key] {
		if v, ok := c.secret(ctx, key); ok {
			return v
		}
		return def
	}
	var v string
	if err := c.DB.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting stores (upserts) a setting.
func (c *Core) SetSetting(ctx context.Context, key, value string) error {
	if c.Secrets != nil && secretKeys[key] {
		c.DB.Exec(ctx, `DELETE FROM settings WHERE key = ?`, key)
		if value == "" {
			return c.Secrets.Delete(key)
		}
		return c.Secrets.Set(key, value)
	}
	res, err := c.DB.Exec(ctx, `UPDATE settings SET value = ? WHERE key = ?`, value, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = c.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

// EnsureSetting returns key, generating and storing it on first use.
func (c *Core) EnsureSetting(ctx context.Context, key string, gen func() string) (string, error) {
	if c.Secrets != nil && secretKeys[key] {
		if v, ok := c.secret(ctx, key); ok {
			return v, nil
		}
		v := gen()
		return v, c.Secrets.Set(key, v)
	}
	return c.setting(ctx, key, gen)
}
