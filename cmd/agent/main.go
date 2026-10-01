// continuum-agent — per-node daemon (FR-N01~N06): enrolls with a one-time token, then keeps an mTLS
// connect-go stream to the Controller and executes disk commands on this host.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"continuum/internal/agent"
	"continuum/internal/tracing"
	"continuum/internal/version"
)

func defaultDir() string {
	if d := os.Getenv("CONTINUUM_AGENT_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Continuum", "agent")
}

func main() {
	// Chrome starts native hosts as `<path> chrome-extension://<id>/ [--parent-window=…]` (FR-X03)
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		if err := agent.ServeNative(context.Background(), defaultDir(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	var dir string
	root := &cobra.Command{Use: "continuum-agent", Short: "Continuum node agent", SilenceUsage: true, Version: version.Version}
	root.PersistentFlags().StringVar(&dir, "dir", defaultDir(), "state directory (env CONTINUUM_AGENT_DIR)")

	var controller, token string
	enroll := &cobra.Command{Use: "enroll", Short: "Enroll this node with a one-time token", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := agent.Enroll(cmd.Context(), dir, controller, token)
		if err != nil {
			return err
		}
		fmt.Printf("enrolled as %s (controller %s, state %s)\n", cfg.NodeID, cfg.Controller, dir)
		return nil
	}}
	enroll.Flags().StringVar(&controller, "controller", os.Getenv("CONTINUUM_CONTROLLER_AGENT_URL"), "controller agent endpoint, e.g. https://controller:8443")
	enroll.Flags().StringVar(&token, "token", os.Getenv("CONTINUUM_ENROLL_TOKEN"), "one-time enrollment token (CTM1.…)")
	enroll.MarkFlagRequired("controller")

	var roots []string
	var peerAddr, peerURL string
	run := &cobra.Command{Use: "run", Short: "Connect to the controller and serve disk commands (foreground)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := agent.Load(dir)
		if err != nil && token != "" && controller != "" { // first start of a container/service: enroll once, restarts reuse the state
			if cfg, err = agent.Enroll(cmd.Context(), dir, controller, token); err == nil {
				fmt.Printf("enrolled as %s\n", cfg.NodeID)
			}
		}
		if err != nil {
			return err
		}
		if len(roots) > 0 {
			cfg.Roots = roots
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if exp := tracing.FromEnv(); exp != nil { // OpenTelemetry: agent spans continue the controller's traces
			if os.Getenv("OTEL_SERVICE_NAME") == "" {
				exp.Service = "continuum-agent"
			}
			tracing.Install(ctx, exp)
			defer exp.Flush(context.Background())
		}
		a := agent.New(cfg)
		if _, err := a.PeerListen(ctx, peerAddr, peerURL); err != nil { // direct agent ↔ agent transfers (D36)
			fmt.Fprintf(os.Stderr, "peer endpoint disabled: %v (transfers relay through the controller)\n", err)
		}
		if scm, err := underSCM(a.Run); scm || err != nil { // started by the Windows Service Control Manager
			return err
		}
		fmt.Printf("continuum agent %s — node %s → %s\n", version.Version, cfg.NodeID, cfg.Controller)
		if err := a.Run(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	}}
	run.Flags().StringSliceVar(&roots, "root", nil, "folders offered as attachable disks (repeatable)")
	run.Flags().StringVar(&peerAddr, "peer-listen", envOr("CONTINUUM_AGENT_PEER_ADDR", ":8444"), "mTLS endpoint for direct agent ↔ agent transfers (off = relay through the controller)")
	run.Flags().StringVar(&peerURL, "peer-url", os.Getenv("CONTINUUM_AGENT_PEER_URL"), "URL other agents use to reach this one (default https://<hostname>:<port>)")
	run.Flags().StringVar(&controller, "controller", os.Getenv("CONTINUUM_CONTROLLER_AGENT_URL"), "enroll first if not enrolled yet (with --token)")
	run.Flags().StringVar(&token, "token", os.Getenv("CONTINUUM_ENROLL_TOKEN"), "one-time token used only when not enrolled")

	status := &cobra.Command{Use: "status", Short: "Show enrollment state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := agent.Load(dir)
		if err != nil {
			return err
		}
		exp, err := agent.CertExpiry(dir)
		if err != nil {
			return err
		}
		fmt.Printf("node        %s\ncontroller  %s\ncertificate valid until %s\nstate dir   %s\n", cfg.NodeID, cfg.Controller, exp.Format("2006-01-02 15:04 MST"), dir)
		return nil
	}}
	root.AddCommand(enroll, run, status, serviceCmd(&dir), nativeHostCmd(&dir))
	if err := root.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
