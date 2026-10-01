// Package auth: users, sessions and the RBAC permission matrix (FR-S01, FR-S02).
//
// Single-user mode: while no user exists the API stays open on localhost (P1/P2 behaviour). Creating the
// first user (always System Admin) switches the Controller to authenticated mode: every /v1 call then needs
// a session token (or CONTINUUM_TOKEN, which acts as System Admin for automation).
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
)

// Roles (FR-S01).
const (
	Viewer        = "viewer"
	Operator      = "operator"
	StorageAdmin  = "storage-admin"
	SecurityAdmin = "security-admin"
	SystemAdmin   = "system-admin"
)

// Roles in display order.
var Roles = []string{Viewer, Operator, StorageAdmin, SecurityAdmin, SystemAdmin}

// Permissions.
const (
	PermRead     = "read"     // dashboards, catalogs, job lists
	PermTransfer = "transfer" // copy/move/delete(trash)/mkdir/rename/scan/verify/job control/conflict resolve
	PermStorage  = "storage"  // disks, volumes, order, drain/rebalance, access point, benchmark, wipe
	PermSecurity = "security" // users, enrollment tokens, node revoke, audit, credentials
	PermSystem   = "system"   // settings, diagnostics
)

// Matrix maps each permission to the roles holding it.
var Matrix = map[string][]string{
	PermRead:     {Viewer, Operator, StorageAdmin, SecurityAdmin, SystemAdmin},
	PermTransfer: {Operator, StorageAdmin, SystemAdmin},
	PermStorage:  {StorageAdmin, SystemAdmin},
	PermSecurity: {SecurityAdmin, SystemAdmin},
	PermSystem:   {SystemAdmin},
}

// Can reports whether role holds perm.
func Can(role, perm string) bool {
	for _, r := range Matrix[perm] {
		if r == role {
			return true
		}
	}
	return false
}

type rule struct {
	method *regexp.Regexp
	re     *regexp.Regexp
	perm   string
}

func r(method, pattern, perm string) rule {
	return rule{regexp.MustCompile("^" + method + "$"), regexp.MustCompile("^" + pattern + "$"), perm}
}

// rules are evaluated in order; the first match wins. Unmatched GETs need read, unmatched mutations system.
var rules = []rule{
	r("GET", `/v1/audit.*`, PermSecurity),
	r("GET", `/v1/access/credentials`, PermSecurity),
	r("GET", `/v1/diagnostics`, PermSystem),
	r("GET", `/v1/users.*`, PermSecurity),
	r("GET", `/v1/approvals.*`, PermSecurity),
	r("GET", `/v1/settings`, PermSystem),
	r("GET", `/v1/fs/browse`, PermTransfer),
	r("GET", `/metrics`, PermRead),
	r("GET", `.*`, PermRead),
	r("POST", `/v1/auth/logout`, PermRead),
	r("POST", `/v1/auth/mfa/(setup|enable|disable)`, PermRead), // own account only
	r("POST", `/v1/approvals/[^/]+/(approve|reject)`, PermSecurity),
	r("PUT", `/v1/auth/password`, PermRead),
	r("POST", `/v1/(plan|jobs)`, PermTransfer), // DRAIN/REBALANCE types are re-checked in the handler
	r("POST", `/v1/jobs/[^/]+/(pause|resume|cancel|retry)`, PermTransfer),
	r("POST", `/v1/volumes/[^/]+/(mkdir|rename|delete|verify|scan)`, PermTransfer),
	r("POST", `/v1/volumes/[^/]+/conflicts/resolve-duplicates`, PermTransfer),
	r("POST", `/v1/conflicts/[^/]+/resolve`, PermTransfer),
	r("POST", `/v1/trash/[^/]+/restore`, PermTransfer),
	r("POST", `/v1/volumes/[^/]+/trash/purge`, PermStorage),
	r("POST", `/v1/notifications/test`, PermSystem),
	r("PUT", `/v1/settings`, PermSystem),
	r("POST", `/v1/nodes/tokens`, PermSecurity),
	r("POST", `/v1/nodes/[^/]+/revoke`, PermSecurity),
	r("(POST|PATCH|DELETE)", `/v1/users.*`, PermSecurity),
	r("(POST|PUT|PATCH|DELETE)", `/v1/(disks|volumes|access|vfs).*`, PermStorage),
}

// Required returns the permission a request needs.
func Required(method, path string) string {
	for _, ru := range rules {
		if ru.method.MatchString(method) && ru.re.MatchString(path) {
			return ru.perm
		}
	}
	if method == http.MethodGet {
		return PermRead
	}
	return PermSystem
}

// User is an account.
type User struct {
	ID          string `json:"user_id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at,omitempty"`
	MFAEnabled  bool   `json:"mfa_enabled"`
	AuthSource  string `json:"auth_source"`
}

// Service manages users and sessions.
type Service struct {
	C          *core.Core
	SessionTTL time.Duration
	Iterations int
	enabled    atomic.Bool // once a user exists it stays true (the last admin cannot be deleted)
}

// New creates the auth service.
func New(c *core.Core) *Service {
	return &Service{C: c, SessionTTL: 12 * time.Hour, Iterations: 600_000}
}

// Enabled is true once at least one user exists.
func (s *Service) Enabled(ctx context.Context) bool {
	if s.enabled.Load() {
		return true
	}
	var n int
	s.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	if n > 0 {
		s.enabled.Store(true)
	}
	return n > 0
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9._@-]{2,64}$`)

func validRole(r string) bool {
	for _, x := range Roles {
		if x == r {
			return true
		}
	}
	return false
}

func checkPassword(p string) error {
	if len(p) < 10 {
		return core.Errf(core.CodeInvalid, "password must have at least 10 characters")
	}
	return nil
}

func (s *Service) hash(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, pw, salt, s.Iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", s.Iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyHash(stored, pw string) bool {
	p := strings.Split(stored, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[2])
	want, err2 := base64.RawStdEncoding.DecodeString(p[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummy hash for constant-ish time on unknown users
var dummy = "pbkdf2-sha256$600000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// Create adds a user. The first user must be a System Admin (bootstrap).
func (s *Service) Create(ctx context.Context, username, password, role, by string) (User, error) {
	if !nameRe.MatchString(username) {
		return User{}, core.Errf(core.CodeInvalid, "username: 2-64 letters, digits or . _ @ -")
	}
	if !validRole(role) {
		return User{}, core.Errf(core.CodeInvalid, "role must be one of %s", strings.Join(Roles, ", "))
	}
	if !s.Enabled(ctx) && role != SystemAdmin {
		return User{}, core.Errf(core.CodeInvalid, "the first user must be a %s", SystemAdmin)
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	h, err := s.hash(password)
	if err != nil {
		return User{}, err
	}
	u := User{ID: store.NewID("usr"), Username: username, Role: role, CreatedAt: store.Now()}
	if _, err := s.C.DB.Exec(ctx, `INSERT INTO users (user_id, username, password_hash, role, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		u.ID, username, h, role, u.CreatedAt, u.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, core.Errf(core.CodeConflict, "user %q exists", username)
		}
		return User{}, err
	}
	s.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "user.create", Target: username, Result: role})
	return u, nil
}

// List returns all users.
func (s *Service) List(ctx context.Context) ([]User, error) {
	rows, err := s.C.DB.Query(ctx, `SELECT user_id, username, role, disabled, created_at, COALESCE(last_login_at,''), mfa_enabled, auth_source FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var dis, mfa int
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &dis, &u.CreatedAt, &u.LastLoginAt, &mfa, &u.AuthSource); err != nil {
			return nil, err
		}
		u.Disabled, u.MFAEnabled = dis != 0, mfa != 0
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) get(ctx context.Context, ref string) (User, string, error) {
	var u User
	var dis, mfa int
	var h string
	err := s.C.DB.QueryRow(ctx, `SELECT user_id, username, role, disabled, created_at, COALESCE(last_login_at,''), password_hash, mfa_enabled, auth_source FROM users WHERE user_id=? OR username=?`, ref, ref).
		Scan(&u.ID, &u.Username, &u.Role, &dis, &u.CreatedAt, &u.LastLoginAt, &h, &mfa, &u.AuthSource)
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", core.Errf(core.CodeNotFound, "user %q not found", ref)
	}
	u.Disabled, u.MFAEnabled = dis != 0, mfa != 0
	return u, h, err
}

// Lookup returns a user by id or username.
func (s *Service) Lookup(ctx context.Context, ref string) (User, error) {
	u, _, err := s.get(ctx, ref)
	return u, err
}

// Update changes role / disabled / password. The last enabled System Admin cannot be demoted or disabled.
type Update struct {
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
	Password *string `json:"password,omitempty"`
}

func (s *Service) admins(ctx context.Context, except string) int {
	var n int
	s.C.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role=? AND disabled=0 AND user_id<>?`, SystemAdmin, except).Scan(&n)
	return n
}

// Patch applies an Update.
func (s *Service) Patch(ctx context.Context, ref string, up Update, by string) (User, error) {
	u, _, err := s.get(ctx, ref)
	if err != nil {
		return u, err
	}
	now := store.Now()
	if up.Role != nil {
		if !validRole(*up.Role) {
			return u, core.Errf(core.CodeInvalid, "role must be one of %s", strings.Join(Roles, ", "))
		}
		if u.Role == SystemAdmin && *up.Role != SystemAdmin && s.admins(ctx, u.ID) == 0 {
			return u, core.Errf(core.CodeConflict, "cannot demote the last system admin")
		}
		s.C.DB.Exec(ctx, `UPDATE users SET role=?, updated_at=? WHERE user_id=?`, *up.Role, now, u.ID)
	}
	if up.Disabled != nil {
		if *up.Disabled && u.Role == SystemAdmin && s.admins(ctx, u.ID) == 0 {
			return u, core.Errf(core.CodeConflict, "cannot disable the last system admin")
		}
		s.C.DB.Exec(ctx, `UPDATE users SET disabled=?, updated_at=? WHERE user_id=?`, boolInt(*up.Disabled), now, u.ID)
		if *up.Disabled {
			s.C.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID)
		}
	}
	if up.Password != nil {
		if err := checkPassword(*up.Password); err != nil {
			return u, err
		}
		h, err := s.hash(*up.Password)
		if err != nil {
			return u, err
		}
		s.C.DB.Exec(ctx, `UPDATE users SET password_hash=?, failed_logins=0, locked_until=NULL, updated_at=? WHERE user_id=?`, h, now, u.ID)
		s.C.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID) // existing sessions end
	}
	s.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "user.update", Target: u.Username, Result: "ok"})
	u, _, err = s.get(ctx, u.ID)
	return u, err
}

// Delete removes a user (not the last system admin).
func (s *Service) Delete(ctx context.Context, ref, by string) error {
	u, _, err := s.get(ctx, ref)
	if err != nil {
		return err
	}
	if u.Role == SystemAdmin && s.admins(ctx, u.ID) == 0 {
		return core.Errf(core.CodeConflict, "cannot delete the last system admin")
	}
	s.C.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID)
	s.C.DB.Exec(ctx, `DELETE FROM users WHERE user_id=?`, u.ID)
	s.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "user.delete", Target: u.Username, Result: "ok"})
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Session is a login result.
type Session struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	User      User   `json:"user"`
}

// ErrLogin is returned for any failed login (no user enumeration).
var ErrLogin = core.Errf(core.CodePermission, "invalid username or password")

// Login checks credentials; 5 failures lock the account for 5 minutes.
func (s *Service) Login(ctx context.Context, username, password, code, client string) (*Session, error) {
	var u User
	var h, locked, source string
	var dis, failed int
	err := s.C.DB.QueryRow(ctx, `SELECT user_id, username, role, disabled, password_hash, failed_logins, COALESCE(locked_until,''), auth_source FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &dis, &h, &failed, &locked, &source)
	cfg, ldapOn := s.ldapConfig(ctx)
	if err != nil && ldapOn { // not a local account: try the directory (FR-S02)
		return s.ldapLogin(ctx, cfg, username, password, code, client)
	}
	if err != nil {
		verifyHash(dummy, password)
		s.C.Audit.Append(ctx, audit.Entry{Actor: username, Action: "auth.login", Result: "failed"})
		return nil, ErrLogin
	}
	if locked != "" {
		if t, err := time.Parse(time.RFC3339Nano, locked); err == nil && time.Now().Before(t) {
			return nil, core.Errf(core.CodePermission, "account locked after repeated failures — try again later")
		}
	}
	if source == "ldap" {
		if !ldapOn {
			return nil, ErrLogin
		}
		sess, err := s.ldapLogin(ctx, cfg, username, password, code, client)
		if errors.Is(err, ErrLogin) && failed+1 >= 5 {
			s.C.DB.Exec(ctx, `UPDATE users SET failed_logins=0, locked_until=? WHERE user_id=?`, time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339Nano), u.ID)
		}
		return sess, err
	}
	if dis != 0 || !verifyHash(h, password) {
		failed++
		lock := any(nil)
		if failed >= 5 {
			lock, failed = time.Now().UTC().Add(5*time.Minute).Format(time.RFC3339Nano), 0
		}
		s.C.DB.Exec(ctx, `UPDATE users SET failed_logins=?, locked_until=? WHERE user_id=?`, failed, lock, u.ID)
		s.C.Audit.Append(ctx, audit.Entry{Actor: username, Action: "auth.login", Result: "failed"})
		return nil, ErrLogin
	}
	if err := s.checkMFA(ctx, u.ID, code); err != nil { // second factor (FR-S02)
		if errors.Is(err, ErrLogin) {
			s.C.DB.Exec(ctx, `UPDATE users SET failed_logins=failed_logins+1 WHERE user_id=?`, u.ID)
			s.C.Audit.Append(ctx, audit.Entry{Actor: username, Action: "auth.login", Result: "failed: mfa"})
		}
		return nil, err
	}
	return s.newSession(ctx, u, client)
}

// newSession issues a bearer token for a user (password+MFA or SSO).
func (s *Service) newSession(ctx context.Context, u User, client string) (*Session, error) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := "cts_" + base64.RawURLEncoding.EncodeToString(b)
	exp := time.Now().UTC().Add(s.SessionTTL)
	now := store.Now()
	if _, err := s.C.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at, client) VALUES (?,?,?,?,?)`,
		tokenHash(tok), u.ID, now, exp.Format(time.RFC3339Nano), client); err != nil {
		return nil, err
	}
	s.C.DB.Exec(ctx, `UPDATE users SET failed_logins=0, locked_until=NULL, last_login_at=? WHERE user_id=?`, now, u.ID)
	s.C.Audit.Append(ctx, audit.Entry{Actor: u.Username, Action: "auth.login", Result: "ok " + client})
	u.LastLoginAt = now
	return &Session{Token: tok, ExpiresAt: exp.Format(time.RFC3339), User: u}, nil
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// Resolve returns the user of a session token.
func (s *Service) Resolve(ctx context.Context, tok string) (User, bool) {
	var u User
	var exp string
	var dis int
	err := s.C.DB.QueryRow(ctx, `SELECT u.user_id, u.username, u.role, u.disabled, s.expires_at FROM sessions s JOIN users u ON u.user_id=s.user_id WHERE s.token_hash=?`,
		tokenHash(tok)).Scan(&u.ID, &u.Username, &u.Role, &dis, &exp)
	if err != nil || dis != 0 {
		return u, false
	}
	if t, err := time.Parse(time.RFC3339Nano, exp); err != nil || time.Now().After(t) {
		s.C.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(tok))
		return u, false
	}
	s.C.DB.Exec(ctx, `UPDATE sessions SET last_used_at=? WHERE token_hash=?`, store.Now(), tokenHash(tok))
	return u, true
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, tok string) {
	s.C.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(tok))
}

// ChangePassword lets a user change their own password (old password required).
func (s *Service) ChangePassword(ctx context.Context, userID, oldPw, newPw string) error {
	u, h, err := s.get(ctx, userID)
	if err != nil {
		return err
	}
	if !verifyHash(h, oldPw) {
		return ErrLogin
	}
	_, err = s.Patch(ctx, u.ID, Update{Password: &newPw}, u.Username)
	return err
}
