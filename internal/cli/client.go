// Package cli implements the `continuum` (alias `ctm`) command line (FR-C01~C10, PRD §9).
// Every command calls the same Controller REST API as the GUI (DP05).
package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"continuum/internal/auth"
	"continuum/internal/core"
)

// Exit codes (FR-C06, PRD §9.4).
const (
	ExitOK        = 0
	ExitGeneral   = 1
	ExitUsage     = 2
	ExitSpace     = 3
	ExitVerify    = 4
	ExitPerm      = 5
	ExitOffline   = 6
	ExitConflict  = 7
	ExitRevision  = 8
	ExitInterrupt = 130
)

// APIError is a decoded {"error":{code,message}} body.
type APIError struct {
	Status int
	Code   string         `json:"code"`
	Msg    string         `json:"message"`
	Info   map[string]any `json:"info,omitempty"`
}

func (e *APIError) Error() string { return e.Msg }

// ExitCode maps an error to the CLI exit code table.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Code {
		case core.CodeInvalid:
			return ExitUsage
		case core.CodeInsufficientSpace:
			return ExitSpace
		case core.CodeVerifyFailed:
			return ExitVerify
		case core.CodePermission, core.CodeUnauthenticated, auth.CodeApprovalRequired:
			return ExitPerm
		case core.CodeOffline:
			return ExitOffline
		case core.CodeConflict:
			return ExitConflict
		case core.CodeRevisionMismatch:
			return ExitRevision
		}
	}
	var ce *exitError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ExitGeneral
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// Client talks to the Controller.
type Client struct {
	Base     string
	Token    string
	Approval string // X-Approval-Id for actions under two-person approval (FR-S05)
	HTTP     *http.Client
}

func (c *Client) do(method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}
	return c.send(method, path, "application/json", raw, out)
}

// PostRaw sends a non-JSON body (volume import YAML).
func (c *Client) PostRaw(path, contentType string, body []byte, out any) error {
	return c.send("POST", path, contentType, body, out)
}

func (c *Client) send(method, path, contentType string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Approval != "" {
		req.Header.Set("X-Approval-Id", c.Approval)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &exitError{ExitOffline, fmt.Sprintf("cannot reach controller at %s: %v (start it with `make local-all` or set --controller)", c.Base, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var wrap struct {
			Error APIError `json:"error"`
		}
		b, _ := io.ReadAll(resp.Body)
		if json.Unmarshal(b, &wrap) != nil || wrap.Error.Msg == "" {
			return &APIError{Status: resp.StatusCode, Code: "internal", Msg: strings.TrimSpace(string(b))}
		}
		wrap.Error.Status = resp.StatusCode
		return &wrap.Error
	}
	if out == nil {
		return nil
	}
	if w, ok := out.(io.Writer); ok {
		_, err := io.Copy(w, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Get(path string, out any) error        { return c.do("GET", path, nil, out) }
func (c *Client) Post(path string, body, out any) error { return c.do("POST", path, body, out) }
func (c *Client) Put(path string, body, out any) error  { return c.do("PUT", path, body, out) }
func (c *Client) Patch(path string, body, out any) error {
	return c.do("PATCH", path, body, out)
}
func (c *Client) Delete(path string, out any) error { return c.do("DELETE", path, nil, out) }

// Watch streams SSE progress events until the job leaves RUNNING/QUEUED.
func (c *Client) Watch(jobID string, fn func(raw []byte)) error {
	req, _ := http.NewRequest("GET", strings.TrimRight(c.Base, "/")+"/v1/jobs/"+url.PathEscape(jobID)+"/watch", nil)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	cl := *c.HTTP
	cl.Timeout = 0
	resp, err := cl.Do(req)
	if err != nil {
		return &exitError{ExitOffline, err.Error()}
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			fn([]byte(line[6:]))
		}
	}
	return sc.Err()
}

func newClient(base, token string) *Client {
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func q(s string) string { return url.QueryEscape(s) }
