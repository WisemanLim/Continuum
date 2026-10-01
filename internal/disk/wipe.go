package disk

import (
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// IsNetworkFS reports network filesystems (latency warning in preflight check 10).
func IsNetworkFS(fstype string) bool {
	switch fstype {
	case "smbfs", "cifs", "nfs", "nfs4", "afpfs", "webdav", "fuse.sshfs":
		return true
	}
	return false
}

// Wipe overwrites every regular file under root with random data `passes` times (fsync each pass), then
// removes the files and empty folders, root itself included (FR-D22, file level). SSD wear-levelling and
// snapshots can keep old blocks — full-device secure erase stays an OS tool (ATA/NVMe sanitize).
func Wipe(ctx context.Context, root string, passes int) (files int, bytes int64, err error) {
	if passes < 1 {
		passes = 1
	}
	if passes > 7 {
		passes = 7
	}
	var paths, dirs []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		} else if d.Type().IsRegular() {
			paths = append(paths, p)
		} else {
			os.Remove(p) // symlinks etc.: nothing to overwrite
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	buf := make([]byte, 1<<20)
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return files, bytes, err
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			return files, bytes, err
		}
		for pass := 0; pass < passes; pass++ {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				f.Close()
				return files, bytes, err
			}
			for left := fi.Size(); left > 0; {
				n := min(left, int64(len(buf)))
				rand.Read(buf[:n])
				if _, err := f.Write(buf[:n]); err != nil {
					f.Close()
					return files, bytes, err
				}
				left -= n
			}
			if err := f.Sync(); err != nil {
				f.Close()
				return files, bytes, err
			}
		}
		f.Close()
		if err := os.Remove(p); err != nil {
			return files, bytes, err
		}
		files++
		bytes += fi.Size()
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) }) // deepest first
	for _, d := range dirs {
		os.Remove(d)
	}
	return files, bytes, nil
}
