// Package tracing exports OpenTelemetry traces (FR-O01 observability) without the OTel SDK: spans are batched
// and posted as OTLP/HTTP JSON to any collector (OpenTelemetry Collector, Jaeger, Tempo, Honeycomb…).
// Standard environment: OTEL_EXPORTER_OTLP_TRACES_ENDPOINT (full URL) or OTEL_EXPORTER_OTLP_ENDPOINT (+ /v1/traces),
// OTEL_EXPORTER_OTLP_HEADERS (k=v,k2=v2), OTEL_SERVICE_NAME (default continuum-controller), OTEL_TRACES_SAMPLER_ARG
// (ratio 0..1, default 1). Without an endpoint tracing is off and Start costs almost nothing.
// Incoming W3C traceparent headers are honoured, so a span joins the caller's trace.
package tracing

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"continuum/internal/version"
)

// Span kinds (OTLP enum).
const (
	KindInternal = 1
	KindServer   = 2
	KindClient   = 3
)

// Span is one timed operation.
type Span struct {
	exp      *Exporter
	traceID  [16]byte
	spanID   [8]byte
	parent   [8]byte
	sampled  bool
	mu       sync.Mutex
	name     string
	kind     int
	start    time.Time
	attrs    map[string]any
	errMsg   string
	finished bool
}

// Exporter batches finished spans.
type Exporter struct {
	URL     string
	Headers map[string]string
	Service string
	Ratio   float64
	Client  *http.Client

	mu    sync.Mutex
	queue []map[string]any
	kick  chan struct{}
	Sent  int // spans delivered (tests, diagnostics)
}

var global atomic.Pointer[Exporter]

// FromEnv builds an exporter from the standard OTEL_* variables; nil when no endpoint is set.
func FromEnv() *Exporter {
	u := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if u == "" {
		if base := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); base != "" {
			u = strings.TrimRight(base, "/") + "/v1/traces"
		}
	}
	if u == "" || os.Getenv("OTEL_TRACES_EXPORTER") == "none" || os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return nil
	}
	e := &Exporter{URL: u, Headers: map[string]string{}, Service: os.Getenv("OTEL_SERVICE_NAME"), Ratio: 1}
	if e.Service == "" {
		e.Service = "continuum-controller"
	}
	for _, kv := range strings.Split(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			k, _ = url.QueryUnescape(strings.TrimSpace(k))
			v, _ = url.QueryUnescape(strings.TrimSpace(v))
			e.Headers[k] = v
		}
	}
	if r, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64); err == nil && r >= 0 && r <= 1 {
		e.Ratio = r
	}
	return e
}

// Uninstall turns tracing off (tests).
func Uninstall() { global.Store(nil) }

// Install makes e the process exporter and starts its flush loop (every 2 s or 256 spans) until ctx ends.
func Install(ctx context.Context, e *Exporter) {
	if e == nil {
		return
	}
	if e.Client == nil {
		e.Client = &http.Client{Timeout: 10 * time.Second}
	}
	e.kick = make(chan struct{}, 1)
	global.Store(e)
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				e.Flush(context.Background())
				return
			case <-t.C:
			case <-e.kick:
			}
			e.Flush(context.WithoutCancel(ctx)) // a shutdown must not abort an export in flight
		}
	}()
}

type ctxKey struct{}

// FromContext returns the active span (nil when none).
func FromContext(ctx context.Context) *Span {
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}

func randBytes(b []byte) { rand.Read(b) }

// Start begins a span under the span in ctx (or a new trace). With tracing off it returns ctx unchanged and nil;
// every *Span method accepts a nil receiver.
func Start(ctx context.Context, name string, kind int) (context.Context, *Span) {
	e := global.Load()
	if e == nil {
		return ctx, nil
	}
	s := &Span{exp: e, name: name, kind: kind, start: time.Now(), attrs: map[string]any{}}
	if p := FromContext(ctx); p != nil {
		s.traceID, s.parent, s.sampled = p.traceID, p.spanID, p.sampled
	} else {
		randBytes(s.traceID[:])
		s.sampled = sample(s.traceID, e.Ratio)
	}
	randBytes(s.spanID[:])
	return context.WithValue(ctx, ctxKey{}, s), s
}

// sample is TraceIdRatioBased: deterministic on the trace id so all services agree.
func sample(id [16]byte, ratio float64) bool {
	if ratio >= 1 {
		return true
	}
	return float64(binary.BigEndian.Uint64(id[8:])>>11)/float64(1<<53) < ratio
}

// StartRemote begins a span that continues the trace named by a W3C traceparent (e.g. a Controller → agent
// command); an empty or invalid header starts a new trace.
func StartRemote(ctx context.Context, traceparent, name string, kind int) (context.Context, *Span) {
	if tid, sid, sampled, ok := parseTraceParent(traceparent); ok {
		ctx = context.WithValue(ctx, ctxKey{}, &Span{traceID: tid, spanID: sid, sampled: sampled})
	}
	return Start(ctx, name, kind)
}

// SetName renames the span (e.g. to the matched route).
func (s *Span) SetName(n string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.name = n
	s.mu.Unlock()
}

// Set adds an attribute (string, bool, int/int64, float64).
func (s *Span) Set(k string, v any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs[k] = v
	s.mu.Unlock()
}

// Fail marks the span status as error.
func (s *Span) Fail(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.errMsg = msg
	s.mu.Unlock()
}

// TraceParent renders the W3C header for outgoing calls.
func (s *Span) TraceParent() string {
	if s == nil {
		return ""
	}
	f := "00"
	if s.sampled {
		f = "01"
	}
	return "00-" + hex.EncodeToString(s.traceID[:]) + "-" + hex.EncodeToString(s.spanID[:]) + "-" + f
}

// End finishes the span and queues it for export.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	end := time.Now()
	attrs := make([]map[string]any, 0, len(s.attrs))
	for k, v := range s.attrs {
		attrs = append(attrs, map[string]any{"key": k, "value": anyValue(v)})
	}
	sp := map[string]any{"traceId": hex.EncodeToString(s.traceID[:]), "spanId": hex.EncodeToString(s.spanID[:]), "name": s.name, "kind": s.kind,
		"startTimeUnixNano": strconv.FormatInt(s.start.UnixNano(), 10), "endTimeUnixNano": strconv.FormatInt(end.UnixNano(), 10), "attributes": attrs}
	if s.parent != [8]byte{} {
		sp["parentSpanId"] = hex.EncodeToString(s.parent[:])
	}
	if s.errMsg != "" {
		sp["status"] = map[string]any{"code": 2, "message": s.errMsg}
	}
	s.mu.Unlock()
	if !s.sampled {
		return
	}
	e := s.exp
	e.mu.Lock()
	if len(e.queue) < 8192 { // drop instead of growing without bound when the collector is down
		e.queue = append(e.queue, sp)
	}
	full := len(e.queue) >= 256
	e.mu.Unlock()
	if full {
		select {
		case e.kick <- struct{}{}:
		default:
		}
	}
}

func anyValue(v any) map[string]any {
	switch x := v.(type) {
	case string:
		return map[string]any{"stringValue": x}
	case bool:
		return map[string]any{"boolValue": x}
	case int:
		return map[string]any{"intValue": strconv.Itoa(x)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(x, 10)}
	case float64:
		return map[string]any{"doubleValue": x}
	}
	return map[string]any{"stringValue": fmt.Sprint(v)}
}

// Flush posts queued spans (one OTLP ExportTraceServiceRequest).
func (e *Exporter) Flush(ctx context.Context) error {
	e.mu.Lock()
	batch := e.queue
	e.queue = nil
	e.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	host, _ := os.Hostname()
	res := map[string]any{"attributes": []map[string]any{
		{"key": "service.name", "value": anyValue(e.Service)},
		{"key": "service.version", "value": anyValue(version.Version)},
		{"key": "host.name", "value": anyValue(host)},
	}}
	body, _ := json.Marshal(map[string]any{"resourceSpans": []map[string]any{{"resource": res,
		"scopeSpans": []map[string]any{{"scope": map[string]any{"name": "continuum", "version": version.Version}, "spans": batch}}}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.Headers {
		req.Header.Set(k, v)
	}
	r, err := e.Client.Do(req)
	if err == nil {
		r.Body.Close()
		if r.StatusCode >= 300 {
			err = fmt.Errorf("collector returned %s", r.Status)
		}
	}
	if err != nil {
		log.Printf("tracing: export %d span(s): %v", len(batch), err)
		e.mu.Lock()
		if len(e.queue)+len(batch) <= 8192 { // retry with the next flush (collector restart, network blip)
			e.queue = append(batch, e.queue...)
		}
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	e.Sent += len(batch)
	e.mu.Unlock()
	return nil
}

// parseTraceParent reads a W3C traceparent header.
func parseTraceParent(h string) (tid [16]byte, sid [8]byte, sampled, ok bool) {
	p := strings.Split(strings.TrimSpace(h), "-")
	if len(p) != 4 || p[0] != "00" || len(p[1]) != 32 || len(p[2]) != 16 || len(p[3]) != 2 {
		return
	}
	a, err1 := hex.DecodeString(p[1])
	b, err2 := hex.DecodeString(p[2])
	f, err3 := hex.DecodeString(p[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}
	copy(tid[:], a)
	copy(sid[:], b)
	if tid == [16]byte{} || sid == [8]byte{} {
		return
	}
	return tid, sid, f[0]&1 == 1, true
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
	w.ResponseWriter.WriteHeader(c)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps SSE streaming working through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware creates a SERVER span per request (joins an incoming traceparent). Wrap the route mux with
// Route so the span is named after the matched pattern instead of the raw path (low cardinality).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if global.Load() == nil || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if tid, sid, sampled, ok := parseTraceParent(r.Header.Get("traceparent")); ok {
			ctx = context.WithValue(ctx, ctxKey{}, &Span{traceID: tid, spanID: sid, sampled: sampled})
		}
		ctx, s := Start(ctx, r.Method, KindServer)
		s.Set("http.request.method", r.Method)
		s.Set("url.path", r.URL.Path)
		s.Set("user_agent.original", r.UserAgent())
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(ctx))
		if sw.code == 0 {
			sw.code = 200
		}
		s.Set("http.response.status_code", sw.code)
		if sw.code >= 500 {
			s.Fail(http.StatusText(sw.code))
		}
		s.End()
	})
}

// Route names the request span after the ServeMux pattern ("GET /v1/disks/{id}") once routing is done.
func Route(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if s := FromContext(r.Context()); s != nil && r.Pattern != "" {
			s.SetName(r.Pattern)
			s.Set("http.route", r.Pattern)
		}
	})
}
