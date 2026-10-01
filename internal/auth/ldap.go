package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
)

// LDAP / Active Directory sign-in (FR-S02 enterprise): service-account bind → search the user → bind as the
// user with the typed password → map group membership to a role. Settings:
//
//	ldap.url            ldap://dc.example.com:389 or ldaps://…:636
//	ldap.starttls       true = upgrade ldap:// with StartTLS (default true for ldap://)
//	ldap.bind_dn        service account DN (empty = anonymous search)
//	ldap.bind_password  service account password (secret store)
//	ldap.user_base      search base, e.g. ou=people,dc=example,dc=com
//	ldap.user_filter    default (&(objectClass=person)(uid=%s)); AD: (&(objectClass=user)(sAMAccountName=%s))
//	ldap.group_attr     default memberOf
//	ldap.role_map       "cn=storage,ou=groups,dc=example,dc=com=storage-admin;cn=ops,…=operator" (first match by role rank)
//	ldap.default_role   role when no group matches; empty = refuse the login
//	ldap.ca_file        PEM bundle for a private directory CA (default: system roots)
type ldapConfig struct {
	url, bindDN, bindPW, base, filter, groupAttr, defRole, caFile string
	startTLS                                                      bool
	roles                                                         map[string]string // lower-cased group DN → role
}

// LDAPTimeout bounds the directory round trips of one login.
var LDAPTimeout = 10 * time.Second

func (s *Service) ldapConfig(ctx context.Context) (ldapConfig, bool) {
	c := s.C
	cfg := ldapConfig{url: c.Setting(ctx, "ldap.url", ""), bindDN: c.Setting(ctx, "ldap.bind_dn", ""), bindPW: c.Setting(ctx, "ldap.bind_password", ""),
		base: c.Setting(ctx, "ldap.user_base", ""), filter: c.Setting(ctx, "ldap.user_filter", "(&(objectClass=person)(uid=%s))"),
		groupAttr: c.Setting(ctx, "ldap.group_attr", "memberOf"), defRole: c.Setting(ctx, "ldap.default_role", ""), caFile: c.Setting(ctx, "ldap.ca_file", ""), roles: map[string]string{}}
	cfg.startTLS = c.Setting(ctx, "ldap.starttls", "true") != "false"
	for _, kv := range strings.Split(c.Setting(ctx, "ldap.role_map", ""), ";") {
		i := strings.LastIndex(kv, "=")
		if i <= 0 {
			continue
		}
		cfg.roles[strings.ToLower(strings.TrimSpace(kv[:i]))] = strings.TrimSpace(kv[i+1:])
	}
	return cfg, cfg.url != "" && cfg.base != ""
}

// LDAPEnabled reports whether directory sign-in is configured.
func (s *Service) LDAPEnabled(ctx context.Context) bool {
	_, ok := s.ldapConfig(ctx)
	return ok
}

var errLDAPDenied = errors.New("ldap: denied")

func (cfg ldapConfig) dial() (*ldap.Conn, error) {
	u, err := url.Parse(cfg.url)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	if cfg.caFile != "" {
		pem, err := os.ReadFile(cfg.caFile)
		if err != nil {
			return nil, fmt.Errorf("ldap.ca_file: %w", err)
		}
		tc.RootCAs = x509.NewCertPool()
		if !tc.RootCAs.AppendCertsFromPEM(pem) {
			return nil, errors.New("ldap.ca_file holds no PEM certificate")
		}
	}
	d := &net.Dialer{Timeout: LDAPTimeout}
	conn, err := ldap.DialURL(cfg.url, ldap.DialWithDialer(d), ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(LDAPTimeout)
	if u.Scheme == "ldap" && cfg.startTLS {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS: %w (set ldap.starttls=false only for a trusted local directory)", err)
		}
	}
	return conn, nil
}

// ldapAuthenticate returns the role for username/password, errLDAPDenied for bad credentials or no role.
func (s *Service) ldapAuthenticate(ctx context.Context, cfg ldapConfig, username, password string) (string, string, error) {
	if password == "" { // an empty password is an unauthenticated bind and always "succeeds" (RFC 4513 §5.1.2)
		return "", "", errLDAPDenied
	}
	conn, err := cfg.dial()
	if err != nil {
		return "", "", err
	}
	defer conn.Close()
	if cfg.bindDN != "" {
		if err := conn.Bind(cfg.bindDN, cfg.bindPW); err != nil {
			return "", "", fmt.Errorf("service bind: %w", err)
		}
	}
	res, err := conn.Search(ldap.NewSearchRequest(cfg.base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, int(LDAPTimeout/time.Second), false,
		fmt.Sprintf(cfg.filter, ldap.EscapeFilter(username)), []string{"dn", cfg.groupAttr}, nil))
	if err != nil {
		return "", "", fmt.Errorf("search: %w", err)
	}
	if len(res.Entries) != 1 {
		return "", "", errLDAPDenied // unknown or ambiguous user
	}
	e := res.Entries[0]
	if err := conn.Bind(e.DN, password); err != nil {
		return "", "", errLDAPDenied
	}
	role := ""
	for _, g := range e.GetAttributeValues(cfg.groupAttr) {
		if r, ok := cfg.roles[strings.ToLower(g)]; ok && validRole(r) && rank(r) > rank(role) {
			role = r
		}
	}
	if role == "" {
		role = cfg.defRole
	}
	if !validRole(role) {
		return e.DN, "", errLDAPDenied
	}
	return e.DN, role, nil
}

func rank(role string) int {
	for i, r := range Roles {
		if r == role {
			return i + 1
		}
	}
	return 0
}

// ldapLogin signs in a directory user, creating or updating the local account (role follows the groups on
// every login). The local TOTP second factor still applies.
func (s *Service) ldapLogin(ctx context.Context, cfg ldapConfig, username, password, code, client string) (*Session, error) {
	dn, role, err := s.ldapAuthenticate(ctx, cfg, username, password)
	if errors.Is(err, errLDAPDenied) {
		s.C.DB.Exec(ctx, `UPDATE users SET failed_logins=failed_logins+1 WHERE username=? AND auth_source='ldap'`, username)
		s.C.Audit.Append(ctx, audit.Entry{Actor: username, Action: "auth.login", Result: "failed: ldap"})
		return nil, ErrLogin
	}
	if err != nil {
		s.C.Audit.Append(ctx, audit.Entry{Actor: username, Action: "auth.login", Result: "error: ldap " + err.Error()})
		return nil, core.Errf(core.CodeOffline, "directory unavailable: %v", err)
	}
	u, _, gerr := s.get(ctx, username)
	now := store.Now()
	if gerr != nil {
		if !s.Enabled(ctx) {
			return nil, core.Errf(core.CodePermission, "create a local system admin before enabling LDAP")
		}
		u = User{ID: store.NewID("usr"), Username: username, Role: role, CreatedAt: now, AuthSource: "ldap"}
		if _, err := s.C.DB.Exec(ctx, `INSERT INTO users (user_id, username, password_hash, role, auth_source, created_at, updated_at) VALUES (?,?,?,?,'ldap',?,?)`,
			u.ID, username, "!ldap", role, now, now); err != nil {
			return nil, err
		}
		s.C.Audit.Append(ctx, audit.Entry{Actor: "ldap", Action: "user.create", Target: username, Result: role + " (" + dn + ")"})
	} else {
		if u.AuthSource != "ldap" {
			return nil, ErrLogin // a local account with the same name never falls through to the directory
		}
		if u.Disabled {
			return nil, ErrLogin
		}
		if u.Role != role {
			s.C.DB.Exec(ctx, `UPDATE users SET role=?, updated_at=? WHERE user_id=?`, role, now, u.ID)
			s.C.Audit.Append(ctx, audit.Entry{Actor: "ldap", Action: "user.role", Target: username, Result: u.Role + " → " + role})
			u.Role = role
		}
	}
	if err := s.checkMFA(ctx, u.ID, code); err != nil {
		return nil, err
	}
	return s.newSession(ctx, u, client)
}
