package agent

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"continuum/internal/agentproto"
	"continuum/internal/pki"
)

// newTestAgent writes an agent state dir with a certificate from ca.
func newTestAgent(t *testing.T, ca *pki.CA, node string) *Agent {
	t.Helper()
	dir := t.TempDir()
	keyPEM, pubPEM, err := pki.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, _, _, err := ca.IssueClient(node, pubPEM)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
	for n, b := range map[string][]byte{"key.pem": keyPEM, "cert.pem": certPEM, "ca.pem": caPEM} {
		os.WriteFile(filepath.Join(dir, n), b, 0o600)
	}
	return New(&Config{NodeID: node, dir: dir})
}

// E32 security: the peer endpoint needs a CA-issued client certificate AND a CA-signed ticket naming this
// source node, the caller's node and the exact path; partial pulls resume at the partial's length.
func TestPeerEndpointSecurity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ca, _ := pki.LoadOrCreate(t.TempDir(), "ctl_test")
	other, _ := pki.LoadOrCreate(t.TempDir(), "ctl_other")
	src := newTestAgent(t, ca, "nod_src")
	dst := newTestAgent(t, ca, "nod_dst")
	intruder := newTestAgent(t, other, "nod_dst") // same node id, foreign CA
	peerURL, err := src.PeerListen(ctx, "127.0.0.1:0", "https://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "data.bin")
	content := strings.Repeat("continuum-", 50_000) // 500 kB
	os.WriteFile(file, []byte(content), 0o644)

	get := func(a *Agent, ticket, path string) (int, string) {
		cl, err := a.peerClient("nod_src")
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", peerURL+agentproto.PeerPath+"?path="+url.QueryEscape(path), nil)
		req.Header.Set(agentproto.TicketHeader, ticket)
		res, err := cl.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	good, _ := ca.SignTicket("nod_src", "nod_dst", file, time.Minute)
	if code, body := get(dst, good, file); code != 200 || body != content {
		t.Fatalf("valid ticket: %d (%d bytes)", code, len(body))
	}
	if code, _ := get(dst, "", file); code != 403 {
		t.Fatalf("missing ticket: %d", code)
	}
	if code, _ := get(dst, good, "/etc/hosts"); code != 403 {
		t.Fatalf("ticket for another path: %d", code)
	}
	forOther, _ := ca.SignTicket("nod_src", "nod_x", file, time.Minute)
	if code, _ := get(dst, forOther, file); code != 403 {
		t.Fatalf("ticket for another receiver: %d", code)
	}
	expired, _ := ca.SignTicket("nod_src", "nod_dst", file, -time.Minute)
	if code, _ := get(dst, expired, file); code != 403 {
		t.Fatalf("expired ticket: %d", code)
	}
	forged, _ := other.SignTicket("nod_src", "nod_dst", file, time.Minute)
	if code, _ := get(dst, forged, file); code != 403 {
		t.Fatalf("ticket signed by a foreign CA: %d", code)
	}
	if code, msg := get(intruder, good, file); code != 0 || !strings.Contains(msg, "certificate") {
		t.Fatalf("foreign-CA client certificate must fail the handshake: %d %s", code, msg)
	}

	// resumable pull: a partial with the first 100 kB is completed from offset 100000 and verified
	part := filepath.Join(t.TempDir(), "out.bin.partial.w")
	os.WriteFile(part, []byte(content[:100_000]), 0o600)
	final := strings.TrimSuffix(part, ".partial.w")
	res, err := dst.pull(ctx, agentproto.PullArgs{Peer: peerURL, PeerNode: "nod_src", Ticket: good, Source: file, Final: final, Partial: part,
		Size: int64(len(content)), Verify: true})
	if err != nil || res.Resumed != 100_000 || res.Bytes != int64(len(content)-100_000) || !res.Direct || res.Checksum != res.SourceSum {
		t.Fatalf("resume: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(final); string(b) != content {
		t.Fatal("resumed file differs")
	}
	// a corrupted partial is caught by the BLAKE3 comparison and discarded
	os.WriteFile(part, []byte(strings.Repeat("X", 100_000)), 0o600)
	if _, err := dst.pull(ctx, agentproto.PullArgs{Peer: peerURL, PeerNode: "nod_src", Ticket: good, Source: file, Final: final + "2", Partial: part,
		Size: int64(len(content)), Verify: true}); err == nil || !strings.Contains(err.Error(), "verify failed") {
		t.Fatalf("corrupt partial must fail verification: %v", err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("failed partial must be removed so the retry starts over")
	}
	// unreachable peer is reported as such (controller falls back to relaying)
	if _, err := dst.pull(ctx, agentproto.PullArgs{Peer: "https://127.0.0.1:1", PeerNode: "nod_src", Ticket: good, Source: file, Final: final + "3",
		Partial: part, Size: int64(len(content))}); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("unreachable: %v", err)
	}
}
