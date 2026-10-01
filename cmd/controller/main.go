// continuum-controller — control plane (M1: embedded Controller + local Agent in one process).
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"continuum/internal/access"
	"continuum/internal/api"
	"continuum/internal/core"
	"continuum/internal/hub"
	"continuum/internal/pki"
	"continuum/internal/secrets"
	"continuum/internal/store"
	"continuum/internal/tracing"
	"continuum/internal/transfer"
	"continuum/internal/version"
	"continuum/internal/watch"
)

func main() {
	env := getenv("ENV", "local")
	addr := getenv("CONTROLLER_ADDR", "127.0.0.1:8080")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if exp := tracing.FromEnv(); exp != nil { // OpenTelemetry traces (OTLP/HTTP JSON)
		tracing.Install(ctx, exp)
		defer exp.Flush(context.Background()) // last spans on shutdown
		log.Printf("tracing: OTLP export to %s (service %s)", exp.URL, exp.Service)
	}

	db, err := store.Open(getenv("DATABASE_URL", "sqlite:///./local.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	c, err := core.Open(ctx, db, nil)
	if err != nil {
		log.Fatalf("catalog: %v", err)
	}
	secDir := os.Getenv("CONTINUUM_SECRETS_DIR")
	if secDir == "" {
		base, _ := os.UserConfigDir()
		secDir = filepath.Join(base, "Continuum", "secrets-"+c.ControllerID)
	}
	c.Secrets = secrets.Open("continuum-"+c.ControllerID, secDir)
	log.Printf("secrets: %s", c.Secrets.Name())
	eng := transfer.New(c)
	eng.NoDirect = os.Getenv("CONTINUUM_DIRECT") == "off" // D36: agents copy node ↔ node without the controller
	if err := eng.Recover(ctx); err != nil {
		log.Printf("recover: %v", err)
	}
	origins := strings.Split(getenv("CONTINUUM_ALLOWED_ORIGINS",
		"http://localhost:5173,http://127.0.0.1:5173,tauri://localhost,http://tauri.localhost,https://tauri.localhost,chrome-extension://*"), ",")
	accessDir := os.Getenv("CONTINUUM_ACCESS_DIR")
	if accessDir == "" {
		base, _ := os.UserConfigDir()
		accessDir = filepath.Join(base, "Continuum", "access-"+c.ControllerID)
	}
	srv := &api.Server{C: c, E: eng, Env: env, Token: os.Getenv("CONTINUUM_TOKEN"), Origins: origins, WebDir: os.Getenv("CONTINUUM_WEB_DIR"),
		A: access.New(c, accessDir)}
	if h, err := startAgentListener(ctx, c, eng); err != nil {
		log.Fatalf("agent listener: %v", err)
	} else {
		srv.H = h
	}
	go srv.Background(ctx, 30*time.Second)
	if os.Getenv("CONTINUUM_WATCH") != "off" { // FR-K05: direct edits reach the catalog within seconds
		if w, err := watch.New(c, eng); err != nil {
			log.Printf("watch: disabled: %v", err)
		} else {
			go w.Run(ctx)
		}
	}
	if os.Getenv("CONTINUUM_EXIT_WITH_PARENT") == "1" {
		go exitWithParent(stop) // desktop sidecar: never outlive the shell
	}

	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		log.Print("shutting down: pausing running jobs (partials kept for resume)")
		srv.UnmountAll()
		eng.Shutdown()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	log.Printf("continuum controller %s listening on %s (env=%s, controller=%s, node=%s)", version.Version, addr, env, c.ControllerID, c.LocalNode.Hostname)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// startAgentListener serves the mTLS agent endpoint (FR-S03). CONTINUUM_AGENT_ADDR=off disables it.
func startAgentListener(ctx context.Context, c *core.Core, eng *transfer.Engine) (*hub.Hub, error) {
	addr := getenv("CONTINUUM_AGENT_ADDR", "127.0.0.1:8443")
	if addr == "off" {
		return nil, nil
	}
	pkiDir := os.Getenv("CONTINUUM_PKI_DIR")
	if pkiDir == "" {
		base, _ := os.UserConfigDir()
		pkiDir = filepath.Join(base, "Continuum", "pki-"+c.ControllerID)
	}
	ca, err := pki.LoadOrCreate(pkiDir, c.ControllerID)
	if err != nil {
		return nil, err
	}
	tc, err := ca.ServerTLS(pki.LocalHosts(strings.Split(os.Getenv("CONTINUUM_AGENT_SANS"), ",")...))
	if err != nil {
		return nil, err
	}
	public := os.Getenv("CONTINUUM_AGENT_URL")
	if public == "" {
		host, port, _ := net.SplitHostPort(addr)
		if host == "" || host == "0.0.0.0" || host == "::" {
			host, _ = os.Hostname()
		}
		public = "https://" + net.JoinHostPort(host, port)
	}
	h := hub.New(c, ca, public)
	if n, err := strconv.Atoi(os.Getenv("CONTINUUM_AGENT_HEARTBEAT_SEC")); err == nil && n > 0 {
		h.HeartbeatSec = n // tests / slow links; default 10 s, offline after 3 missed
	}
	c.Remote = h
	h.OnNodeOnline = func(string) { eng.ResumeOffline(context.Background()) }
	eng.Remote = h
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	hs := &http.Server{Handler: h.Handler(), TLSConfig: tc, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		hs.Close()
	}()
	go func() {
		if err := hs.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("agent listener: %v", err)
		}
	}()
	go h.Run(ctx)
	log.Printf("agent endpoint %s (mTLS, CA %s…)", public, ca.Fingerprint()[:16])
	return h, nil
}

// exitWithParent triggers a graceful shutdown when the parent process disappears (re-parented to init/launchd).
func exitWithParent(stop context.CancelFunc) {
	parent := os.Getppid()
	for range time.Tick(time.Second) {
		if p := os.Getppid(); p != parent || p == 1 {
			log.Print("parent process exited — shutting down")
			stop()
			return
		}
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
