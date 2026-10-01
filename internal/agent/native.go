package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"continuum/internal/disk"
	"continuum/internal/version"
)

// NativeHostName is the Chrome native messaging host id (FR-X03).
const NativeHostName = "com.continuum.agent"

// ReadNative reads one native-messaging frame (uint32 little-endian length + JSON).
func ReadNative(r io.Reader, v any) error {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return err
	}
	if n > 1<<20 { // Chrome → host messages are limited to 4 GB, but ours are tiny; refuse garbage
		return fmt.Errorf("native message too large (%d bytes)", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteNative writes one frame (host → Chrome messages must stay under 1 MB).
func WriteNative(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("native reply exceeds 1 MB")
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// NativeRequest is a message from the extension.
type NativeRequest struct {
	Type string `json:"type"` // hello | discover
}

// ServeNative answers extension requests on stdin/stdout until Chrome closes the pipe (T3 bridge):
// hello → agent identity + local Controller URL; discover → local volumes (UUID, free space, bus).
func ServeNative(ctx context.Context, dir string, in io.Reader, out io.Writer) error {
	for {
		var req NativeRequest
		if err := ReadNative(in, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var reply map[string]any
		switch req.Type {
		case "hello":
			hostname, _ := os.Hostname()
			reply = map[string]any{"type": "hello", "version": version.Version, "os": runtime.GOOS, "hostname": hostname,
				"controller_api": envOr("CONTINUUM_CONTROLLER", "http://127.0.0.1:8080"), "enrolled": false}
			if cfg, err := Load(dir); err == nil {
				reply["enrolled"], reply["node_id"], reply["controller_agent"] = true, cfg.NodeID, cfg.Controller
			}
		case "discover":
			ds, err := disk.Discover(ctx, "", disk.Stat)
			if err != nil {
				reply = map[string]any{"type": "error", "error": err.Error()}
			} else {
				reply = map[string]any{"type": "disks", "disks": ds}
			}
		default:
			reply = map[string]any{"type": "error", "error": "unknown request " + req.Type}
		}
		if err := WriteNative(out, reply); err != nil {
			return err
		}
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// NativeManifest is the host manifest Chrome reads.
func NativeManifest(exe string, extensionIDs []string) []byte {
	origins := make([]string, 0, len(extensionIDs))
	for _, id := range extensionIDs {
		origins = append(origins, "chrome-extension://"+id+"/")
	}
	b, _ := json.MarshalIndent(map[string]any{
		"name": NativeHostName, "description": "Continuum local agent bridge (T3)", "path": exe, "type": "stdio", "allowed_origins": origins,
	}, "", "  ")
	return b
}

// NativeManifestDir is the per-user NativeMessagingHosts directory of a browser (Windows uses the registry).
func NativeManifestDir(browser string) (string, error) {
	home, _ := os.UserHomeDir()
	type loc struct{ mac, linux, winKey string }
	m := map[string]loc{
		"chrome":   {"Library/Application Support/Google/Chrome", ".config/google-chrome", `Software\Google\Chrome`},
		"chromium": {"Library/Application Support/Chromium", ".config/chromium", `Software\Chromium`},
		"edge":     {"Library/Application Support/Microsoft Edge", ".config/microsoft-edge", `Software\Microsoft\Edge`},
		"brave":    {"Library/Application Support/BraveSoftware/Brave-Browser", ".config/BraveSoftware/Brave-Browser", `Software\BraveSoftware\Brave-Browser`},
	}
	l, ok := m[browser]
	if !ok {
		return "", fmt.Errorf("unknown browser %q (chrome|chromium|edge|brave)", browser)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, l.mac, "NativeMessagingHosts"), nil
	case "linux":
		return filepath.Join(home, l.linux, "NativeMessagingHosts"), nil
	case "windows":
		base, _ := os.UserConfigDir()
		return filepath.Join(base, "Continuum", "native-host"), nil // manifest file; registry points at it
	}
	return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
}

// InstallNative writes the manifest (and on Windows registers it under HKCU).
func InstallNative(browser, dir, exe string, extensionIDs []string) (string, error) {
	if dir == "" {
		var err error
		if dir, err = NativeManifestDir(browser); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, NativeHostName+".json")
	if err := os.WriteFile(p, NativeManifest(exe, extensionIDs), 0o644); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		key := map[string]string{"chrome": `Software\Google\Chrome`, "chromium": `Software\Chromium`, "edge": `Software\Microsoft\Edge`, "brave": `Software\BraveSoftware\Brave-Browser`}[browser]
		if out, err := exec.Command("reg", "add", `HKCU\`+key+`\NativeMessagingHosts\`+NativeHostName, "/ve", "/t", "REG_SZ", "/d", p, "/f").CombinedOutput(); err != nil {
			return p, fmt.Errorf("reg add: %v %s", err, out)
		}
	}
	return p, nil
}
