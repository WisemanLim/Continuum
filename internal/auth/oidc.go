package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
)

// OIDC single sign-on (FR-S02 enterprise step): Authorization Code + PKCE against any OpenID Provider
// (Keycloak, Entra ID, Google, Okta…). Settings: oidc.issuer, oidc.client_id, oidc.client_secret (secret
// store), oidc.redirect_url, oidc.default_role (role for users created on first SSO login), oidc.username_claim.
type OIDC struct {
	S      *Service
	Client *http.Client

	mu      sync.Mutex
	pending map[string]oidcState // state → flow data (10 min)
	meta    *oidcMeta
	metaAt  time.Time
	keys    map[string]*rsa.PublicKey
}

type oidcState struct {
	verifier, nonce, ret string
	at                   time.Time
}

type oidcMeta struct {
	Issuer   string `json:"issuer"`
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURL  string `json:"jwks_uri"`
}

// NewOIDC creates the SSO handler.
func NewOIDC(s *Service) *OIDC {
	return &OIDC{S: s, Client: &http.Client{Timeout: 15 * time.Second}, pending: map[string]oidcState{}, keys: map[string]*rsa.PublicKey{}}
}

type oidcConfig struct{ issuer, clientID, secret, redirect, role, claim string }

func (o *OIDC) config(ctx context.Context) oidcConfig {
	c := o.S.C
	return oidcConfig{issuer: strings.TrimRight(c.Setting(ctx, "oidc.issuer", ""), "/"), clientID: c.Setting(ctx, "oidc.client_id", ""),
		secret: c.Setting(ctx, "oidc.client_secret", ""), redirect: c.Setting(ctx, "oidc.redirect_url", ""),
		role: c.Setting(ctx, "oidc.default_role", Viewer), claim: c.Setting(ctx, "oidc.username_claim", "preferred_username")}
}

// Enabled reports whether SSO is configured.
func (o *OIDC) Enabled(ctx context.Context) bool {
	c := o.config(ctx)
	return c.issuer != "" && c.clientID != "" && c.redirect != ""
}

func (o *OIDC) discover(ctx context.Context, issuer string) (*oidcMeta, error) {
	o.mu.Lock()
	if o.meta != nil && o.meta.Issuer == issuer && time.Since(o.metaAt) < time.Hour {
		m := o.meta
		o.mu.Unlock()
		return m, nil
	}
	o.mu.Unlock()
	var m oidcMeta
	if err := o.getJSON(ctx, issuer+"/.well-known/openid-configuration", &m); err != nil {
		return nil, err
	}
	if m.Issuer != issuer {
		return nil, fmt.Errorf("issuer mismatch: discovery says %q", m.Issuer)
	}
	o.mu.Lock()
	o.meta, o.metaAt, o.keys = &m, time.Now(), map[string]*rsa.PublicKey{}
	o.mu.Unlock()
	return &m, nil
}

func (o *OIDC) getJSON(ctx context.Context, u string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("GET %s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
}

func randURL(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// LoginURL starts a flow; ret is where the UI wants to land afterwards.
func (o *OIDC) LoginURL(ctx context.Context, ret string) (string, error) {
	c := o.config(ctx)
	if c.issuer == "" {
		return "", core.Errf(core.CodeInvalid, "SSO is not configured (oidc.issuer, oidc.client_id, oidc.redirect_url)")
	}
	m, err := o.discover(ctx, c.issuer)
	if err != nil {
		return "", core.Errf(core.CodeOffline, "OIDC discovery: %v", err)
	}
	state, nonce, verifier := randURL(18), randURL(18), randURL(32)
	sum := sha256.Sum256([]byte(verifier))
	o.mu.Lock()
	for k, v := range o.pending {
		if time.Since(v.at) > 10*time.Minute {
			delete(o.pending, k)
		}
	}
	o.pending[state] = oidcState{verifier: verifier, nonce: nonce, ret: ret, at: time.Now()}
	o.mu.Unlock()
	q := url.Values{"response_type": {"code"}, "client_id": {c.clientID}, "redirect_uri": {c.redirect}, "scope": {"openid profile email"},
		"state": {state}, "nonce": {nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	sep := "?"
	if strings.Contains(m.AuthURL, "?") {
		sep = "&"
	}
	return m.AuthURL + sep + q.Encode(), nil
}

// Callback exchanges the code, verifies the ID token and returns a session plus the UI return URL.
func (o *OIDC) Callback(ctx context.Context, state, code string) (*Session, string, error) {
	o.mu.Lock()
	st, ok := o.pending[state]
	delete(o.pending, state)
	o.mu.Unlock()
	if !ok || time.Since(st.at) > 10*time.Minute {
		return nil, "", core.Errf(core.CodePermission, "unknown or expired SSO state")
	}
	c := o.config(ctx)
	m, err := o.discover(ctx, c.issuer)
	if err != nil {
		return nil, "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.redirect}, "client_id": {c.clientID}, "code_verifier": {st.verifier}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.secret != "" {
		req.SetBasicAuth(url.QueryEscape(c.clientID), url.QueryEscape(c.secret))
	}
	res, err := o.Client.Do(req)
	if err != nil {
		return nil, "", core.Errf(core.CodeOffline, "token endpoint: %v", err)
	}
	defer res.Body.Close()
	var tr struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tr)
	if res.StatusCode != 200 || tr.IDToken == "" {
		return nil, "", core.Errf(core.CodePermission, "token exchange failed: %s %s", res.Status, tr.Error)
	}
	claims, err := o.verify(ctx, m, tr.IDToken, c.issuer, c.clientID, st.nonce)
	if err != nil {
		o.S.C.Audit.Append(ctx, audit.Entry{Actor: "sso", Action: "auth.login", Result: "failed: " + err.Error()})
		return nil, "", core.Errf(core.CodePermission, "ID token rejected: %v", err)
	}
	name, _ := claims[c.claim].(string)
	if name == "" {
		name, _ = claims["email"].(string)
	}
	if name == "" {
		name, _ = claims["sub"].(string)
	}
	u, err := o.provision(ctx, name, c.role)
	if err != nil {
		return nil, "", err
	}
	sess, err := o.S.newSession(ctx, u, "sso")
	return sess, st.ret, err
}

// provision maps an SSO identity to a user: existing users keep their role, new ones get the default role.
func (o *OIDC) provision(ctx context.Context, name, role string) (User, error) {
	u, _, err := o.S.get(ctx, name)
	if err == nil {
		if u.Disabled {
			return u, core.Errf(core.CodePermission, "user %s is disabled", name)
		}
		return u, nil
	}
	if !validRole(role) || role == SystemAdmin && o.S.Enabled(ctx) {
		role = Viewer // never auto-create admins once accounts exist
	}
	if !o.S.Enabled(ctx) {
		return u, core.Errf(core.CodePermission, "create a local system admin before enabling SSO")
	}
	u = User{ID: store.NewID("usr"), Username: name, Role: role, CreatedAt: store.Now()}
	if _, err := o.S.C.DB.Exec(ctx, `INSERT INTO users (user_id, username, password_hash, role, auth_source, created_at, updated_at) VALUES (?,?,?,?,'oidc',?,?)`,
		u.ID, name, "!oidc", role, u.CreatedAt, u.CreatedAt); err != nil {
		return u, err
	}
	o.S.C.Audit.Append(ctx, audit.Entry{Actor: "sso", Action: "user.create", Target: name, Result: role + " (first SSO login)"})
	return u, nil
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// verify checks an RS256 JWT against the provider's JWKS and the standard claims.
func (o *OIDC) verify(ctx context.Context, m *oidcMeta, tok, issuer, aud, nonce string) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	hb, err := b64(parts[0])
	if err != nil {
		return nil, err
	}
	var hdr struct{ Alg, Kid string }
	if err := json.Unmarshal(hb, &hdr); err != nil || hdr.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported alg %q", hdr.Alg)
	}
	key, err := o.key(ctx, m, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := b64(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("bad signature")
	}
	pb, err := b64(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(pb, &claims); err != nil {
		return nil, err
	}
	if claims["iss"] != issuer {
		return nil, fmt.Errorf("iss %v", claims["iss"])
	}
	okAud := false
	switch a := claims["aud"].(type) {
	case string:
		okAud = a == aud
	case []any:
		for _, x := range a {
			okAud = okAud || x == aud
		}
	}
	if !okAud {
		return nil, errors.New("audience mismatch")
	}
	exp, _ := claims["exp"].(float64)
	if time.Now().Unix() > int64(exp)+60 {
		return nil, errors.New("token expired")
	}
	if claims["nonce"] != nonce {
		return nil, errors.New("nonce mismatch")
	}
	return claims, nil
}

func (o *OIDC) key(ctx context.Context, m *oidcMeta, kid string) (*rsa.PublicKey, error) {
	o.mu.Lock()
	k := o.keys[kid]
	o.mu.Unlock()
	if k != nil {
		return k, nil
	}
	var jwks struct {
		Keys []struct{ Kty, Kid, N, E string } `json:"keys"`
	}
	if err := o.getJSON(ctx, m.JWKSURL, &jwks); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, j := range jwks.Keys {
		if j.Kty != "RSA" {
			continue
		}
		nb, err1 := b64(j.N)
		eb, err2 := b64(j.E)
		if err1 != nil || err2 != nil {
			continue
		}
		o.keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(new(big.Int).SetBytes(eb).Int64())}
	}
	if k := o.keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key id %q", kid)
}
