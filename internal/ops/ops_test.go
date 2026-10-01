package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"continuum/internal/access"
	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
	"continuum/internal/transfer"
)

func TestNotifierTick(t *testing.T) {
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
	c.Audit.Emit(ctx, audit.Event{Kind: "job.failed", JobID: "cpy_old"}, map[string]any{"message": "history"}) // before start: not replayed
	n := NewNotifier(ctx, c)
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m["text"])
	}))
	defer hook.Close()
	c.SetSetting(ctx, "notify.webhook_url", hook.URL)
	c.Audit.Emit(ctx, audit.Event{Kind: "job.done", JobID: "cpy_1"}, nil) // not notable
	c.Audit.Emit(ctx, audit.Event{Kind: "job.failed", JobID: "cpy_2"}, map[string]any{"message": "3 file(s) failed"})
	c.Audit.Emit(ctx, audit.Event{Kind: "conflict.detected"}, map[string]any{"path": "/a.txt", "copies": 2, "kind": "conflict"})
	if sent := n.Tick(ctx); sent != 2 {
		t.Fatalf("sent %d: %v", sent, got)
	}
	if !strings.Contains(got[0], "Job cpy_2 failed: 3 file(s) failed") || !strings.Contains(got[1], "/a.txt exists on 2 disks") {
		t.Fatalf("messages: %v", got)
	}
	if n.Tick(ctx) != 0 {
		t.Fatal("events must be sent once")
	}
}

// E30: every metric queried by the shipped Grafana dashboard is exported by /metrics.
func TestGrafanaDashboard(t *testing.T) {
	b, err := os.ReadFile("../../deploy/grafana/continuum-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		UID    string `json:"uid"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if h, err := os.ReadFile("../../deploy/helm/continuum/dashboards/continuum-dashboard.json"); err != nil || string(h) != string(b) {
		t.Fatal("deploy/helm/continuum/dashboards/continuum-dashboard.json must be a copy of deploy/grafana/continuum-dashboard.json")
	}
	if err := json.Unmarshal(b, &d); err != nil || d.UID == "" || len(d.Panels) < 10 {
		t.Fatalf("dashboard: %v %d panels", err, len(d.Panels))
	}
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
	var out strings.Builder
	if err := Metrics(ctx, &out, c, transfer.New(c), access.New(c, t.TempDir())); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`continuum_[a-z_]+`)
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			for _, m := range re.FindAllString(tg.Expr, -1) {
				if !strings.Contains(out.String(), "# TYPE "+m+" ") {
					t.Errorf("panel %q queries %s which /metrics does not export", p.Title, m)
				}
			}
		}
	}
}
