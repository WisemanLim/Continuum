package core

import (
	"errors"
	"fmt"
)

// Error codes shared by API (HTTP status) and CLI (exit code, FR-C06).
const (
	CodeInvalid           = "invalid"            // HTTP 400 · exit 2
	CodeNotFound          = "not_found"          // HTTP 404 · exit 1
	CodeInsufficientSpace = "insufficient_space" // HTTP 409 · exit 3
	CodeVerifyFailed      = "verify_failed"      // HTTP 422 · exit 4
	CodePermission        = "permission"         // HTTP 403 · exit 5
	CodeUnauthenticated   = "unauthenticated"    // HTTP 401 · exit 5 (login required, FR-S02)
	CodeOffline           = "offline"            // HTTP 409 · exit 6
	CodeConflict          = "conflict"           // HTTP 409 · exit 7
	CodeRevisionMismatch  = "revision_mismatch"  // HTTP 409 · exit 8 (ABORTED)
)

// Error is a typed domain error.
type Error struct {
	Code string         `json:"code"`
	Msg  string         `json:"message"`
	Info map[string]any `json:"info,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf returns the domain code of err ("" if untyped).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
