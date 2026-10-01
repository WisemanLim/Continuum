package transfer

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// srcItem is one scanned source file.
type srcItem struct {
	Rel     string // slash path relative to the copy root (NFC)
	Abs     string // absolute source path
	Size    int64
	Mode    fs.FileMode
	MtimeNs int64
	Link    string // symlink target (Size 0)
}

// scan walks a local source (file or directory) and returns files and the directory count.
// Special files (devices, sockets, pipes) are skipped (FR-T05). Symlinks are kept as links.
func scan(src string, include, exclude []string) (items []srcItem, dirs int, base string, err error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return nil, 0, "", err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil, 0, "", err
	}
	base = norm.NFC.String(filepath.Base(abs))
	if !fi.IsDir() {
		it, ok := toItem(abs, base, fi)
		if ok && match(base, include, exclude) {
			items = append(items, it)
		}
		return items, 0, base, nil
	}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == abs {
			return nil
		}
		rel, _ := filepath.Rel(abs, p)
		rel = norm.NFC.String(filepath.ToSlash(rel))
		if d.IsDir() {
			if !match(rel+"/", nil, exclude) {
				return filepath.SkipDir
			}
			dirs++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if it, ok := toItem(p, rel, info); ok && match(rel, include, exclude) {
			items = append(items, it)
		}
		return nil
	})
	return items, dirs, base, err
}

func toItem(abs, rel string, fi fs.FileInfo) (srcItem, bool) {
	it := srcItem{Rel: rel, Abs: abs, Mode: fi.Mode(), MtimeNs: fi.ModTime().UnixNano()}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(abs)
		if err != nil {
			return it, false
		}
		it.Link = t
		return it, true
	case fi.Mode().IsRegular():
		it.Size = fi.Size()
		return it, true
	default:
		return it, false // device, socket, pipe
	}
}

// match applies --include/--exclude globs (rclone-like subset). Patterns without '/' match the base name.
func match(rel string, include, exclude []string) bool {
	name := path.Base(strings.TrimSuffix(rel, "/"))
	hit := func(pat string) bool {
		target := name
		if strings.Contains(pat, "/") {
			target = strings.TrimSuffix(rel, "/")
		}
		ok, _ := path.Match(pat, target)
		return ok
	}
	for _, p := range exclude {
		if hit(p) {
			return false
		}
	}
	if len(include) == 0 || strings.HasSuffix(rel, "/") {
		return true
	}
	for _, p := range include {
		if hit(p) {
			return true
		}
	}
	return false
}

// defaultExcludes are OS metadata files never copied into a pool.
var defaultExcludes = []string{".DS_Store", "Thumbs.db", "desktop.ini", ".continuum", "*.continuum-partial-*"}
