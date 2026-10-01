package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%s missing: %v", s.Name(), err)
	}
	if err := s.Set("k1", "p@ss w0rd'\"$"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("k1"); err != nil || v != "p@ss w0rd'\"$" {
		t.Fatalf("%s get: %q %v", s.Name(), v, err)
	}
	if err := s.Set("k1", "rotated"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("k1"); v != "rotated" {
		t.Fatalf("%s update: %q", s.Name(), v)
	}
	if err := s.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%s after delete: %v", s.Name(), err)
	}
}

func TestFileStore(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "s", "secrets.json")}
	exercise(t, f)
	fi, _ := os.Stat(f.Path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

// Uses the real OS store under a random throwaway service name and removes what it creates.
func TestOSStore(t *testing.T) {
	if os.Getenv("CONTINUUM_TEST_KEYCHAIN") != "1" {
		t.Skip("set CONTINUUM_TEST_KEYCHAIN=1 to write to the OS keychain")
	}
	b := make([]byte, 4)
	rand.Read(b)
	s := OSStore("continuum-test-" + hex.EncodeToString(b))
	if s == nil {
		t.Skipf("no OS secret store on %s", runtime.GOOS)
	}
	exercise(t, s)
}
