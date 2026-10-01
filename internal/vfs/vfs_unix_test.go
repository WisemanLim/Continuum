//go:build linux || darwin

package vfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
	"continuum/internal/transfer"
)

// Linux: needs /dev/fuse + CAP_SYS_ADMIN (e.g. docker run --device /dev/fuse --cap-add SYS_ADMIN).
// macOS: needs macFUSE (mount_macfuse). Skipped otherwise.
func TestVFS(t *testing.T) {
	if runtime.GOOS == "darwin" && os.Getenv("CONTINUUM_TEST_MACOS_MOUNT") != "1" {
		// a FUSE-T (NFS loopback) mount panicked the macOS kernel during E31 — never mount on a dev Mac by default
		t.Skip("macOS mount test disabled (set CONTINUUM_TEST_MACOS_MOUNT=1 on a disposable machine)")
	}
	if err := Available(); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(storetest.URL(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// disk sizes are simulated: d1 = 3 MiB, d2 = 1 GiB
	caps := map[string]uint64{}
	stat := func(p string) (disk.Capacity, error) {
		for k, total := range caps {
			if strings.HasPrefix(p, k) {
				var used uint64
				filepath.Walk(k, func(_ string, fi os.FileInfo, err error) error {
					if err == nil && fi.Mode().IsRegular() {
						used += uint64(fi.Size())
					}
					return nil
				})
				free := total - min(used, total)
				return disk.Capacity{Total: total, Free: free, Available: free}, nil
			}
		}
		return disk.Stat(p)
	}
	c, err := core.Open(ctx, db, stat)
	if err != nil {
		t.Fatal(err)
	}
	e := transfer.New(c)
	defer e.Shutdown()
	zero := int64(0)
	var ids []string
	for name, size := range map[string]uint64{"d1": 3 << 20, "d2": 1 << 30} {
		p := filepath.Join(root, name)
		os.MkdirAll(p, 0o755)
		caps[p] = size
		res, err := c.Attach(ctx, core.AttachRequest{Path: p, Name: name, MinFreeBytes: &zero})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Disk.ID)
	}
	d1, _ := c.GetDisk(ctx, "d1")
	d2, _ := c.GetDisk(ctx, "d2")
	vol, err := c.CreateVolume(ctx, core.CreateVolumeRequest{Name: "fuse", MinFreeBytes: &zero, DiskIDs: []string{d1.ID, d2.ID}})
	if err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(root, "mnt")
	f, err := Mount(c, e, vol.ID, mnt, false)
	if err != nil {
		t.Skipf("FUSE mount not permitted here (needs CAP_SYS_ADMIN or fusermount3): %v", err)
	}
	defer f.Unmount()

	// 1) write through the mount → lands on ordinal 1 and appears in the catalog
	os.MkdirAll(filepath.Join(mnt, "docs", "2026"), 0o755)
	data := bytes.Repeat([]byte("continuum "), 100_000) // 1 MB
	if err := os.WriteFile(filepath.Join(mnt, "docs", "2026", "a.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(d1.DataRoot, "docs", "2026", "a.bin")); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("file must be on d1: %v", err)
	}
	objs, err := c.Locate(ctx, vol.ID, "/docs/2026/a.bin")
	if err != nil || objs[0].DiskID != d1.ID || objs[0].Size != int64(len(data)) {
		t.Fatalf("catalog: %v %+v", err, objs)
	}
	// 2) d1 fills up → the next file spills over to d2 (placement policy, not mergerfs ff)
	big := bytes.Repeat([]byte{7}, 2_500_000)
	os.WriteFile(filepath.Join(mnt, "docs", "big.bin"), big, 0o644)
	os.WriteFile(filepath.Join(mnt, "docs", "next.bin"), []byte("spill"), 0o644)
	if o, _ := c.Locate(ctx, vol.ID, "/docs/next.bin"); len(o) != 1 || o[0].DiskID != d2.ID {
		t.Fatalf("spillover to d2: %+v", o)
	}
	// 3) read back, list, stat
	if b, err := os.ReadFile(filepath.Join(mnt, "docs", "2026", "a.bin")); err != nil || !bytes.Equal(b, data) {
		t.Fatal("read back")
	}
	ents, _ := os.ReadDir(filepath.Join(mnt, "docs"))
	if len(ents) != 3 {
		t.Fatalf("readdir: %v", ents)
	}
	// 4) editor-style save: write temp + rename over the original
	tmp := filepath.Join(mnt, "docs", ".a.bin.swp")
	os.WriteFile(tmp, []byte("new content"), 0o644)
	if err := os.Rename(tmp, filepath.Join(mnt, "docs", "2026", "a.bin")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(mnt, "docs", "2026", "a.bin")); string(b) != "new content" {
		t.Fatalf("rename over: %q", b)
	}
	// 5) append + truncate update the catalog size
	fh, _ := os.OpenFile(filepath.Join(mnt, "docs", "next.bin"), os.O_APPEND|os.O_WRONLY, 0)
	fh.Write([]byte("-more"))
	fh.Close()
	if o, _ := c.Locate(ctx, vol.ID, "/docs/next.bin"); o[0].Size != int64(len("spill-more")) {
		t.Fatalf("append size: %d", o[0].Size)
	}
	// 6) delete → volume trash; rmdir non-empty refused
	if err := os.Remove(filepath.Join(mnt, "docs", "big.bin")); err != nil {
		t.Fatal(err)
	}
	if tr, _ := e.Trash(ctx, vol.Slug); len(tr) == 0 {
		t.Fatal("deleted file must be in the trash")
	}
	if err := os.Remove(filepath.Join(mnt, "docs")); err == nil {
		t.Fatal("rmdir of a non-empty folder must fail")
	}
	// symlinks (cp -r of trees with links)
	if err := os.Symlink("2026/a.bin", filepath.Join(mnt, "docs", "latest")); err != nil {
		t.Fatal(err)
	}
	if tgt, err := os.Readlink(filepath.Join(mnt, "docs", "latest")); err != nil || tgt != "2026/a.bin" {
		t.Fatalf("readlink: %q %v", tgt, err)
	}
	if b, err := os.ReadFile(filepath.Join(mnt, "docs", "latest")); err != nil || string(b) != "new content" {
		t.Fatalf("follow link: %q %v", b, err)
	}
	// xattrs: go-fuse refuses them (ENOTSUP → cp -a / Finder fall back); FUSE-T keeps them on the disk file
	err = unix.Setxattr(filepath.Join(mnt, "docs", "next.bin"), "user.tag", []byte("x"), 0)
	if xattrPassThrough {
		buf := make([]byte, 8)
		n, gerr := unix.Getxattr(filepath.Join(d2.DataRoot, "docs", "next.bin"), "user.tag", buf)
		if err != nil || gerr != nil || string(buf[:n]) != "x" {
			t.Fatalf("xattr pass-through: set %v get %v %q", err, gerr, buf[:n])
		}
		if m, _ := filepath.Glob(filepath.Join(root, "d*", ".continuum", "docs", "._*")); len(m) != 0 {
			t.Fatalf("AppleDouble files on the disks: %v", m)
		}
	} else if err != unix.ENOTSUP {
		t.Fatalf("setxattr: %v", err)
	}
	// 7) read-only mount refuses writes
	f.Unmount()
	ro, err := Mount(c, e, vol.ID, mnt, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Unmount()
	if err := os.WriteFile(filepath.Join(mnt, "x.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("read-only mount must refuse writes")
	}
}
