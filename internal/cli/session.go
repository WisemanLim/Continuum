package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"continuum/internal/auth"
)

// sessionFile stores login tokens per controller URL (0600) — FR-S02.
func sessionFile() string {
	if p := os.Getenv("CONTINUUM_SESSION_FILE"); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Continuum", "cli-sessions.json")
}

func loadSessions() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(sessionFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func saveSessions(m map[string]string) error {
	p := sessionFile()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func normURL(u string) string { return strings.TrimRight(u, "/") }

// savedToken returns the session saved by `continuum login` for this controller.
func savedToken(controller string) string { return loadSessions()[normURL(controller)] }

func (a *app) readPassword(prompt string) (string, error) {
	if pw := os.Getenv("CONTINUUM_PASSWORD"); pw != "" { // non-interactive (CI, scripts)
		return pw, nil
	}
	fmt.Fprint(a.errOut, prompt)
	if f, ok := a.in.(*os.File); ok && isTTY(f) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(a.errOut)
		return string(b), err
	}
	line, err := bufio.NewReader(a.in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readLine reads one visible line (TOTP code).
func (a *app) readLine(prompt string) (string, error) {
	fmt.Fprint(a.errOut, prompt)
	line, err := bufio.NewReader(a.in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a *app) authCmds() []*cobra.Command {
	var user, code string
	login := &cobra.Command{Use: "login [username]", Short: "Log in and store a session token for this controller", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			user = args[0]
		}
		if user == "" {
			return &usageError{"username required"}
		}
		pw, err := a.readPassword("password: ")
		if err != nil {
			return err
		}
		var s auth.Session
		body := map[string]string{"username": user, "password": pw, "code": code, "client": "cli"}
		err = a.c.Post("/v1/auth/login", body, &s)
		var ae *APIError
		if errors.As(err, &ae) && ae.Info["mfa_required"] == true && code == "" { // second factor (FR-S02)
			if body["code"], err = a.readLine("authenticator code: "); err != nil {
				return err
			}
			err = a.c.Post("/v1/auth/login", body, &s)
		}
		if err != nil {
			return err
		}
		m := loadSessions()
		m[normURL(a.controller)] = s.Token
		if err := saveSessions(m); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "logged in as %s (%s) until %s\n", s.User.Username, s.User.Role, s.ExpiresAt)
		return nil
	}}
	login.Flags().StringVar(&code, "code", os.Getenv("CONTINUUM_TOTP"), "6-digit authenticator code when MFA is on (env CONTINUUM_TOTP)")
	logout := &cobra.Command{Use: "logout", Short: "End the session and forget the token", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		a.c.Post("/v1/auth/logout", map[string]any{}, nil)
		m := loadSessions()
		delete(m, normURL(a.controller))
		fmt.Fprintln(a.out, "logged out")
		return saveSessions(m)
	}}
	whoami := &cobra.Command{Use: "whoami", Short: "Show the current user and permissions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var me struct {
			Username    string   `json:"username"`
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
			AuthEnabled bool     `json:"auth_enabled"`
		}
		if err := a.c.Get("/v1/auth/me", &me); err != nil {
			return err
		}
		return a.output(me, func(w io.Writer) {
			fmt.Fprintf(w, "%s\t%s\t%s\tauth=%v\n", me.Username, me.Role, strings.Join(me.Permissions, ","), me.AuthEnabled)
		})
	}}

	uc := &cobra.Command{Use: "user", Short: "Users and roles (viewer, operator, storage-admin, security-admin, system-admin)"}
	uc.AddCommand(&cobra.Command{Use: "list", Short: "List users", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var us []auth.User
		if err := a.c.Get("/v1/users", &us); err != nil {
			return err
		}
		return a.output(us, func(w io.Writer) {
			fmt.Fprintln(w, "USER\tROLE\tDISABLED\tMFA\tSOURCE\tLAST LOGIN")
			for _, u := range us {
				fmt.Fprintf(w, "%s\t%s\t%v\t%v\t%s\t%s\n", u.Username, u.Role, u.Disabled, u.MFAEnabled, u.AuthSource, u.LastLoginAt)
			}
		})
	}})
	var role string
	add := &cobra.Command{Use: "add <username>", Short: "Create a user (the first user must be system-admin)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		pw, err := a.readPassword("new password (min 10 chars): ")
		if err != nil {
			return err
		}
		var u auth.User
		if err := a.c.Post("/v1/users", map[string]string{"username": args[0], "password": pw, "role": role}, &u); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "created %s (%s)\n", u.Username, u.Role)
		return nil
	}}
	add.Flags().StringVar(&role, "role", auth.Viewer, "viewer|operator|storage-admin|security-admin|system-admin")
	uc.AddCommand(add)
	patch := func(name string, up auth.Update) error {
		var u auth.User
		if err := a.c.Patch("/v1/users/"+q(name), up, &u); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s\t%s\tdisabled=%v\n", u.Username, u.Role, u.Disabled)
		return nil
	}
	uc.AddCommand(&cobra.Command{Use: "set-role <username> <role>", Short: "Change a user's role", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return patch(args[0], auth.Update{Role: &args[1]})
	}})
	for _, v := range []bool{true, false} {
		dis := v
		name := map[bool]string{true: "disable", false: "enable"}[v]
		uc.AddCommand(&cobra.Command{Use: name + " <username>", Short: strings.ToUpper(name[:1]) + name[1:] + " a user (disable ends their sessions)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return patch(args[0], auth.Update{Disabled: &dis})
		}})
	}
	uc.AddCommand(&cobra.Command{Use: "passwd <username>", Short: "Set a user's password (admin)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		pw, err := a.readPassword("new password: ")
		if err != nil {
			return err
		}
		return patch(args[0], auth.Update{Password: &pw})
	}})
	del := &cobra.Command{Use: "delete <username>", Short: "Delete a user", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.confirmName("delete user", args[0]); err != nil {
			return err
		}
		if err := a.c.Delete("/v1/users/"+q(args[0]), nil); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "deleted", args[0])
		return nil
	}}
	del.Flags().BoolVarP(&a.yes, "yes", "y", false, "skip name confirmation")
	uc.AddCommand(del)

	// TOTP for the logged-in user (FR-S02)
	mfa := &cobra.Command{Use: "mfa", Short: "Authenticator (TOTP) second factor for your own account"}
	mfa.AddCommand(&cobra.Command{Use: "setup", Short: "Create a secret; add it to an authenticator app, then run `user mfa enable <code>`", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var m auth.MFASetup
		if err := a.c.Post("/v1/auth/mfa/setup", map[string]any{}, &m); err != nil {
			return err
		}
		return a.output(m, func(w io.Writer) {
			fmt.Fprintf(w, "secret:\t%s\nURI:\t%s\n", m.Secret, m.OTPAuthURL)
		})
	}})
	for _, op := range []string{"enable", "disable"} {
		mfa.AddCommand(&cobra.Command{Use: op + " <code>", Short: strings.ToUpper(op[:1]) + op[1:] + " MFA (needs a current code)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.c.Post("/v1/auth/mfa/"+op, map[string]string{"code": args[0]}, nil); err != nil {
				return err
			}
			fmt.Fprintln(a.out, "MFA", op+"d")
			return nil
		}})
	}
	uc.AddCommand(mfa)

	// two-person approval (FR-S05)
	ap := &cobra.Command{Use: "approval", Short: "Two-person approval of destructive actions (setting security.approval_actions)"}
	var state string
	apList := &cobra.Command{Use: "list", Short: "List approval requests", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var as []auth.Approval
		if err := a.c.Get("/v1/approvals?state="+q(state), &as); err != nil {
			return err
		}
		return a.output(as, func(w io.Writer) {
			fmt.Fprintln(w, "ID\tACTION\tTARGET\tREQUESTED BY\tSTATE\tDECIDED BY\tEXPIRES")
			for _, x := range as {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", x.ID, x.Action, x.Target, x.RequestedBy, x.State, x.DecidedBy, x.ExpiresAt)
			}
		})
	}}
	apList.Flags().StringVar(&state, "state", "pending", "pending|approved|rejected|used (empty = all)")
	ap.AddCommand(apList)
	for _, op := range []string{"approve", "reject"} {
		ap.AddCommand(&cobra.Command{Use: op + " <id>", Short: strings.ToUpper(op[:1]) + op[1:] + " a request (not your own)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			var x auth.Approval
			if err := a.c.Post("/v1/approvals/"+q(args[0])+"/"+op, map[string]any{}, &x); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "%s\t%s %s\t%s\n", x.ID, x.Action, x.Target, x.State)
			return nil
		}})
	}
	return []*cobra.Command{login, logout, whoami, uc, ap}
}
