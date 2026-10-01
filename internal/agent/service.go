package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

// Service names (FR-N04).
const (
	LaunchdLabel   = "com.continuum.agent"
	SystemdUnit    = "continuum-agent.service"
	WindowsService = "ContinuumAgent"
)

// ServiceSpec describes the installed service.
type ServiceSpec struct {
	Exe    string   // absolute path of continuum-agent
	Dir    string   // state directory
	Roots  []string // --root folders
	System bool     // system-wide (root/Administrator) instead of the current user
	LogDir string
}

// Args are the daemon arguments.
func (s ServiceSpec) Args() []string {
	a := []string{"run", "--dir", s.Dir}
	for _, r := range s.Roots {
		a = append(a, "--root", r)
	}
	return a
}

var launchdTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Exe}}</string>{{range .Args}}
    <string>{{.}}</string>{{end}}
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
`))

var systemdTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=Continuum storage agent
After=network-online.target local-fs.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.Exec}}
Restart=always
RestartSec=5
# disks may be mounted late (USB): the agent reports them when they appear
TimeoutStopSec=20
{{if .System}}ProtectSystem=full
PrivateTmp=true
NoNewPrivileges=true
{{end}}
[Install]
WantedBy={{if .System}}multi-user.target{{else}}default.target{{end}}
`))

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\$%") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`).Replace(s) + `"`
}

// RenderLaunchd returns the launchd plist.
func RenderLaunchd(s ServiceSpec) string {
	args := s.Args()
	for i := range args {
		args[i] = xmlEscape(args[i])
	}
	var b bytes.Buffer
	launchdTmpl.Execute(&b, map[string]any{"Label": LaunchdLabel, "Exe": xmlEscape(s.Exe), "Args": args, "Log": xmlEscape(filepath.Join(s.LogDir, "agent.log"))})
	return b.String()
}

// RenderSystemd returns the systemd unit.
func RenderSystemd(s ServiceSpec) string {
	parts := []string{systemdQuote(s.Exe)}
	for _, a := range s.Args() {
		parts = append(parts, systemdQuote(a))
	}
	var b bytes.Buffer
	systemdTmpl.Execute(&b, map[string]any{"Exec": strings.Join(parts, " "), "System": s.System})
	return b.String()
}

// UnitPath is where the service definition is written for this OS.
func UnitPath(s ServiceSpec) (string, error) {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if s.System {
			return "/Library/LaunchDaemons/" + LaunchdLabel + ".plist", nil
		}
		return filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist"), nil
	case "linux":
		if s.System {
			return "/etc/systemd/system/" + SystemdUnit, nil
		}
		return filepath.Join(home, ".config", "systemd", "user", SystemdUnit), nil
	case "windows":
		return "", nil // registered with the Service Control Manager
	}
	return "", fmt.Errorf("unsupported OS %s", runtime.GOOS)
}

// Render returns the service definition for this OS ("" on Windows).
func Render(s ServiceSpec) string {
	switch runtime.GOOS {
	case "darwin":
		return RenderLaunchd(s)
	case "linux":
		return RenderSystemd(s)
	}
	return ""
}

// InstallCommands are the OS commands that load/start (install) or stop/unload (uninstall) the service.
func InstallCommands(s ServiceSpec, unit string, install bool) [][]string {
	switch runtime.GOOS {
	case "darwin":
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if s.System {
			domain = "system"
		}
		if install {
			return [][]string{{"launchctl", "bootstrap", domain, unit}}
		}
		return [][]string{{"launchctl", "bootout", domain + "/" + LaunchdLabel}}
	case "linux":
		ctl := []string{"systemctl"}
		if !s.System {
			ctl = append(ctl, "--user")
		}
		if install {
			return [][]string{append(append([]string{}, ctl...), "daemon-reload"), append(append([]string{}, ctl...), "enable", "--now", SystemdUnit)}
		}
		return [][]string{append(append([]string{}, ctl...), "disable", "--now", SystemdUnit)}
	case "windows":
		if install {
			bin := `"` + s.Exe + `" ` + strings.Join(quoteWin(s.Args()), " ")
			return [][]string{{"sc.exe", "create", WindowsService, "binPath=", bin, "start=", "auto", "DisplayName=", "Continuum Agent"},
				{"sc.exe", "failure", WindowsService, "reset=", "86400", "actions=", "restart/5000/restart/5000/restart/30000"},
				{"sc.exe", "start", WindowsService}}
		}
		return [][]string{{"sc.exe", "stop", WindowsService}, {"sc.exe", "delete", WindowsService}}
	}
	return nil
}

func quoteWin(a []string) []string {
	out := make([]string, len(a))
	for i, s := range a {
		if strings.ContainsAny(s, " \t") {
			s = `"` + s + `"`
		}
		out[i] = s
	}
	return out
}

// StatusCommand prints the service state.
func StatusCommand(s ServiceSpec) []string {
	switch runtime.GOOS {
	case "darwin":
		if s.System {
			return []string{"launchctl", "print", "system/" + LaunchdLabel}
		}
		return []string{"launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchdLabel)}
	case "linux":
		if s.System {
			return []string{"systemctl", "status", "--no-pager", SystemdUnit}
		}
		return []string{"systemctl", "--user", "status", "--no-pager", SystemdUnit}
	case "windows":
		return []string{"sc.exe", "query", WindowsService}
	}
	return nil
}

// Run executes a command, returning combined output.
func Run(cmd []string) (string, error) {
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	return string(out), err
}
