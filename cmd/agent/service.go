package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"continuum/internal/agent"
)

// serviceCmd installs the agent as launchd daemon / systemd unit / Windows service (FR-N04).
func serviceCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "service", Short: "Install, remove or inspect the OS service (launchd / systemd / Windows Service)"}
	var system, noStart bool
	var roots []string
	var unitDir string
	spec := func() (agent.ServiceSpec, error) {
		exe, err := os.Executable()
		if err != nil {
			return agent.ServiceSpec{}, err
		}
		exe, _ = filepath.EvalSymlinks(exe)
		absDir, _ := filepath.Abs(*dir)
		return agent.ServiceSpec{Exe: exe, Dir: absDir, Roots: roots, System: system, LogDir: absDir}, nil
	}
	unitPath := func(s agent.ServiceSpec) (string, error) {
		p, err := agent.UnitPath(s)
		if unitDir != "" && p != "" {
			p = filepath.Join(unitDir, filepath.Base(p))
		}
		return p, err
	}
	addFlags := func(cmd *cobra.Command) {
		cmd.Flags().BoolVar(&system, "system", false, "system-wide service (root / Administrator) instead of the current user")
		cmd.Flags().StringSliceVar(&roots, "root", nil, "folders offered as attachable disks (repeatable)")
	}
	print := &cobra.Command{Use: "print", Short: "Print the service definition without installing", RunE: func(cmd *cobra.Command, args []string) error {
		s, err := spec()
		if err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			for _, c := range agent.InstallCommands(s, "", true) {
				fmt.Println(c)
			}
			return nil
		}
		fmt.Print(agent.Render(s))
		return nil
	}}
	addFlags(print)
	install := &cobra.Command{Use: "install", Short: "Install and start the service", RunE: func(cmd *cobra.Command, args []string) error {
		s, err := spec()
		if err != nil {
			return err
		}
		if _, err := agent.Load(s.Dir); err != nil {
			return fmt.Errorf("enroll first: %w", err)
		}
		p, err := unitPath(s)
		if err != nil {
			return err
		}
		if p != "" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(agent.Render(s)), 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", p)
		}
		if noStart {
			return nil
		}
		for _, c := range agent.InstallCommands(s, p, true) {
			if out, err := agent.Run(c); err != nil {
				return fmt.Errorf("%v: %v\n%s", c, err, out)
			}
		}
		fmt.Println("service installed and started")
		return nil
	}}
	addFlags(install)
	install.Flags().BoolVar(&noStart, "no-start", false, "only write the definition")
	install.Flags().StringVar(&unitDir, "unit-dir", "", "write the definition into this directory (packaging, tests)")
	uninstall := &cobra.Command{Use: "uninstall", Short: "Stop and remove the service (state and certificates are kept)", RunE: func(cmd *cobra.Command, args []string) error {
		s, err := spec()
		if err != nil {
			return err
		}
		p, err := unitPath(s)
		if err != nil {
			return err
		}
		if !noStart {
			for _, c := range agent.InstallCommands(s, p, false) {
				agent.Run(c) // best effort: may already be stopped
			}
			for i := 0; i < 20; i++ { // launchctl bootout returns before the job is gone
				if _, err := agent.Run(agent.StatusCommand(s)); err != nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		if p != "" {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		fmt.Println("service removed")
		return nil
	}}
	uninstall.Flags().BoolVar(&system, "system", false, "system-wide service")
	uninstall.Flags().BoolVar(&noStart, "no-stop", false, "only remove the definition")
	uninstall.Flags().StringVar(&unitDir, "unit-dir", "", "definition directory used at install")
	status := &cobra.Command{Use: "status", Short: "Show the service state", RunE: func(cmd *cobra.Command, args []string) error {
		s, err := spec()
		if err != nil {
			return err
		}
		out, err := agent.Run(agent.StatusCommand(s))
		fmt.Print(out)
		return err
	}}
	status.Flags().BoolVar(&system, "system", false, "system-wide service")
	c.AddCommand(print, install, uninstall, status)
	return c
}
