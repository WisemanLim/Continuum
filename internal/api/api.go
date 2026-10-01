// Package api is the Controller REST/JSON + SSE API (PRD §13.4, FR-C03).
// GUI, CLI and Chrome Extension all call these endpoints (DP05).
package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"continuum/internal/access"
	"continuum/internal/audit"
	"continuum/internal/auth"
	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/hub"
	"continuum/internal/ops"
	"continuum/internal/tracing"
	"continuum/internal/transfer"
	"continuum/internal/version"
	"continuum/internal/vfs"
)

// Server holds dependencies.
type Server struct {
	C       *core.Core
	E       *transfer.Engine
	Env     string
	Token   string   // optional bearer token (FR-S02 local token)
	Origins []string // allowed CORS origins (exact, or prefix ending with '*')
	WebDir  string   // optional static web UI (apps/web/dist)
	A       *access.Manager
	N       *ops.Notifier
	H       *hub.Hub // nil when the agent listener is disabled (single-machine mode)
	Auth    *auth.Service
	OIDC    *auth.OIDC // SSO (FR-S02); configured through oidc.* settings

	vfsMu  sync.Mutex
	mounts map[string]*mount // mountpoint → native VFS (FR-A06)
}

type mount struct {
	fs         *vfs.FS
	Volume     string `json:"volume"`
	Mountpoint string `json:"mountpoint"`
	ReadOnly   bool   `json:"read_only"`
	Since      string `json:"since"`
}

// Handler builds the HTTP handler.
func (s *Server) Handler() http.Handler {
	if s.A == nil {
		s.A = access.New(s.C, filepath.Join(os.TempDir(), "continuum-access-"+s.C.ControllerID))
	}
	if s.N == nil {
		s.N = ops.NewNotifier(context.Background(), s.C)
	}
	if s.Auth == nil {
		s.Auth = auth.New(s.C)
	}
	if s.OIDC == nil {
		s.OIDC = auth.NewOIDC(s.Auth)
	}
	m := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		writeJSON(w, 200, map[string]string{"status": "ok", "env": s.Env, "version": version.Version})
	}
	m.HandleFunc("GET /healthz", health)
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.C.DB.PingContext(r.Context()); err != nil {
			writeJSON(w, 503, map[string]string{"status": "db unavailable"})
			return
		}
		health(w, r)
	})

	h := func(fn func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			out, err := fn(w, r)
			if err != nil {
				writeErr(w, err)
				return
			}
			if out != nil {
				status := 200
				if r.Method == http.MethodPost {
					status = 201
				}
				if sc, ok := out.(statusResp); ok {
					status, out = sc.status, sc.body
				}
				writeJSON(w, status, out)
			}
		}
	}

	m.HandleFunc("GET /v1/info", h(s.info))
	m.HandleFunc("GET /v1/overview", h(s.overview))
	m.HandleFunc("GET /v1/nodes", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.C.Nodes(r.Context()) }))

	// disks (FR-D)
	// authentication & users (FR-S01, S02)
	m.HandleFunc("GET /v1/auth/status", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return map[string]any{"enabled": s.Auth.Enabled(r.Context()), "roles": auth.Roles, "matrix": auth.Matrix, "oidc": s.OIDC.Enabled(r.Context()), "ldap": s.Auth.LDAPEnabled(r.Context())}, nil
	}))
	m.HandleFunc("POST /v1/auth/login", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Username, Password, Code, Client string }
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return ok200(s.Auth.Login(r.Context(), in.Username, in.Password, in.Code, in.Client))
	}))
	// SSO: browser is redirected to the IdP, comes back to /callback, lands on the UI with #oidc_token=
	m.HandleFunc("GET /v1/auth/oidc/login", func(w http.ResponseWriter, r *http.Request) {
		ret := r.URL.Query().Get("return")
		if !s.returnAllowed(ret, r.Host) {
			ret = "/"
		}
		u, err := s.OIDC.LoginURL(r.Context(), ret)
		if err != nil {
			writeErr(w, err)
			return
		}
		http.Redirect(w, r, u, http.StatusFound)
	})
	m.HandleFunc("GET /v1/auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeErr(w, core.Errf(core.CodePermission, "SSO failed: %s %s", e, q.Get("error_description")))
			return
		}
		sess, ret, err := s.OIDC.Callback(r.Context(), q.Get("state"), q.Get("code"))
		if err != nil {
			writeErr(w, err)
			return
		}
		http.Redirect(w, r, ret+"#oidc_token="+url.QueryEscape(sess.Token), http.StatusFound)
	})
	// TOTP MFA for the caller's own account (FR-S02)
	m.HandleFunc("POST /v1/auth/mfa/{op}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Code string }
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		u, err := s.Auth.Lookup(r.Context(), actor(r))
		if err != nil || !s.Auth.Enabled(r.Context()) {
			return nil, core.Errf(core.CodeInvalid, "MFA needs a user account (log in first)")
		}
		switch r.PathValue("op") {
		case "setup":
			return ok200(s.Auth.SetupMFA(r.Context(), u.ID))
		case "enable":
			return ok200(map[string]bool{"mfa_enabled": true}, s.Auth.EnableMFA(r.Context(), u.ID, in.Code))
		case "disable":
			return ok200(map[string]bool{"mfa_enabled": false}, s.Auth.DisableMFA(r.Context(), u.ID, in.Code))
		}
		return nil, core.Errf(core.CodeNotFound, "unknown MFA operation")
	}))
	// two-person approval (FR-S05)
	m.HandleFunc("GET /v1/approvals", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.Auth.Approvals(r.Context(), r.URL.Query().Get("state"))
	}))
	m.HandleFunc("GET /v1/approvals/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.Auth.Approval(r.Context(), r.PathValue("id"))
	}))
	for _, op := range []string{"approve", "reject"} {
		m.HandleFunc("POST /v1/approvals/{id}/"+op, h(func(w http.ResponseWriter, r *http.Request) (any, error) {
			return ok200(s.Auth.Decide(r.Context(), r.PathValue("id"), actor(r), op == "approve"))
		}))
	}
	m.HandleFunc("POST /v1/auth/logout", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		s.Auth.Logout(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return ok200(map[string]bool{"ok": true}, nil)
	}))
	m.HandleFunc("GET /v1/auth/me", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		role := roleOf(r)
		perms := []string{}
		for _, p := range []string{auth.PermRead, auth.PermTransfer, auth.PermStorage, auth.PermSecurity, auth.PermSystem} {
			if auth.Can(role, p) {
				perms = append(perms, p)
			}
		}
		out := map[string]any{"username": actor(r), "role": role, "permissions": perms, "auth_enabled": s.Auth.Enabled(r.Context()), "mfa_enabled": false}
		if u, err := s.Auth.Lookup(r.Context(), actor(r)); err == nil {
			out["mfa_enabled"], out["auth_source"] = u.MFAEnabled, u.AuthSource
		}
		return out, nil
	}))
	m.HandleFunc("PUT /v1/auth/password", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Old string `json:"old_password"`
			New string `json:"new_password"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		us, err := s.Auth.List(r.Context())
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			if u.Username == actor(r) {
				return ok200(map[string]bool{"ok": true}, s.Auth.ChangePassword(r.Context(), u.ID, in.Old, in.New))
			}
		}
		return nil, core.Errf(core.CodeInvalid, "no user session")
	}))
	m.HandleFunc("GET /v1/users", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.Auth.List(r.Context()) }))
	m.HandleFunc("POST /v1/users", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct{ Username, Password, Role string }
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.Auth.Create(r.Context(), in.Username, in.Password, in.Role, actor(r))
	}))
	m.HandleFunc("PATCH /v1/users/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in auth.Update
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.Auth.Patch(r.Context(), r.PathValue("id"), in, actor(r))
	}))
	m.HandleFunc("DELETE /v1/users/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if u, err := s.Auth.Lookup(r.Context(), r.PathValue("id")); err == nil {
			if err := s.gate(r, "user.delete", u.ID); err != nil {
				return nil, err
			}
		}
		return ok200(map[string]bool{"deleted": true}, s.Auth.Delete(r.Context(), r.PathValue("id"), actor(r)))
	}))

	// operations (FR-O01, O04, D26, G16)
	m.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := ops.Metrics(r.Context(), &buf, s.C, s.E, s.A); err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write(buf.Bytes())
	})
	m.HandleFunc("GET /v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		maskPaths := r.URL.Query().Get("mask") != "0"
		var buf bytes.Buffer
		if err := ops.Diagnostics(r.Context(), &buf, s.C, s.E, s.A, maskPaths); err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="continuum-diagnostics-`+time.Now().UTC().Format("20060102-150405")+`.zip"`)
		w.Write(buf.Bytes())
	})
	m.HandleFunc("GET /v1/notifications", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		evs, err := s.C.Audit.Events(r.Context(), "", 500)
		if err != nil {
			return nil, err
		}
		names := ops.DiskNames(r.Context(), s.C)
		type note struct {
			audit.Event
			Message string `json:"message"`
		}
		out := []note{}
		for _, ev := range evs {
			if ops.IsNotable(ev.Kind) && len(out) < 50 {
				out = append(out, note{ev, ops.Message(ev, names)})
			}
		}
		return out, nil
	}))
	m.HandleFunc("POST /v1/notifications/test", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		url := s.C.Setting(r.Context(), "notify.webhook_url", "")
		mail, mailOn := ops.EmailSettings(r.Context(), s.C)
		if url == "" && !mailOn {
			return nil, core.Errf(core.CodeInvalid, "set notify.webhook_url or notify.smtp_host + notify.email_to first")
		}
		out := map[string]any{"sent": true}
		if url != "" {
			if err := s.N.Send(r.Context(), url, s.C.Setting(r.Context(), "notify.format", "slack"), "test notification", audit.Event{Kind: "test"}); err != nil {
				return nil, core.Errf(core.CodeOffline, "webhook: %v", err)
			}
			out["webhook"] = true
		}
		if mailOn {
			if err := ops.SendEmail(r.Context(), mail, "Continuum: test notification", []string{"test notification"}); err != nil {
				return nil, core.Errf(core.CodeOffline, "e-mail: %v", err)
			}
			out["email"] = true
		}
		return ok200(out, nil)
	}))
	m.HandleFunc("GET /v1/settings", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.settings(r.Context()), nil
	}))
	m.HandleFunc("PUT /v1/settings", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in map[string]string
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		for k, v := range in {
			if !slices.Contains(settingKeys, k) {
				return nil, core.Errf(core.CodeInvalid, "unknown setting %q", k)
			}
			if k == "notify.format" && v != "" && v != "slack" && v != "discord" && v != "generic" {
				return nil, core.Errf(core.CodeInvalid, "notify.format must be slack|discord|generic")
			}
			if k == "notify.webhook_url" && v != "" && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
				return nil, core.Errf(core.CodeInvalid, "notify.webhook_url must be an http(s) URL")
			}
			if k == "security.approval_actions" {
				for _, a := range strings.Split(v, ",") {
					if a = strings.TrimSpace(a); a != "" && !slices.Contains(auth.ApprovalActions, a) {
						return nil, core.Errf(core.CodeInvalid, "security.approval_actions: %q is not one of %s", a, strings.Join(auth.ApprovalActions, ", "))
					}
				}
			}
			if k == "notify.smtp_tls" && v != "" && v != "starttls" && v != "tls" && v != "none" {
				return nil, core.Errf(core.CodeInvalid, "notify.smtp_tls must be starttls|tls|none")
			}
			if k == "ldap.url" && v != "" && !strings.HasPrefix(v, "ldap://") && !strings.HasPrefix(v, "ldaps://") {
				return nil, core.Errf(core.CodeInvalid, "ldap.url must be ldap://host:389 or ldaps://host:636")
			}
			if k == "ldap.user_filter" && v != "" && strings.Count(v, "%s") != 1 {
				return nil, core.Errf(core.CodeInvalid, "ldap.user_filter needs exactly one %%s for the username")
			}
			if k == "ldap.role_map" {
				for _, kv := range strings.Split(v, ";") {
					if i := strings.LastIndex(kv, "="); strings.TrimSpace(kv) != "" && (i <= 0 || !slices.Contains(auth.Roles, strings.TrimSpace(kv[i+1:]))) {
						return nil, core.Errf(core.CodeInvalid, "ldap.role_map entry %q must be <group DN>=<role>", kv)
					}
				}
			}
			if k == "ldap.default_role" && v != "" && !slices.Contains(auth.Roles, v) {
				return nil, core.Errf(core.CodeInvalid, "ldap.default_role must be one of %s", strings.Join(auth.Roles, ", "))
			}
			if k == "oidc.default_role" && v != "" && !slices.Contains(auth.Roles, v) {
				return nil, core.Errf(core.CodeInvalid, "oidc.default_role must be one of %s", strings.Join(auth.Roles, ", "))
			}
		}
		for k, v := range in {
			if v == masked {
				continue // UI echoed the mask back: keep the stored secret
			}
			if err := s.C.SetSetting(r.Context(), k, v); err != nil {
				return nil, err
			}
		}
		s.C.Audit.Append(r.Context(), audit.Entry{Action: "settings.update", Target: strings.Join(slices.Sorted(maps.Keys(in)), ","), Result: "ok"})
		return s.settings(r.Context()), nil
	}))

	// native VFS (FR-A06, Linux FUSE)
	m.HandleFunc("GET /v1/vfs", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.vfsList(), nil }))
	m.HandleFunc("POST /v1/vfs/mount", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Volume     string `json:"volume"`
			Mountpoint string `json:"mountpoint"`
			ReadOnly   bool   `json:"read_only"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		if in.Volume == "" || !filepath.IsAbs(in.Mountpoint) {
			return nil, core.Errf(core.CodeInvalid, "volume and an absolute mountpoint are required")
		}
		s.vfsMu.Lock()
		defer s.vfsMu.Unlock()
		if s.mounts == nil {
			s.mounts = map[string]*mount{}
		}
		if _, ok := s.mounts[in.Mountpoint]; ok {
			return nil, core.Errf(core.CodeConflict, "%s is already mounted", in.Mountpoint)
		}
		f, err := vfs.Mount(s.C, s.E, in.Volume, in.Mountpoint, in.ReadOnly)
		if err != nil {
			return nil, err
		}
		mt := &mount{fs: f, Volume: in.Volume, Mountpoint: in.Mountpoint, ReadOnly: in.ReadOnly, Since: time.Now().UTC().Format(time.RFC3339)}
		s.mounts[in.Mountpoint] = mt
		return mt, nil
	}))
	m.HandleFunc("POST /v1/vfs/unmount", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Mountpoint string `json:"mountpoint"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		s.vfsMu.Lock()
		defer s.vfsMu.Unlock()
		mt, ok := s.mounts[in.Mountpoint]
		if !ok {
			return nil, core.Errf(core.CodeNotFound, "%s is not mounted", in.Mountpoint)
		}
		if err := mt.fs.Unmount(); err != nil {
			return nil, core.Errf(core.CodeConflict, "unmount %s: %v (files still open?)", in.Mountpoint, err)
		}
		delete(s.mounts, in.Mountpoint)
		return ok200(map[string]bool{"unmounted": true}, nil)
	}))

	// SMB access point (FR-A01~A05, A08)
	m.HandleFunc("GET /v1/access", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.access().Status(r.Context()), nil }))
	m.HandleFunc("GET /v1/access/credentials", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.access().Credentials(r.Context()) }))
	m.HandleFunc("POST /v1/access/enable", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Bind string `json:"bind"`
			Port int    `json:"port"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.access().Enable(r.Context(), in.Bind, in.Port)
	}))
	m.HandleFunc("POST /v1/access/disable", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.access().Disable(r.Context()) }))
	m.HandleFunc("POST /v1/access/reload", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.access().Reload(r.Context()) }))

	m.HandleFunc("GET /v1/fs/browse", h(s.browse))

	// distributed agents (FR-N01, N05, N09)
	m.HandleFunc("POST /v1/nodes/tokens", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if s.H == nil {
			return nil, core.Errf(core.CodeInvalid, "agent listener disabled (CONTINUUM_AGENT_ADDR=off)")
		}
		var in struct {
			Label  string `json:"label"`
			TTLMin int    `json:"ttl_min"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return s.H.CreateToken(r.Context(), in.Label, actor(r), time.Duration(in.TTLMin)*time.Minute)
	}))
	m.HandleFunc("POST /v1/nodes/{id}/revoke", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if s.H == nil {
			return nil, core.Errf(core.CodeInvalid, "agent listener disabled")
		}
		if err := s.gate(r, "node.revoke", r.PathValue("id")); err != nil {
			return nil, err
		}
		return ok200(map[string]bool{"revoked": true}, s.H.Revoke(r.Context(), r.PathValue("id"), actor(r)))
	}))
	m.HandleFunc("GET /v1/nodes/{id}/inventory", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		id := r.PathValue("id")
		if id == s.C.LocalNode.ID || s.H == nil {
			ds, err := s.C.Discover(r.Context())
			return map[string]any{"online": true, "disks": ds, "roots": []string{}}, err
		}
		inv, _ := s.H.Inventory(id)
		disks := inv.Disks
		if disks == nil {
			disks = []disk.Descriptor{}
		}
		roots := inv.Roots
		if roots == nil {
			roots = []string{}
		}
		return map[string]any{"online": s.H.Online(id), "disks": disks, "roots": roots}, nil
	}))
	m.HandleFunc("GET /v1/disks", h(s.listDisks))
	m.HandleFunc("GET /v1/disks/discover", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.C.Discover(r.Context()) }))
	m.HandleFunc("POST /v1/disks/preflight", h(s.preflight))
	m.HandleFunc("POST /v1/disks", h(s.attach))
	m.HandleFunc("POST /v1/disks/refresh", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		s.C.RefreshAll(r.Context())
		return s.C.ListDisks(r.Context(), core.DiskFilter{})
	}))
	m.HandleFunc("GET /v1/disks/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.C.GetDisk(r.Context(), r.PathValue("id"))
	}))
	m.HandleFunc("PATCH /v1/disks/{id}", h(s.patchDisk))
	// disk detail (FR-D13~D16, D27)
	m.HandleFunc("POST /v1/disks/{id}/drain", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in transfer.PlanRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		d, err := s.C.GetDisk(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if d.VolumeID == "" {
			return nil, core.Errf(core.CodeInvalid, "disk %s is not in a volume", d.Name)
		}
		if err := s.gate(r, "disk.drain", d.ID); err != nil {
			return nil, err
		}
		in.Type, in.Volume, in.Source = transfer.TypeDrain, d.VolumeID, d.ID
		j, err := s.E.Start(r.Context(), in)
		if err != nil {
			return nil, err
		}
		return statusResp{http.StatusAccepted, j}, nil
	}))
	m.HandleFunc("GET /v1/disks/{id}/smart", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Smart(r.Context(), r.PathValue("id"), r.URL.Query().Get("refresh") != "")
	}))
	m.HandleFunc("GET /v1/disks/{id}/smart/history", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.SmartHistory(r.Context(), r.PathValue("id"), atoi(r.URL.Query().Get("limit")))
	}))
	m.HandleFunc("GET /v1/disks/{id}/usage", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Usage(r.Context(), "", r.PathValue("id"), atoi(r.URL.Query().Get("top")), atoi(r.URL.Query().Get("depth")))
	}))
	m.HandleFunc("GET /v1/disks/{id}/dupes", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		d, err := s.C.GetDisk(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if d.VolumeID == "" {
			return nil, core.Errf(core.CodeInvalid, "disk %s is not in a volume", d.Name)
		}
		return s.E.Dupes(r.Context(), d.VolumeID, d.ID, int64(atoi(r.URL.Query().Get("min_size"))))
	}))
	m.HandleFunc("GET /v1/disks/{id}/bench", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.BenchHistory(r.Context(), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/disks/{id}/bench", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			SizeBytes int64 `json:"size_bytes"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return ok200(s.E.Bench(r.Context(), r.PathValue("id"), in.SizeBytes))
	}))
	m.HandleFunc("POST /v1/disks/{id}/wipe", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Confirm string `json:"confirm"`
			Passes  int    `json:"passes"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		if d, err := s.C.GetDisk(r.Context(), r.PathValue("id")); err == nil {
			if err := s.gate(r, "disk.wipe", d.ID); err != nil {
				return nil, err
			}
		}
		return ok200(s.E.Wipe(r.Context(), r.PathValue("id"), in.Confirm, in.Passes))
	}))
	m.HandleFunc("POST /v1/disks/{id}/reveal", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		d, err := s.C.GetDisk(r.Context(), r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		if d.NodeID != s.C.LocalNode.ID {
			return nil, core.Errf(core.CodeInvalid, "disk %s is on another node", d.Name)
		}
		if err := disk.Reveal(d.DataRoot); err != nil {
			return nil, core.Errf(core.CodeOffline, "%v", err)
		}
		return map[string]string{"opened": d.DataRoot}, nil
	}))
	m.HandleFunc("GET /v1/volumes/{v}/local-mount", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		v, err := s.vol(r)
		if err != nil {
			return nil, err
		}
		mp := r.URL.Query().Get("mountpoint")
		if mp == "" {
			mp = "/mnt/continuum/" + v.Slug
		}
		return access.RenderLocalMount(v, s.C.LocalNode.ID, mp)
	}))
	m.HandleFunc("GET /v1/volumes/{v}/usage", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Usage(r.Context(), r.PathValue("v"), "", atoi(r.URL.Query().Get("top")), atoi(r.URL.Query().Get("depth")))
	}))
	m.HandleFunc("GET /v1/volumes/{v}/dupes", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Dupes(r.Context(), r.PathValue("v"), r.URL.Query().Get("disk"), int64(atoi(r.URL.Query().Get("min_size"))))
	}))

	m.HandleFunc("POST /v1/disks/{id}/detach", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return ok200(s.C.Detach(r.Context(), r.PathValue("id")))
	}))
	m.HandleFunc("POST /v1/disks/{id}/enable", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return ok200(s.C.Enable(r.Context(), r.PathValue("id")))
	}))
	m.HandleFunc("DELETE /v1/disks/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return map[string]bool{"deleted": true}, s.C.Unregister(r.Context(), r.PathValue("id"))
	}))

	// volumes (FR-V)
	m.HandleFunc("GET /v1/volumes", h(func(w http.ResponseWriter, r *http.Request) (any, error) { return s.C.ListVolumes(r.Context()) }))
	m.HandleFunc("POST /v1/volumes", h(s.createVolume))
	m.HandleFunc("POST /v1/volumes/import", h(s.importVolume))
	m.HandleFunc("GET /v1/volumes/{v}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.C.GetVolume(r.Context(), r.PathValue("v"))
	}))
	m.HandleFunc("PATCH /v1/volumes/{v}", h(s.patchVolume))
	m.HandleFunc("DELETE /v1/volumes/{v}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if err := s.gate(r, "volume.delete", s.volumeID(r, r.PathValue("v"))); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, s.C.DeleteVolume(r.Context(), r.PathValue("v"))
	}))
	m.HandleFunc("PUT /v1/volumes/{v}/disks", h(s.setDisks))
	m.HandleFunc("GET /v1/volumes/{v}/export", s.exportVolume)

	// catalog / file operations (FR-K, FR-T11~T14)
	m.HandleFunc("GET /v1/volumes/{v}/files", h(s.listFiles))
	m.HandleFunc("GET /v1/volumes/{v}/locate", h(s.locate))
	m.HandleFunc("POST /v1/volumes/{v}/mkdir", h(s.mkdir))
	m.HandleFunc("POST /v1/volumes/{v}/rename", h(s.rename))
	m.HandleFunc("POST /v1/volumes/{v}/delete", h(s.delete))
	m.HandleFunc("POST /v1/volumes/{v}/verify", h(s.verify))
	m.HandleFunc("GET /v1/volumes/{v}/trash", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Trash(r.Context(), r.PathValue("v"))
	}))
	m.HandleFunc("POST /v1/volumes/{v}/trash/purge", h(s.purge))
	m.HandleFunc("POST /v1/volumes/{v}/scan", h(s.scan))

	// Conflict Center (FR-K04, FR-G15)
	m.HandleFunc("GET /v1/volumes/{v}/conflicts", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		v, err := s.vol(r)
		if err != nil {
			return nil, err
		}
		return s.E.Conflicts(r.Context(), v.ID, r.URL.Query().Get("state"))
	}))
	m.HandleFunc("POST /v1/volumes/{v}/conflicts/resolve-duplicates", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		n, err := s.E.ResolveDuplicates(r.Context(), r.PathValue("v"))
		return ok200(map[string]int{"resolved": n}, err)
	}))
	m.HandleFunc("POST /v1/conflicts/{id}/resolve", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Keep string `json:"keep"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return ok200(s.E.ResolveConflict(r.Context(), r.PathValue("id"), in.Keep))
	}))
	m.HandleFunc("POST /v1/trash/{id}/restore", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return ok200(s.E.Restore(r.Context(), r.PathValue("id")))
	}))

	// jobs (FR-T, FR-P07)
	m.HandleFunc("POST /v1/plan", h(s.plan))
	m.HandleFunc("POST /v1/jobs", h(s.startJob))
	m.HandleFunc("GET /v1/jobs", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.List(r.Context(), r.URL.Query().Get("state"), s.volumeID(r, r.URL.Query().Get("volume")), atoi(r.URL.Query().Get("limit")))
	}))
	m.HandleFunc("GET /v1/jobs/{id}", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Get(r.Context(), r.PathValue("id"))
	}))
	m.HandleFunc("GET /v1/jobs/{id}/items", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.E.Items(r.Context(), r.PathValue("id"), r.URL.Query().Get("state"))
	}))
	m.HandleFunc("GET /v1/jobs/{id}/errors", s.jobErrors)
	for _, act := range []string{"pause", "resume", "cancel", "retry"} {
		act := act
		m.HandleFunc("POST /v1/jobs/{id}/"+act, h(func(w http.ResponseWriter, r *http.Request) (any, error) {
			var j *transfer.Job
			var err error
			switch act {
			case "pause":
				j, err = s.E.Pause(r.Context(), r.PathValue("id"))
			case "resume":
				j, err = s.E.Resume(r.Context(), r.PathValue("id"))
			case "cancel":
				j, err = s.E.Cancel(r.Context(), r.PathValue("id"))
			case "retry":
				j, err = s.E.Retry(r.Context(), r.PathValue("id"))
			}
			return ok200(j, err)
		}))
	}
	m.HandleFunc("GET /v1/jobs/{id}/watch", s.watch)
	m.HandleFunc("GET /v1/events/stream", s.watch)

	// activity / audit (FR-G06, FR-K06)
	m.HandleFunc("GET /v1/activity", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		limit := atoi(r.URL.Query().Get("limit"))
		if limit == 0 {
			limit = 100
		}
		return s.C.Audit.Events(r.Context(), r.URL.Query().Get("kind"), limit)
	}))
	m.HandleFunc("GET /v1/audit", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		limit := atoi(r.URL.Query().Get("limit"))
		if limit == 0 {
			limit = 100
		}
		return s.C.Audit.List(r.Context(), limit)
	}))
	m.HandleFunc("GET /v1/audit/verify", h(func(w http.ResponseWriter, r *http.Request) (any, error) {
		bad, err := s.C.Audit.Verify(r.Context())
		return map[string]any{"intact": bad == 0, "first_broken_seq": bad}, err
	}))

	if s.WebDir != "" {
		if _, err := os.Stat(filepath.Join(s.WebDir, "index.html")); err == nil {
			m.Handle("GET /", spa(s.WebDir))
		}
	}
	return tracing.Middleware(s.guard(tracing.Route(m)))
}

type statusResp struct {
	status int
	body   any
}

func ok200(v any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return statusResp{200, v}, nil
}

// guard applies CORS (allowed origins only), JSON-only mutations (blocks CSRF via simple requests) and bearer auth.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin, r.Host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-Approval-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		api := strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/metrics"
		if api && origin != "" && !s.originAllowed(origin, r.Host) {
			writeErr(w, core.Errf(core.CodePermission, "origin %s not allowed", origin))
			return
		}
		if api {
			nr, err := s.authorize(r)
			if err != nil {
				writeErr(w, err)
				return
			}
			r = nr
		}
		if api && r.Method != http.MethodGet && r.URL.Path != "/v1/volumes/import" {
			if ct := r.Header.Get("Content-Type"); r.ContentLength != 0 && !strings.HasPrefix(ct, "application/json") {
				writeJSON(w, http.StatusUnsupportedMediaType, errBody(core.Errf(core.CodeInvalid, "Content-Type must be application/json")))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authorize authenticates the caller and checks the RBAC matrix (FR-S01, S02).
// Without users: the optional CONTINUUM_TOKEN is the only gate (single-user mode).
func (s *Server) authorize(r *http.Request) (*http.Request, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		tok = r.URL.Query().Get("token") // EventSource / download links cannot send headers
	}
	path := r.URL.Path
	with := func(user, role string) *http.Request {
		ctx := audit.WithActor(context.WithValue(r.Context(), actorKey{}, user), user)
		return r.WithContext(context.WithValue(ctx, roleKey{}, role))
	}
	if !s.Auth.Enabled(r.Context()) {
		if s.Token != "" && tok != s.Token && path != "/v1/auth/status" {
			return nil, core.Errf(core.CodePermission, "missing or invalid token")
		}
		return with("local", auth.SystemAdmin), nil
	}
	if path == "/v1/auth/status" || (path == "/v1/auth/login" && r.Method == http.MethodPost) || strings.HasPrefix(path, "/v1/auth/oidc/") {
		return r, nil
	}
	var user, role string
	switch {
	case tok == "":
		return nil, core.Errf(core.CodeUnauthenticated, "login required")
	case s.Token != "" && tok == s.Token: // automation token = System Admin
		user, role = "token", auth.SystemAdmin
	default:
		u, ok := s.Auth.Resolve(r.Context(), tok)
		if !ok {
			return nil, core.Errf(core.CodeUnauthenticated, "session expired — log in again")
		}
		user, role = u.Username, u.Role
	}
	if perm := auth.Required(r.Method, path); !auth.Can(role, perm) {
		return nil, core.Errf(core.CodePermission, "role %s lacks the %q permission for %s %s", role, perm, r.Method, path)
	}
	return with(user, role), nil
}

type roleKey struct{}

func roleOf(r *http.Request) string {
	if v, ok := r.Context().Value(roleKey{}).(string); ok {
		return v
	}
	return auth.SystemAdmin
}

// originAllowed: same origin (UI served by this Controller) or an allow-listed origin.
func (s *Server) originAllowed(o, host string) bool {
	if u, err := url.Parse(o); err == nil && u.Host == host && (u.Scheme == "http" || u.Scheme == "https") {
		return true
	}
	for _, a := range s.Origins {
		if a == o || (strings.HasSuffix(a, "*") && strings.HasPrefix(o, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+r.URL.Path)))
		if _, err := os.Stat(p); err != nil {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// ---- handlers ----

func (s *Server) access() *access.Manager { return s.A }

func (s *Server) vfsList() []*mount {
	s.vfsMu.Lock()
	defer s.vfsMu.Unlock()
	out := []*mount{}
	for _, m := range s.mounts {
		out = append(out, m)
	}
	return out
}

// UnmountAll detaches native VFS mounts (controller shutdown).
func (s *Server) UnmountAll() {
	s.vfsMu.Lock()
	defer s.vfsMu.Unlock()
	for k, m := range s.mounts {
		m.fs.Unmount()
		delete(s.mounts, k)
	}
}

// actor names the caller for the audit log (RBAC users from E22; "local" in single-user mode).
func actor(r *http.Request) string {
	if u, ok := r.Context().Value(actorKey{}).(string); ok && u != "" {
		return u
	}
	return "local"
}

type actorKey struct{}

// settingKeys are the user-editable settings (GET/PUT /v1/settings).
var settingKeys = []string{"notify.webhook_url", "notify.format", "security.approval_actions",
	"notify.smtp_host", "notify.smtp_user", "notify.smtp_password", "notify.smtp_tls", "notify.email_from", "notify.email_to",
	"oidc.issuer", "oidc.client_id", "oidc.client_secret", "oidc.redirect_url", "oidc.default_role", "oidc.username_claim",
	"ldap.url", "ldap.starttls", "ldap.bind_dn", "ldap.bind_password", "ldap.user_base", "ldap.user_filter", "ldap.group_attr", "ldap.role_map", "ldap.default_role", "ldap.ca_file"}

const masked = "********" // write-only settings are never returned

func (s *Server) settings(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, k := range settingKeys {
		out[k] = s.C.Setting(ctx, k, "")
		if (k == "oidc.client_secret" || k == "notify.smtp_password" || k == "ldap.bind_password") && out[k] != "" {
			out[k] = masked
		}
	}
	return out
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) (any, error) {
	nodes, err := s.C.Nodes(r.Context())
	if err != nil {
		return nil, err
	}
	online, total := 0, 0
	for _, n := range nodes {
		if n.State == "REVOKED" {
			continue // revoked nodes are not expected to come back
		}
		total++
		if n.State == "ONLINE" {
			online++
		}
	}
	mode := "M1"
	if total > 1 {
		mode = "M2"
	}
	return map[string]any{"controller_id": s.C.ControllerID, "version": version.Version, "env": s.Env, "mode": mode,
		"local_node": s.C.LocalNode, "agents_online": online, "agents_total": total}, nil
}

// overview is the 8-tile dashboard (FR-G02).
func (s *Server) overview(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	vols, err := s.C.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	disks, err := s.C.ListDisks(ctx, core.DiskFilter{})
	if err != nil {
		return nil, err
	}
	nodes, _ := s.C.Nodes(ctx)
	running, _ := s.E.List(ctx, transfer.JobRunning, "", 100)
	var total, free int64
	offline, warnings := 0, []string{}
	for _, d := range disks {
		if d.VolumeID != "" {
			total += d.TotalBytes
			free += d.EffectiveFree
		}
		if d.State == core.StateOffline || d.State == core.StateFailed {
			offline++
			warnings = append(warnings, fmt.Sprintf("disk %s is %s", d.Name, d.State))
		}
		if d.Warning == "capacity-critical" || d.Warning == "capacity-high" {
			warnings = append(warnings, fmt.Sprintf("disk %s is %d%% full", d.Name, d.UsedPct))
		}
	}
	for _, v := range vols {
		warnings = append(warnings, v.Warnings...)
	}
	online := 0
	for _, n := range nodes {
		if n.State == "ONLINE" {
			online++
		}
	}
	recent, _ := s.C.Audit.Events(ctx, "", 10)
	return map[string]any{
		"tiles": map[string]any{"total_bytes": total, "free_bytes": free, "volumes": len(vols), "nodes_online": online, "nodes_total": len(nodes),
			"running_jobs": len(running), "warnings": len(warnings), "offline_disks": offline, "conflicts": s.E.OpenConflicts(ctx)},
		"volumes": vols, "warnings": warnings, "recent": recent,
	}, nil
}

func (s *Server) listDisks(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	return s.C.ListDisks(r.Context(), core.DiskFilter{VolumeID: s.volumeID(r, q.Get("volume")), Unassigned: q.Get("unassigned") == "1" || q.Get("unassigned") == "true"})
}

func (s *Server) volumeID(r *http.Request, ref string) string {
	if ref == "" {
		return ""
	}
	if v, err := s.C.GetVolume(r.Context(), ref); err == nil {
		return v.ID
	}
	return ref
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Path         string `json:"path"`
		MinFreeBytes int64  `json:"min_free_bytes"`
		VolumeID     string `json:"volume_id"`
		NodeID       string `json:"node_id"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, core.Errf(core.CodeInvalid, "path required")
	}
	if in.NodeID != "" && in.NodeID != s.C.LocalNode.ID { // runs on the agent (FR-D04 for remote disks)
		if s.C.Remote == nil {
			return nil, core.Errf(core.CodeInvalid, "agent listener disabled")
		}
		rep, _, _, err := s.C.Remote.Probe(r.Context(), in.NodeID, in.Path, uint64(max(in.MinFreeBytes, 0)), s.C.ControllerID, in.VolumeID)
		if err != nil {
			return nil, core.Errf(core.CodeOffline, "node %s: %v", in.NodeID, err)
		}
		return statusResp{200, rep}, nil
	}
	return statusResp{200, s.C.Preflight(r.Context(), in.Path, in.MinFreeBytes, in.VolumeID)}, nil
}

func (s *Server) attach(w http.ResponseWriter, r *http.Request) (any, error) {
	var in core.AttachRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.VolumeID != "" {
		in.VolumeID = s.volumeID(r, in.VolumeID)
	}
	return s.C.Attach(r.Context(), in)
}

func (s *Server) patchDisk(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Role         *string `json:"role"`
		MinFreeBytes *int64  `json:"min_free_bytes"`
		ClearMinFree bool    `json:"clear_min_free"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	id := r.PathValue("id")
	d, err := s.C.GetDisk(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if in.Role != nil {
		if d, err = s.C.SetRole(r.Context(), id, *in.Role); err != nil {
			return nil, err
		}
	}
	if in.MinFreeBytes != nil || in.ClearMinFree {
		if d, err = s.C.SetMinFree(r.Context(), id, in.MinFreeBytes); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) (any, error) {
	var in core.CreateVolumeRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.C.CreateVolume(r.Context(), in)
}

func (s *Server) patchVolume(w http.ResponseWriter, r *http.Request) (any, error) {
	var in core.UpdateVolumeRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.C.UpdateVolume(r.Context(), r.PathValue("v"), in)
}

func (s *Server) setDisks(w http.ResponseWriter, r *http.Request) (any, error) {
	var in core.SetDisksRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.C.SetDisks(r.Context(), r.PathValue("v"), in)
}

func (s *Server) exportVolume(w http.ResponseWriter, r *http.Request) {
	b, err := s.C.ExportVolume(r.Context(), r.PathValue("v"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.yaml"`, r.PathValue("v")))
	w.Write(b)
}

func (s *Server) importVolume(w http.ResponseWriter, r *http.Request) (any, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return s.C.ImportVolume(r.Context(), b)
}

func (s *Server) vol(r *http.Request) (core.Volume, error) {
	return s.C.GetVolume(r.Context(), r.PathValue("v"))
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) (any, error) {
	v, err := s.vol(r)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	p := q.Get("path")
	if p == "" {
		p = "/"
	}
	diskID := ""
	if ref := q.Get("disk"); ref != "" {
		d, err := s.C.GetDisk(r.Context(), ref)
		if err != nil {
			return nil, err
		}
		diskID = d.ID
	} else if o := atoi(q.Get("ordinal")); o > 0 {
		for _, d := range v.Disks {
			if d.Ordinal == o {
				diskID = d.ID
			}
		}
		if diskID == "" {
			return nil, core.Errf(core.CodeNotFound, "no disk with ordinal %d", o)
		}
	}
	return s.C.ListDir(r.Context(), v.ID, p, diskID)
}

func (s *Server) locate(w http.ResponseWriter, r *http.Request) (any, error) {
	v, err := s.vol(r)
	if err != nil {
		return nil, err
	}
	return s.C.Locate(r.Context(), v.ID, r.URL.Query().Get("path"))
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Path    string `json:"path"`
		Ordinal int    `json:"ordinal"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.E.Mkdir(r.Context(), r.PathValue("v"), in.Path, in.Ordinal)
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct{ From, To string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if err := s.E.Rename(r.Context(), r.PathValue("v"), in.From, in.To); err != nil {
		return nil, err
	}
	return statusResp{200, map[string]string{"from": in.From, "to": in.To}}, nil
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Permanent bool   `json:"permanent"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return ok200(s.E.Delete(r.Context(), r.PathValue("v"), in.Path, in.Recursive, in.Permanent))
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Path string `json:"path"`
		Disk string `json:"disk"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return ok200(s.E.Verify(r.Context(), r.PathValue("v"), in.Path, in.Disk))
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) (any, error) {
	var in transfer.ScanOptions
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return ok200(s.E.Scan(r.Context(), r.PathValue("v"), in))
}

func (s *Server) purge(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		OlderThanDays int `json:"older_than_days"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	n, err := s.E.Purge(r.Context(), r.PathValue("v"), time.Duration(in.OlderThanDays)*24*time.Hour)
	return ok200(map[string]int{"purged": n}, err)
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) (any, error) {
	var in transfer.PlanRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return ok200(s.E.Plan(r.Context(), in))
}

// gate enforces two-person approval; the retry carries the approved id in X-Approval-Id.
func (s *Server) gate(r *http.Request, action, target string) error {
	return s.Auth.Gate(r.Context(), actor(r), action, target, r.Header.Get("X-Approval-Id"))
}

// returnAllowed accepts a same-origin path or an allow-listed origin as the post-SSO landing page.
func (s *Server) returnAllowed(ret, host string) bool {
	if strings.HasPrefix(ret, "/") && !strings.HasPrefix(ret, "//") && !strings.HasPrefix(ret, "/\\") {
		return true
	}
	u, err := url.Parse(ret)
	return err == nil && u.Host != "" && s.originAllowed(u.Scheme+"://"+u.Host, host)
}

func needStorage(r *http.Request, typ string) error {
	if (typ == transfer.TypeDrain || typ == transfer.TypeRebalance) && !auth.Can(roleOf(r), auth.PermStorage) {
		return core.Errf(core.CodePermission, "%s needs the storage permission", typ)
	}
	return nil
}

func (s *Server) startJob(w http.ResponseWriter, r *http.Request) (any, error) {
	var in transfer.PlanRequest
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if k := r.Header.Get("Idempotency-Key"); k != "" && in.IdempotencyKey == "" {
		in.IdempotencyKey = k
	}
	if err := needStorage(r, in.Type); err != nil {
		return nil, err
	}
	if in.Type == transfer.TypeDrain {
		if d, err := s.C.GetDisk(r.Context(), in.Source); err == nil {
			if err := s.gate(r, "disk.drain", d.ID); err != nil {
				return nil, err
			}
		}
	}
	j, err := s.E.Start(r.Context(), in)
	if err != nil {
		return nil, err
	}
	return statusResp{http.StatusAccepted, j}, nil
}

// jobErrors exports failed items as JSON or CSV (FR-T21).
func (s *Server) jobErrors(w http.ResponseWriter, r *http.Request) {
	items, err := s.E.Items(r.Context(), r.PathValue("id"), "failed")
	if err != nil {
		writeErr(w, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		cw := csv.NewWriter(w)
		cw.Write([]string{"item_no", "logical_path", "source", "size", "error"})
		for _, it := range items {
			cw.Write([]string{strconv.Itoa(it.No), it.LogicalPath, it.Source, strconv.FormatInt(it.Size, 10), it.Error})
		}
		cw.Flush()
		return
	}
	writeJSON(w, 200, items)
}

// watch streams progress as Server-Sent Events (FR-T09 WatchJobs).
func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, errors.New("streaming unsupported"))
		return
	}
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	ch, stop := s.E.Subscribe(id)
	defer stop()
	if id != "" {
		j, err := s.E.Get(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if j.Progress != nil {
			send(j.Progress)
		}
		if j.State != transfer.JobRunning && j.State != transfer.JobQueued {
			return
		}
	}
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case p := <-ch:
			send(p)
			if id != "" && p.State != transfer.JobRunning && p.State != transfer.JobQueued && p.State != "" {
				return
			}
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ---- helpers ----

func decode(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return core.Errf(core.CodeInvalid, "invalid JSON: %v", err)
	}
	return nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// StatusFor maps domain codes to HTTP status.
func StatusFor(code string) int {
	switch code {
	case core.CodeInvalid:
		return 400
	case core.CodePermission, auth.CodeApprovalRequired:
		return 403
	case core.CodeUnauthenticated:
		return 401
	case core.CodeNotFound:
		return 404
	case core.CodeInsufficientSpace, core.CodeOffline, core.CodeConflict, core.CodeRevisionMismatch:
		return 409
	case core.CodeVerifyFailed:
		return 422
	}
	return 500
}

func errBody(err error) map[string]any {
	var ce *core.Error
	if errors.As(err, &ce) {
		return map[string]any{"error": ce}
	}
	return map[string]any{"error": map[string]string{"code": "internal", "message": err.Error()}}
}

func writeErr(w http.ResponseWriter, err error) {
	code := core.CodeOf(err)
	if code == "" {
		log.Printf("api: internal error: %v", err)
	}
	writeJSON(w, StatusFor(code), errBody(err))
}

// Background runs periodic capacity refresh + heartbeat (FR-D12: 30 s).
func (s *Server) Background(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	nt := time.NewTicker(5 * time.Second) // notifications: near real time
	defer nt.Stop()
	st := time.NewTicker(time.Hour) // SMART trend (FR-D13)
	defer st.Stop()
	go s.E.SmartAll(ctx)
	for {
		select {
		case <-nt.C:
			s.N.Tick(ctx)
		case <-st.C:
			s.E.SmartAll(ctx)
		case <-t.C:
			s.C.RefreshAll(ctx)
			s.C.Heartbeat(ctx)
			s.E.ScanDue(ctx, time.Now())
			s.E.Recover(ctx) // adopt jobs whose worker died (expired lease, central mode)
			s.E.WindowDue(ctx, time.Now())
			s.A.Sync(ctx) // recreate the SMB container when volumes/disks changed
		case <-ctx.Done():
			return
		}
	}
}
