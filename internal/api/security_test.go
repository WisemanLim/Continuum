package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"continuum/internal/auth"
	"continuum/internal/core"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
	"continuum/internal/transfer"
)

// authHarness: auth enabled with a bootstrap admin; returns the harness and a login helper.
func authHarness(t *testing.T) (*harness, func(user, pw, code string) (int, map[string]any)) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(storetest.URL(t, root))
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := transfer.New(c)
	a := auth.New(c)
	a.Iterations = 1000
	s := &Server{C: c, E: e, Env: "test", Auth: a, Origins: []string{"http://localhost:5173"}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); e.Shutdown(); db.Close() })
	h := &harness{t: t, srv: ts, root: root}
	if code := h.do("POST", "/v1/users", map[string]string{"username": "admin", "password": "0123456789", "role": "system-admin"}, nil); code != 201 {
		t.Fatalf("bootstrap: %d", code)
	}
	login := func(user, pw, code string) (int, map[string]any) {
		var out map[string]any
		saved := h.tok
		h.tok = ""
		st := h.do("POST", "/v1/auth/login", map[string]string{"username": user, "password": pw, "code": code}, &out)
		h.tok = saved
		return st, out
	}
	return h, login
}

func tokenOf(st int, out map[string]any) string {
	if st != 200 {
		panic(fmt.Sprintf("login: %d %v", st, out))
	}
	return out["token"].(string)
}

func errCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

// E29: TOTP enrolment, login with second factor, replay rejection (FR-S02).
func TestMFA(t *testing.T) {
	h, login := authHarness(t)
	h.tok = tokenOf(login("admin", "0123456789", ""))
	var setup auth.MFASetup
	if st := h.do("POST", "/v1/auth/mfa/setup", map[string]any{}, &setup); st != 200 || setup.Secret == "" || !strings.HasPrefix(setup.OTPAuthURL, "otpauth://totp/Continuum:admin?") {
		t.Fatalf("setup: %d %+v", st, setup)
	}
	if st := h.do("POST", "/v1/auth/mfa/enable", map[string]string{"code": "000000"}, nil); st != 400 {
		t.Fatalf("wrong code must not enable: %d", st)
	}
	now := time.Now().Unix() / 30
	c0, _ := auth.TOTPCode(setup.Secret, now)
	if st := h.do("POST", "/v1/auth/mfa/enable", map[string]string{"code": c0}, nil); st != 200 {
		t.Fatalf("enable: %d", st)
	}
	st, out := login("admin", "0123456789", "")
	if st != 401 || out["error"].(map[string]any)["info"].(map[string]any)["mfa_required"] != true {
		t.Fatalf("password only must ask for MFA: %d %v", st, out)
	}
	if st, _ := login("admin", "0123456789", "123456"); st != 403 {
		t.Fatalf("bad code: %d", st)
	}
	if st, _ := login("admin", "0123456789", c0); st != 403 {
		t.Fatalf("replayed code (already used to enable) must fail: %d", st)
	}
	c1, _ := auth.TOTPCode(setup.Secret, now+1) // next step, inside the ±1 window
	h.tok = tokenOf(login("admin", "0123456789", c1))
	var us []auth.User
	h.do("GET", "/v1/users", nil, &us)
	if len(us) != 1 || !us[0].MFAEnabled || us[0].AuthSource != "local" {
		t.Fatalf("users: %+v", us)
	}
	if st := h.do("POST", "/v1/auth/mfa/disable", map[string]string{"code": c1}, nil); st != 403 {
		t.Fatalf("disable with replayed code: %d", st)
	}
}

// E29: two-person approval for destructive actions (FR-S05).
func TestApprovals(t *testing.T) {
	h, login := authHarness(t)
	admin := tokenOf(login("admin", "0123456789", ""))
	h.tok = admin
	for _, u := range []string{"store1", "sec1"} {
		role := map[string]string{"store1": "storage-admin", "sec1": "security-admin"}[u]
		if st := h.do("POST", "/v1/users", map[string]string{"username": u, "password": "pass-" + u + "-1234", "role": role}, nil); st != 201 {
			t.Fatalf("create %s: %d", u, st)
		}
	}
	if st := h.do("PUT", "/v1/settings", map[string]string{"security.approval_actions": "volume.delete,bogus"}, nil); st != 400 {
		t.Fatalf("unknown action must be rejected: %d", st)
	}
	if st := h.do("PUT", "/v1/settings", map[string]string{"security.approval_actions": "volume.delete,disk.wipe"}, nil); st != 200 {
		t.Fatalf("settings: %d", st)
	}
	zero := int64(0)
	mkVol := func() core.Volume {
		p := filepath.Join(h.root, fmt.Sprintf("disk-%d", time.Now().UnixNano()))
		os.MkdirAll(p, 0o755)
		var res core.AttachResult
		if st := h.do("POST", "/v1/disks", core.AttachRequest{Path: p, MinFreeBytes: &zero}, &res); st != 201 {
			t.Fatalf("attach: %d", st)
		}
		var vol core.Volume
		if st := h.do("POST", "/v1/volumes", core.CreateVolumeRequest{Name: "pool", MinFreeBytes: &zero, DiskIDs: []string{res.Disk.ID}}, &vol); st != 201 {
			t.Fatalf("volume: %d", st)
		}
		return vol
	}
	vol := mkVol()
	store1 := tokenOf(login("store1", "pass-store1-1234", ""))
	sec1 := tokenOf(login("sec1", "pass-sec1-1234", ""))

	del := func(tok, approval string) (int, map[string]any) {
		req, _ := http.NewRequest("DELETE", h.srv.URL+"/v1/volumes/pool", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		if approval != "" {
			req.Header.Set("X-Approval-Id", approval)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	st, out := del(store1, "")
	if st != 403 || errCode(out) != auth.CodeApprovalRequired {
		t.Fatalf("first attempt must file a request: %d %v", st, out)
	}
	id := out["error"].(map[string]any)["info"].(map[string]any)["approval_id"].(string)
	if _, out2 := del(store1, ""); out2["error"].(map[string]any)["info"].(map[string]any)["approval_id"] != id {
		t.Fatal("repeat attempt must reuse the pending request")
	}
	if st, _ := del(store1, id); st != 403 {
		t.Fatalf("pending approval must not pass: %d", st)
	}
	// storage-admin cannot decide; the requester cannot approve their own (system-admin path)
	h.tok = store1
	if st := h.do("POST", "/v1/approvals/"+id+"/approve", map[string]any{}, nil); st != 403 {
		t.Fatalf("storage-admin decide: %d", st)
	}
	var list []auth.Approval
	h.tok = sec1
	h.do("GET", "/v1/approvals?state=pending", nil, &list)
	if len(list) != 1 || list[0].ID != id || list[0].Action != "volume.delete" || list[0].Target != vol.ID || list[0].RequestedBy != "store1" {
		t.Fatalf("list: %+v", list)
	}
	var ap auth.Approval
	if st := h.do("POST", "/v1/approvals/"+id+"/approve", map[string]any{}, &ap); st != 200 || ap.State != "approved" || ap.DecidedBy != "sec1" {
		t.Fatalf("approve: %d %+v", st, ap)
	}
	if st := h.do("POST", "/v1/approvals/"+id+"/reject", map[string]any{}, nil); st != 409 {
		t.Fatalf("decided twice: %d", st)
	}
	if st, _ := del(sec1, id); st != 403 {
		t.Fatalf("approval is bound to the requester: %d", st)
	}
	if st, out := del(store1, id); st != 200 {
		t.Fatalf("approved delete: %d %v", st, out)
	}
	h.tok = sec1
	h.do("GET", "/v1/approvals/"+id, nil, &ap)
	if ap.State != "used" {
		t.Fatalf("approval must be single-use: %+v", ap)
	}
	// self-approval refused
	h.tok = admin
	mkVol()
	_, out = del(admin, "")
	id2 := out["error"].(map[string]any)["info"].(map[string]any)["approval_id"].(string)
	if st := h.do("POST", "/v1/approvals/"+id2+"/approve", map[string]any{}, nil); st != 403 {
		t.Fatalf("self-approval: %d", st)
	}
	h.tok = sec1
	if st := h.do("POST", "/v1/approvals/"+id2+"/reject", map[string]any{}, nil); st != 200 {
		t.Fatalf("reject: %d", st)
	}
	if st, _ := del(admin, id2); st != 403 {
		t.Fatalf("rejected approval used: %d", st)
	}
}

// fakeIdP is a minimal OpenID Provider (discovery, token with PKCE check, JWKS).
type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	challenge string // from the authorize redirect
	nonce     string
	sub       string
	aud       string
	badSig    bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: k, sub: "alice@example.test", aud: "continuum"}
	m := http.NewServeMux()
	m.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	m.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		enc := base64.RawURLEncoding
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256",
			"n": enc.EncodeToString(k.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	})
	m.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		user, pass, _ := r.BasicAuth()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || user != "continuum" || pass != "placeholder-secret" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(map[string]any{"iss": f.srv.URL, "aud": f.aud, "sub": f.sub,
			"email": f.sub, "preferred_username": "alice", "nonce": f.nonce, "exp": time.Now().Add(5 * time.Minute).Unix()})})
	})
	f.srv = httptest.NewServer(m)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(claims map[string]any) string {
	enc := base64.RawURLEncoding
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	in := enc.EncodeToString(hb) + "." + enc.EncodeToString(pb)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if f.badSig {
		sig[0] ^= 0xff
	}
	return in + "." + enc.EncodeToString(sig)
}

// E29: OIDC Authorization Code + PKCE against an in-process IdP; JIT provisioning with the default role (FR-S02).
func TestOIDC(t *testing.T) {
	h, login := authHarness(t)
	h.tok = tokenOf(login("admin", "0123456789", ""))
	idp := newFakeIdP(t)
	var st map[string]any
	h.do("GET", "/v1/auth/status", nil, &st)
	if st["oidc"] != false {
		t.Fatalf("oidc off by default: %v", st)
	}
	var set map[string]string
	if code := h.do("PUT", "/v1/settings", map[string]string{"oidc.issuer": idp.srv.URL, "oidc.client_id": "continuum", "oidc.client_secret": "placeholder-secret",
		"oidc.redirect_url": h.srv.URL + "/v1/auth/oidc/callback", "oidc.default_role": "operator"}, &set); code != 200 || set["oidc.client_secret"] != masked {
		t.Fatalf("settings: %d %v", code, set)
	}
	h.do("PUT", "/v1/settings", map[string]string{"oidc.client_secret": masked}, nil) // echoed mask keeps the secret
	h.do("GET", "/v1/auth/status", nil, &st)
	if st["oidc"] != true {
		t.Fatalf("oidc on: %v", st)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := func(ret string) url.Values {
		res, err := noRedirect.Get(h.srv.URL + "/v1/auth/oidc/login?return=" + url.QueryEscape(ret))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != 302 || !strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize?") {
			t.Fatalf("login redirect: %d %s", res.StatusCode, loc)
		}
		q := loc.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "continuum" || q.Get("redirect_uri") != h.srv.URL+"/v1/auth/oidc/callback" {
			t.Fatalf("authorize params: %v", q)
		}
		idp.challenge, idp.nonce = q.Get("code_challenge"), q.Get("nonce")
		return q
	}
	callback := func(state, code string) *http.Response {
		res, err := noRedirect.Get(h.srv.URL + "/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(code))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	// happy path → redirect to the UI with the session token in the fragment
	q := start("/settings")
	res := callback(q.Get("state"), "good-code")
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, "/settings#oidc_token=cts_") {
		t.Fatalf("callback: %d %s", res.StatusCode, loc)
	}
	tok, _ := url.QueryUnescape(strings.SplitN(loc, "#oidc_token=", 2)[1])
	var me map[string]any
	saved := h.tok
	h.tok = tok
	if code := h.do("GET", "/v1/auth/me", nil, &me); code != 200 || me["username"] != "alice" || me["role"] != "operator" {
		t.Fatalf("sso session: %d %v", code, me)
	}
	h.tok = saved
	var us []auth.User
	h.do("GET", "/v1/users", nil, &us)
	found := false
	for _, u := range us {
		found = found || u.Username == "alice" && u.AuthSource == "oidc"
	}
	if !found {
		t.Fatalf("JIT user: %+v", us)
	}
	if st, _ := login("alice", "!oidc", ""); st != 403 {
		t.Fatalf("SSO users have no password: %d", st)
	}
	// state is single-use
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 403 {
		t.Fatalf("replayed state: %d", res.StatusCode)
	}
	// wrong PKCE / code, bad signature, wrong audience, wrong nonce, open redirect
	q = start("/")
	idp.challenge = "tampered"
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 403 {
		t.Fatalf("PKCE mismatch: %d", res.StatusCode)
	}
	q = start("/")
	idp.badSig = true
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 403 {
		t.Fatalf("bad signature: %d", res.StatusCode)
	}
	idp.badSig = false
	q = start("/")
	idp.aud = "someone-else"
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 403 {
		t.Fatalf("audience: %d", res.StatusCode)
	}
	idp.aud = "continuum"
	q = start("/")
	idp.nonce = "other"
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 403 {
		t.Fatalf("nonce: %d", res.StatusCode)
	}
	q = start("https://evil.example/steal")
	if res := callback(q.Get("state"), "good-code"); res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), "/#oidc_token=") {
		t.Fatalf("foreign return must fall back to /: %s", res.Header.Get("Location"))
	}
	q = start("http://localhost:5173/")
	if res := callback(q.Get("state"), "good-code"); !strings.HasPrefix(res.Header.Get("Location"), "http://localhost:5173/#oidc_token=") {
		t.Fatalf("allow-listed dev origin: %s", res.Header.Get("Location"))
	}
}
