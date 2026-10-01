package disk

import (
	"os"
	"syscall"
)

// noCache sets F_NOCACHE so benchmark reads hit the device, not the unified buffer cache.
func noCache(f *os.File) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_NOCACHE, 1)
	return e == 0
}

func dropCache(*os.File) {}
