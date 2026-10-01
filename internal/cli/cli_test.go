package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"continuum/internal/api"
	"continuum/internal/core"
	"continuum/internal/store"
	"continuum/internal/store/storetest"
	"continuum/internal/transfer"
)

type cliEnv struct {
	t    *testing.T
	url  string
	root string
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(storetest.URL(t, root))
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := transfer.New(c)
	ts := httptest.NewServer((&api.Server{C: c, E: e, Env: "test"}).Handler())
	t.Cleanup(func() { ts.Close(); e.Shutdown(); db.Close() })
	return &cliEnv{t: t, url: ts.URL, root: root}
}

// run executes the CLI; returns stdout, stderr, exit code.
func (c *cliEnv) run(args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := Execute(append([]string{"--controller", c.url}, args...), &out, &errb, strings.NewReader(""))
	return out.String(), errb.String(), code
}

func (c *cliEnv) must(args ...string) string {
	c.t.Helper()
	out, errs, code := c.run(args...)
	if code != 0 {
		c.t.Fatalf("continuum %s → exit %d\n%s%s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

func TestCLIEndToEnd(t *testing.T) {
	c := newCLIEnv(t)
	for _, n := range []string{"disk-01", "win-d", "mac-ext"} {
		os.MkdirAll(filepath.Join(c.root, n), 0o755)
	}
	d1 := strings.TrimSpace(c.must("-q", "disk", "attach", filepath.Join(c.root, "disk-01"), "--min-free", "0"))
	c.must("disk", "attach", "--path", filepath.Join(c.root, "win-d"), "--min-free", "0")
	c.must("disk", "attach", filepath.Join(c.root, "mac-ext"), "--role", "portable", "--min-free", "0")
	if out := c.must("disk", "list", "--unassigned"); !strings.Contains(out, "mac-ext") || !strings.Contains(out, "portable") {
		t.Fatalf("disk list:\n%s", out)
	}
	out := c.must("volume", "create", "research-pool", "--disks", d1+",win-d,mac-ext", "--policy", "topdir", "--min-free", "1GiB")
	if !strings.Contains(out, "3 disks") || !strings.Contains(out, "excluded from automatic placement") {
		t.Fatalf("volume create:\n%s", out)
	}
	// reorder by name (--before)
	out = c.must("volume", "move", "research-pool", "mac-ext", "--before", "disk-01")
	if !strings.Contains(out, "1\tdsk") && !strings.Contains(out, "mac-ext") {
		t.Fatalf("move:\n%s", out)
	}
	c.must("volume", "order", "research-pool", "set", "disk-01,win-d,mac-ext")

	src := filepath.Join(c.root, "src", "dataset")
	for f := 0; f < 3; f++ {
		for i := 0; i < 5; i++ {
			p := filepath.Join(src, fmt.Sprintf("folder%02d", f), fmt.Sprintf("file%d.bin", i))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, bytes.Repeat([]byte{byte(i)}, 4096), 0o644)
		}
	}
	// plan output (PRD §9.3 shape)
	out = c.must("plan", "copy", src, "research-pool:/research/dataset", "--start-ordinal", "1")
	for _, want := range []string{"Source", "Files:", "15", "Planned placement", "[1]", "disk-01", "(excluded: role=portable)", "Sufficient capacity:", "YES"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan missing %q:\n%s", want, out)
		}
	}
	// copy without --yes in a non-interactive session → usage error (FR-C04)
	if _, errs, code := c.run("copy", src, "research-pool:/research/dataset"); code != ExitUsage || !strings.Contains(errs, "--yes") {
		t.Fatalf("copy without --yes: exit %d %s", code, errs)
	}
	out = c.must("copy", src, "research-pool:/research/dataset", "--yes")
	if !strings.Contains(out, "DONE") || !strings.Contains(out, "files=15") {
		t.Fatalf("copy:\n%s", out)
	}
	// forced ordinal (R04)
	rel := filepath.Join(c.root, "src", "release")
	os.MkdirAll(rel, 0o755)
	os.WriteFile(filepath.Join(rel, "pkg.tar"), []byte("pkg"), 0o644)
	c.must("copy", rel, "research-pool:/releases/v1.0.0", "--target-ordinal", "2", "--yes")
	if out := c.must("locate", "research-pool:/releases/v1.0.0/pkg.tar"); !strings.Contains(out, "win-d (ordinal 2)") && !strings.Contains(out, "disk=win-d (ordinal 2)") {
		t.Fatalf("locate:\n%s", out)
	}
	// ls, ls --physical
	if out := c.must("ls", "research-pool:/research/dataset"); !strings.Contains(out, "folder00/") {
		t.Fatalf("ls:\n%s", out)
	}
	if out := c.must("ls", "research-pool:/releases/v1.0.0", "--physical", "--ordinal", "2"); !strings.Contains(out, "pkg.tar") {
		t.Fatalf("ls --physical:\n%s", out)
	}
	// mkdir / mv / rm / trash
	c.must("mkdir", "research-pool:/projects/2026", "--ordinal", "1")
	c.must("mv", "research-pool:/research/dataset/folder02", "research-pool:/research/dataset/folder-02")
	out = c.must("rm", "research-pool:/research/dataset/folder-02", "--recursive")
	if !strings.Contains(out, "moved to trash: 5 files") {
		t.Fatalf("rm:\n%s", out)
	}
	if out := c.must("trash", "list", "research-pool"); strings.Count(out, "tmb_") != 5 {
		t.Fatalf("trash list:\n%s", out)
	}
	if _, errs, code := c.run("rm", "research-pool:/research", "--recursive", "--permanent"); code != ExitUsage || !strings.Contains(errs, "destructive") {
		t.Fatalf("permanent rm without --yes must refuse: %d %s", code, errs)
	}
	// verify + jobs + activity + audit
	if out := c.must("verify", "research-pool:/"); !strings.Contains(out, "mismatched=0") {
		t.Fatalf("verify:\n%s", out)
	}
	if out := c.must("job", "list"); strings.Count(out, "DONE") < 2 {
		t.Fatalf("job list:\n%s", out)
	}
	c.must("activity", "tail", "-n", "5")
	c.must("activity", "verify")
	// json output
	if out := c.must("--format", "json", "volume", "list"); !strings.Contains(out, `"slug": "research-pool"`) {
		t.Fatalf("json:\n%s", out)
	}
	// export → import on same controller must conflict on name
	yaml := filepath.Join(c.root, "vol.yaml")
	c.must("volume", "export", "research-pool", "-o", yaml)
	if _, _, code := c.run("volume", "import", yaml); code != ExitConflict {
		t.Fatalf("import duplicate name → exit %d, want %d", code, ExitConflict)
	}
}

func TestExitCodes(t *testing.T) {
	c := newCLIEnv(t)
	os.MkdirAll(filepath.Join(c.root, "d"), 0o755)
	c.must("disk", "attach", filepath.Join(c.root, "d"), "--min-free", "0")
	c.must("volume", "create", "v", "--disks", "d", "--min-free", "0")
	src := filepath.Join(c.root, "f.bin")
	os.WriteFile(src, []byte("x"), 0o644)
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"nope"}, ExitUsage},
		{[]string{"ls", "not-a-volpath"}, ExitUsage},
		{[]string{"volume", "inspect", "missing"}, ExitGeneral}, // not_found → 1
		{[]string{"copy", src, "v:/f.bin", "--min-free", "1PiB", "--yes"}, ExitSpace},
		{[]string{"copy", src, "v:/f.bin", "--target-ordinal", "9", "--yes"}, ExitOffline},
	}
	for _, tc := range cases {
		if _, errs, code := c.run(tc.args...); code != tc.want {
			t.Errorf("%v → exit %d, want %d (%s)", tc.args, code, tc.want, errs)
		}
	}
	// controller unreachable → 6
	var out, errb bytes.Buffer
	if code := Execute([]string{"--controller", "http://127.0.0.1:1", "volume", "list"}, &out, &errb, strings.NewReader("")); code != ExitOffline {
		t.Errorf("unreachable controller → %d", code)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"100GiB": 100 << 30, "1GB": 1_000_000_000, "500M": 500 << 20, "0": 0, "1": 1, "4096": 4096, "1.5TiB": 1649267441664} {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseSize("lots"); err == nil {
		t.Error("expected error")
	}
	if v, p, err := splitVolPath("research-pool:/a/b"); err != nil || v != "research-pool" || p != "/a/b" {
		t.Error("splitVolPath")
	}
	if _, _, err := splitVolPath(`C:\data`); err == nil {
		t.Error("windows path must not parse as volume path")
	}
}
