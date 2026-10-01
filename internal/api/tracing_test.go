package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"continuum/internal/tracing"
)

// E30: the Controller API emits OTLP spans named after routes when an exporter is installed.
func TestAPITracing(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer col.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exp := &tracing.Exporter{URL: col.URL + "/v1/traces", Service: "t", Ratio: 1}
	tracing.Install(ctx, exp)
	defer tracing.Uninstall()
	h := newHarness(t, "")
	h.do("GET", "/v1/volumes", nil, nil)
	h.do("GET", "/v1/disks/nope", nil, nil)
	if err := exp.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(bodies, "")
	for _, want := range []string{`"name":"GET /v1/volumes"`, `"name":"GET /v1/disks/{id}"`, `"intValue":"404"`} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %s in %s", want, all)
		}
	}
}
