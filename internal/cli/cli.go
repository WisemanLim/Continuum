package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"continuum/internal/access"
	"continuum/internal/core"
	"continuum/internal/disk"
	"continuum/internal/placement"
	"continuum/internal/transfer"
	"continuum/internal/version"
)

type app struct {
	controller string
	token      string
	format     string
	quiet      bool
	approval   string
	yes        bool
	out        io.Writer
	errOut     io.Writer
	in         io.Reader
	c          *Client
	tty        bool
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	a := &app{out: stdout, errOut: stderr, in: stdin}
	root := a.root()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err != nil {
		code := ExitCode(err)
		msg := err.Error()
		if code == ExitGeneral && (strings.Contains(msg, "unknown command") || strings.Contains(msg, "unknown flag") ||
			strings.Contains(msg, "unknown shorthand") || strings.Contains(msg, "accepts") || strings.Contains(msg, "requires") ||
			strings.Contains(msg, "required flag")) {
			code = ExitUsage
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		var ae *APIError
		if a.format != "json" && errorsAs(err, &ae) && ae.Info != nil {
			if p, ok := ae.Info["plan"]; ok {
				b, _ := json.Marshal(p)
				var pr transfer.PlanResult
				if json.Unmarshal(b, &pr) == nil {
					tw := tabwriter.NewWriter(stderr, 0, 2, 2, ' ', 0)
					a.printPlan(tw, pr)
					tw.Flush()
				}
			}
		}
		return code
	}
	return ExitOK
}

func errorsAs(err error, target **APIError) bool {
	for e := err; e != nil; {
		if ae, ok := e.(*APIError); ok {
			*target = ae
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "continuum",
		Short:         "Continuum — ordinal-based cross-platform disk pooling (alias: ctm)",
		Long:          "Continuum CLI. Same Controller API as the desktop GUI and Chrome extension.\n\nExamples:\n  continuum disk attach --path /Volumes/ExternalSSD --role portable\n  continuum volume create research-pool --disks disk-01,win-d --min-free 100GiB\n  continuum plan copy ./dataset research-pool:/research/dataset\n  continuum copy ./dataset research-pool:/research/dataset --yes",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			tok := a.token
			if tok == "" {
				tok = savedToken(a.controller) // `continuum login`
			}
			a.c = newClient(a.controller, tok)
			a.c.Approval = a.approval
			if f, ok := a.out.(*os.File); ok {
				a.tty = isTTY(f)
			}
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&a.controller, "controller", env("CONTINUUM_CONTROLLER", "http://127.0.0.1:8080"), "Controller URL (env CONTINUUM_CONTROLLER)")
	pf.StringVar(&a.token, "token", os.Getenv("CONTINUUM_TOKEN"), "API token (env CONTINUUM_TOKEN)")
	pf.StringVar(&a.format, "format", env("CONTINUUM_FORMAT", "table"), "output format: table|json|yaml")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "print IDs only")
	pf.StringVar(&a.approval, "approval", os.Getenv("CONTINUUM_APPROVAL"), "approved request id for a two-person-approval action (env CONTINUUM_APPROVAL)")

	root.AddCommand(a.versionCmd(), a.nodeCmd(), a.diskCmd(), a.volumeCmd(), a.planCmd(), a.copyCmd(), a.moveCmd(),
		a.mkdirCmd(), a.mvCmd(), a.rmCmd(), a.trashCmd(), a.lsCmd(), a.locateCmd(), a.verifyCmd(), a.scanCmd(), a.conflictCmd(), a.accessCmd(), a.vfsCmd(), a.diagCmd(), a.settingsCmd(), a.jobCmd(), a.activityCmd())
	root.AddCommand(a.authCmds()...)
	return root
}

func (a *app) confirm(prompt string) error {
	if a.yes {
		return nil
	}
	f, ok := a.in.(*os.File)
	if !ok || !isTTY(f) {
		return &usageError{prompt + " — non-interactive session: re-run with --yes"}
	}
	fmt.Fprintf(a.errOut, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(a.in).ReadString('\n')
	if s := strings.ToLower(strings.TrimSpace(line)); s == "y" || s == "yes" {
		return nil
	}
	return &exitError{ExitInterrupt, "aborted"}
}

// confirmName requires typing the target name for destructive actions (FR-S05, NFR-U03).
func (a *app) confirmName(action, name string) error {
	if a.yes {
		return nil
	}
	f, ok := a.in.(*os.File)
	if !ok || !isTTY(f) {
		return &usageError{action + " " + name + " is destructive — non-interactive session: re-run with --yes"}
	}
	fmt.Fprintf(a.errOut, "%s %q is destructive. Type the name to confirm: ", action, name)
	line, _ := bufio.NewReader(a.in).ReadString('\n')
	if strings.TrimSpace(line) == name {
		return nil
	}
	return &exitError{ExitInterrupt, "name mismatch — aborted"}
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print CLI and controller versions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(a.out, "continuum", version.Version)
		var info map[string]any
		if err := a.c.Get("/v1/info", &info); err == nil {
			fmt.Fprintf(a.out, "controller %v (%v) at %s\n", info["version"], info["controller_id"], a.controller)
		}
		return nil
	}}
}

func (a *app) nodeCmd() *cobra.Command {
	c := &cobra.Command{Use: "node", Short: "Storage nodes (agents)"}
	c.AddCommand(&cobra.Command{Use: "list", Short: "List nodes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var ns []core.Node
		if err := a.c.Get("/v1/nodes", &ns); err != nil {
			return err
		}
		return a.output(ns, func(w io.Writer) {
			fmt.Fprintln(w, "NODE\tHOSTNAME\tOS\tAGENT\tSTATE\tDISKS\tHEARTBEAT")
			for _, n := range ns {
				fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%s\t%d\t%s\n", n.ID, n.Hostname, n.OS, n.OSVersion, n.AgentVersion, n.State, n.DiskCount, n.LastHeartbeat)
			}
		})
	}})
	var label string
	var ttl int
	tok := &cobra.Command{Use: "token", Short: "Create a one-time enrollment token for a new agent (FR-N01)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var t struct {
			Token, ExpiresAt, Command string
		}
		if err := a.c.Post("/v1/nodes/tokens", map[string]any{"label": label, "ttl_min": ttl}, &t); err != nil {
			return err
		}
		if a.quiet {
			fmt.Fprintln(a.out, t.Token)
			return nil
		}
		return a.output(t, func(w io.Writer) {
			fmt.Fprintf(w, "token (single use, expires %s):\n  %s\n\non the new node run:\n  %s\n", t.ExpiresAt, t.Token, t.Command)
		})
	}}
	tok.Flags().StringVar(&label, "label", "", "note shown in the audit log")
	tok.Flags().IntVar(&ttl, "ttl", 60, "minutes until the token expires (max 10080)")
	c.AddCommand(tok)
	revoke := &cobra.Command{Use: "revoke <node>", Short: "Revoke a node: certificate rejected, disks offline", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.confirmName("revoke", args[0]); err != nil {
			return err
		}
		if err := a.c.Post("/v1/nodes/"+q(args[0])+"/revoke", map[string]any{}, nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "revoked", args[0])
		return nil
	}}
	revoke.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip name confirmation")
	c.AddCommand(revoke)
	c.AddCommand(&cobra.Command{Use: "inventory <node>", Short: "Volumes and folders a node offers for attach", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var inv struct {
			Online bool              `json:"online"`
			Disks  []disk.Descriptor `json:"disks"`
			Roots  []string          `json:"roots"`
		}
		if err := a.c.Get("/v1/nodes/"+q(args[0])+"/inventory", &inv); err != nil {
			return err
		}
		return a.output(inv, func(w io.Writer) {
			fmt.Fprintf(w, "online\t%v\n", inv.Online)
			for _, r := range inv.Roots {
				fmt.Fprintf(w, "root\t%s\n", r)
			}
			for _, d := range inv.Disks {
				fmt.Fprintf(w, "volume\t%s\t%s\t%s free\n", d.MountPath, d.FilesystemType, humanBytes(int64(d.AvailableBytes)))
			}
		})
	}})
	return c
}

// ---- disk ----

func (a *app) diskCmd() *cobra.Command {
	c := &cobra.Command{Use: "disk", Short: "Disk management (discover, attach, detach, role)"}
	var local bool
	discover := &cobra.Command{Use: "discover", Short: "Discover local disks/volumes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var ds []core.DiscoveredDisk
		if local { // FR-C10: offline mode, no controller needed
			raw, err := disk.Discover(context.Background(), "local", disk.Stat)
			if err != nil {
				return err
			}
			for _, d := range raw {
				ds = append(ds, core.DiscoveredDisk{Descriptor: d})
			}
		} else if err := a.c.Get("/v1/disks/discover", &ds); err != nil {
			return err
		}
		return a.output(ds, func(w io.Writer) {
			fmt.Fprintln(w, "MOUNT\tLABEL\tFS\tBUS\tSIZE\tFREE\tREMOVABLE\tIDENTITY\tREGISTERED")
			for _, d := range ds {
				reg := "-"
				if d.DiskID != "" {
					reg = d.DiskID
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%v\t%s\t%s\n", d.MountPath, d.VolumeLabel, d.FilesystemType, d.BusType,
					humanBytes(int64(d.TotalBytes)), humanBytes(int64(d.AvailableBytes)), d.Removable, short(d.FilesystemUUID, 20), reg)
			}
		})
	}}
	discover.Flags().BoolVar(&local, "local", false, "run discovery locally without a controller")
	c.AddCommand(discover)

	var volume string
	var unassigned bool
	list := &cobra.Command{Use: "list", Short: "List registered disks", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		path := "/v1/disks"
		switch {
		case volume != "":
			path += "?volume=" + q(volume)
		case unassigned:
			path += "?unassigned=1"
		}
		var ds []core.Disk
		if err := a.c.Get(path, &ds); err != nil {
			return err
		}
		return a.printDisks(ds)
	}}
	list.Flags().StringVar(&volume, "volume", "", "only disks of this volume")
	list.Flags().BoolVar(&unassigned, "unassigned", false, "only disks not in any volume")
	c.AddCommand(list)

	c.AddCommand(&cobra.Command{Use: "inspect <disk>", Short: "Show disk details", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var d core.Disk
		if err := a.c.Get("/v1/disks/"+q(args[0]), &d); err != nil {
			return err
		}
		if a.format == "table" {
			a.format = "yaml"
		}
		return a.output(d, nil)
	}})

	var pfMin string
	preflight := &cobra.Command{Use: "preflight <path>", Short: "Run the 10 pre-attach checks without registering", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		mf, err := parseSize(pfMin)
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(args[0])
		var rep disk.Report
		if err := a.c.Post("/v1/disks/preflight", map[string]any{"path": abs, "min_free_bytes": mf}, &rep); err != nil {
			return err
		}
		return a.output(rep, func(w io.Writer) { printChecks(w, rep) })
	}}
	preflight.Flags().StringVar(&pfMin, "min-free", "1GiB", "reserve to validate")
	c.AddCommand(preflight)

	var at struct {
		path, name, role, minFree, volume, node string
		ordinal                                 int
	}
	attach := &cobra.Command{Use: "attach [path]", Short: "Register a disk mount point or folder (runs preflight)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			at.path = args[0]
		}
		if at.path == "" {
			return &usageError{"path required (argument or --path)"}
		}
		abs := at.path
		if at.node == "" { // a remote node resolves its own paths
			abs, _ = filepath.Abs(at.path)
		}
		req := core.AttachRequest{Path: abs, Name: at.name, Role: at.role, VolumeID: at.volume, Ordinal: at.ordinal, NodeID: at.node}
		if at.minFree != "" {
			mf, err := parseSize(at.minFree)
			if err != nil {
				return err
			}
			req.MinFreeBytes = &mf
		}
		var res core.AttachResult
		if err := a.c.Post("/v1/disks", req, &res); err != nil {
			var ae *APIError
			if errorsAs(err, &ae) && ae.Info != nil {
				b, _ := json.Marshal(ae.Info["preflight"])
				var rep disk.Report
				if json.Unmarshal(b, &rep) == nil {
					printChecks(a.errOut, rep)
				}
			}
			return err
		}
		if a.quiet {
			fmt.Fprintln(a.out, res.Disk.ID)
			return nil
		}
		return a.output(res, func(w io.Writer) {
			printChecks(w, res.Preflight)
			verb := "attached"
			if res.Reattach {
				verb = "re-attached (marker)"
			}
			fmt.Fprintf(w, "\n%s %s  %s  %s  role=%s  %s free of %s\n", verb, res.Disk.ID, res.Disk.Name, res.Disk.MountPath, res.Disk.Role,
				humanBytes(res.Disk.EffectiveFree), humanBytes(res.Disk.TotalBytes))
		})
	}}
	f := attach.Flags()
	f.StringVar(&at.path, "path", "", "mount point or folder")
	f.StringVar(&at.name, "name", "", "display name (default: label or folder name)")
	f.StringVar(&at.role, "role", "always-on", "always-on|semi-permanent|portable|import|archive")
	f.StringVar(&at.minFree, "min-free", "", "per-disk reserve override (e.g. 100GiB)")
	f.StringVar(&at.volume, "volume", "", "append to this volume")
	f.IntVar(&at.ordinal, "ordinal", 0, "insert position in the volume (1-based)")
	f.StringVar(&at.node, "node", "", "agent node that owns the path (default: this host)")
	c.AddCommand(attach)

	detach := &cobra.Command{Use: "detach <disk>", Short: "Detach immediately (data kept, files shown offline)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.confirmName("detach", args[0]); err != nil {
			return err
		}
		var d core.Disk
		if err := a.c.Post("/v1/disks/"+q(args[0])+"/detach", nil, &d); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "detached %s (%s)\n", d.Name, d.State)
		return nil
	}}
	detach.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip confirmation")
	c.AddCommand(detach)
	var refresh bool
	smart := &cobra.Command{Use: "smart <disk>", Short: "SMART health, temperature and attributes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var s disk.Smart
		p := "/v1/disks/" + q(args[0]) + "/smart"
		if refresh {
			p += "?refresh=1"
		}
		if err := a.c.Get(p, &s); err != nil {
			return err
		}
		if err := a.output(s, func(w io.Writer) {
			fmt.Fprintf(w, "status\t%s (%s)\n", s.Status, s.Source)
			if s.Model != "" {
				fmt.Fprintf(w, "model\t%s %s\n", s.Model, s.Serial)
			}
			if s.Temperature != nil {
				fmt.Fprintf(w, "temperature\t%d °C\n", *s.Temperature)
			}
			if s.PowerOnH != nil {
				fmt.Fprintf(w, "power-on\t%d h\n", *s.PowerOnH)
			}
			for _, at := range s.Attributes {
				flag := ""
				if at.Failed {
					flag = "FAILED"
				}
				fmt.Fprintf(w, "  %d\t%s\t%d\t%s\t%s\n", at.ID, at.Name, at.Value, at.Raw, flag)
			}
			if s.Note != "" {
				fmt.Fprintf(w, "note\t%s\n", s.Note)
			}
		}); err != nil {
			return err
		}
		if s.Passed != nil && !*s.Passed {
			return &exitError{ExitVerify, "SMART reports a failing device"}
		}
		return nil
	}}
	smart.Flags().BoolVar(&refresh, "refresh", false, "query the device again (default: cached result)")
	c.AddCommand(smart)
	var top int
	usage := &cobra.Command{Use: "usage <disk>", Short: "Usage analysis: folders, largest files, file types", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var u transfer.Usage
		if err := a.c.Get(fmt.Sprintf("/v1/disks/%s/usage?top=%d", q(args[0]), top), &u); err != nil {
			return err
		}
		return a.output(u, func(w io.Writer) {
			fmt.Fprintf(w, "%s in %d files\n\nFOLDER\tSIZE\tFILES\n", humanBytes(u.Bytes), u.Files)
			for _, c := range u.Tree.Children {
				fmt.Fprintf(w, "%s\t%s\t%d\n", c.Path, humanBytes(c.Bytes), c.Files)
			}
			fmt.Fprintln(w, "\nLARGEST\tSIZE\tDISK")
			for _, f := range u.Top {
				fmt.Fprintf(w, "%s\t%s\t%s\n", f.LogicalPath, humanBytes(f.Size), f.Disk)
			}
			fmt.Fprintln(w, "\nTYPE\tSIZE\tFILES")
			for _, t := range u.Types {
				fmt.Fprintf(w, "%s\t%s\t%d\n", t.Ext, humanBytes(t.Bytes), t.Files)
			}
		})
	}}
	usage.Flags().IntVar(&top, "top", 20, "largest N files")
	c.AddCommand(usage)
	var minSize string
	dupes := &cobra.Command{Use: "dupes <disk>", Short: "Find duplicate files (size → partial hash → BLAKE3); delete with `continuum rm`", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ms, err := parseSize(minSize)
		if err != nil {
			return &usageError{err.Error()}
		}
		var rep transfer.DupeReport
		if err := a.c.Get(fmt.Sprintf("/v1/disks/%s/dupes?min_size=%d", q(args[0]), ms), &rep); err != nil {
			return err
		}
		return a.output(rep, func(w io.Writer) {
			fmt.Fprintf(w, "%d groups, %s reclaimable (%d files scanned, %d hashed)\n", len(rep.Groups), humanBytes(rep.Wasted), rep.Scanned, rep.Hashed)
			for _, g := range rep.Groups {
				fmt.Fprintf(w, "\n%s × %d  (%s)\n", humanBytes(g.Size), len(g.Files), g.Checksum[:12])
				for _, f := range g.Files {
					fmt.Fprintf(w, "  %s\t%s#%d\n", f.LogicalPath, f.Disk, f.Ordinal)
				}
			}
		})
	}}
	dupes.Flags().StringVar(&minSize, "min-size", "1", "ignore files smaller than this (e.g. 1M)")
	c.AddCommand(dupes)
	var benchSize string
	bench := &cobra.Command{Use: "bench <disk>", Short: "Benchmark sequential write/read and 4K random read (temp file in the data root)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		sz, err := parseSize(benchSize)
		if err != nil {
			return &usageError{err.Error()}
		}
		var b disk.Bench
		if err := a.c.Post("/v1/disks/"+q(args[0])+"/bench", map[string]int64{"size_bytes": sz}, &b); err != nil {
			return err
		}
		return a.output(b, func(w io.Writer) {
			fmt.Fprintf(w, "seq write\t%.0f MB/s\nseq read\t%.0f MB/s\nrandom 4K read\t%.0f IOPS\nfile\t%s\tcache bypass=%v\n", b.SeqWriteMBs, b.SeqReadMBs, b.RandReadIOPS, humanBytes(b.SizeBytes), b.CacheBypass)
		})
	}}
	bench.Flags().StringVar(&benchSize, "size", "256M", "test file size")
	c.AddCommand(bench)
	var passes int
	wipe := &cobra.Command{Use: "wipe <disk>", Short: "Securely delete the disk's Continuum data (overwrite + remove) and unregister it — drain first", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var d core.Disk
		if err := a.c.Get("/v1/disks/"+q(args[0]), &d); err != nil {
			return err
		}
		if err := a.confirmName("wipe", d.Name); err != nil {
			return err
		}
		var r transfer.WipeResult
		if err := a.c.Post("/v1/disks/"+q(args[0])+"/wipe", map[string]any{"confirm": d.Name, "passes": passes}, &r); err != nil {
			return err
		}
		return a.output(r, func(w io.Writer) {
			fmt.Fprintf(w, "wiped %s: %d files, %s overwritten %d× and removed; disk unregistered\n", r.Disk, r.Files, humanBytes(r.Bytes), r.Passes)
		})
	}}
	wipe.Flags().IntVar(&passes, "passes", 1, "overwrite passes (1-7)")
	wipe.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip name confirmation")
	c.AddCommand(wipe)
	c.AddCommand(&cobra.Command{Use: "open <disk>", Short: "Open the disk's data folder in Finder/Explorer", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var out map[string]string
		if err := a.c.Post("/v1/disks/"+q(args[0])+"/reveal", map[string]any{}, &out); err != nil {
			return err
		}
		return a.output(out, func(w io.Writer) { fmt.Fprintln(w, "opened", out["opened"]) })
	}})
	c.AddCommand(&cobra.Command{Use: "enable <disk>", Short: "Bring a detached disk back online", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var d core.Disk
		if err := a.c.Post("/v1/disks/"+q(args[0])+"/enable", nil, &d); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "enabled %s (%s)\n", d.Name, d.State)
		return nil
	}})
	c.AddCommand(&cobra.Command{Use: "role <disk> <role>", Short: "Change role (always-on|semi-permanent|portable|import|archive)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		var d core.Disk
		if err := a.c.Patch("/v1/disks/"+q(args[0]), map[string]string{"role": args[1]}, &d); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s role=%s\n", d.Name, d.Role)
		return nil
	}})
	remove := &cobra.Command{Use: "remove <disk>", Short: "Unregister a disk that holds no catalog objects", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.confirmName("remove", args[0]); err != nil {
			return err
		}
		if err := a.c.Delete("/v1/disks/"+q(args[0]), nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "removed", args[0])
		return nil
	}}
	remove.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip confirmation")
	c.AddCommand(remove)
	return c
}

func printChecks(w io.Writer, rep disk.Report) {
	fmt.Fprintf(w, "Preflight %s\n", rep.Path)
	for _, ch := range rep.Checks {
		mark := map[string]string{"pass": "✓", "warn": "!", "fail": "✗"}[ch.Status]
		fmt.Fprintf(w, "  %s %2d %-12s %s\n", mark, ch.No, ch.Name, ch.Detail)
	}
}

func (a *app) printDisks(ds []core.Disk) error {
	if a.quiet {
		for _, d := range ds {
			fmt.Fprintln(a.out, d.ID)
		}
		return nil
	}
	return a.output(ds, func(w io.Writer) {
		fmt.Fprintln(w, "ORD\tDISK\tNAME\tNODE\tROLE\tSTATE\tFREE\tTOTAL\tUSED\tRESERVE\tPATH")
		for _, d := range ds {
			ord := "-"
			if d.Ordinal > 0 {
				ord = fmt.Sprint(d.Ordinal)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d%%\t%s\t%s\n", ord, d.ID, d.Name, d.NodeName, d.Role, d.State,
				humanBytes(d.EffectiveFree), humanBytes(d.TotalBytes), d.UsedPct, humanBytes(d.EffectiveMinFree), d.MountPath)
		}
	})
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ---- volume ----

func (a *app) volumeCmd() *cobra.Command {
	c := &cobra.Command{Use: "volume", Short: "Volumes (ordered disk pools)"}
	var cr struct {
		disks, policy, minFree, mount string
		spill                         bool
	}
	create := &cobra.Command{Use: "create <name>", Short: "Create a volume from disks in order", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req := core.CreateVolumeRequest{Name: args[0], PlacementPolicy: policyAlias(cr.policy), MountPath: cr.mount, DiskIDs: splitList(cr.disks)}
		if cr.minFree != "" {
			mf, err := parseSize(cr.minFree)
			if err != nil {
				return err
			}
			req.MinFreeBytes = &mf
		}
		if cmd.Flags().Changed("spillover") || cmd.Flags().Changed("no-spillover") {
			s := cr.spill
			req.AllowSpillover = &s
		}
		var v core.Volume
		if err := a.c.Post("/v1/volumes", req, &v); err != nil {
			return err
		}
		return a.printVolume(v)
	}}
	f := create.Flags()
	f.StringVar(&cr.disks, "disks", "", "comma-separated disk ids/names in ordinal order (required)")
	f.StringVar(&cr.policy, "policy", "", "fill|topdir|whole|mostfree|balanced (default fill)")
	f.StringVar(&cr.minFree, "min-free", "", "reserve per disk (default 100GiB)")
	f.StringVar(&cr.mount, "mount-path", "", "access point path")
	f.BoolVar(&cr.spill, "spillover", true, "allow spillover to next disk")
	create.MarkFlagRequired("disks")
	c.AddCommand(create)

	c.AddCommand(&cobra.Command{Use: "list", Short: "List volumes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var vs []core.Volume
		if err := a.c.Get("/v1/volumes", &vs); err != nil {
			return err
		}
		if a.quiet {
			for _, v := range vs {
				fmt.Fprintln(a.out, v.Slug)
			}
			return nil
		}
		return a.output(vs, func(w io.Writer) {
			fmt.Fprintln(w, "VOLUME\tNAME\tPOLICY\tDISKS\tUSABLE\tFREE\tSTORED\tREV")
			for _, v := range vs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\n", v.Slug, v.Name, v.PlacementPolicy, v.Summary.DiskCount,
					humanBytes(v.Summary.UsableBytes), humanBytes(v.Summary.FreeBytes), humanBytes(v.Summary.StoredBytes), v.Revision)
			}
		})
	}})
	c.AddCommand(&cobra.Command{Use: "inspect <volume>", Short: "Show a volume and its ordered disks", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var v core.Volume
		if err := a.c.Get("/v1/volumes/"+q(args[0]), &v); err != nil {
			return err
		}
		return a.printVolume(v)
	}})

	var ed struct {
		name, policy, minFree, mount string
		spill                        bool
	}
	edit := &cobra.Command{Use: "edit <volume>", Short: "Edit configuration (optimistic locking)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var v core.Volume
		if err := a.c.Get("/v1/volumes/"+q(args[0]), &v); err != nil {
			return err
		}
		req := core.UpdateVolumeRequest{ExpectedRevision: v.Revision}
		fl := cmd.Flags()
		if fl.Changed("name") {
			req.Name = &ed.name
		}
		if fl.Changed("policy") {
			p := policyAlias(ed.policy)
			req.PlacementPolicy = &p
		}
		if fl.Changed("mount-path") {
			req.MountPath = &ed.mount
		}
		if fl.Changed("min-free") {
			mf, err := parseSize(ed.minFree)
			if err != nil {
				return err
			}
			req.MinFreeBytes = &mf
		}
		if fl.Changed("spillover") {
			req.AllowSpillover = &ed.spill
		}
		if err := a.c.Patch("/v1/volumes/"+q(args[0]), req, &v); err != nil {
			return err
		}
		return a.printVolume(v)
	}}
	ef := edit.Flags()
	ef.StringVar(&ed.name, "name", "", "new name")
	ef.StringVar(&ed.policy, "policy", "", "placement policy")
	ef.StringVar(&ed.minFree, "min-free", "", "reserve per disk")
	ef.StringVar(&ed.mount, "mount-path", "", "access point path")
	ef.BoolVar(&ed.spill, "spillover", true, "allow spillover")
	c.AddCommand(edit)

	del := &cobra.Command{Use: "delete <volume>", Short: "Delete the volume registry entry (files stay on disks)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.confirmName("delete volume", args[0]); err != nil {
			return err
		}
		if err := a.c.Delete("/v1/volumes/"+q(args[0]), nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "deleted volume", args[0], "(files remain on the disks)")
		return nil
	}}
	del.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip confirmation")
	c.AddCommand(del)

	var outFile string
	export := &cobra.Command{Use: "export <volume>", Short: "Export configuration as YAML (FR-V22)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var w io.Writer = a.out
		if outFile != "" {
			fh, err := os.Create(outFile)
			if err != nil {
				return err
			}
			defer fh.Close()
			w = fh
		}
		return a.c.Get("/v1/volumes/"+q(args[0])+"/export", w)
	}}
	export.Flags().StringVarP(&outFile, "output", "o", "", "write to file")
	c.AddCommand(export)
	c.AddCommand(&cobra.Command{Use: "import <file.yaml>", Short: "Import a volume configuration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var v core.Volume
		if err := a.c.PostRaw("/v1/volumes/import", "application/yaml", b, &v); err != nil {
			return err
		}
		return a.printVolume(v)
	}})

	order := &cobra.Command{Use: "order <volume> set <d1,d2,...>", Short: "Set disk order (new placements only; see --rebalance note)", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		if args[1] != "set" {
			return &usageError{"usage: volume order <volume> set d1,d2,..."}
		}
		return a.setOrder(args[0], splitList(args[2]))
	}}
	c.AddCommand(order)
	c.AddCommand(a.drainCmd(false), a.rebalanceCmd(false))

	var before, after string
	move := &cobra.Command{Use: "move <volume> <disk> --before|--after <disk>", Short: "Move one disk in the order", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		var v core.Volume
		if err := a.c.Get("/v1/volumes/"+q(args[0]), &v); err != nil {
			return err
		}
		ref := before
		if ref == "" {
			ref = after
		}
		if ref == "" {
			return &usageError{"--before or --after required"}
		}
		ids := []string{}
		var moving string
		for _, d := range v.Disks {
			if d.ID == args[1] || d.Name == args[1] {
				moving = d.ID
				continue
			}
			ids = append(ids, d.ID)
		}
		if moving == "" {
			return &APIError{Code: core.CodeNotFound, Msg: "disk " + args[1] + " is not in " + args[0]}
		}
		out := []string{}
		placed := false
		for _, d := range v.Disks {
			if d.ID == moving {
				continue
			}
			match := d.ID == ref || d.Name == ref
			if match && before != "" {
				out = append(out, moving)
				placed = true
			}
			out = append(out, d.ID)
			if match && after != "" {
				out = append(out, moving)
				placed = true
			}
		}
		if !placed {
			return &APIError{Code: core.CodeNotFound, Msg: "reference disk " + ref + " is not in " + args[0]}
		}
		return a.setOrder(args[0], out)
	}}
	move.Flags().StringVar(&before, "before", "", "place before this disk")
	move.Flags().StringVar(&after, "after", "", "place after this disk")
	c.AddCommand(move)
	return c
}

func (a *app) setOrder(vol string, ids []string) error {
	var v core.Volume
	if err := a.c.Get("/v1/volumes/"+q(vol), &v); err != nil {
		return err
	}
	resolved := make([]string, 0, len(ids))
	for _, ref := range ids {
		found := ref
		for _, d := range v.Disks {
			if d.Name == ref {
				found = d.ID
			}
		}
		resolved = append(resolved, found)
	}
	if err := a.c.Put("/v1/volumes/"+q(vol)+"/disks", core.SetDisksRequest{DiskIDs: resolved, ExpectedRevision: v.Revision}, &v); err != nil {
		return err
	}
	if err := a.printVolume(v); err != nil {
		return err
	}
	if a.format == "table" {
		fmt.Fprintln(a.errOut, "note: order changes apply to new placements only; existing files stay where they are (DP03).")
	}
	return nil
}

func (a *app) printVolume(v core.Volume) error {
	if a.quiet {
		fmt.Fprintln(a.out, v.Slug)
		return nil
	}
	return a.output(v, func(w io.Writer) {
		fmt.Fprintf(w, "Volume: %s (%s)\tpolicy=%s\tmin_free=%s\tspillover=%v\trevision=%d\n", v.Name, v.Slug, v.PlacementPolicy,
			humanBytes(v.MinFreeBytes), v.AllowSpillover, v.Revision)
		fmt.Fprintf(w, "%d disks · %s usable · %s free · %s stored\n\n", v.Summary.DiskCount, humanBytes(v.Summary.UsableBytes),
			humanBytes(v.Summary.FreeBytes), humanBytes(v.Summary.StoredBytes))
		fmt.Fprintln(w, "ORD\tDISK\tNAME\tNODE\tROLE\tSTATE\tFREE\tTOTAL")
		for _, d := range v.Disks {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.Ordinal, d.ID, d.Name, d.NodeName, d.Role, d.State, humanBytes(d.EffectiveFree), humanBytes(d.TotalBytes))
		}
		for _, warn := range v.Warnings {
			fmt.Fprintf(w, "! %s\n", warn)
		}
	})
}

func policyAlias(p string) string {
	switch strings.ToLower(p) {
	case "fill", "file", "fill-in-order", "file-spill":
		return core.PolicyFillInOrder
	case "topdir", "topdir-affinity":
		return core.PolicyTopdirAffinity
	case "whole", "whole-tree-only":
		return core.PolicyWholeTree
	case "mostfree", "most-free":
		return core.PolicyMostFree
	case "balanced":
		return core.PolicyBalanced
	}
	return p
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- plan / copy / move ----

type xferFlags struct {
	start, target, transfers  int
	placement, spill, minFree string
	verify, onConflict, bw    string
	include, exclude          []string
	dryRun, wait, noWait      bool
	idem, onInsufficient      string
	window                    string
}

func (x *xferFlags) register(cmd *cobra.Command, copy bool) {
	f := cmd.Flags()
	f.IntVar(&x.start, "start-ordinal", 0, "first ordinal to consider (R06)")
	f.IntVar(&x.target, "target-ordinal", 0, "force a single disk — no spillover (R04)")
	f.StringVar(&x.placement, "placement", "", "fill|topdir|whole|mostfree|balanced (default: volume policy)")
	f.StringVar(&x.spill, "spillover", "", "next|fail (default: volume setting)")
	f.StringVar(&x.minFree, "min-free", "", "reserve override for this job")
	f.StringVar(&x.verify, "verify", "blake3", "blake3|none")
	f.StringVar(&x.onConflict, "on-conflict", "skip", "skip|replace|rename|newer")
	f.StringVar(&x.onInsufficient, "on-insufficient", "fail", "fail|wait (wait = PAUSED_NEED_SPACE until a disk is added)")
	if copy {
		f.StringSliceVar(&x.include, "include", nil, "glob to include (repeatable)")
		f.StringSliceVar(&x.exclude, "exclude", nil, "glob to exclude (repeatable)")
		f.StringVar(&x.bw, "bwlimit", "", "bandwidth limit, e.g. 50M (bytes/s)")
		f.IntVar(&x.transfers, "transfers", 0, "parallel files (default 4)")
		f.StringVar(&x.idem, "idempotency-key", "", "safe re-run key (FR-T22)")
	}
	f.BoolVar(&x.dryRun, "dry-run", false, "show the plan only")
	f.BoolVar(&x.noWait, "no-wait", false, "return after the job is queued")
	f.StringVar(&x.window, "window", "", "run only inside this local time window, e.g. 22:00-06:00")
}

func (x *xferFlags) request(typ, vol, src, dst string) (transfer.PlanRequest, error) {
	req := transfer.PlanRequest{Type: typ, Volume: vol, Source: src, Destination: dst, StartOrdinal: x.start, ForcedOrdinal: x.target,
		Policy: policyAlias(x.placement), Verify: x.verify, OnConflict: x.onConflict, OnInsufficient: x.onInsufficient,
		Include: x.include, Exclude: x.exclude, Transfers: x.transfers, IdempotencyKey: x.idem, Window: x.window}
	switch x.spill {
	case "":
	case "next", "true", "on":
		t := true
		req.Spillover = &t
	case "fail", "false", "off":
		f := false
		req.Spillover = &f
	default:
		return req, &usageError{"--spillover must be next|fail"}
	}
	if x.minFree != "" {
		mf, err := parseSize(x.minFree)
		if err != nil {
			return req, err
		}
		req.MinFreeBytes = &mf
	}
	if x.bw != "" {
		b, err := parseSize(x.bw)
		if err != nil {
			return req, err
		}
		req.BwLimit = b
	}
	return req, nil
}

func (a *app) planCmd() *cobra.Command {
	c := &cobra.Command{Use: "plan", Short: "Show placement plans without changing anything (FR-C04)"}
	var x xferFlags
	cp := &cobra.Command{Use: "copy <src> <volume>:/<dst>", Short: "Plan a copy", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		vol, dst, err := splitVolPath(args[1])
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(args[0])
		req, err := x.request(transfer.TypeCopy, vol, abs, dst)
		if err != nil {
			return err
		}
		return a.showPlan(req)
	}}
	x.register(cp, true)
	var y xferFlags
	mv := &cobra.Command{Use: "move <volume>:/<src> <volume>:/<dst>", Short: "Plan a move", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		vol, src, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		_, dst, err := splitVolPath(args[1])
		if err != nil {
			return err
		}
		req, err := y.request(transfer.TypeMove, vol, src, dst)
		if err != nil {
			return err
		}
		return a.showPlan(req)
	}}
	y.register(mv, false)
	c.AddCommand(cp, mv, a.drainCmd(true), a.rebalanceCmd(true))
	return c
}

// relocFlags are shared by drain / rebalance (FR-D10, FR-P10).
type relocFlags struct {
	window         string
	start          int
	placement, bw  string
	dryRun, noWait bool
	path           string
}

func (r *relocFlags) register(cmd *cobra.Command, planOnly bool) {
	f := cmd.Flags()
	f.StringVar(&r.bw, "bwlimit", "", "bandwidth limit, e.g. 50M (bytes/s)")
	f.StringVar(&r.window, "window", "", "run only inside this local time window, e.g. 22:00-06:00")
	if !planOnly {
		f.BoolVar(&r.dryRun, "dry-run", false, "show the plan only")
		f.BoolVar(&r.noWait, "no-wait", false, "return after the job is queued")
	}
}

func (a *app) runReloc(req transfer.PlanRequest, r *relocFlags, planOnly bool) error {
	req.Window = r.window
	if r.bw != "" {
		b, err := parseSize(r.bw)
		if err != nil {
			return err
		}
		req.BwLimit = b
	}
	if planOnly {
		return a.showPlan(req)
	}
	return a.runTransfer(req, &xferFlags{dryRun: r.dryRun, noWait: r.noWait})
}

func (a *app) drainCmd(planOnly bool) *cobra.Command {
	var r relocFlags
	cmd := &cobra.Command{Use: "drain <volume> <disk>", Short: "Move every file off a disk (copy → verify → delete), then remove it from the volume",
		Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReloc(transfer.PlanRequest{Type: transfer.TypeDrain, Volume: args[0], Source: args[1], StartOrdinal: r.start}, &r, planOnly)
		}}
	r.register(cmd, planOnly)
	cmd.Flags().IntVar(&r.start, "start-ordinal", 0, "first destination ordinal")
	if !planOnly {
		cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "start without confirmation")
	}
	return cmd
}

func (a *app) rebalanceCmd(planOnly bool) *cobra.Command {
	var r relocFlags
	cmd := &cobra.Command{Use: "rebalance <volume>", Short: "Re-place existing data by the current order/policy (only misplaced files move)",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReloc(transfer.PlanRequest{Type: transfer.TypeRebalance, Volume: args[0], Source: r.path, Policy: policyAlias(r.placement)}, &r, planOnly)
		}}
	r.register(cmd, planOnly)
	cmd.Flags().StringVar(&r.path, "path", "/", "only this folder")
	cmd.Flags().StringVar(&r.placement, "placement", "", "fill|topdir|whole|mostfree|balanced (default: volume policy)")
	if !planOnly {
		cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "start without confirmation")
	}
	return cmd
}

func (a *app) showPlan(req transfer.PlanRequest) error {
	var pr transfer.PlanResult
	if err := a.c.Post("/v1/plan", req, &pr); err != nil {
		return err
	}
	if err := a.output(pr, func(w io.Writer) { a.printPlan(w, pr) }); err != nil {
		return err
	}
	if !pr.Sufficient {
		code := ExitSpace
		if pr.Error == "ForcedTargetUnavailable" || pr.Error == "NoWritableDisk" {
			code = ExitOffline
		}
		return &exitError{code, pr.Error}
	}
	return nil
}

// printPlan renders PRD §9.3.
func (a *app) printPlan(w io.Writer, pr transfer.PlanResult) {
	fmt.Fprintln(w, "Source")
	fmt.Fprintf(w, "  Files:\t%d\tDirectories:\t%d\tLogical size:\t%s\n", pr.Files, pr.Dirs, humanBytes(pr.Bytes))
	fmt.Fprintln(w, "Placement")
	start := "-"
	if pr.StartOrd > 0 {
		start = fmt.Sprint(pr.StartOrd)
	}
	spill := "next writable"
	if !pr.Spillover {
		spill = "fail"
	}
	fmt.Fprintf(w, "  Volume:\t%s\tStart ordinal:\t%s\tPolicy:\t%s\tSpillover:\t%s\tMin free:\t%s\n", pr.Volume, start, pr.Policy, spill, humanBytes(pr.MinFree))
	fmt.Fprintf(w, "  Destination:\t%s\n", pr.Destination)
	fmt.Fprintln(w, "Planned placement")
	planned := map[string]bool{}
	for _, p := range pr.Placements {
		planned[p.DiskID] = true
		fmt.Fprintf(w, "  [%d]\t%s (%s)\t%s\t%d files\tremaining after: %s above reserve\n", p.Ordinal, p.Name, p.Node, humanBytes(p.Bytes), p.Files, humanBytes(p.RemainingAboveReserve))
	}
	ex := append([]placement.Excluded(nil), pr.Excluded...)
	sort.Slice(ex, func(i, j int) bool { return ex[i].Ordinal < ex[j].Ordinal })
	for _, e := range ex {
		fmt.Fprintf(w, "  [%d]\t%s\t—\t(excluded: %s)\n", e.Ordinal, e.Name, e.Reason)
	}
	fmt.Fprintln(w, "Result")
	yes := "YES"
	if !pr.Sufficient {
		yes = "NO — " + pr.Error
		if pr.Unplaced > 0 {
			yes += fmt.Sprintf(" (%d files, %s unplaced)", pr.Unplaced, humanBytes(pr.UnplacedB))
		}
	}
	fmt.Fprintf(w, "  Sufficient capacity:\t%s\tEstimated duration:\t%s\tWarnings:\t%d\tExisting paths:\t%d\n", yes,
		(time.Duration(pr.ETASeconds) * time.Second).String(), len(pr.Warnings), pr.Existing)
	for i, wn := range pr.Warnings {
		if i >= 10 {
			fmt.Fprintf(w, "  … %d more warnings\n", len(pr.Warnings)-10)
			break
		}
		fix := ""
		if wn.Fix != "" {
			fix = " → " + wn.Fix
		}
		fmt.Fprintf(w, "  ! %s %s%s (%s)\n", wn.Code, wn.Path, fix, wn.Msg)
	}
	if tw, ok := w.(interface{ Flush() error }); ok {
		tw.Flush()
	}
}

func (a *app) copyCmd() *cobra.Command {
	var x xferFlags
	cmd := &cobra.Command{Use: "copy <src> <volume>:/<dst>", Short: "Copy local files/folders into a volume by ordinal", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		vol, dst, err := splitVolPath(args[1])
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(args[0])
		req, err := x.request(transfer.TypeCopy, vol, abs, dst)
		if err != nil {
			return err
		}
		return a.runTransfer(req, &x)
	}}
	x.register(cmd, true)
	cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "start without confirmation")
	return cmd
}

func (a *app) moveCmd() *cobra.Command {
	var x xferFlags
	cmd := &cobra.Command{Use: "move <volume>:/<src> <volume>:/<dst>", Short: "Move within a volume (same disk = rename; --target-ordinal = copy+verify+delete)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		vol, src, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		_, dst, err := splitVolPath(args[1])
		if err != nil {
			return err
		}
		req, err := x.request(transfer.TypeMove, vol, src, dst)
		if err != nil {
			return err
		}
		return a.runTransfer(req, &x)
	}}
	x.register(cmd, false)
	cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "start without confirmation")
	return cmd
}

func (a *app) runTransfer(req transfer.PlanRequest, x *xferFlags) error {
	if x.dryRun {
		return a.showPlan(req)
	}
	if !a.yes {
		if err := a.showPlan(req); err != nil {
			return err
		}
		if err := a.confirm("Start " + strings.ToLower(req.Type) + "?"); err != nil {
			return err
		}
	}
	var j transfer.Job
	if err := a.c.Post("/v1/jobs", req, &j); err != nil {
		return err
	}
	if a.quiet || x.noWait {
		fmt.Fprintln(a.out, j.ID)
		return nil
	}
	return a.watchJob(j.ID)
}

// watchJob renders progress: TTY bar, else one JSON line per event (PRD §9.4).
func (a *app) watchJob(id string) error {
	var last transfer.Progress
	err := a.c.Watch(id, func(raw []byte) {
		json.Unmarshal(raw, &last)
		if a.format == "json" || !a.tty {
			fmt.Fprintln(a.out, string(raw))
			return
		}
		pct := 0
		if last.BytesTotal > 0 {
			pct = int(last.BytesDone * 100 / last.BytesTotal)
		}
		fmt.Fprintf(a.out, "\r%s %3d%%  %d/%d files  %s/%s  %s/s  ETA %s   ", bar(last.BytesDone, last.BytesTotal, 24), pct,
			last.FilesDone+last.FilesSkip, last.FilesTotal, humanBytes(last.BytesDone), humanBytes(last.BytesTotal),
			humanBytes(last.RateBps), time.Duration(last.ETASeconds)*time.Second)
	})
	if err != nil {
		return err
	}
	var j transfer.Job
	if err := a.c.Get("/v1/jobs/"+q(id), &j); err != nil {
		return err
	}
	if a.format != "json" && a.tty {
		fmt.Fprintln(a.out)
	}
	if a.format != "json" {
		fmt.Fprintf(a.out, "%s %s", j.ID, j.State)
		if j.Progress != nil {
			fmt.Fprintf(a.out, "  files=%d skipped=%d failed=%d bytes=%s", j.Progress.FilesDone, j.Progress.FilesSkip, j.Progress.FilesFail, humanBytes(j.Progress.BytesDone))
			names := []string{}
			for _, p := range j.Progress.Placements {
				names = append(names, fmt.Sprintf("%s:%d/%s", p.Name, p.Files, humanBytes(p.Bytes)))
			}
			sort.Strings(names)
			if len(names) > 0 {
				fmt.Fprintf(a.out, "  [%s]", strings.Join(names, " "))
			}
		}
		if j.Error != "" {
			fmt.Fprintf(a.out, "  (%s)", j.Error)
		}
		fmt.Fprintln(a.out)
	}
	switch j.State {
	case transfer.JobDone:
		return nil
	case transfer.JobPausedNeedSpace:
		return &exitError{ExitSpace, j.Error}
	case transfer.JobPaused:
		return &exitError{ExitOffline, j.Error}
	case transfer.JobCancelled:
		return &exitError{ExitInterrupt, "cancelled"}
	default:
		for _, it := range []string{"checksum mismatch"} {
			if strings.Contains(j.Error, it) {
				return &exitError{ExitVerify, j.Error}
			}
		}
		return &exitError{ExitGeneral, j.Error}
	}
}

// ---- file ops ----

func (a *app) mkdirCmd() *cobra.Command {
	var ord int
	cmd := &cobra.Command{Use: "mkdir <volume>:/<path>", Short: "Create a logical folder (lazy; --ordinal creates it on that disk now)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		vol, p, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		var o core.Object
		if err := a.c.Post("/v1/volumes/"+q(vol)+"/mkdir", map[string]any{"path": p, "ordinal": ord}, &o); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "created", vol+":"+o.LogicalPath)
		return nil
	}}
	cmd.Flags().IntVar(&ord, "ordinal", 0, "create physically on this disk now")
	return cmd
}

func (a *app) mvCmd() *cobra.Command {
	return &cobra.Command{Use: "mv <volume>:/<old> <volume>:/<new>", Short: "Rename a file or folder on every disk", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		vol, from, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		_, to, err := splitVolPath(args[1])
		if err != nil {
			return err
		}
		if err := a.c.Post("/v1/volumes/"+q(vol)+"/rename", map[string]string{"from": from, "to": to}, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "renamed %s → %s\n", from, to)
		return nil
	}}
}

func (a *app) rmCmd() *cobra.Command {
	var recursive, permanent bool
	cmd := &cobra.Command{Use: "rm <volume>:/<path>", Short: "Delete (default: volume trash; --permanent unlinks)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		vol, p, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		if permanent {
			if err := a.confirmName("permanently delete", p); err != nil {
				return err
			}
		}
		var res transfer.DeleteResult
		if err := a.c.Post("/v1/volumes/"+q(vol)+"/delete", map[string]any{"path": p, "recursive": recursive, "permanent": permanent}, &res); err != nil {
			return err
		}
		where := "moved to trash"
		if res.Permanent {
			where = "permanently deleted"
		}
		fmt.Fprintf(a.out, "%s: %d files (%s)\n", where, res.Files, humanBytes(res.Bytes))
		return nil
	}}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "delete folders recursively")
	cmd.Flags().BoolVar(&permanent, "permanent", false, "skip the trash")
	cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func (a *app) trashCmd() *cobra.Command {
	c := &cobra.Command{Use: "trash", Short: "Volume trash (list, restore, purge)"}
	c.AddCommand(&cobra.Command{Use: "list <volume>", Short: "List trash", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var ts []transfer.Tomb
		if err := a.c.Get("/v1/volumes/"+q(args[0])+"/trash", &ts); err != nil {
			return err
		}
		return a.output(ts, func(w io.Writer) {
			fmt.Fprintln(w, "ID\tPATH\tSIZE\tDELETED\tPURGE AFTER")
			for _, t := range ts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.LogicalPath, humanBytes(t.Size), t.DeletedAt, t.PurgeAfter)
			}
		})
	}})
	c.AddCommand(&cobra.Command{Use: "restore <id>", Short: "Restore a trashed file", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var o core.Object
		if err := a.c.Post("/v1/trash/"+q(args[0])+"/restore", nil, &o); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "restored", o.LogicalPath)
		return nil
	}})
	var older string
	purge := &cobra.Command{Use: "purge <volume>", Short: "Permanently delete trash entries", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		days := 0
		if older != "" {
			d, err := parseDays(older)
			if err != nil {
				return err
			}
			days = d
		}
		if err := a.confirmName("purge trash of", args[0]); err != nil {
			return err
		}
		var res map[string]int
		if err := a.c.Post("/v1/volumes/"+q(args[0])+"/trash/purge", map[string]int{"older_than_days": days}, &res); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "purged %d entries\n", res["purged"])
		return nil
	}}
	purge.Flags().StringVar(&older, "older-than", "", "e.g. 30d (default: entries past their retention)")
	purge.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip confirmation")
	c.AddCommand(purge)
	return c
}

func parseDays(s string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%d", &n); err != nil || n < 0 {
		return 0, &usageError{"--older-than expects days like 30d"}
	}
	return n, nil
}

func (a *app) lsCmd() *cobra.Command {
	var physical, long bool
	var ord int
	cmd := &cobra.Command{Use: "ls <volume>:/<path>", Short: "List a logical folder (--physical --ordinal N: files on one disk)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		vol, p, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		path := "/v1/volumes/" + q(vol) + "/files?path=" + q(p)
		if physical && ord > 0 {
			path += fmt.Sprintf("&ordinal=%d", ord)
		}
		var objs []core.Object
		if err := a.c.Get(path, &objs); err != nil {
			return err
		}
		return a.output(objs, func(w io.Writer) {
			if long || physical {
				fmt.Fprintln(w, "TYPE\tNAME\tSIZE\tORD\tDISK\tSTATE\tCHECKSUM\tPHYSICAL")
			}
			for _, o := range objs {
				name := o.Name
				if o.Type == "dir" {
					name += "/"
				}
				if !long && !physical {
					fmt.Fprintln(w, name)
					continue
				}
				ordS, sum := "-", "-"
				if o.Ordinal > 0 {
					ordS = fmt.Sprint(o.Ordinal)
				}
				if o.Checksum != "" {
					sum = o.Checksum[:12]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", o.Type, name, humanBytes(o.Size), ordS, o.DiskName, o.State, sum, o.PhysicalPath)
			}
		})
	}}
	cmd.Flags().BoolVar(&physical, "physical", false, "show physical placement")
	cmd.Flags().IntVar(&ord, "ordinal", 0, "with --physical: only files on this disk")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "long listing")
	return cmd
}

func (a *app) locateCmd() *cobra.Command {
	return &cobra.Command{Use: "locate <volume>:/<path>", Short: "Show the physical location of a logical path", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		vol, p, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		var objs []core.Object
		if err := a.c.Get("/v1/volumes/"+q(vol)+"/locate?path="+q(p), &objs); err != nil {
			return err
		}
		return a.output(objs, func(w io.Writer) {
			for _, o := range objs {
				fmt.Fprintf(w, "%s:%s → disk=%s (ordinal %d) state=%s path=%s\n", vol, o.LogicalPath, o.DiskName, o.Ordinal, o.State, o.PhysicalPath)
			}
		})
	}}
}

func (a *app) verifyCmd() *cobra.Command {
	var diskRef string
	cmd := &cobra.Command{Use: "verify <volume>:/<path>", Short: "Re-hash files against the catalog (report only)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		vol, p, err := splitVolPath(args[0])
		if err != nil {
			return err
		}
		var rep transfer.VerifyReport
		if err := a.c.Post("/v1/volumes/"+q(vol)+"/verify", map[string]string{"path": p, "disk": diskRef}, &rep); err != nil {
			return err
		}
		if err := a.output(rep, func(w io.Writer) {
			fmt.Fprintf(w, "checked=%d ok=%d mismatched=%d missing=%d unhashed=%d\n", rep.Checked, rep.OK, len(rep.Mismatched), len(rep.Missing), rep.Unhashed)
			for _, m := range rep.Mismatched {
				fmt.Fprintf(w, "  MISMATCH %s on %s\n", m.LogicalPath, m.Disk)
			}
			for _, m := range rep.Missing {
				fmt.Fprintf(w, "  MISSING  %s on %s\n", m.LogicalPath, m.Disk)
			}
		}); err != nil {
			return err
		}
		if len(rep.Mismatched)+len(rep.Missing) > 0 {
			return &exitError{ExitVerify, "verification found problems"}
		}
		return nil
	}}
	cmd.Flags().StringVar(&diskRef, "disk", "", "only files on this disk")
	return cmd
}

func (a *app) scanCmd() *cobra.Command {
	var opt transfer.ScanOptions
	cmd := &cobra.Command{Use: "scan <volume>", Short: "Reconcile the catalog with files edited directly on the disks", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var rep transfer.ScanReport
		if err := a.c.Post("/v1/volumes/"+q(args[0])+"/scan", opt, &rep); err != nil {
			return err
		}
		return a.output(rep, func(w io.Writer) {
			fmt.Fprintf(w, "%s scan of %s: %d disks, %d files, %d ms\n", rep.Mode, rep.Volume, rep.Disks, rep.Files, rep.DurationMs)
			fmt.Fprintf(w, "added=%d removed=%d modified=%d corrupt=%d partials=%d (removed %d) conflicts=%d\n",
				len(rep.Added), len(rep.Removed), len(rep.Modified), len(rep.Corrupt), len(rep.Partials), rep.PartialsFixed, rep.Conflicts)
			for _, g := range []struct {
				tag string
				es  []transfer.ScanEntry
			}{{"ADDED", rep.Added}, {"REMOVED", rep.Removed}, {"MODIFIED", rep.Modified}, {"CORRUPT", rep.Corrupt}, {"PARTIAL", rep.Partials}} {
				for _, e := range g.es {
					fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", g.tag, e.LogicalPath, e.Disk, e.Note)
				}
			}
			for _, d := range rep.Skipped {
				fmt.Fprintf(w, "  SKIPPED\tdisk %s (offline)\n", d)
			}
		})
	}}
	f := cmd.Flags()
	f.BoolVar(&opt.Full, "full", false, "re-hash every file (integrity check)")
	f.StringVar(&opt.Disk, "disk", "", "only this disk")
	f.BoolVar(&opt.FixPartials, "fix-partials", false, "delete partial files left by dead jobs")
	return cmd
}

func (a *app) conflictCmd() *cobra.Command {
	c := &cobra.Command{Use: "conflict", Short: "Conflict Center: same path on several disks"}
	var state string
	list := &cobra.Command{Use: "list <volume>", Short: "List conflicts", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var cs []transfer.Conflict
		if err := a.c.Get("/v1/volumes/"+q(args[0])+"/conflicts?state="+q(state), &cs); err != nil {
			return err
		}
		return a.output(cs, func(w io.Writer) {
			fmt.Fprintln(w, "ID\tKIND\tPATH\tCOPIES\tSTATE")
			for _, x := range cs {
				var ds []string
				for _, cd := range x.Candidates {
					ds = append(ds, fmt.Sprintf("%s#%d(%s)", cd.Disk, cd.Ordinal, humanBytes(cd.Size)))
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.ID, x.Kind, x.LogicalPath, strings.Join(ds, ", "), x.State)
			}
		})
	}}
	list.Flags().StringVar(&state, "state", "open", "open|resolved|'' (all)")
	c.AddCommand(list)
	var keep string
	resolve := &cobra.Command{Use: "resolve <conflict-id>", Short: "Resolve: --keep lowest|newest|both|<disk> (others go to trash)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var x transfer.Conflict
		if err := a.c.Post("/v1/conflicts/"+q(args[0])+"/resolve", map[string]string{"keep": keep}, &x); err != nil {
			return err
		}
		return a.output(x, func(w io.Writer) { fmt.Fprintf(w, "resolved %s: %s\n", x.LogicalPath, x.Resolution) })
	}}
	resolve.Flags().StringVar(&keep, "keep", "lowest", "lowest|newest|both|<disk>")
	c.AddCommand(resolve)
	c.AddCommand(&cobra.Command{Use: "resolve-duplicates <volume>", Short: "Keep the lowest-ordinal copy of every identical duplicate", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var out map[string]int
		if err := a.c.Post("/v1/volumes/"+q(args[0])+"/conflicts/resolve-duplicates", map[string]any{}, &out); err != nil {
			return err
		}
		return a.output(out, func(w io.Writer) { fmt.Fprintf(w, "resolved %d duplicates\n", out["resolved"]) })
	}})
	return c
}

func (a *app) accessCmd() *cobra.Command {
	c := &cobra.Command{Use: "access", Short: "SMB access point: one share per volume (+ per disk) for Finder/Explorer"}
	show := func(s access.Status) error {
		return a.output(s, func(w io.Writer) {
			state := "disabled"
			if s.Enabled {
				state = map[bool]string{true: "running", false: "enabled, not running"}[s.Running]
			}
			fmt.Fprintf(w, "access point\t%s\t%s:%d\tclients=%d\n", state, s.Bind, s.Port, s.Clients)
			if s.Password != "" {
				fmt.Fprintf(w, "credentials\t%s / %s\n", s.User, s.Password)
			}
			if !s.ImagePresent {
				fmt.Fprintf(w, "image\t%s missing — run make local-build\n", s.Image)
			}
			if s.Error != "" {
				fmt.Fprintf(w, "error\t%s\n", s.Error)
			}
			fmt.Fprintln(w, "SHARE\tKIND\tMODE\tURL\tBRANCHES")
			for _, sh := range s.Shares {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", sh.Name, sh.Kind, map[bool]string{true: "ro", false: "rw"}[sh.ReadOnly], sh.URL, strings.Join(sh.Branches, " "))
			}
		})
	}
	c.AddCommand(&cobra.Command{Use: "status", Short: "Show the access point and its shares", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var s access.Status
		if err := a.c.Get("/v1/access", &s); err != nil {
			return err
		}
		return show(s)
	}})
	c.AddCommand(&cobra.Command{Use: "credentials", Short: "Show the SMB user and password", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var s access.Status
		if err := a.c.Get("/v1/access/credentials", &s); err != nil {
			return err
		}
		return show(s)
	}})
	var bind string
	var port int
	enable := &cobra.Command{Use: "enable", Short: "Start the Samba + mergerfs container (Docker)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var s access.Status
		if err := a.c.Post("/v1/access/enable", map[string]any{"bind": bind, "port": port}, &s); err != nil {
			return err
		}
		return show(s)
	}}
	enable.Flags().StringVar(&bind, "bind", "", "listen address (default 127.0.0.1; 0.0.0.0 = LAN)")
	enable.Flags().IntVar(&port, "port", 0, "host port (default 4445)")
	c.AddCommand(enable)
	var mp string
	var print bool
	lm := &cobra.Command{Use: "local-mount <volume>", Short: "Linux: mergerfs union of this host's disks (fstab line, systemd unit or run it now)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if mp == "" {
			mp = "/mnt/continuum/" + args[0]
		}
		var m access.LocalMount
		if err := a.c.Get("/v1/volumes/"+q(args[0])+"/local-mount?mountpoint="+q(mp), &m); err != nil {
			return err
		}
		if print {
			return a.output(m, func(w io.Writer) {
				fmt.Fprintf(w, "# one-shot\n%s\n\n# /etc/fstab\n%s\n\n# /etc/systemd/system/%s\n%s", m.Command, m.Fstab, m.UnitName, m.SystemdUnit)
			})
		}
		out, err := exec.Command("sh", "-c", m.Command).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		fmt.Fprintln(a.out, "mounted", mp)
		return nil
	}}
	lm.Flags().StringVar(&mp, "mountpoint", "", "mount point (default /mnt/continuum/<volume>)")
	lm.Flags().BoolVar(&print, "print", false, "print the command, fstab line and systemd unit instead of mounting")
	c.AddCommand(lm)
	var share, plistDir string
	var noLoad bool
	ac := &cobra.Command{Use: "autoconnect", Short: "macOS: keep an SMB share mounted (login, every 5 min, network changes)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var s access.Status
		if err := a.c.Get("/v1/access/credentials", &s); err != nil {
			return err
		}
		var url string
		for _, sh := range s.Shares {
			if sh.Name == share {
				url = sh.URL
			}
		}
		if url == "" {
			return &usageError{"unknown share " + share + " (see `continuum access status`)"}
		}
		authURL := strings.Replace(url, "smb://", "smb://"+s.User+"@", 1)
		label := "com.continuum.smb." + share
		if runtime.GOOS != "darwin" || noLoad && plistDir == "-" {
			fmt.Fprintln(a.out, access.RenderAutoconnect(label, authURL))
			return nil
		}
		host := strings.SplitN(strings.TrimPrefix(url, "smb://"), "/", 2)[0]
		h, port, _ := strings.Cut(host, ":")
		if err := keychainSMB(h, port, s.User, s.Password); err != nil { // mount volume reads it from the login keychain
			return err
		}
		if plistDir == "" {
			home, _ := os.UserHomeDir()
			plistDir = filepath.Join(home, "Library", "LaunchAgents")
		}
		p := filepath.Join(plistDir, label+".plist")
		if err := os.WriteFile(p, []byte(access.RenderAutoconnect(label, authURL)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "wrote", p)
		if !noLoad {
			if out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), p).CombinedOutput(); err != nil {
				return fmt.Errorf("launchctl: %v %s", err, out)
			}
			fmt.Fprintln(a.out, "loaded — the share mounts now and after every login")
		}
		return nil
	}}
	ac.Flags().StringVar(&share, "share", "", "share name, e.g. research-pool")
	ac.Flags().StringVar(&plistDir, "dir", "", "LaunchAgents directory (default ~/Library/LaunchAgents; \"-\" with --no-load prints only, no keychain change)")
	ac.Flags().BoolVar(&noLoad, "no-load", false, "only write the agent")
	ac.MarkFlagRequired("share")
	c.AddCommand(ac)
	for _, act := range []string{"disable", "reload"} {
		c.AddCommand(&cobra.Command{Use: act, Short: strings.ToUpper(act[:1]) + act[1:] + " the access point", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			var s access.Status
			if err := a.c.Post("/v1/access/"+act, map[string]any{}, &s); err != nil {
				return err
			}
			return show(s)
		}})
	}
	return c
}

func (a *app) vfsCmd() *cobra.Command {
	c := &cobra.Command{Use: "vfs", Short: "Native filesystem mount of a volume (Linux FUSE; writes follow the placement policy)"}
	var ro bool
	mnt := &cobra.Command{Use: "mount <volume> <mountpoint>", Short: "Mount a volume (runs inside the controller process)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		abs, _ := filepath.Abs(args[1])
		var m map[string]any
		if err := a.c.Post("/v1/vfs/mount", map[string]any{"volume": args[0], "mountpoint": abs, "read_only": ro}, &m); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "mounted %s at %s%s\n", args[0], abs, map[bool]string{true: " (read-only)", false: ""}[ro])
		return nil
	}}
	mnt.Flags().BoolVar(&ro, "read-only", false, "mount read-only")
	c.AddCommand(mnt)
	c.AddCommand(&cobra.Command{Use: "unmount <mountpoint>", Short: "Unmount", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		abs, _ := filepath.Abs(args[0])
		if err := a.c.Post("/v1/vfs/unmount", map[string]any{"mountpoint": abs}, nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "unmounted", abs)
		return nil
	}})
	c.AddCommand(&cobra.Command{Use: "list", Short: "List mounts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var ms []map[string]any
		if err := a.c.Get("/v1/vfs", &ms); err != nil {
			return err
		}
		return a.output(ms, func(w io.Writer) {
			fmt.Fprintln(w, "VOLUME\tMOUNTPOINT\tMODE\tSINCE")
			for _, m := range ms {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", m["volume"], m["mountpoint"], map[bool]string{true: "ro", false: "rw"}[m["read_only"] == true], m["since"])
			}
		})
	}})
	return c
}

func (a *app) diagCmd() *cobra.Command {
	c := &cobra.Command{Use: "diag", Short: "Diagnostics (FR-O04)"}
	var out string
	var noMask bool
	bundle := &cobra.Command{Use: "bundle", Short: "Download a diagnostics zip (paths masked by default; never secrets)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if out == "" {
			out = "continuum-diagnostics-" + time.Now().Format("20060102-150405") + ".zip"
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		q := "?mask=1"
		if noMask {
			q = "?mask=0"
		}
		if err := a.c.Get("/v1/diagnostics"+q, f); err != nil {
			os.Remove(out)
			return err
		}
		fmt.Fprintln(a.out, out)
		return nil
	}}
	bundle.Flags().StringVarP(&out, "output", "o", "", "zip file (default continuum-diagnostics-<time>.zip)")
	bundle.Flags().BoolVar(&noMask, "no-mask", false, "keep real paths and host names")
	c.AddCommand(bundle)
	return c
}

func (a *app) settingsCmd() *cobra.Command {
	c := &cobra.Command{Use: "settings", Short: "Controller settings (notifications)"}
	show := func(m map[string]string) error {
		return a.output(m, func(w io.Writer) {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "%s\t%s\n", k, m[k])
			}
		})
	}
	c.AddCommand(&cobra.Command{Use: "get", Short: "Show settings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var m map[string]string
		if err := a.c.Get("/v1/settings", &m); err != nil {
			return err
		}
		return show(m)
	}})
	c.AddCommand(&cobra.Command{Use: "set <key=value>...", Short: "Change settings, e.g. notify.webhook_url=https://hooks.slack.com/… notify.format=slack|discord|generic", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		in := map[string]string{}
		for _, kv := range args {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return &usageError{"expected key=value, got " + kv}
			}
			in[k] = v
		}
		var m map[string]string
		if err := a.c.Put("/v1/settings", in, &m); err != nil {
			return err
		}
		return show(m)
	}})
	c.AddCommand(&cobra.Command{Use: "test-notify", Short: "Send a test message to the configured webhook", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.c.Post("/v1/notifications/test", map[string]any{}, nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "sent")
		return nil
	}})
	return c
}

// ---- jobs / activity ----

func (a *app) jobCmd() *cobra.Command {
	c := &cobra.Command{Use: "job", Short: "Transfer jobs"}
	var state string
	list := &cobra.Command{Use: "list", Short: "List jobs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var js []transfer.Job
		if err := a.c.Get("/v1/jobs?state="+q(state), &js); err != nil {
			return err
		}
		if a.quiet {
			for _, j := range js {
				fmt.Fprintln(a.out, j.ID)
			}
			return nil
		}
		return a.output(js, func(w io.Writer) {
			fmt.Fprintln(w, "JOB\tTYPE\tSTATE\tFILES\tBYTES\tDESTINATION\tCREATED")
			for _, j := range js {
				files, bytes := "-", "-"
				if j.Progress != nil {
					files = fmt.Sprintf("%d/%d", j.Progress.FilesDone+j.Progress.FilesSkip, j.Progress.FilesTotal)
					bytes = humanBytes(j.Progress.BytesDone)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Type, j.State, files, bytes, j.Destination, j.CreatedAt)
			}
		})
	}}
	list.Flags().StringVar(&state, "state", "", "filter by state (RUNNING, PAUSED, DONE, FAILED …)")
	c.AddCommand(list)
	c.AddCommand(&cobra.Command{Use: "watch <job>", Short: "Follow job progress", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.watchJob(args[0])
	}})
	for _, act := range []string{"pause", "resume", "cancel", "retry"} {
		act := act
		c.AddCommand(&cobra.Command{Use: act + " <job>", Short: strings.ToUpper(act[:1]) + act[1:] + " a job", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			var j transfer.Job
			if err := a.c.Post("/v1/jobs/"+q(args[0])+"/"+act, nil, &j); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "%s %s\n", j.ID, j.State)
			return nil
		}})
	}
	var out string
	errs := &cobra.Command{Use: "errors <job>", Short: "Export failed items (-o file.csv)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		var w io.Writer = a.out
		if out != "" {
			fh, err := os.Create(out)
			if err != nil {
				return err
			}
			defer fh.Close()
			w = fh
		}
		format := "json"
		if strings.HasSuffix(out, ".csv") || a.format == "csv" {
			format = "csv"
		}
		return a.c.Get("/v1/jobs/"+q(args[0])+"/errors?format="+format, w)
	}}
	errs.Flags().StringVarP(&out, "output", "o", "", "write to file (.csv → CSV)")
	c.AddCommand(errs)
	return c
}

func (a *app) activityCmd() *cobra.Command {
	c := &cobra.Command{Use: "activity", Short: "Events and audit log"}
	var n int
	tail := &cobra.Command{Use: "tail", Short: "Recent events", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var evs []map[string]any
		if err := a.c.Get(fmt.Sprintf("/v1/activity?limit=%d", n), &evs); err != nil {
			return err
		}
		return a.output(evs, func(w io.Writer) {
			for i := len(evs) - 1; i >= 0; i-- {
				e := evs[i]
				p, _ := json.Marshal(e["payload"])
				if string(p) == "null" {
					p = nil
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%s\n", e["ts"], e["kind"], firstNonEmpty(e["job_id"], e["disk_id"], e["volume_id"]), p)
			}
		})
	}}
	tail.Flags().IntVarP(&n, "lines", "n", 20, "number of events")
	c.AddCommand(tail)
	c.AddCommand(&cobra.Command{Use: "verify", Short: "Verify the audit hash chain", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var r map[string]any
		if err := a.c.Get("/v1/audit/verify", &r); err != nil {
			return err
		}
		if r["intact"] == true {
			fmt.Fprintln(a.out, "audit chain intact")
			return nil
		}
		return &exitError{ExitVerify, fmt.Sprintf("audit chain broken at seq %v", r["first_broken_seq"])}
	}})
	return c
}

func firstNonEmpty(vs ...any) any {
	for _, v := range vs {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "-"
}
