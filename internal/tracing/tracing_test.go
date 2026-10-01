package tracing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type collector struct {
	mu    sync.Mutex
	spans []map[string]any
	auth  string
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []map[string]any `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []map[string]any `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	b, _ := io.ReadAll(r.Body)
	if r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/json" || json.Unmarshal(b, &req) != nil {
		w.WriteHeader(400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.auth = r.Header.Get("Authorization")
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			c.spans = append(c.spans, ss.Spans...)
		}
	}
}

func attr(sp map[string]any, k string) any {
	for _, a := range sp["attributes"].([]any) {
		m := a.(map[string]any)
		if m["key"] == k {
			for _, v := range m["value"].(map[string]any) {
				return v
			}
		}
	}
	return nil
}

func TestOTLPExport(t *testing.T) {
	col := &collector{}
	srv := httptest.NewServer(col)
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer%20placeholder")
	t.Setenv("OTEL_SERVICE_NAME", "ctm-test")
	exp := FromEnv()
	if exp == nil || exp.URL != srv.URL+"/v1/traces" || exp.Headers["Authorization"] != "Bearer placeholder" {
		t.Fatalf("env: %+v", exp)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Install(ctx, exp)
	defer Uninstall()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/disks/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, child := Start(r.Context(), "db.query", KindInternal)
		child.Set("rows", 3)
		child.End()
		w.WriteHeader(404)
	})
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	h := httptest.NewServer(Middleware(Route(mux)))
	defer h.Close()
	req, _ := http.NewRequest("GET", h.URL+"/v1/disks/dsk_123", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	http.DefaultClient.Do(req)
	http.Get(h.URL + "/boom")
	http.Get(h.URL + "/healthz") // not traced
	if err := exp.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	col.mu.Lock()
	defer col.mu.Unlock()
	if len(col.spans) != 3 || col.auth != "Bearer placeholder" {
		t.Fatalf("spans %d auth %q: %v", len(col.spans), col.auth, col.spans)
	}
	var server, child, boom map[string]any
	for _, s := range col.spans {
		switch s["name"] {
		case "GET /v1/disks/{id}":
			server = s
		case "db.query":
			child = s
		case "GET /boom":
			boom = s
		}
	}
	if server == nil || child == nil || boom == nil {
		t.Fatalf("names: %v", col.spans)
	}
	if server["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" || server["parentSpanId"] != "00f067aa0ba902b7" || server["kind"].(float64) != KindServer {
		t.Fatalf("server span must join the incoming trace: %v", server)
	}
	if child["traceId"] != server["traceId"] || child["parentSpanId"] != server["spanId"] {
		t.Fatalf("child: %v", child)
	}
	if attr(server, "http.response.status_code") != "404" || attr(server, "http.route") != "GET /v1/disks/{id}" || attr(child, "rows") != "3" {
		t.Fatalf("attrs: %v / %v", server["attributes"], child["attributes"])
	}
	if st, _ := boom["status"].(map[string]any); st == nil || st["code"].(float64) != 2 {
		t.Fatalf("5xx must be an error span: %v", boom)
	}
	if boom["traceId"] == server["traceId"] || boom["parentSpanId"] != nil {
		t.Fatal("request without traceparent starts a new root trace")
	}
}

func TestOffAndSampling(t *testing.T) {
	Uninstall()
	ctx, s := Start(context.Background(), "x", KindInternal)
	if s != nil || FromContext(ctx) != nil {
		t.Fatal("tracing off must be a no-op")
	}
	s.Set("k", 1) // nil-safe
	s.End()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	if FromEnv() != nil {
		t.Fatal("no endpoint → off")
	}
	n := 0
	for i := 0; i < 4000; i++ {
		var id [16]byte
		randBytes(id[:])
		if sample(id, 0.25) {
			n++
		}
	}
	if n < 800 || n > 1200 {
		t.Fatalf("ratio sampler: %d/4000", n)
	}
	if _, _, _, ok := parseTraceParent("00-00000000000000000000000000000000-00f067aa0ba902b7-01"); ok {
		t.Fatal("all-zero trace id is invalid")
	}
	if !strings.HasPrefix((&Span{sampled: true}).TraceParent(), "00-") {
		t.Fatal("traceparent")
	}
}
