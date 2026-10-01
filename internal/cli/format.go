package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// output renders v in --format (table|json|yaml). table uses the supplied renderer.
func (a *app) output(v any, table func(w io.Writer)) error {
	switch a.format {
	case "json":
		enc := json.NewEncoder(a.out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "yaml":
		b, err := json.Marshal(v) // normalise via JSON tags
		if err != nil {
			return err
		}
		var generic any
		json.Unmarshal(b, &generic)
		return yaml.NewEncoder(a.out).Encode(generic)
	case "table", "":
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		table(tw)
		return tw.Flush()
	default:
		return &usageError{"--format must be table|json|yaml"}
	}
}

// humanBytes renders sizes like "24.0 GB" (SI, TB = 10^12) or IEC with --iec.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

var sizeRe = regexp.MustCompile(`^(?i)\s*([0-9]+(?:\.[0-9]+)?)\s*([kmgtp]?)(i?)(b?)\s*$`)

// parseSize accepts 100GiB, 1GB, 500M, 1.5TiB, 1073741824.
func parseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, &usageError{fmt.Sprintf("invalid size %q (examples: 100GiB, 1GB, 500M)", s)}
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	base := 1000.0
	if m[3] != "" || (m[2] != "" && m[4] == "") {
		base = 1024 // "GiB" or bare "G" → binary
	}
	exp := 0 // bare number = bytes
	if m[2] != "" {
		exp = strings.Index("kmgtp", strings.ToLower(m[2])) + 1
	}
	for i := 0; i < exp; i++ {
		f *= base
	}
	return int64(f), nil
}

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// splitVolPath parses "volume:/path".
func splitVolPath(s string) (vol, path string, err error) {
	i := strings.Index(s, ":")
	driveLetter := runtime.GOOS == "windows" && i == 1 && isDriveLetter(s[0]) // C:\data is a local path on Windows
	if i <= 0 || (len(s) > i+1 && s[i+1] == '\\') || driveLetter {
		return "", "", &usageError{fmt.Sprintf("expected <volume>:/<path>, got %q", s)}
	}
	path = s[i+1:]
	if path == "" {
		path = "/"
	}
	return s[:i], path, nil
}

func isDriveLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func bar(done, total int64, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	n := int(float64(done) / float64(total) * float64(width))
	if n > width {
		n = width
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}
