package auth

import (
	"context"
	"strings"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
)

// Approval is a two-person approval request for a destructive action (FR-S05).
type Approval struct {
	ID          string `json:"approval_id"`
	Action      string `json:"action"`
	Target      string `json:"target"`
	RequestedBy string `json:"requested_by"`
	State       string `json:"state"`
	DecidedBy   string `json:"decided_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	ExpiresAt   string `json:"expires_at"`
}

// ApprovalActions can require a second person (setting security.approval_actions, comma separated).
var ApprovalActions = []string{"disk.wipe", "disk.drain", "volume.delete", "user.delete", "node.revoke"}

// CodeApprovalRequired is returned (HTTP 403) with info.approval_id until a second person approves.
const CodeApprovalRequired = "approval_required"

func (s *Service) approvalNeeded(ctx context.Context, action string) bool {
	if !s.Enabled(ctx) {
		return false // single-user mode: nobody to approve
	}
	for _, a := range strings.Split(s.C.Setting(ctx, "security.approval_actions", ""), ",") {
		if strings.TrimSpace(a) == action {
			return true
		}
	}
	return false
}

// Gate lets a destructive action through when no approval is configured, or when approvalID is an approved,
// unused approval of the same requester, action and target; otherwise it files (or reuses) a request.
func (s *Service) Gate(ctx context.Context, actor, action, target, approvalID string) error {
	if !s.approvalNeeded(ctx, action) {
		return nil
	}
	now := store.Now()
	if approvalID != "" {
		res, err := s.C.DB.Exec(ctx, `UPDATE approvals SET state='used' WHERE approval_id=? AND action=? AND target=? AND requested_by=? AND state='approved' AND expires_at > ?`,
			approvalID, action, target, actor, now)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 1 {
				s.C.Audit.Append(ctx, audit.Entry{Actor: actor, Action: "approval.use", Target: action + " " + target, Result: approvalID})
				return nil
			}
		}
		return core.Errf(core.CodePermission, "approval %s is not approved for %s %s (or expired / already used)", approvalID, action, target)
	}
	var id string
	if err := s.C.DB.QueryRow(ctx, `SELECT approval_id FROM approvals WHERE action=? AND target=? AND requested_by=? AND state IN ('pending','approved') AND expires_at > ?`,
		action, target, actor, now).Scan(&id); err != nil {
		id = store.NewID("apr")
		s.C.DB.Exec(ctx, `INSERT INTO approvals (approval_id, action, target, requested_by, state, created_at, expires_at) VALUES (?,?,?,?,'pending',?,?)`,
			id, action, target, actor, now, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339Nano))
		s.C.Audit.Append(ctx, audit.Entry{Actor: actor, Action: "approval.request", Target: action + " " + target, Result: id})
		s.C.Audit.Emit(ctx, audit.Event{Kind: "approval.requested"}, map[string]any{"approval_id": id, "action": action, "target": target, "by": actor})
	}
	e := core.Errf(CodeApprovalRequired, "%s %s needs a second person's approval (request %s) — retry with it once approved", action, target, id)
	e.Info = map[string]any{"approval_id": id}
	return e
}

// Decide approves or rejects a pending request; the requester cannot approve their own.
func (s *Service) Decide(ctx context.Context, id, by string, approve bool) (*Approval, error) {
	a, err := s.Approval(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.State != "pending" {
		return nil, core.Errf(core.CodeConflict, "approval %s is %s", id, a.State)
	}
	if a.RequestedBy == by {
		return nil, core.Errf(core.CodePermission, "a second person must decide (requested by %s)", by)
	}
	state := map[bool]string{true: "approved", false: "rejected"}[approve]
	s.C.DB.Exec(ctx, `UPDATE approvals SET state=?, decided_by=?, decided_at=? WHERE approval_id=? AND state='pending'`, state, by, store.Now(), id)
	s.C.Audit.Append(ctx, audit.Entry{Actor: by, Action: "approval." + state, Target: a.Action + " " + a.Target, Result: id})
	return s.Approval(ctx, id)
}

// Approval returns one request.
func (s *Service) Approval(ctx context.Context, id string) (*Approval, error) {
	var a Approval
	err := s.C.DB.QueryRow(ctx, `SELECT approval_id, action, target, requested_by, state, COALESCE(decided_by,''), created_at, expires_at FROM approvals WHERE approval_id=?`, id).
		Scan(&a.ID, &a.Action, &a.Target, &a.RequestedBy, &a.State, &a.DecidedBy, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return nil, core.Errf(core.CodeNotFound, "approval %q not found", id)
	}
	return &a, nil
}

// Approvals lists requests (state "" = all), newest first.
func (s *Service) Approvals(ctx context.Context, state string) ([]Approval, error) {
	q := `SELECT approval_id, action, target, requested_by, state, COALESCE(decided_by,''), created_at, expires_at FROM approvals`
	var args []any
	if state != "" {
		q += ` WHERE state=?`
		args = append(args, state)
	}
	rows, err := s.C.DB.Query(ctx, q+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var a Approval
		rows.Scan(&a.ID, &a.Action, &a.Target, &a.RequestedBy, &a.State, &a.DecidedBy, &a.CreatedAt, &a.ExpiresAt)
		out = append(out, a)
	}
	return out, rows.Err()
}
