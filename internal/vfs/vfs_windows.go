//go:build windows

package vfs

import (
	"continuum/internal/core"
	"continuum/internal/transfer"
)

// FS is unavailable on Windows in this build: WinFsp (cgofuse, cgo) is a later Phase 4 step.
type FS struct {
	Volume   string
	ReadOnly bool
}

// Mount always fails on Windows.
func Mount(c *core.Core, e *transfer.Engine, volume, dir string, readOnly bool) (*FS, error) {
	return nil, core.Errf(core.CodeInvalid, "native VFS is not available on Windows yet (WinFsp, Phase 4) — use the SMB access point")
}

func (f *FS) Unmount() error { return nil }
func (f *FS) Wait()          {}

// Available reports why mounting is not possible on Windows.
func Available() error {
	return core.Errf(core.CodeInvalid, "native VFS is not available on Windows yet (WinFsp) — use the SMB access point")
}
