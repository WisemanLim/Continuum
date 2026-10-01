package ops

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/secrets"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
)

// fakeSMTP accepts one session per connection and records AUTH, envelope and DATA.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	auth []string
	rcpt []string
	data []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { c.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			say("250-fake")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
			f.mu.Lock()
			f.auth = append(f.auth, strings.ReplaceAll(string(b), "\x00", "|"))
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(up, "MAIL FROM"):
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO"):
			f.mu.Lock()
			f.rcpt = append(f.rcpt, cmd[len("RCPT TO:"):])
			f.mu.Unlock()
			say("250 ok")
		case up == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = append(f.data, b.String())
			f.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func TestEmailNotifications(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(storetest.URL(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := core.Open(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Secrets = &secrets.File{Path: t.TempDir() + "/secrets.json"}
	srv := newFakeSMTP(t)
	n := NewNotifier(ctx, c)
	for k, v := range map[string]string{"notify.smtp_host": srv.ln.Addr().String(), "notify.smtp_user": "alerts@example.test", "notify.smtp_password": "placeholder-pw",
		"notify.email_to": "ops@example.test, oncall@example.test", "notify.smtp_tls": "none"} {
		c.SetSetting(ctx, k, v)
	}
	var stored string
	db.QueryRow(ctx, `SELECT COALESCE(MAX(value),'') FROM settings WHERE key='notify.smtp_password'`).Scan(&stored)
	if stored != "" {
		t.Fatalf("SMTP password must live in the secret store, not the DB: %q", stored)
	}
	c.Audit.Emit(ctx, audit.Event{Kind: "job.done"}, nil)
	c.Audit.Emit(ctx, audit.Event{Kind: "job.failed", JobID: "cpy_9"}, map[string]any{"message": "disk full"})
	c.Audit.Emit(ctx, audit.Event{Kind: "conflict.detected"}, map[string]any{"path": "/.hidden", "copies": 2, "kind": "conflict"})
	if sent := n.Tick(ctx); sent != 1 {
		t.Fatalf("one digest mail expected, sent %d", sent)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.data) != 1 || len(srv.rcpt) != 2 || srv.auth[0] != "|alerts@example.test|placeholder-pw" {
		t.Fatalf("smtp: data=%d rcpt=%v auth=%v", len(srv.data), srv.rcpt, srv.auth)
	}
	m := srv.data[0]
	for _, want := range []string{"Subject: Continuum: 2 alerts", "From: alerts@example.test", "To: ops@example.test, oncall@example.test", "Job cpy_9 failed: disk full", "/.hidden exists on 2 disks"} {
		if !strings.Contains(m, want) {
			t.Fatalf("mail lacks %q:\n%s", want, m)
		}
	}
	// starttls mode refuses a server without STARTTLS (no silent plaintext downgrade)
	cfg, _ := EmailSettings(ctx, c)
	cfg.TLS = "starttls"
	if err := SendEmail(ctx, cfg, "x", nil); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("starttls downgrade: %v", err)
	}
	cfg.TLS = "none"
	if err := SendEmail(ctx, cfg, "bad\r\nBcc: evil@example.test", nil); err == nil {
		t.Fatal("header injection accepted")
	}
}
