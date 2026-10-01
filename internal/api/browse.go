package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"continuum/internal/core"
)

// browseEntry is one folder in the Controller-host folder picker.
type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// browse lists sub-folders of a directory on the Controller host (folder picker for copy source and
// attach — works the same in browser, desktop and extension; replaces a native dialog, FR-G06).
func (s *Server) browse(w http.ResponseWriter, r *http.Request) (any, error) {
	p := r.URL.Query().Get("path")
	home, _ := os.UserHomeDir()
	if p == "" {
		p = home
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return nil, core.Errf(core.CodeInvalid, "path must be absolute")
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, core.Errf(core.CodeNotFound, "cannot read %s: %v", p, err)
	}
	dirs := []browseEntry{}
	files := 0
	hidden := r.URL.Query().Get("hidden") == "1"
	for _, e := range ents {
		if !hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(p, e.Name())); err == nil {
				isDir = fi.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, browseEntry{Name: e.Name(), Path: filepath.Join(p, e.Name())})
		} else {
			files++
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	roots := []browseEntry{{Name: "Home", Path: home}}
	switch runtime.GOOS {
	case "darwin":
		if vs, err := os.ReadDir("/Volumes"); err == nil {
			for _, v := range vs {
				roots = append(roots, browseEntry{Name: v.Name(), Path: filepath.Join("/Volumes", v.Name())})
			}
		}
	case "windows":
		for c := 'C'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if _, err := os.Stat(d); err == nil {
				roots = append(roots, browseEntry{Name: d, Path: d})
			}
		}
	default:
		roots = append(roots, browseEntry{Name: "/", Path: "/"})
		for _, base := range []string{"/mnt", "/media"} {
			if vs, err := os.ReadDir(base); err == nil {
				for _, v := range vs {
					roots = append(roots, browseEntry{Name: v.Name(), Path: filepath.Join(base, v.Name())})
				}
			}
		}
	}
	return map[string]any{"path": p, "parent": parent, "dirs": dirs, "files": files, "roots": roots}, nil
}
