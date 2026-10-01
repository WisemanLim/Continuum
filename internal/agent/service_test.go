package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceDefinitions(t *testing.T) {
	s := ServiceSpec{Exe: "/opt/continuum/continuum-agent", Dir: "/var/lib/continuum agent", Roots: []string{"/mnt/disk 1", "/mnt/b&c"}, LogDir: "/var/log/continuum"}
	unit := RenderSystemd(s)
	for _, want := range []string{`ExecStart=/opt/continuum/continuum-agent run --dir "/var/lib/continuum agent" --root "/mnt/disk 1" --root /mnt/b&c`, "Restart=always", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
	s.System = true
	if u := RenderSystemd(s); !strings.Contains(u, "WantedBy=multi-user.target") || !strings.Contains(u, "NoNewPrivileges=true") {
		t.Fatalf("system unit:\n%s", u)
	}
	plist := RenderLaunchd(s)
	for _, want := range []string{"<string>com.continuum.agent</string>", "<string>/mnt/b&amp;c</string>", "<key>KeepAlive</key><true/>", "/var/log/continuum/agent.log"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if runtime.GOOS == "darwin" { // validate with the OS tool
		p := filepath.Join(t.TempDir(), "a.plist")
		os.WriteFile(p, []byte(plist), 0o644)
		if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %v %s", err, out)
		}
	}
	win := InstallCommands(ServiceSpec{Exe: `C:\Program Files\Continuum\continuum-agent.exe`, Dir: `C:\ProgramData\Continuum`}, "", true)
	if runtime.GOOS == "windows" && !strings.Contains(strings.Join(win[0], " "), `"C:\Program Files\Continuum\continuum-agent.exe" run --dir`) {
		t.Fatalf("sc create: %v", win[0])
	}
}
