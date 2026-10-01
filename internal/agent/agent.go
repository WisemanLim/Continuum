// Package agent is the node agent (FR-N01~N06): enrollment, the mTLS connect-go stream to the Controller,
// heartbeats with statfs of watched paths, inventory, and command execution on this host's filesystem.
package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/shirou/gopsutil/v4/host"

	"continuum/internal/agentproto"
	"continuum/internal/disk"
	"continuum/internal/nodefs"
	"continuum/internal/pki"
	"continuum/internal/tracing"
	"continuum/internal/version"
)

// Config is the agent's persistent state (<dir>/agent.json + cert.pem, key.pem 0600, ca.pem).
type Config struct {
	NodeID     string   `json:"node_id"`
	Controller string   `json:"controller"` // https://host:8443
	Roots      []string `json:"roots,omitempty"`
	dir        string
}

// Load reads the agent config from dir.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err != nil {
		return nil, fmt.Errorf("agent not enrolled (%s): %w", dir, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.dir = dir
	return &c, nil
}

func (c *Config) save() error {
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(c.dir, "agent.json"), b, 0o600)
}

// Enroll exchanges a one-time token for a client certificate (FR-N01). The Controller certificate must chain
// to the CA whose fingerprint is embedded in the token.
func Enroll(ctx context.Context, dir, controller, token string) (*Config, error) {
	p := strings.Split(strings.TrimSpace(token), ".")
	if len(p) != 3 || p[0] != "CTM1" || len(p[2]) != 64 {
		return nil, errors.New("malformed enrollment token")
	}
	pin := p[2]
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // verified below against the pinned CA
		VerifyConnection: func(cs tls.ConnectionState) error {
			for _, c := range cs.PeerCertificates {
				if pki.Fingerprint(c.Raw) == pin && c.IsCA {
					pool := x509.NewCertPool()
					pool.AddCert(c)
					_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: pool})
					return err
				}
			}
			return errors.New("controller certificate does not match the CA in the token")
		}}
	keyPEM, pubPEM, err := pki.NewKey()
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: token, PublicKey: string(pubPEM), Hostname: hostname, OS: runtime.GOOS, Version: version.Version})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controller, "/")+agentproto.EnrollPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("enrollment refused: %s", strings.TrimSpace(string(b)))
	}
	var er agentproto.EnrollResponse
	if err := json.NewDecoder(res.Body).Decode(&er); err != nil {
		return nil, err
	}
	if cb, _ := pem.Decode([]byte(er.CAPEM)); cb == nil || pki.Fingerprint(cb.Bytes) != pin {
		return nil, errors.New("CA returned by the controller does not match the token")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for name, data := range map[string][]byte{"key.pem": keyPEM, "cert.pem": []byte(er.CertPEM), "ca.pem": []byte(er.CAPEM)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	c := &Config{NodeID: er.NodeID, Controller: strings.TrimRight(controller, "/"), dir: dir}
	return c, c.save()
}

// Agent runs the stream.
type Agent struct {
	Cfg   *Config
	FS    nodefs.FS
	Roots []string
	// PeerURL is advertised for direct agent ↔ agent transfers (set by PeerListen).
	PeerURL string

	cmds   cmdRegistry
	mu     sync.Mutex
	watch  []string
	client *http.Client
	sendMu sync.Mutex
}

// New creates an agent from a loaded config.
func New(cfg *Config) *Agent { return &Agent{Cfg: cfg, FS: nodefs.Local{}, Roots: cfg.Roots} }

func (a *Agent) tlsConfig() (*tls.Config, *x509.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(a.Cfg.dir, "cert.pem"), filepath.Join(a.Cfg.dir, "key.pem"))
	if err != nil {
		return nil, nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(a.Cfg.dir, "ca.pem"))
	if err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: pool, NextProtos: []string{"h2"}}, leaf, nil
}

func (a *Agent) httpClient() (*http.Client, *x509.Certificate, error) {
	tc, leaf, err := a.tlsConfig()
	if err != nil {
		return nil, nil, err
	}
	tr := &http.Transport{TLSClientConfig: tc, ForceAttemptHTTP2: true, IdleConnTimeout: 90 * time.Second}
	tr.Protocols = new(http.Protocols)
	tr.Protocols.SetHTTP2(true)
	return &http.Client{Transport: tr}, leaf, nil
}

// Run connects and reconnects with exponential backoff until ctx ends (FR-N03).
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // was healthy: reconnect quickly
		}
		log.Printf("agent: disconnected: %v — retry in %s", err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (a *Agent) session(ctx context.Context) error {
	client, leaf, err := a.httpClient()
	if err != nil {
		return err
	}
	if time.Until(leaf.NotAfter) < pki.ClientCertTTL/3 { // FR-N09: rotate before expiry
		if err := a.renew(ctx, client); err != nil {
			log.Printf("agent: certificate renewal failed: %v", err)
		} else if client, _, err = a.httpClient(); err != nil {
			return err
		}
	}
	a.client = client
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cc := connect.NewClient[agentproto.AgentMsg, agentproto.ControllerMsg](client, a.Cfg.Controller+agentproto.ConnectProcedure, connect.WithCodec(agentproto.Codec{}))
	st := cc.CallBidiStream(sctx)
	defer st.CloseResponse()
	defer st.CloseRequest()
	hostname, _ := os.Hostname()
	osVer := ""
	if hi, err := host.InfoWithContext(ctx); err == nil {
		osVer = hi.Platform + " " + hi.PlatformVersion
	}
	if err := a.send(st, &agentproto.AgentMsg{Hello: &agentproto.Hello{NodeID: a.Cfg.NodeID, Hostname: hostname, OS: runtime.GOOS, OSVersion: osVer, Arch: runtime.GOARCH,
		Version: version.Version, Protocol: agentproto.Protocol, Capabilities: a.capabilities(), PeerURL: a.PeerURL}}); err != nil {
		return err
	}
	w, err := st.Receive()
	if err != nil {
		return err
	}
	if w.Welcome == nil {
		return errors.New("expected welcome")
	}
	a.setWatch(w.Welcome.Watch)
	log.Printf("agent: connected to %s as %s (protocol %d)", a.Cfg.Controller, a.Cfg.NodeID, w.Welcome.Protocol)
	a.beat(st)
	go a.sendInventory(sctx, st)
	go func() {
		t := time.NewTicker(time.Duration(max(w.Welcome.HeartbeatSec, 1)) * time.Second)
		inv := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		defer inv.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				if a.beat(st) != nil {
					cancel()
					return
				}
			case <-inv.C:
				a.sendInventory(sctx, st)
			}
		}
	}()
	for {
		m, err := st.Receive()
		if err != nil {
			return err
		}
		switch {
		case m.Welcome != nil:
			a.setWatch(m.Welcome.Watch)
			a.beat(st)
		case m.Command != nil:
			c := *m.Command
			if c.Op == agentproto.OpCancel { // answered inline: it must not queue behind the command it stops
				var ca agentproto.CancelArgs
				json.Unmarshal(c.Args, &ca)
				a.cmds.cancel(ca.ID)
				a.send(st, &agentproto.AgentMsg{Result: &agentproto.Result{ID: c.ID}})
				continue
			}
			cctx, ccancel := context.WithCancel(sctx)
			a.cmds.add(c.ID, ccancel)
			id := c.ID
			cctx = agentproto.WithProgress(cctx, func(b int64) { // long commands report while they run
				a.send(st, &agentproto.AgentMsg{Progress: &agentproto.Progress{ID: id, Bytes: b}})
			})
			go func() {
				defer ccancel()
				defer a.cmds.done(c.ID)
				tctx, span := tracing.StartRemote(cctx, c.TraceParent, "agent "+c.Op, tracing.KindServer)
				span.Set("continuum.node.id", a.Cfg.NodeID)
				span.Set("continuum.agent.op", c.Op)
				res := a.exec(tctx, c)
				if res.Error != "" {
					span.Fail(res.Error)
				}
				span.End()
				a.send(st, &agentproto.AgentMsg{Result: res})
			}()
		}
	}
}

type sender interface {
	Send(*agentproto.AgentMsg) error
}

func (a *Agent) send(st sender, m *agentproto.AgentMsg) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	return st.Send(m)
}

func (a *Agent) setWatch(w []string) {
	a.mu.Lock()
	a.watch = w
	a.mu.Unlock()
}

func (a *Agent) beat(st sender) error {
	a.mu.Lock()
	watch := append([]string(nil), a.watch...)
	a.mu.Unlock()
	hb := &agentproto.Heartbeat{TS: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, p := range watch {
		ps := agentproto.PathStat{Path: p}
		if _, err := os.Stat(p); err == nil {
			ps.Exists = true
			ps.Cap, _ = disk.Stat(p)
		}
		hb.Paths = append(hb.Paths, ps)
	}
	return a.send(st, &agentproto.AgentMsg{Heartbeat: hb})
}

func (a *Agent) sendInventory(ctx context.Context, st sender) {
	// roots are known now; volume discovery can take seconds (sleeping disks), so send it as a second update
	a.send(st, &agentproto.AgentMsg{Inventory: &agentproto.Inventory{Roots: a.Roots}})
	ds, _ := disk.Discover(ctx, a.Cfg.NodeID, disk.Stat)
	a.send(st, &agentproto.AgentMsg{Inventory: &agentproto.Inventory{Disks: ds, Roots: a.Roots}})
}

func (a *Agent) renew(ctx context.Context, client *http.Client) error {
	keyPEM, pubPEM, err := pki.NewKey()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(agentproto.RenewRequest{PublicKey: string(pubPEM)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.Controller+agentproto.RenewPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("renew refused: %s", strings.TrimSpace(string(b)))
	}
	var er agentproto.EnrollResponse
	if err := json.NewDecoder(res.Body).Decode(&er); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.Cfg.dir, "key.pem"), keyPEM, 0o600); err != nil {
		return err
	}
	log.Printf("agent: certificate rotated (valid until %s)", er.NotAfter)
	return os.WriteFile(filepath.Join(a.Cfg.dir, "cert.pem"), []byte(er.CertPEM), 0o600)
}

func (a *Agent) capabilities() []string {
	caps := []string{"fs", "put", "get", "probe", "smart", "bench", "wipe", "pull", "cancel"}
	if a.PeerURL != "" {
		caps = append(caps, "peer")
	}
	return caps
}

// exec runs one command with the local filesystem.
func (a *Agent) exec(ctx context.Context, c agentproto.Command) *agentproto.Result {
	res := &agentproto.Result{ID: c.ID}
	out, err := a.do(ctx, c)
	if err != nil {
		res.Error, res.Kind = err.Error(), nodefs.Kind(err)
		return res
	}
	if out != nil {
		res.Data, _ = json.Marshal(out)
	}
	return res
}

func (a *Agent) do(ctx context.Context, c agentproto.Command) (any, error) {
	var p agentproto.PathArgs
	decode := func(v any) error { return json.Unmarshal(c.Args, v) }
	fs := a.FS
	switch c.Op {
	case agentproto.OpStat, agentproto.OpReadDir, agentproto.OpWalk, agentproto.OpMkdirAll, agentproto.OpRemove, agentproto.OpRemoveAll,
		agentproto.OpHash, agentproto.OpPartial, agentproto.OpStatFS, agentproto.OpReadFile, agentproto.OpSmart:
		if err := decode(&p); err != nil {
			return nil, err
		}
	}
	switch c.Op {
	case agentproto.OpStat:
		return fs.Stat(ctx, p.Path)
	case agentproto.OpReadDir:
		return fs.ReadDir(ctx, p.Path)
	case agentproto.OpWalk:
		return fs.Walk(ctx, p.Path)
	case agentproto.OpMkdirAll:
		return nil, fs.MkdirAll(ctx, p.Path)
	case agentproto.OpRemove:
		return nil, fs.Remove(ctx, p.Path)
	case agentproto.OpRemoveAll:
		return nil, fs.RemoveAll(ctx, p.Path)
	case agentproto.OpHash:
		s, err := fs.Hash(ctx, p.Path)
		return agentproto.HashResult{Sum: s}, err
	case agentproto.OpPartial:
		s, err := fs.PartialHash(ctx, p.Path)
		return agentproto.HashResult{Sum: s}, err
	case agentproto.OpStatFS:
		return fs.StatFS(ctx, p.Path)
	case agentproto.OpReadFile:
		return fs.ReadFile(ctx, p.Path)
	case agentproto.OpRename, agentproto.OpSymlink:
		var r agentproto.RenameArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		if c.Op == agentproto.OpRename {
			return nil, fs.Rename(ctx, r.From, r.To)
		}
		return nil, fs.Symlink(ctx, r.From, r.To)
	case agentproto.OpChtimes:
		var r agentproto.ChtimesArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return nil, fs.Chtimes(ctx, r.Path, r.MtimeNs)
	case agentproto.OpWriteFile:
		var r agentproto.WriteFileArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return nil, fs.WriteFile(ctx, r.Path, r.Data, fsMode(r.Mode))
	case agentproto.OpPut:
		var r agentproto.PutArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		body, err := a.blobGet(ctx, r.BlobID)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return fs.Put(ctx, r, body)
	case agentproto.OpGet:
		var r agentproto.GetArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return nil, a.blobPut(ctx, r.BlobID, r.Path)
	case agentproto.OpProbe:
		var r agentproto.ProbeArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return Probe(ctx, a.Cfg.NodeID, r), nil
	case agentproto.OpDiscover:
		return disk.Discover(ctx, a.Cfg.NodeID, disk.Stat)
	case agentproto.OpSmart:
		return disk.ReadSmart(ctx, p.Path), nil
	case agentproto.OpBench:
		var r agentproto.BenchArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return disk.RunBench(ctx, r.Path, r.Size)
	case agentproto.OpPull:
		var r agentproto.PullArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		return a.pull(ctx, r)
	case agentproto.OpWipe:
		var r agentproto.WipeArgs
		if err := decode(&r); err != nil {
			return nil, err
		}
		files, bytes, err := disk.Wipe(ctx, r.Path, r.Passes)
		return agentproto.WipeResult{Files: files, Bytes: bytes}, err
	}
	return nil, fmt.Errorf("unsupported op %q", c.Op)
}

func fsMode(m uint32) fs.FileMode {
	if m == 0 {
		return 0o644
	}
	return fs.FileMode(m)
}

// Probe = preflight (10 checks) + identity, run on this host (FR-D04 for remote disks).
func Probe(ctx context.Context, nodeID string, r agentproto.ProbeArgs) agentproto.ProbeResult {
	mount, fstype := disk.MountOf(r.Path)
	rep := disk.Preflight(disk.PreflightInput{Path: r.Path, MinFreeBytes: r.MinFreeBytes, ControllerID: r.ControllerID, VolumeID: r.VolumeID,
		NetworkBacked: disk.IsNetworkFS(fstype), Stat: disk.Stat})
	var meta disk.Descriptor
	if ds, err := disk.Discover(ctx, nodeID, disk.Stat); err == nil {
		for _, d := range ds {
			if d.MountPath == mount {
				meta = d
			}
		}
	}
	identity := disk.PathIdentity(r.Path)
	if mount == r.Path && meta.FilesystemUUID != "" {
		identity = meta.FilesystemUUID
	}
	return agentproto.ProbeResult{Report: rep, Identity: identity, Meta: meta}
}

func (a *Agent) blobGet(ctx context.Context, id string) (io.ReadCloser, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.Cfg.Controller+agentproto.BlobPath+id, nil)
	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, fmt.Errorf("blob %s: %s", id, res.Status)
	}
	return res.Body, nil
}

func (a *Agent) blobPut(ctx context.Context, id, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, a.Cfg.Controller+agentproto.BlobPath+id, f)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("blob upload %s: %s", id, res.Status)
	}
	return nil
}

// CertExpiry returns the NotAfter of the enrolled certificate.
func CertExpiry(dir string) (time.Time, error) {
	b, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return time.Time{}, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return time.Time{}, errors.New("bad cert.pem")
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

// RenewNow rotates the certificate immediately (tests, `continuum-agent run` does it near expiry).
func (a *Agent) RenewNow(ctx context.Context) error {
	client, _, err := a.httpClient()
	if err != nil {
		return err
	}
	return a.renew(ctx, client)
}
