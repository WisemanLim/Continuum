//go:build darwin && cgo && fuset

package fuse

// Continuum patch: with -tags fuset, load FUSE-T's libfuse-t.dylib before macFUSE's libfuse.2.dylib.

/*
#cgo CFLAGS: -DCGOFUSE_FUSET_FIRST
*/
import "C"
