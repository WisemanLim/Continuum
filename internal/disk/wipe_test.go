package disk

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWipe(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".continuum")
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	secret := bytes.Repeat([]byte("TOP-SECRET "), 50_000)
	f := filepath.Join(root, "a", "b", "s.bin")
	os.WriteFile(f, secret, 0o644)
	os.WriteFile(filepath.Join(root, "volume.json"), []byte("{}"), 0o644)
	os.Symlink(f, filepath.Join(root, "link"))
	// keep a handle to the inode: after wipe its content must be random, not the secret
	h, _ := os.Open(f)
	defer h.Close()
	files, n, err := Wipe(context.Background(), root, 2)
	if err != nil || files != 2 || n != int64(len(secret))+2 {
		t.Fatalf("wipe: %d %d %v", files, n, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("data root must be gone")
	}
	buf := make([]byte, len(secret))
	h.ReadAt(buf, 0)
	if bytes.Contains(buf, []byte("TOP-SECRET")) {
		t.Fatal("content was not overwritten")
	}
}
