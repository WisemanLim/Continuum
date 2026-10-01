package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/zeebo/blake3"

	"continuum/internal/agentproto"
	"continuum/internal/nodefs"
	"continuum/internal/pki"
)

// Direct agent ↔ agent transfers (PRD D36, §7.4 rules ②–④): the receiving agent pulls from the source agent
// over mTLS (both certificates issued by the Controller CA) with a Controller-signed ticket naming source node,
// receiving node and path. The Controller only schedules; bytes never pass through it.

func (a *Agent) caCert() (*x509.Certificate, error) {
	b, err := os.ReadFile(filepath.Join(a.Cfg.dir, "ca.pem"))
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("bad ca.pem")
	}
	return x509.ParseCertificate(blk.Bytes)
}

// ServePeers listens on addr for peer agents until ctx ends.
func (a *Agent) ServePeers(ctx context.Context, ln net.Listener) error {
	ca, err := a.caCert()
	if err != nil {
		return err
	}
	// reload the (rotating) certificate per handshake
	getCert := func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		c, err := tls.LoadX509KeyPair(filepath.Join(a.Cfg.dir, "cert.pem"), filepath.Join(a.Cfg.dir, "key.pem"))
		return &c, err
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: getCert, ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			_, err := pki.VerifyNodeCert(ca, raw)
			return err
		}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentproto.PeerPath, func(w http.ResponseWriter, r *http.Request) { a.servePeerFile(w, r, ca) })
	srv := &http.Server{Handler: mux, TLSConfig: tc, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	err = srv.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *Agent) servePeerFile(w http.ResponseWriter, r *http.Request, ca *x509.Certificate) {
	peer, err := pki.VerifyNodeCert(ca, rawPeerCerts(r.TLS))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	p := r.URL.Query().Get("path")
	t, err := pki.VerifyTicket(ca, r.Header.Get(agentproto.TicketHeader))
	if err != nil || t.Src != a.Cfg.NodeID || t.Dst != peer || t.Path != p {
		http.Error(w, fmt.Sprintf("ticket not valid for %s → %s %s: %v", a.Cfg.NodeID, peer, p, err), http.StatusForbidden)
		return
	}
	off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	f, err := os.Open(p)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, fs.ErrNotExist) {
			code = http.StatusNotFound
		}
		http.Error(w, err.Error(), code)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || off < 0 || off > st.Size() {
		http.Error(w, "bad offset", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h := blake3.New()
	if _, err := io.CopyN(h, f, off); err != nil { // the trailer hash always covers the whole file
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Trailer", agentproto.SumTrailer)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Continuum-Size", strconv.FormatInt(st.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(io.MultiWriter(w, h), f); err != nil {
		return // peer went away; it resumes from its partial length
	}
	w.Header().Set(agentproto.SumTrailer, hex.EncodeToString(h.Sum(nil)))
}

func rawPeerCerts(cs *tls.ConnectionState) [][]byte {
	if cs == nil {
		return nil
	}
	out := make([][]byte, len(cs.PeerCertificates))
	for i, c := range cs.PeerCertificates {
		out[i] = c.Raw
	}
	return out
}

func (a *Agent) peerClient(peerNode string) (*http.Client, error) {
	ca, err := a.caCert()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(a.Cfg.dir, "cert.pem"), filepath.Join(a.Cfg.dir, "key.pem"))
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		InsecureSkipVerify: true, // replaced by the CA + node-id check below (peer certs carry no host SANs)
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			node, err := pki.VerifyNodeCert(ca, raw)
			if err == nil && node != peerNode {
				err = fmt.Errorf("peer certificate is %s, expected %s", node, peerNode)
			}
			return err
		}}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tc, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 30 * time.Second}}, nil
}

// partialLocks serialises attempts on one partial file (a cancelled attempt may still be closing).
var partialLocks sync.Map

// pull executes OpPull.
func (a *Agent) pull(ctx context.Context, r agentproto.PullArgs) (agentproto.PullResult, error) {
	var res agentproto.PullResult
	mu, _ := partialLocks.LoadOrStore(r.Partial, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if err := os.MkdirAll(filepath.Dir(r.Partial), 0o755); err != nil {
		return res, err
	}
	var off int64
	if fi, err := os.Stat(r.Partial); err == nil {
		off = fi.Size()
		if off > r.Size {
			off = 0
		}
	}
	out, err := os.OpenFile(r.Partial, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return res, nodefs.Classify(err)
	}
	if err := out.Truncate(off); err != nil {
		out.Close()
		return res, err
	}
	if _, err := out.Seek(off, io.SeekStart); err != nil {
		out.Close()
		return res, err
	}
	res.Resumed = off
	var body io.Reader
	var srcSum func() string
	if r.Peer == "" { // same node: plain local copy (rule ①)
		f, err := os.Open(r.Source)
		if err != nil {
			out.Close()
			return res, err
		}
		defer f.Close()
		h := blake3.New()
		if _, err := io.CopyN(h, f, off); err != nil {
			out.Close()
			return res, err
		}
		body = io.TeeReader(f, h)
		srcSum = func() string { return hex.EncodeToString(h.Sum(nil)) }
	} else {
		cl, err := a.peerClient(r.PeerNode)
		if err != nil {
			out.Close()
			return res, err
		}
		u := r.Peer + agentproto.PeerPath + "?path=" + url.QueryEscape(r.Source) + "&offset=" + strconv.FormatInt(off, 10)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set(agentproto.TicketHeader, r.Ticket)
		resp, err := cl.Do(req)
		if err != nil {
			out.Close()
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			return res, fmt.Errorf("%w: %v", nodefs.ErrPeerUnreachable, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			out.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			if resp.StatusCode == http.StatusNotFound {
				return res, fmt.Errorf("%s on %s: %w", r.Source, r.PeerNode, fs.ErrNotExist)
			}
			return res, fmt.Errorf("peer %s: %s %s", r.PeerNode, resp.Status, b)
		}
		body = resp.Body
		srcSum = func() string { return resp.Trailer.Get(agentproto.SumTrailer) }
		res.Direct = true
	}
	report := agentproto.ProgressFrom(ctx)
	n, err := copyLimited(ctx, out, body, r.BwLimit, func(n int64) {
		if report != nil {
			report(off + n)
		}
	})
	res.Bytes = n
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err() // partial kept: next attempt resumes at its length
		}
		return res, nodefs.Classify(err)
	}
	if off+n != r.Size {
		return res, fmt.Errorf("short transfer: %d of %d bytes", off+n, r.Size)
	}
	res.SourceSum = srcSum()
	if r.Verify {
		if res.Checksum, err = (nodefs.Local{}).Hash(ctx, r.Partial); err != nil {
			return res, err
		}
		if res.SourceSum == "" || res.Checksum != res.SourceSum {
			os.Remove(r.Partial)
			return res, fmt.Errorf("verify failed: source %.16s target %.16s", res.SourceSum, res.Checksum)
		}
	}
	os.Chmod(r.Partial, fsMode(r.Mode))
	if r.MtimeNs != 0 {
		t := time.Unix(0, r.MtimeNs)
		os.Chtimes(r.Partial, t, t)
	}
	if err := os.Rename(r.Partial, r.Final); err != nil {
		return res, err
	}
	return res, nil
}

// copyLimited copies with an optional bandwidth limit, reporting progress at most every 250 ms.
func copyLimited(ctx context.Context, w io.Writer, r io.Reader, bw int64, progress func(n int64)) (int64, error) {
	buf := make([]byte, 1<<20)
	var n int64
	start := time.Now()
	lastReport := start
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		k, err := r.Read(buf)
		if k > 0 {
			if _, werr := w.Write(buf[:k]); werr != nil {
				return n, werr
			}
			n += int64(k)
			if time.Since(lastReport) >= 250*time.Millisecond {
				progress(n)
				lastReport = time.Now()
			}
			if bw > 0 {
				if d := time.Duration(float64(n)/float64(bw)*float64(time.Second)) - time.Since(start); d > 0 {
					select {
					case <-time.After(d):
					case <-ctx.Done():
						return n, ctx.Err()
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

// running commands that can be cancelled (OpCancel).
type cmdRegistry struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

func (c *cmdRegistry) add(id string, cancel context.CancelFunc) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]context.CancelFunc{}
	}
	c.m[id] = cancel
	c.mu.Unlock()
}

func (c *cmdRegistry) done(id string) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

func (c *cmdRegistry) cancel(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.m[id]; f != nil {
		f()
		return true
	}
	return false
}

// PeerListen starts the peer endpoint (addr "" or "off" disables it) and returns the URL to advertise.
func (a *Agent) PeerListen(ctx context.Context, addr, advertise string) (string, error) {
	if addr == "" || addr == "off" {
		return "", nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if advertise == "" {
		host, _ := os.Hostname()
		advertise = "https://" + net.JoinHostPort(host, port)
	} else if u, err := url.Parse(advertise); err == nil && u.Port() == "0" { // ":0" listen: fill in the real port
		u.Host = net.JoinHostPort(u.Hostname(), port)
		advertise = u.String()
	}
	go func() {
		if err := a.ServePeers(ctx, ln); err != nil {
			log.Printf("agent: peer endpoint: %v", err)
		}
	}()
	a.PeerURL = advertise
	log.Printf("agent: peer endpoint %s (direct agent ↔ agent transfers)", advertise)
	return advertise, nil
}
