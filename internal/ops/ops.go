// Package ops: Prometheus metrics (FR-O01), diagnostics bundle (FR-O04) and notifications (FR-D26, G16).
package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"continuum/internal/access"
	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/transfer"
	"continuum/internal/version"
)

// ---- metrics ----

func esc(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) }

// Metrics writes the Prometheus text exposition format.
func Metrics(ctx context.Context, w io.Writer, c *core.Core, e *transfer.Engine, a *access.Manager) error {
	vols, err := c.ListVolumes(ctx)
	if err != nil {
		return err
	}
	disks, err := c.ListDisks(ctx, core.DiskFilter{})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "# HELP continuum_build_info Build information.\n# TYPE continuum_build_info gauge\ncontinuum_build_info{version=\"%s\",goos=\"%s\"} 1\n", esc(version.Version), runtime.GOOS)

	fmt.Fprint(w, "# HELP continuum_volume_bytes Volume capacity by kind (usable, free, stored).\n# TYPE continuum_volume_bytes gauge\n")
	for _, v := range vols {
		for k, n := range map[string]int64{"usable": v.Summary.UsableBytes, "free": v.Summary.FreeBytes, "stored": v.Summary.StoredBytes} {
			fmt.Fprintf(w, "continuum_volume_bytes{volume=\"%s\",kind=\"%s\"} %d\n", esc(v.Slug), k, n)
		}
	}
	fmt.Fprint(w, "# HELP continuum_disk_bytes Disk capacity by kind.\n# TYPE continuum_disk_bytes gauge\n")
	fmt.Fprint(w, "# HELP continuum_disk_up 1 if the disk is ONLINE.\n# TYPE continuum_disk_up gauge\n")
	fmt.Fprint(w, "# HELP continuum_disk_state Current disk state (1 for the active state).\n# TYPE continuum_disk_state gauge\n")
	vslug := map[string]string{}
	for _, v := range vols {
		vslug[v.ID] = v.Slug
	}
	for _, d := range disks {
		lbl := fmt.Sprintf("disk=\"%s\",volume=\"%s\",ordinal=\"%d\"", esc(d.Name), esc(vslug[d.VolumeID]), d.Ordinal)
		for k, n := range map[string]int64{"total": d.TotalBytes, "free": d.FreeBytes, "effective_free": d.EffectiveFree, "reserved": d.ReservedBytes} {
			fmt.Fprintf(w, "continuum_disk_bytes{%s,kind=\"%s\"} %d\n", lbl, k, n)
		}
		up := 0
		if d.State == core.StateOnline {
			up = 1
		}
		fmt.Fprintf(w, "continuum_disk_up{%s} %d\n", lbl, up)
		fmt.Fprintf(w, "continuum_disk_state{%s,state=\"%s\"} 1\n", lbl, d.State)
	}

	type jobAgg struct {
		n           int
		bytes, fail int64
	}
	byState := map[string]*jobAgg{}
	for _, s := range []string{transfer.JobQueued, transfer.JobRunning, transfer.JobPaused, transfer.JobPausedNeedSpace, transfer.JobDone, transfer.JobFailed, transfer.JobCancelled} {
		byState[s] = &jobAgg{}
	}
	rows, err := c.DB.Query(ctx, `SELECT state, COUNT(*), COALESCE(SUM(errors_count),0) FROM jobs GROUP BY state`)
	if err == nil {
		for rows.Next() {
			var s string
			var n int
			var f int64
			rows.Scan(&s, &n, &f)
			if byState[s] == nil {
				byState[s] = &jobAgg{}
			}
			byState[s].n, byState[s].fail = n, f
		}
		rows.Close()
	}
	var bytesDone, filesDone int64
	rows, err = c.DB.Query(ctx, `SELECT COALESCE(SUM(size_bytes),0), COUNT(*) FROM job_items WHERE state='done'`)
	if err == nil {
		for rows.Next() {
			rows.Scan(&bytesDone, &filesDone)
		}
		rows.Close()
	}
	var failedItems int64
	c.DB.QueryRow(ctx, `SELECT COUNT(*) FROM job_items WHERE state='failed'`).Scan(&failedItems)
	states := make([]string, 0, len(byState))
	for s := range byState {
		states = append(states, s)
	}
	sort.Strings(states)
	fmt.Fprint(w, "# HELP continuum_jobs Jobs by state.\n# TYPE continuum_jobs gauge\n")
	for _, s := range states {
		fmt.Fprintf(w, "continuum_jobs{state=\"%s\"} %d\n", s, byState[s].n)
	}
	queue := byState[transfer.JobQueued].n + byState[transfer.JobPaused].n + byState[transfer.JobPausedNeedSpace].n
	fmt.Fprintf(w, "# HELP continuum_queue_depth Jobs waiting (queued or paused).\n# TYPE continuum_queue_depth gauge\ncontinuum_queue_depth %d\n", queue)
	fmt.Fprintf(w, "# HELP continuum_transferred_bytes_total Bytes committed by jobs.\n# TYPE continuum_transferred_bytes_total counter\ncontinuum_transferred_bytes_total %d\n", bytesDone)
	fmt.Fprintf(w, "# HELP continuum_transferred_files_total Files committed by jobs.\n# TYPE continuum_transferred_files_total counter\ncontinuum_transferred_files_total %d\n", filesDone)
	fmt.Fprintf(w, "# HELP continuum_failed_files_total Files that failed in jobs.\n# TYPE continuum_failed_files_total counter\ncontinuum_failed_files_total %d\n", failedItems)
	direct, resumed := e.DirectStats()
	fmt.Fprintf(w, "# HELP continuum_direct_transfers_total Files copied agent ↔ agent without the controller relaying bytes.\n# TYPE continuum_direct_transfers_total counter\ncontinuum_direct_transfers_total %d\n", direct)
	fmt.Fprintf(w, "# HELP continuum_direct_resumed_total Direct transfers that resumed from a partial file.\n# TYPE continuum_direct_resumed_total counter\ncontinuum_direct_resumed_total %d\n", resumed)
	fmt.Fprintf(w, "# HELP continuum_conflicts_open Open conflicts (same path on several disks).\n# TYPE continuum_conflicts_open gauge\ncontinuum_conflicts_open %d\n", e.OpenConflicts(ctx))
	if a != nil {
		st := a.Status(ctx)
		up := 0
		if st.Running {
			up = 1
		}
		fmt.Fprintf(w, "# HELP continuum_access_up 1 if the SMB access point is running.\n# TYPE continuum_access_up gauge\ncontinuum_access_up %d\n", up)
		fmt.Fprintf(w, "# HELP continuum_access_clients Connected SMB clients.\n# TYPE continuum_access_clients gauge\ncontinuum_access_clients %d\n", st.Clients)
	}
	return nil
}

// ---- diagnostics bundle ----

var pathLike = regexp.MustCompile(`^(/|[A-Za-z]:\\|\\\\)`)

// mask replaces absolute paths and host names with stable hashes (same input → same token).
func mask(v any, host string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && (k == "hostname" || k == "node_name" || (host != "" && s == host)) {
				x[k] = token("host", s)
				continue
			}
			x[k] = mask(val, host)
		}
		return x
	case []any:
		for i := range x {
			x[i] = mask(x[i], host)
		}
		return x
	case string:
		if pathLike.MatchString(x) && len(x) > 1 {
			return token("path", x)
		}
		return x
	}
	return v
}

func token(kind, s string) string {
	h := sha256.Sum256([]byte(s))
	return "<" + kind + ":" + hex.EncodeToString(h[:4]) + ">"
}

// Diagnostics writes a zip with inventory, recent jobs/events, audit status and access point state.
// Secrets are never included; with maskPaths, absolute paths and host names become hashes.
func Diagnostics(ctx context.Context, w io.Writer, c *core.Core, e *transfer.Engine, a *access.Manager, maskPaths bool) error {
	zw := zip.NewWriter(w)
	now := time.Now()
	create := func(name string) (io.Writer, error) {
		return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
	}
	host, _ := os.Hostname()
	add := func(name string, v any, err error) {
		if err != nil {
			v = map[string]string{"error": err.Error()}
		}
		b, _ := json.Marshal(v)
		var generic any
		json.Unmarshal(b, &generic)
		if maskPaths {
			generic = mask(generic, host)
		}
		f, _ := create(name)
		enc := json.NewEncoder(f)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		enc.Encode(generic)
	}
	add("info.json", map[string]any{"version": version.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"controller_id": c.ControllerID, "hostname": host, "generated_at": time.Now().UTC().Format(time.RFC3339), "masked": maskPaths}, nil)
	nodes, err := c.Nodes(ctx)
	add("nodes.json", nodes, err)
	vols, err := c.ListVolumes(ctx)
	add("volumes.json", vols, err)
	disks, err := c.ListDisks(ctx, core.DiskFilter{})
	add("disks.json", disks, err)
	jobs, err := e.List(ctx, "", "", 100)
	add("jobs.json", jobs, err)
	evs, err := c.Audit.Events(ctx, "", 500)
	add("events.json", evs, err)
	seq, err := c.Audit.Verify(ctx)
	add("audit-verify.json", map[string]any{"intact": err == nil && seq == 0, "first_broken_seq": seq}, nil)
	settings := map[string]string{}
	if rows, err := c.DB.Query(ctx, `SELECT key, value FROM settings`); err == nil {
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			if strings.Contains(k, "password") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "webhook") {
				v = "<redacted>"
			}
			settings[k] = v
		}
		rows.Close()
	}
	add("settings.json", settings, nil)
	if a != nil {
		add("access.json", a.Status(ctx), nil)
	}
	f, _ := create("README.txt")
	fmt.Fprintf(f, "Continuum diagnostics bundle %s\nNo passwords, tokens or webhook URLs are included.\nmasked paths: %v\n", version.Version, maskPaths)
	return zw.Close()
}

// ---- notifications ----

// Notable event kinds for the notification center and webhooks.
var Notable = []string{"disk.offline", "disk.capacity", "disk.smart_warning", "disk.drained", "job.failed", "job.paused_need_space", "conflict.detected", "access.failed"}

// IsNotable reports whether kind is shown in the notification center.
func IsNotable(kind string) bool {
	for _, k := range Notable {
		if k == kind {
			return true
		}
	}
	return false
}

// Message renders a one-line human text for an event.
func Message(ev audit.Event, names map[string]string) string {
	var p map[string]any
	json.Unmarshal(ev.Payload, &p)
	name := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}
	switch ev.Kind {
	case "disk.offline":
		return fmt.Sprintf("Disk %s went offline — its files are shown as offline", name(ev.DiskID))
	case "disk.capacity":
		return fmt.Sprintf("Disk %s is %v%% full (%v)", name(ev.DiskID), p["used_pct"], p["level"])
	case "disk.smart_warning":
		return fmt.Sprintf("SMART warning on disk %s: %v", name(ev.DiskID), p["status"])
	case "disk.drained":
		return fmt.Sprintf("Disk %s drained and removed from its volume", name(ev.DiskID))
	case "job.failed":
		return fmt.Sprintf("Job %s failed: %v", ev.JobID, p["message"])
	case "job.paused_need_space":
		return fmt.Sprintf("Job %s is waiting for space — attach a disk and resume", ev.JobID)
	case "conflict.detected":
		return fmt.Sprintf("Conflict: %v exists on %v disks (%v)", p["path"], p["copies"], p["kind"])
	}
	return ev.Kind
}

// Notifier posts notable events to a webhook (Slack / Discord / generic JSON) and/or e-mail, settings driven:
// notify.webhook_url, notify.format (slack|discord|generic); notify.smtp_* / notify.email_* (see EmailSettings).
type Notifier struct {
	C      *core.Core
	Client *http.Client
	mu     sync.Mutex
	last   string // last event_id handled
}

// NewNotifier starts after the newest existing event (no replay of history).
func NewNotifier(ctx context.Context, c *core.Core) *Notifier {
	n := &Notifier{C: c, Client: &http.Client{Timeout: 10 * time.Second}}
	c.DB.QueryRow(ctx, `SELECT COALESCE(MAX(event_id),'') FROM events`).Scan(&n.last)
	return n
}

// Tick sends notable events newer than the last tick. Returns how many were sent.
func (n *Notifier) Tick(ctx context.Context) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	url := n.C.Setting(ctx, "notify.webhook_url", "")
	rows, err := n.C.DB.Query(ctx, `SELECT event_id, kind, COALESCE(volume_id,''), COALESCE(disk_id,''), COALESCE(job_id,''), COALESCE(payload_json,''), ts
		FROM events WHERE event_id > ? ORDER BY event_id LIMIT 200`, n.last)
	if err != nil {
		return 0
	}
	var evs []audit.Event
	for rows.Next() {
		var ev audit.Event
		var p string
		rows.Scan(&ev.ID, &ev.Kind, &ev.VolumeID, &ev.DiskID, &ev.JobID, &p, &ev.TS)
		ev.Payload = json.RawMessage(p)
		evs = append(evs, ev)
	}
	rows.Close()
	if len(evs) == 0 {
		return 0
	}
	n.last = evs[len(evs)-1].ID
	mail, mailOn := EmailSettings(ctx, n.C)
	if url == "" && !mailOn {
		return 0
	}
	names := DiskNames(ctx, n.C)
	sent := 0
	var lines []string
	for _, ev := range evs {
		if !IsNotable(ev.Kind) {
			continue
		}
		msg := Message(ev, names)
		lines = append(lines, ev.TS+"  "+msg)
		if url != "" {
			if err := n.Send(ctx, url, n.C.Setting(ctx, "notify.format", "slack"), msg, ev); err == nil {
				sent++
			}
		}
	}
	if mailOn && len(lines) > 0 { // one digest per tick, not one mail per event
		subject := "Continuum: " + strings.TrimSpace(strings.SplitN(lines[0], "  ", 2)[1])
		if len(lines) > 1 {
			subject = fmt.Sprintf("Continuum: %d alerts", len(lines))
		}
		if err := SendEmail(ctx, mail, subject, lines); err == nil {
			sent++
		} else {
			log.Printf("notify: e-mail: %v", err)
		}
	}
	return sent
}

// Send posts one message.
func (n *Notifier) Send(ctx context.Context, url, format, text string, ev audit.Event) error {
	var body any
	switch format {
	case "discord":
		body = map[string]any{"content": "Continuum: " + text}
	case "generic":
		body = map[string]any{"source": "continuum", "text": text, "event": ev}
	default: // slack (also accepted by Mattermost, Rocket.Chat, Telegram-bridges)
		body = map[string]any{"text": "Continuum: " + text}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", res.Status)
	}
	return nil
}

// DiskNames maps disk ids to names for messages.
func DiskNames(ctx context.Context, c *core.Core) map[string]string {
	out := map[string]string{}
	if ds, err := c.ListDisks(ctx, core.DiskFilter{}); err == nil {
		for _, d := range ds {
			out[d.ID] = d.Name
		}
	}
	return out
}
