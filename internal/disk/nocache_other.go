//go:build !darwin && !linux

package disk

import "os"

func noCache(*os.File) bool { return false }

func dropCache(*os.File) {}
