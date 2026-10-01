package ops

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"continuum/internal/core"
	"continuum/internal/version"
)

// EmailConfig is read from settings: notify.smtp_host (host:port), notify.smtp_user, notify.smtp_password
// (secret store), notify.email_from, notify.email_to (comma separated), notify.smtp_tls (starttls|tls|none).
type EmailConfig struct {
	Addr, User, Password, From, TLS string
	To                              []string
}

// EmailSettings loads the SMTP configuration; ok=false when e-mail is not configured.
func EmailSettings(ctx context.Context, c *core.Core) (EmailConfig, bool) {
	cfg := EmailConfig{Addr: c.Setting(ctx, "notify.smtp_host", ""), User: c.Setting(ctx, "notify.smtp_user", ""),
		Password: c.Setting(ctx, "notify.smtp_password", ""), From: c.Setting(ctx, "notify.email_from", ""),
		TLS: c.Setting(ctx, "notify.smtp_tls", "starttls")}
	for _, a := range strings.Split(c.Setting(ctx, "notify.email_to", ""), ",") {
		if a = strings.TrimSpace(a); a != "" {
			cfg.To = append(cfg.To, a)
		}
	}
	if cfg.From == "" {
		cfg.From = cfg.User
	}
	return cfg, cfg.Addr != "" && len(cfg.To) > 0 && cfg.From != ""
}

func headerSafe(s string) bool { return !strings.ContainsAny(s, "\r\n") }

// SendEmail delivers one plain-text message. STARTTLS is required in starttls mode (no silent downgrade);
// net/smtp refuses PLAIN auth over an unencrypted link except to localhost.
func SendEmail(ctx context.Context, cfg EmailConfig, subject string, lines []string) error {
	for _, s := range append([]string{cfg.From, subject}, cfg.To...) {
		if !headerSafe(s) {
			return errors.New("e-mail header contains a line break")
		}
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("notify.smtp_host must be host:port: %v", err)
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if cfg.TLS == "tls" { // implicit TLS (port 465)
		conn, err = tls.DialWithDialer(&d, "tcp", cfg.Addr, &tls.Config{ServerName: host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", cfg.Addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if err := cl.Hello("continuum"); err != nil {
		return err
	}
	if cfg.TLS == "starttls" || cfg.TLS == "" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer STARTTLS (set notify.smtp_tls=none only for a trusted local relay)")
		}
		if err := cl.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if cfg.User != "" {
		if err := cl.Auth(smtp.PlainAuth("", cfg.User, cfg.Password, host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(cfg.From); err != nil {
		return err
	}
	for _, to := range cfg.To {
		if err := cl.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nX-Mailer: Continuum %s\r\n\r\n",
		cfg.From, strings.Join(cfg.To, ", "), subject, time.Now().Format(time.RFC1123Z), version.Version)
	for _, l := range lines { // net/smtp's DATA writer does the dot-stuffing
		b.WriteString(l + "\r\n")
	}
	if _, err := w.Write([]byte(b.String())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
