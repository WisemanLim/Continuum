package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"continuum/internal/agentproto"
	"continuum/internal/tracing"
)

// E36: a Controller → agent command is one trace: the hub's CLIENT span carries a traceparent, the agent's
// SERVER span continues it (same trace id, parent = client span), and both reach the OTLP collector.
func TestCommandTracePropagation(t *testing.T) {
	var mu sync.Mutex
	var spans []map[string]any
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []map[string]any `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		json.Unmarshal(b, &req)
		mu.Lock()
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				spans = append(spans, ss.Spans...)
			}
		}
		mu.Unlock()
	}))
	defer col.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exp := &tracing.Exporter{URL: col.URL + "/v1/traces", Service: "t", Ratio: 1}
	tracing.Install(ctx, exp)
	defer tracing.Uninstall()

	r := newRig(t)
	_, node := r.startPeerAgent(t.TempDir(), false)
	rootCtx, root := tracing.Start(r.ctx, "test root", tracing.KindInternal)
	var fi agentproto.FileInfo
	if err := r.h.Call(rootCtx, node, agentproto.OpStat, agentproto.PathArgs{Path: os.TempDir()}, &fi); err != nil {
		t.Fatal(err)
	}
	if err := r.h.Call(rootCtx, node, agentproto.OpStat, agentproto.PathArgs{Path: "/definitely/missing"}, &fi); err == nil {
		t.Fatal("missing path must fail")
	}
	root.End()
	exp.Flush(ctx)

	mu.Lock()
	defer mu.Unlock()
	var clients, servers []map[string]any
	for _, s := range spans {
		if s["name"] != "agent stat" {
			continue
		}
		if s["kind"].(float64) == tracing.KindClient {
			clients = append(clients, s)
		} else if s["kind"].(float64) == tracing.KindServer {
			servers = append(servers, s)
		}
	}
	if len(clients) != 2 || len(servers) != 2 {
		t.Fatalf("spans: %d client %d server (%v)", len(clients), len(servers), spans)
	}
	for i, c := range clients {
		var srv map[string]any
		for _, s := range servers {
			if s["parentSpanId"] == c["spanId"] {
				srv = s
			}
		}
		if srv == nil || srv["traceId"] != c["traceId"] {
			t.Fatalf("client %d has no agent span continuing it: %v / %v", i, c, servers)
		}
		tp := strings.Split(root.TraceParent(), "-") // 00-<trace>-<span>-01
		if c["traceId"] != tp[1] || c["parentSpanId"] != tp[2] {
			t.Fatalf("client span must be a child of the caller's span %s: %v", root.TraceParent(), c)
		}
	}
	failed := 0
	for _, s := range append(clients, servers...) {
		if st, _ := s["status"].(map[string]any); st != nil && st["code"].(float64) == 2 {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("the failing stat must be an error span on both sides: %d", failed)
	}
}
