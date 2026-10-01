// Package hub is the Controller side of distributed agents (FR-N01~N06, N09, S03, S07): enrollment with
// one-time tokens, the mTLS listener, the connect-go bidirectional stream, request/response commands,
// blob relay for file bytes, heartbeat-based offline detection and a nodefs.FS for every remote node.
package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	"continuum/internal/agentproto"
	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/nodefs"
	"continuum/internal/pki"
	"continuum/internal/store"
	"continuum/internal/tracing"
)

// ErrNodeOffline: the node has no live session (callers treat it like an unplugged disk).
var ErrNodeOffline = errors.New("node offline")

// Hub manages agent sessions.
type Hub struct {
	C            *core.Core
	CA           *pki.CA
	PublicURL    string        // agent endpoint shown in the enrollment command, e.g. https://host:8443
	CallTimeout  time.Duration // small commands (default 60 s)
	HeartbeatSec int           // agents beat every N s (default 10); 3 missed → offline
	// OnNodeOnline is called after a node reconnects and its disks were refreshed (auto-resume, FR-N05).
	OnNodeOnline func(nodeID string)

	mu       sync.Mutex
	sessions map[string]*session
	blobs    map[string]*blob
	stats    map[string]map[string]agentproto.PathStat
	inv      map[string]agentproto.Inventory
}

type session struct {
	node    string
	hello   agentproto.Hello
	out     chan *agentproto.ControllerMsg
	mu      sync.Mutex
	pending map[string]chan *agentproto.Result
	prog    map[string]func(int64) // command id → progress callback (agentproto.WithProgress)
	done    chan struct{}
	seen    time.Time
}

type blob struct {
	node string
	r    io.Reader      // agent GET (download)
	w    *io.PipeWriter // agent PUT (upload)
	got  chan struct{}  // closed when the agent fetched / finished uploading
}

// New creates a hub.
func New(c *core.Core, ca *pki.CA, publicURL string) *Hub {
	return &Hub{C: c, CA: ca, PublicURL: publicURL, CallTimeout: 60 * time.Second, HeartbeatSec: agentproto.HeartbeatSeconds,
		sessions: map[string]*session{}, blobs: map[string]*blob{}, stats: map[string]map[string]agentproto.PathStat{}, inv: map[string]agentproto.Inventory{}}
}

type ctxKey struct{}

// Handler serves the agent endpoint (mount it on a TLS listener using CA.ServerTLS).
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+agentproto.EnrollPath, h.enroll)
	mux.Handle("POST "+agentproto.RenewPath, h.auth(http.HandlerFunc(h.renew)))
	mux.Handle(agentproto.BlobPath, h.auth(http.HandlerFunc(h.blobHTTP)))
	mux.Handle(agentproto.ConnectProcedure, h.auth(connect.NewBidiStreamHandler(agentproto.ConnectProcedure, h.connect, connect.WithCodec(agentproto.Codec{}))))
	return mux
}

// auth requires a client certificate issued to a registered, non-revoked node with the current serial.
func (h *Hub) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node, serial, err := pki.PeerNode(r.TLS)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var cur, revoked string
		if err := h.C.DB.QueryRow(r.Context(), `SELECT COALESCE(cert_serial,''), COALESCE(revoked_at,'') FROM nodes WHERE node_id=?`, node).Scan(&cur, &revoked); err != nil {
			http.Error(w, "unknown node", http.StatusUnauthorized)
			return
		}
		if revoked != "" || cur != serial {
			http.Error(w, "certificate revoked or rotated", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, node)))
	})
}

func nodeOf(ctx context.Context) string { s, _ := ctx.Value(ctxKey{}).(string); return s }

// ---- enrollment (FR-N01) ----

// Token is a created enrollment token.
type Token struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	Command   string `json:"command"`
	CA        string `json:"ca_sha256"`
}

// CreateToken issues a one-time token "CTM1.<secret>.<ca sha256>" (the CA fingerprint lets the agent pin
// the Controller certificate before trusting anything it says).
func (h *Hub) CreateToken(ctx context.Context, label, by string, ttl time.Duration) (*Token, error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		ttl = time.Hour
	}
	b := make([]byte, 24)
	rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(secret))
	exp := time.Now().UTC().Add(ttl).Format(time.RFC3339Nano)
	if _, err := h.C.DB.Exec(ctx, `INSERT INTO enroll_tokens (token_hash, label, created_by, created_at, expires_at) VALUES (?,?,?,?,?)`,
		hex.EncodeToString(sum[:]), label, by, store.Now(), exp); err != nil {
		return nil, err
	}
	tok := "CTM1." + secret + "." + h.CA.Fingerprint()
	h.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "node.token", Target: label, Result: "expires " + exp})
	return &Token{Token: tok, ExpiresAt: exp, CA: h.CA.Fingerprint(),
		Command: fmt.Sprintf("continuum-agent enroll --controller %s --token %s && continuum-agent run", h.PublicURL, tok)}, nil
}

// ParseToken splits a token into secret and CA fingerprint.
func ParseToken(tok string) (secret, caFP string, err error) {
	p := strings.Split(strings.TrimSpace(tok), ".")
	if len(p) != 3 || p[0] != "CTM1" || len(p[2]) != 64 {
		return "", "", errors.New("malformed enrollment token")
	}
	return p[1], p[2], nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (h *Hub) enroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req agentproto.EnrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	secret, _, err := ParseToken(req.Token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(secret))
	th := hex.EncodeToString(sum[:])
	nodeID := store.NewID("nod")
	// single use: the UPDATE only succeeds once and only before expiry
	res, err := h.C.DB.Exec(ctx, `UPDATE enroll_tokens SET used_at=?, node_id=? WHERE token_hash=? AND used_at IS NULL AND expires_at > ?`,
		store.Now(), nodeID, th, store.Now())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.C.Audit.Append(ctx, audit.Entry{Actor: "agent", Action: "node.enroll", Target: req.Hostname, Result: "rejected: invalid, used or expired token"})
		http.Error(w, "invalid, used or expired token", http.StatusUnauthorized)
		return
	}
	certPEM, serial, notAfter, err := h.CA.IssueClient(nodeID, []byte(req.PublicKey))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := store.Now()
	if _, err := h.C.DB.Exec(ctx, `INSERT INTO nodes (node_id, hostname, os, agent_version, state, cert_serial, cert_not_after, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, nodeID, req.Hostname, req.OS, req.Version, "OFFLINE", serial, notAfter.UTC().Format(time.RFC3339), now, now); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	h.C.Audit.Append(ctx, audit.Entry{Actor: "agent", Action: "node.enroll", Target: nodeID, NodeID: nodeID, Result: "ok " + req.Hostname})
	h.C.Audit.Emit(ctx, audit.Event{Kind: "node.enrolled", NodeID: nodeID}, map[string]any{"hostname": req.Hostname, "os": req.OS})
	writeJSON(w, 200, agentproto.EnrollResponse{NodeID: nodeID, CertPEM: string(certPEM), CAPEM: string(h.CA.CertPEM), NotAfter: notAfter.UTC().Format(time.RFC3339)})
}

// renew rotates the client certificate (FR-N09); the old serial stops working immediately.
func (h *Hub) renew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	node := nodeOf(ctx)
	var req agentproto.RenewRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	certPEM, serial, notAfter, err := h.CA.IssueClient(node, []byte(req.PublicKey))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.C.DB.Exec(ctx, `UPDATE nodes SET cert_serial=?, cert_not_after=?, updated_at=? WHERE node_id=?`, serial, notAfter.UTC().Format(time.RFC3339), store.Now(), node)
	h.C.Audit.Append(ctx, audit.Entry{Actor: "agent", Action: "node.cert_rotate", Target: node, NodeID: node, Result: "ok"})
	writeJSON(w, 200, agentproto.EnrollResponse{NodeID: node, CertPEM: string(certPEM), CAPEM: string(h.CA.CertPEM), NotAfter: notAfter.UTC().Format(time.RFC3339)})
}

// Revoke disables a node: its certificate stops working, the session closes, its disks go OFFLINE.
func (h *Hub) Revoke(ctx context.Context, nodeID, by string) error {
	if nodeID == h.C.LocalNode.ID {
		return core.Errf(core.CodeInvalid, "the local node cannot be revoked")
	}
	res, err := h.C.DB.Exec(ctx, `UPDATE nodes SET revoked_at=?, cert_serial=NULL, state='REVOKED', updated_at=? WHERE node_id=?`, store.Now(), store.Now(), nodeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return core.Errf(core.CodeNotFound, "node %q not found", nodeID)
	}
	h.drop(nodeID)
	h.C.SetNodeDisksOffline(ctx, nodeID)
	h.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "node.revoke", Target: nodeID, NodeID: nodeID, Result: "ok"})
	return nil
}

func (h *Hub) drop(nodeID string) {
	h.mu.Lock()
	s := h.sessions[nodeID]
	delete(h.sessions, nodeID)
	h.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
	default:
		close(s.done)
		for _, ch := range s.pending {
			close(ch)
		}
		s.pending = map[string]chan *agentproto.Result{}
	}
}

// ---- stream (FR-N02, N03, N05, N06) ----

func (h *Hub) connect(ctx context.Context, st *connect.BidiStream[agentproto.AgentMsg, agentproto.ControllerMsg]) error {
	node := nodeOf(ctx)
	first, err := st.Receive()
	if err != nil {
		return err
	}
	if first.Hello == nil || first.Hello.NodeID != node {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first message must be hello for this certificate's node"))
	}
	hello := *first.Hello
	if hello.Protocol < agentproto.MinProtocol {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("agent protocol %d too old (min %d) — upgrade the agent", hello.Protocol, agentproto.MinProtocol))
	}
	proto := min(hello.Protocol, agentproto.Protocol)
	caps, _ := json.Marshal(hello.Capabilities)
	now := store.Now()
	h.C.DB.Exec(ctx, `UPDATE nodes SET hostname=?, os=?, os_version=?, arch=?, agent_version=?, capabilities_json=?, protocol=?, state='ONLINE', last_heartbeat=?, updated_at=? WHERE node_id=?`,
		hello.Hostname, hello.OS, hello.OSVersion, hello.Arch, hello.Version, string(caps), proto, now, now, node)

	s := &session{node: node, hello: hello, out: make(chan *agentproto.ControllerMsg, 64), pending: map[string]chan *agentproto.Result{}, prog: map[string]func(int64){}, done: make(chan struct{}), seen: time.Now()}
	h.mu.Lock()
	old := h.sessions[node]
	h.sessions[node] = s
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		h.mu.Lock()
		if h.sessions[node] == s {
			delete(h.sessions, node)
		}
		h.mu.Unlock()
		s.close()
	}()
	h.C.Audit.Emit(ctx, audit.Event{Kind: "node.online", NodeID: node}, map[string]any{"hostname": hello.Hostname, "version": hello.Version, "protocol": proto})
	if err := st.Send(&agentproto.ControllerMsg{Welcome: &agentproto.Welcome{NodeID: node, Protocol: proto, HeartbeatSec: h.HeartbeatSec, Watch: h.watchList(ctx, node)}}); err != nil {
		return err
	}

	sendErr := make(chan error, 1)
	go func() { // single writer: connect streams are not safe for concurrent Send
		for {
			select {
			case m := <-s.out:
				if err := st.Send(m); err != nil {
					sendErr <- err
					return
				}
			case <-s.done:
				sendErr <- nil
				return
			case <-ctx.Done():
				sendErr <- ctx.Err()
				return
			}
		}
	}()
	recvErr := make(chan error, 1)
	firstBeat := true
	go func() {
		for {
			m, err := st.Receive()
			if err != nil {
				recvErr <- err
				return
			}
			s.mu.Lock()
			s.seen = time.Now()
			s.mu.Unlock()
			switch {
			case m.Heartbeat != nil:
				h.heartbeat(ctx, node, m.Heartbeat)
				if firstBeat && h.OnNodeOnline != nil {
					firstBeat = false
					go h.OnNodeOnline(node)
				}
			case m.Inventory != nil:
				h.mu.Lock()
				h.inv[node] = *m.Inventory
				h.mu.Unlock()
			case m.Progress != nil:
				s.mu.Lock()
				fn := s.prog[m.Progress.ID]
				s.mu.Unlock()
				if fn != nil {
					fn(m.Progress.Bytes)
				}
			case m.Result != nil:
				s.mu.Lock()
				ch := s.pending[m.Result.ID]
				delete(s.pending, m.Result.ID)
				delete(s.prog, m.Result.ID)
				s.mu.Unlock()
				if ch != nil {
					ch <- m.Result
				}
			}
		}
	}()
	select {
	case err := <-recvErr:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case err := <-sendErr:
		return err
	}
}

// watchList: mount paths and data roots of the node's disks (reported in every heartbeat).
func (h *Hub) watchList(ctx context.Context, node string) []string {
	out := []string{}
	ds, err := h.C.ListDisks(ctx, core.DiskFilter{})
	if err != nil {
		return out
	}
	for _, d := range ds {
		if d.NodeID == node {
			out = append(out, d.MountPath, d.DataRoot)
		}
	}
	return out
}

// RefreshWatch pushes an updated watch list (after attach/detach).
func (h *Hub) RefreshWatch(ctx context.Context, node string) {
	h.mu.Lock()
	s := h.sessions[node]
	h.mu.Unlock()
	if s == nil {
		return
	}
	select {
	case s.out <- &agentproto.ControllerMsg{Welcome: &agentproto.Welcome{NodeID: node, Protocol: agentproto.Protocol, HeartbeatSec: h.HeartbeatSec, Watch: h.watchList(ctx, node)}}:
	case <-s.done:
	}
}

func (h *Hub) heartbeat(ctx context.Context, node string, hb *agentproto.Heartbeat) {
	h.mu.Lock()
	m := map[string]agentproto.PathStat{}
	for _, p := range hb.Paths {
		m[p.Path] = p
	}
	h.stats[node] = m
	h.mu.Unlock()
	now := store.Now()
	h.C.DB.Exec(ctx, `UPDATE nodes SET state='ONLINE', last_heartbeat=?, updated_at=? WHERE node_id=?`, now, now, node)
	if ds, err := h.C.ListDisks(ctx, core.DiskFilter{}); err == nil {
		for _, d := range ds {
			if d.NodeID == node {
				h.C.RefreshCapacity(ctx, d.ID)
			}
		}
	}
}

// Run watches heartbeats: 3 missed → NODE_OFFLINE and its disks OFFLINE (FR-N05).
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	limit := time.Duration(h.HeartbeatSec*agentproto.MissedBeats) * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := h.C.DB.Query(ctx, `SELECT node_id, COALESCE(last_heartbeat,'') FROM nodes WHERE state='ONLINE' AND node_id<>?`, h.C.LocalNode.ID)
		if err != nil {
			continue
		}
		var stale []string
		for rows.Next() {
			var id, ts string
			rows.Scan(&id, &ts)
			last, err := time.Parse(time.RFC3339Nano, ts)
			h.mu.Lock()
			s := h.sessions[id]
			h.mu.Unlock()
			if s != nil {
				s.mu.Lock()
				if s.seen.After(last) {
					last = s.seen
				}
				s.mu.Unlock()
			}
			if err != nil || time.Since(last) > limit {
				stale = append(stale, id)
			}
		}
		rows.Close()
		for _, id := range stale {
			h.drop(id)
			h.C.DB.Exec(ctx, `UPDATE nodes SET state='OFFLINE', updated_at=? WHERE node_id=? AND state='ONLINE'`, store.Now(), id)
			h.mu.Lock()
			delete(h.stats, id)
			h.mu.Unlock()
			h.C.SetNodeDisksOffline(ctx, id)
			h.C.Audit.Emit(ctx, audit.Event{Kind: "node.offline", NodeID: id}, map[string]any{"missed_heartbeats": agentproto.MissedBeats})
			log.Printf("hub: node %s offline (no heartbeat for %s)", id, limit)
		}
	}
}

// Online reports whether the node has a live session.
func (h *Hub) Online(node string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[node] != nil
}

// Sessions lists connected node ids.
func (h *Hub) Sessions() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		out = append(out, id)
	}
	return out
}

// Inventory returns the last inventory reported by a node.
func (h *Hub) Inventory(node string) (agentproto.Inventory, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	inv, ok := h.inv[node]
	return inv, ok
}

// Call runs one command on a node and decodes the result into out (may be nil).
func (h *Hub) Call(ctx context.Context, node, op string, args, out any) (err error) {
	h.mu.Lock()
	s := h.sessions[node]
	h.mu.Unlock()
	if s == nil {
		return fmt.Errorf("%s: %w", node, ErrNodeOffline)
	}
	raw, _ := json.Marshal(args)
	id := store.NewID("cmd")
	ch := make(chan *agentproto.Result, 1)
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", node, ErrNodeOffline)
	default:
	}
	s.pending[id] = ch
	if fn := agentproto.ProgressFrom(ctx); fn != nil {
		s.prog[id] = fn
	}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		delete(s.pending, id)
		delete(s.prog, id)
		s.mu.Unlock()
	}
	ctx, span := tracing.Start(ctx, "agent "+op, tracing.KindClient) // the agent continues this trace
	span.Set("continuum.node.id", node)
	span.Set("continuum.agent.op", op)
	defer func() {
		if err != nil {
			span.Fail(err.Error())
		}
		span.End()
	}()
	select {
	case s.out <- &agentproto.ControllerMsg{Command: &agentproto.Command{ID: id, Op: op, Args: raw, TraceParent: span.TraceParent()}}:
	case <-s.done:
		return fmt.Errorf("%s: %w", node, ErrNodeOffline)
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
	var timeout <-chan time.Time
	if op != agentproto.OpPut && op != agentproto.OpGet && op != agentproto.OpPull && op != agentproto.OpBench && op != agentproto.OpWipe && op != agentproto.OpWalk && op != agentproto.OpHash {
		timer := time.NewTimer(h.CallTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: %w", node, ErrNodeOffline)
		}
		if res.Error != "" {
			return remoteErr(res)
		}
		if out != nil && len(res.Data) > 0 {
			return json.Unmarshal(res.Data, out)
		}
		return nil
	case <-timeout:
		cancel()
		return fmt.Errorf("%s %s: timed out: %w", node, op, ErrNodeOffline)
	case <-ctx.Done():
		cancel()
		h.stopRemote(s, id)
		return ctx.Err()
	}
}

// stopRemote asks the agent to abandon a command the caller gave up on (pause / cancel of a long pull);
// best effort: an old agent ignores it and the command finishes on its own.
func (h *Hub) stopRemote(s *session, id string) {
	if !slices.Contains(s.hello.Capabilities, "cancel") {
		return
	}
	raw, _ := json.Marshal(agentproto.CancelArgs{ID: id})
	select {
	case s.out <- &agentproto.ControllerMsg{Command: &agentproto.Command{ID: store.NewID("cmd"), Op: agentproto.OpCancel, Args: raw}}:
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

// Pull makes dstNode fetch src from srcNode directly (D36): same node → local copy on that agent; different
// nodes → mTLS agent ↔ agent with a CA-signed ticket. Resumes from the partial's length.
func (h *Hub) Pull(ctx context.Context, srcNode, src, dstNode string, a agentproto.PullArgs) (agentproto.PullResult, error) {
	var res agentproto.PullResult
	h.mu.Lock()
	ss, ds := h.sessions[srcNode], h.sessions[dstNode]
	h.mu.Unlock()
	if ds == nil {
		return res, fmt.Errorf("%s: %w", dstNode, ErrNodeOffline)
	}
	if !slices.Contains(ds.hello.Capabilities, "pull") {
		return res, nodefs.ErrNoDirect
	}
	a.Source = src
	if srcNode != dstNode {
		if ss == nil {
			return res, fmt.Errorf("%s: %w", srcNode, ErrNodeOffline)
		}
		if ss.hello.PeerURL == "" {
			return res, nodefs.ErrNoDirect
		}
		t, err := h.CA.SignTicket(srcNode, dstNode, src, 15*time.Minute)
		if err != nil {
			return res, err
		}
		a.Peer, a.PeerNode, a.Ticket = ss.hello.PeerURL, srcNode, t
	}
	err := h.Call(ctx, dstNode, agentproto.OpPull, a, &res)
	return res, err
}

// ---- blobs ----

func (h *Hub) newBlob(node string, r io.Reader, w *io.PipeWriter) string {
	id := store.NewID("blb")
	h.mu.Lock()
	h.blobs[id] = &blob{node: node, r: r, w: w, got: make(chan struct{})}
	h.mu.Unlock()
	return id
}

func (h *Hub) dropBlob(id string) {
	h.mu.Lock()
	delete(h.blobs, id)
	h.mu.Unlock()
}

func (h *Hub) blobHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, agentproto.BlobPath)
	h.mu.Lock()
	b := h.blobs[id]
	if b != nil {
		delete(h.blobs, id) // one use
	}
	h.mu.Unlock()
	if b == nil || b.node != nodeOf(r.Context()) {
		http.Error(w, "unknown blob", http.StatusNotFound)
		return
	}
	defer close(b.got)
	switch r.Method {
	case http.MethodGet:
		if b.r == nil {
			http.Error(w, "blob is upload-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.CopyBuffer(w, b.r, make([]byte, 1<<20))
	case http.MethodPut:
		if b.w == nil {
			http.Error(w, "blob is download-only", http.StatusMethodNotAllowed)
			return
		}
		_, err := io.CopyBuffer(b.w, r.Body, make([]byte, 1<<20))
		b.w.CloseWithError(err)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

// ---- core.Remote (attach / capacity / marker for remote disks) ----

// Probe runs the 10 preflight checks and identity detection on the node.
func (h *Hub) Probe(ctx context.Context, node, path string, minFree uint64, controllerID, volumeID string) (disk.Report, string, disk.Descriptor, error) {
	var res agentproto.ProbeResult
	err := h.Call(ctx, node, agentproto.OpProbe, agentproto.ProbeArgs{Path: path, MinFreeBytes: minFree, ControllerID: controllerID, VolumeID: volumeID}, &res)
	return res.Report, res.Identity, res.Meta, err
}

// WriteMarker writes <data_root>/volume.json on the node.
func (h *Hub) WriteMarker(ctx context.Context, node, dataRoot string, m disk.Marker) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	fs := h.FS(node)
	if err := fs.MkdirAll(ctx, dataRoot); err != nil {
		return err
	}
	return fs.WriteFile(ctx, dataRoot+"/"+disk.MarkerName, b, 0o644)
}

// Capacity returns the last heartbeat statfs of a path (known=false before the first heartbeat).
func (h *Hub) Capacity(node, mountPath, dataRoot string) (c disk.Capacity, exists, known bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[node] == nil {
		return c, false, true // offline node: its disks are unreachable
	}
	m, ok := h.stats[node]
	if !ok {
		return c, false, false
	}
	dr, ok1 := m[dataRoot]
	mp, ok2 := m[mountPath]
	if !ok1 && !ok2 {
		return c, false, false
	}
	exists = dr.Exists
	if mp.Exists {
		c = mp.Cap
	} else {
		c = dr.Cap
	}
	return c, exists, true
}

// Discover lists volumes mounted on the node.
func (h *Hub) Discover(ctx context.Context, node string) ([]disk.Descriptor, error) {
	var out []disk.Descriptor
	err := h.Call(ctx, node, agentproto.OpDiscover, struct{}{}, &out)
	return out, err
}
