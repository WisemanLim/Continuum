package api

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type harness struct {
	t    *testing.T
	srv  *httptest.Server
	root string
	tok  string
}

func newHarness(t *testing.T, token string) *harness {
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
	s := &Server{C: c, E: e, Env: "test", Token: token, Origins: []string{"http://localhost:5173", "chrome-extension://*"}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); e.Shutdown(); db.Close() })
	return &harness{t: t, srv: ts, root: root, tok: token}
}

func (h *harness) do(method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.tok != "" {
		req.Header.Set("Authorization", "Bearer "+h.tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestAPIFlow(t *testing.T) {
	h := newHarness(t, "")
	var disks []string
	zero := int64(0)
	for _, n := range []string{"disk-01", "disk-02"} {
		p := filepath.Join(h.root, n)
		os.MkdirAll(p, 0o755)
		var res core.AttachResult
		if st := h.do("POST", "/v1/disks", core.AttachRequest{Path: p, MinFreeBytes: &zero}, &res); st != 201 {
			t.Fatalf("attach %s: %d", n, st)
		}
		disks = append(disks, res.Disk.ID)
	}
	var unassigned []core.Disk
	h.do("GET", "/v1/disks?unassigned=1", nil, &unassigned)
	if len(unassigned) != 2 {
		t.Fatalf("available storage %d", len(unassigned))
	}
	var vol core.Volume
	mf := int64(0)
	if st := h.do("POST", "/v1/volumes", core.CreateVolumeRequest{Name: "Research Pool", MinFreeBytes: &mf, DiskIDs: disks}, &vol); st != 201 {
		t.Fatalf("create volume %d", st)
	}
	// optimistic locking over HTTP → 409 revision_mismatch
	var e map[string]map[string]any
	if st := h.do("PUT", "/v1/volumes/research-pool/disks", core.SetDisksRequest{DiskIDs: []string{disks[1], disks[0]}, ExpectedRevision: vol.Revision + 5}, &e); st != 409 || e["error"]["code"] != core.CodeRevisionMismatch {
		t.Fatalf("stale revision: %d %v", st, e)
	}
	// plan + start + SSE watch
	src := filepath.Join(h.root, "src", "data")
	for i := 0; i < 20; i++ {
		os.MkdirAll(src, 0o755)
		os.WriteFile(filepath.Join(src, fmt.Sprintf("f%02d.txt", i)), bytes.Repeat([]byte("x"), 1000), 0o644)
	}
	var plan transfer.PlanResult
	if st := h.do("POST", "/v1/plan", transfer.PlanRequest{Volume: "research-pool", Source: src, Destination: "/data"}, &plan); st != 200 || plan.Files != 20 || !plan.Sufficient {
		t.Fatalf("plan %d %+v", st, plan.Plan)
	}
	var job transfer.Job
	if st := h.do("POST", "/v1/jobs", transfer.PlanRequest{Volume: "research-pool", Source: src, Destination: "/data"}, &job); st != 202 {
		t.Fatalf("start %d", st)
	}
	resp, err := http.Get(h.srv.URL + "/v1/jobs/" + job.ID + "/watch")
	if err != nil {
		t.Fatal(err)
	}
	final := ""
	sc := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(20 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			var p transfer.Progress
			json.Unmarshal([]byte(line[6:]), &p)
			final = p.State
		}
	}
	resp.Body.Close()
	if final != transfer.JobDone {
		t.Fatalf("SSE last state %q", final)
	}
	var files []core.Object
	h.do("GET", "/v1/volumes/research-pool/files?path=/data", nil, &files)
	if len(files) != 20 {
		t.Fatalf("files %d", len(files))
	}
	var loc []core.Object
	if st := h.do("GET", "/v1/volumes/research-pool/locate?path=/data/f00.txt", nil, &loc); st != 200 || loc[0].PhysicalPath == "" {
		t.Fatalf("locate %d %+v", st, loc)
	}
	var ov map[string]any
	h.do("GET", "/v1/overview", nil, &ov)
	if ov["tiles"].(map[string]any)["volumes"].(float64) != 1 {
		t.Fatalf("overview %v", ov["tiles"])
	}
	var ver map[string]any
	h.do("GET", "/v1/audit/verify", nil, &ver)
	if ver["intact"] != true {
		t.Fatal("audit chain")
	}
	if st := h.do("GET", "/v1/volumes/nope", nil, nil); st != 404 {
		t.Fatalf("unknown volume → %d", st)
	}
	// insufficient space → 409 insufficient_space
	big := int64(1) << 62
	if st := h.do("POST", "/v1/jobs", transfer.PlanRequest{Volume: "research-pool", Source: src, Destination: "/x", MinFreeBytes: &big}, &e); st != 409 || e["error"]["code"] != core.CodeInsufficientSpace {
		t.Fatalf("insufficient: %d %v", st, e)
	}
}

func TestSecurityGuards(t *testing.T) {
	h := newHarness(t, "s3cret")
	// no token
	resp, _ := http.Get(h.srv.URL + "/v1/volumes")
	if resp.StatusCode != 403 {
		t.Fatalf("missing token → %d", resp.StatusCode)
	}
	// health stays public
	resp, _ = http.Get(h.srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	// foreign origin refused even with token (browser-driven CSRF)
	req, _ := http.NewRequest("GET", h.srv.URL+"/v1/volumes", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 403 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("evil origin → %d", resp.StatusCode)
	}
	// allowed origins get CORS headers
	req.Header.Set("Origin", "chrome-extension://abcdef")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "chrome-extension://abcdef" {
		t.Fatalf("extension origin → %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	// same-origin requests (UI served by the controller) carry Origin = own host → allowed
	req.Header.Set("Origin", h.srv.URL)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("same origin → %d", resp.StatusCode)
	}
	// simple (non-JSON) POST is rejected → no CSRF via HTML forms
	req, _ = http.NewRequest("POST", h.srv.URL+"/v1/volumes/x/delete", strings.NewReader("path=/"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form POST → %d", resp.StatusCode)
	}
}

// E11/E12: SCAN + Conflict Center over HTTP.
func TestScanAndConflictsAPI(t *testing.T) {
	h := newHarness(t, "")
	zero := int64(0)
	var ids, roots []string
	for _, n := range []string{"a", "b"} {
		p := filepath.Join(h.root, n)
		os.MkdirAll(p, 0o755)
		var res core.AttachResult
		h.do("POST", "/v1/disks", core.AttachRequest{Path: p, MinFreeBytes: &zero}, &res)
		ids, roots = append(ids, res.Disk.ID), append(roots, res.Disk.DataRoot)
	}
	var vol core.Volume
	h.do("POST", "/v1/volumes", core.CreateVolumeRequest{Name: "pool", MinFreeBytes: &zero, DiskIDs: ids}, &vol)
	os.WriteFile(filepath.Join(roots[0], "x.txt"), []byte("one"), 0o644)
	os.WriteFile(filepath.Join(roots[1], "x.txt"), []byte("two!"), 0o644)
	var rep transfer.ScanReport
	if st := h.do("POST", "/v1/volumes/pool/scan", transfer.ScanOptions{}, &rep); st != 200 || len(rep.Added) != 2 || rep.Conflicts != 1 {
		t.Fatalf("scan %d %+v", st, rep)
	}
	var ov map[string]map[string]any
	h.do("GET", "/v1/overview", nil, &ov)
	if ov["tiles"]["conflicts"] != float64(1) {
		t.Fatalf("overview conflicts tile: %v", ov["tiles"]["conflicts"])
	}
	var cs []transfer.Conflict
	h.do("GET", "/v1/volumes/pool/conflicts?state=open", nil, &cs)
	if len(cs) != 1 || cs[0].Kind != "conflict" || len(cs[0].Candidates) != 2 {
		t.Fatalf("conflicts %+v", cs)
	}
	var bad map[string]map[string]any
	if st := h.do("POST", "/v1/conflicts/"+cs[0].ID+"/resolve", map[string]string{"keep": "nope"}, &bad); st != 400 {
		t.Fatalf("invalid keep: %d", st)
	}
	var done transfer.Conflict
	if st := h.do("POST", "/v1/conflicts/"+cs[0].ID+"/resolve", map[string]string{"keep": "b"}, &done); st != 200 || done.State != "resolved" {
		t.Fatalf("resolve %d %+v", st, done)
	}
	if b, _ := os.ReadFile(filepath.Join(roots[0], "x.txt")); b != nil {
		t.Fatal("copy on disk a must be trashed")
	}
	var n map[string]int
	if st := h.do("POST", "/v1/volumes/pool/conflicts/resolve-duplicates", map[string]any{}, &n); st != 200 || n["resolved"] != 0 {
		t.Fatalf("resolve-duplicates %d %v", st, n)
	}
}

// E16: metrics, diagnostics bundle (masked, no secrets), settings, notifications + webhook.
func TestOpsEndpoints(t *testing.T) {
	h := newHarness(t, "")
	zero := int64(0)
	p := filepath.Join(h.root, "ops-disk")
	os.MkdirAll(p, 0o755)
	var res core.AttachResult
	h.do("POST", "/v1/disks", core.AttachRequest{Path: p, Name: "ops-1", MinFreeBytes: &zero}, &res)
	h.do("POST", "/v1/volumes", core.CreateVolumeRequest{Name: "Ops", MinFreeBytes: &zero, DiskIDs: []string{res.Disk.ID}}, nil)

	r, _ := http.Get(h.srv.URL + "/metrics")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	for _, want := range []string{"# TYPE continuum_volume_bytes gauge", `continuum_disk_up{disk="ops-1",volume="ops",ordinal="1"} 1`, "continuum_queue_depth 0", "continuum_conflicts_open 0", "continuum_build_info"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}

	var bad map[string]map[string]any
	if st := h.do("PUT", "/v1/settings", map[string]string{"notify.webhook_url": "ftp://x"}, &bad); st != 400 {
		t.Fatalf("invalid webhook url: %d", st)
	}
	if st := h.do("PUT", "/v1/settings", map[string]string{"nope": "1"}, &bad); st != 400 {
		t.Fatalf("unknown key: %d", st)
	}
	got := make(chan map[string]any, 10)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer hook.Close()
	var set map[string]string
	if st := h.do("PUT", "/v1/settings", map[string]string{"notify.webhook_url": hook.URL, "notify.format": "discord"}, &set); st != 200 || set["notify.format"] != "discord" {
		t.Fatalf("settings: %d %v", st, set)
	}
	if st := h.do("POST", "/v1/notifications/test", map[string]any{}, nil); st != 200 {
		t.Fatalf("test notification: %d", st)
	}
	if m := <-got; !strings.Contains(fmt.Sprint(m["content"]), "test notification") {
		t.Fatalf("discord payload: %v", m)
	}

	// a disk going offline → notification center + webhook
	os.RemoveAll(p)
	h.do("POST", "/v1/disks/refresh", map[string]any{}, nil)
	var notes []map[string]any
	h.do("GET", "/v1/notifications", nil, &notes)
	if len(notes) == 0 || notes[0]["kind"] != "disk.offline" || !strings.Contains(fmt.Sprint(notes[0]["message"]), "ops-1 went offline") {
		t.Fatalf("notifications: %v", notes)
	}

	// diagnostics: zip, masked paths, secrets redacted
	r, _ = http.Get(h.srv.URL + "/v1/diagnostics")
	zb, _ := io.ReadAll(r.Body)
	r.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		all += f.Name + "\n" + string(b)
	}
	for _, want := range []string{"disks.json", "volumes.json", "events.json", "settings.json", `"notify.webhook_url": "<redacted>"`, "<path:"} {
		if !strings.Contains(all, want) {
			t.Fatalf("diagnostics missing %q", want)
		}
	}
	if strings.Contains(all, h.root) || strings.Contains(all, hook.URL) {
		t.Fatal("diagnostics must not contain raw paths or the webhook URL")
	}
}

func TestBrowse(t *testing.T) {
	h := newHarness(t, "")
	os.MkdirAll(filepath.Join(h.root, "pick", "Beta"), 0o755)
	os.MkdirAll(filepath.Join(h.root, "pick", "alpha"), 0o755)
	os.MkdirAll(filepath.Join(h.root, "pick", ".hidden"), 0o755)
	os.WriteFile(filepath.Join(h.root, "pick", "f.txt"), []byte("x"), 0o644)
	var out struct {
		Path   string `json:"path"`
		Parent string `json:"parent"`
		Dirs   []struct{ Name, Path string }
		Files  int `json:"files"`
	}
	if st := h.do("GET", "/v1/fs/browse?path="+url.QueryEscape(filepath.Join(h.root, "pick")), nil, &out); st != 200 {
		t.Fatalf("browse %d", st)
	}
	if len(out.Dirs) != 2 || out.Dirs[0].Name != "alpha" || out.Files != 1 || out.Parent != h.root {
		t.Fatalf("browse: %+v", out)
	}
	if st := h.do("GET", "/v1/fs/browse?path=relative", nil, nil); st != 400 {
		t.Fatalf("relative path: %d", st)
	}
}

// E22: bootstrap, login, RBAC matrix, lockout, last-admin guard, automation token, audit actor (FR-S01, S02).
func TestAuthRBAC(t *testing.T) {
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
	a.Iterations = 1000 // fast tests
	s := &Server{C: c, E: e, Env: "test", Token: "automation-secret", Auth: a, Origins: []string{"http://localhost:5173"}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); e.Shutdown(); db.Close() })
	h := &harness{t: t, srv: ts, root: root, tok: "automation-secret"} // single-user mode: CONTINUUM_TOKEN gates

	var st map[string]any
	h.do("GET", "/v1/auth/status", nil, &st)
	if st["enabled"] != false {
		t.Fatalf("status: %v", st)
	}
	if code := h.do("POST", "/v1/users", map[string]string{"username": "op", "password": "0123456789", "role": "operator"}, nil); code != 400 {
		t.Fatalf("first user must be system admin: %d", code)
	}
	if code := h.do("POST", "/v1/users", map[string]string{"username": "admin", "password": "0123456789", "role": "system-admin"}, nil); code != 201 {
		t.Fatalf("bootstrap admin: %d", code)
	}
	login := func(user, pw string) string {
		var out struct{ Token string }
		h.tok = ""
		if code := h.do("POST", "/v1/auth/login", map[string]string{"username": user, "password": pw}, &out); code != 200 {
			return ""
		}
		return out.Token
	}
	h.tok = ""
	if code := h.do("GET", "/v1/volumes", nil, nil); code != 401 {
		t.Fatalf("auth mode without token: %d", code)
	}
	admin := login("admin", "0123456789")
	if admin == "" {
		t.Fatal("admin login")
	}
	h.tok = admin
	for _, u := range []struct{ name, role string }{{"viewer1", "viewer"}, {"op1", "operator"}, {"store1", "storage-admin"}, {"sec1", "security-admin"}} {
		if code := h.do("POST", "/v1/users", map[string]string{"username": u.name, "password": "pass-" + u.name + "-1234", "role": u.role}, nil); code != 201 {
			t.Fatalf("create %s: %d", u.name, code)
		}
	}
	tokens := map[string]string{"system-admin": admin}
	for _, u := range []struct{ name, role string }{{"viewer1", "viewer"}, {"op1", "operator"}, {"store1", "storage-admin"}, {"sec1", "security-admin"}} {
		tokens[u.role] = login(u.name, "pass-"+u.name+"-1234")
	}
	// matrix: (method, path, body) → allowed roles
	cases := []struct {
		method, path string
		body         any
		allowed      []string
	}{
		{"GET", "/v1/volumes", nil, []string{"viewer", "operator", "storage-admin", "security-admin", "system-admin"}},
		{"GET", "/v1/audit", nil, []string{"security-admin", "system-admin"}},
		{"GET", "/v1/users", nil, []string{"security-admin", "system-admin"}},
		{"GET", "/v1/settings", nil, []string{"system-admin"}},
		{"POST", "/v1/plan", transfer.PlanRequest{Volume: "nope", Source: root, Destination: "/x"}, []string{"operator", "storage-admin", "system-admin"}},
		{"POST", "/v1/volumes", core.CreateVolumeRequest{Name: ""}, []string{"storage-admin", "system-admin"}},
		{"POST", "/v1/nodes/tokens", map[string]any{}, []string{"security-admin", "system-admin"}},
	}
	for _, cs := range cases {
		for role, tok := range tokens {
			h.tok = tok
			code := h.do(cs.method, cs.path, cs.body, nil)
			allowed := false
			for _, a := range cs.allowed {
				if a == role {
					allowed = true
				}
			}
			if allowed && (code == 403 || code == 401) || !allowed && code != 403 {
				t.Errorf("%s %s as %s → %d (allowed=%v)", cs.method, cs.path, role, code, allowed)
			}
		}
	}
	// DRAIN via /v1/jobs needs storage even though /v1/jobs is a transfer route
	h.tok = tokens["operator"]
	if code := h.do("POST", "/v1/jobs", transfer.PlanRequest{Type: transfer.TypeDrain, Volume: "x", Source: "y"}, nil); code != 403 {
		t.Fatalf("operator drain: %d", code)
	}
	// automation token = system admin
	h.tok = "automation-secret"
	if code := h.do("GET", "/v1/settings", nil, nil); code != 200 {
		t.Fatalf("automation token: %d", code)
	}
	// me + audit actor
	h.tok = tokens["storage-admin"]
	var me map[string]any
	h.do("GET", "/v1/auth/me", nil, &me)
	if me["username"] != "store1" || me["role"] != "storage-admin" {
		t.Fatalf("me: %v", me)
	}
	h.do("POST", "/v1/volumes", core.CreateVolumeRequest{Name: "Owned"}, nil)
	var aud []map[string]any
	h.tok = admin
	h.do("GET", "/v1/audit?limit=50", nil, &aud)
	found := false
	for _, en := range aud {
		if en["actor"] == "store1" {
			found = true
		}
	}
	if !found {
		t.Fatal("audit must name the acting user")
	}
	// last admin cannot be demoted / disabled / deleted
	var users []map[string]any
	h.do("GET", "/v1/users", nil, &users)
	var adminID string
	for _, u := range users {
		if u["username"] == "admin" {
			adminID = u["user_id"].(string)
		}
	}
	role := "viewer"
	if code := h.do("PATCH", "/v1/users/"+adminID, auth.Update{Role: &role}, nil); code != 409 {
		t.Fatalf("demote last admin: %d", code)
	}
	if code := h.do("DELETE", "/v1/users/"+adminID, nil, nil); code != 409 {
		t.Fatalf("delete last admin: %d", code)
	}
	// disabling a user ends their sessions
	dis := true
	h.do("PATCH", "/v1/users/op1", auth.Update{Disabled: &dis}, nil)
	h.tok = tokens["operator"]
	if code := h.do("GET", "/v1/volumes", nil, nil); code != 401 {
		t.Fatalf("disabled user session: %d", code)
	}
	// lockout after 5 failures (even the right password is refused while locked)
	for i := 0; i < 5; i++ {
		login("viewer1", "wrong-password")
	}
	if login("viewer1", "pass-viewer1-1234") != "" {
		t.Fatal("account must be locked")
	}
	// logout
	h.tok = tokens["security-admin"]
	h.do("POST", "/v1/auth/logout", map[string]any{}, nil)
	if code := h.do("GET", "/v1/volumes", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}
