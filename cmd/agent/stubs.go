package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"continuum/internal/agent"
)

// nativeHostCmd: Chrome native messaging host (FR-X03 T3) + manifest install.
func nativeHostCmd(dir *string) *cobra.Command {
	c := &cobra.Command{Use: "native-host", Short: "Chrome extension bridge (T3): serve stdio, or install the host manifest", RunE: func(cmd *cobra.Command, args []string) error {
		return agent.ServeNative(cmd.Context(), *dir, os.Stdin, os.Stdout)
	}}
	var browser, manifestDir string
	var ids []string
	install := &cobra.Command{Use: "install", Short: "Register this binary as the native messaging host for the Continuum extension", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if len(ids) == 0 {
			return fmt.Errorf("--extension-id required (chrome://extensions)")
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, _ = filepath.EvalSymlinks(exe)
		p, err := agent.InstallNative(browser, manifestDir, exe, ids)
		if err != nil {
			return err
		}
		fmt.Println("wrote", p)
		return nil
	}}
	install.Flags().StringVar(&browser, "browser", "chrome", "chrome|chromium|edge|brave")
	install.Flags().StringSliceVar(&ids, "extension-id", nil, "allowed extension id (repeatable)")
	install.Flags().StringVar(&manifestDir, "manifest-dir", "", "write the manifest here instead of the browser default (tests, packaging)")
	uninstall := &cobra.Command{Use: "uninstall", Short: "Remove the host manifest", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		d := manifestDir
		if d == "" {
			var err error
			if d, err = agent.NativeManifestDir(browser); err != nil {
				return err
			}
		}
		if err := os.Remove(filepath.Join(d, agent.NativeHostName+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("removed")
		return nil
	}}
	uninstall.Flags().StringVar(&browser, "browser", "chrome", "chrome|chromium|edge|brave")
	uninstall.Flags().StringVar(&manifestDir, "manifest-dir", "", "manifest directory used at install")
	c.AddCommand(install, uninstall)
	return c
}
