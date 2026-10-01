package disk

import (
	"os"

	"golang.org/x/sys/unix"
)

func noCache(*os.File) bool { return true }

// dropCache evicts the file's pages (after fsync) so reads hit the device.
func dropCache(f *os.File) { unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED) }
