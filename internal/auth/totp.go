package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"continuum/internal/audit"
	"continuum/internal/core"
	"continuum/internal/store"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// TOTPCode computes the RFC 6238 code (SHA-1, 30 s, 6 digits) for a step counter.
func TOTPCode(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000), nil
}

// verifyTOTP accepts the current step ±1 and returns the matched counter (replay protection: > last).
func verifyTOTP(secret, code string, now time.Time, last int64) (int64, bool) {
	code = strings.TrimSpace(code)
	step := now.Unix() / 30
	for _, c := range []int64{step, step - 1, step + 1} {
		if c <= last {
			continue
		}
		if want, err := TOTPCode(secret, c); err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return c, true
		}
	}
	return 0, false
}

// MFASetup is returned when a user starts TOTP enrollment.
type MFASetup struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

// SetupMFA creates a pending TOTP secret; EnableMFA confirms it with a first code.
func (s *Service) SetupMFA(ctx context.Context, userID string) (*MFASetup, error) {
	u, _, err := s.get(ctx, userID)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 20)
	rand.Read(b)
	secret := b32.EncodeToString(b)
	s.C.DB.Exec(ctx, `UPDATE users SET totp_pending=?, updated_at=? WHERE user_id=?`, secret, store.Now(), u.ID)
	q := url.Values{"secret": {secret}, "issuer": {"Continuum"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return &MFASetup{Secret: secret, OTPAuthURL: "otpauth://totp/" + url.PathEscape("Continuum:"+u.Username) + "?" + q.Encode()}, nil
}

// EnableMFA activates the pending secret after a valid code.
func (s *Service) EnableMFA(ctx context.Context, userID, code string) error {
	var pending string
	if err := s.C.DB.QueryRow(ctx, `SELECT COALESCE(totp_pending,'') FROM users WHERE user_id=?`, userID).Scan(&pending); err != nil || pending == "" {
		return core.Errf(core.CodeInvalid, "start MFA setup first")
	}
	c, ok := verifyTOTP(pending, code, time.Now(), 0)
	if !ok {
		return core.Errf(core.CodeInvalid, "code does not match — check the authenticator clock")
	}
	s.C.DB.Exec(ctx, `UPDATE users SET totp_secret=?, totp_pending=NULL, totp_last=?, mfa_enabled=1, updated_at=? WHERE user_id=?`, pending, c, store.Now(), userID)
	s.C.Audit.Append(ctx, audit.Entry{Action: "user.mfa_enable", Target: userID, Result: "ok"})
	return nil
}

// DisableMFA turns TOTP off (needs a current code).
func (s *Service) DisableMFA(ctx context.Context, userID, code string) error {
	if err := s.checkMFA(ctx, userID, code); err != nil {
		return err
	}
	s.C.DB.Exec(ctx, `UPDATE users SET totp_secret=NULL, mfa_enabled=0, updated_at=? WHERE user_id=?`, store.Now(), userID)
	s.C.Audit.Append(ctx, audit.Entry{Action: "user.mfa_disable", Target: userID, Result: "ok"})
	return nil
}

// checkMFA verifies a code for a user with MFA on (nil when MFA is off).
func (s *Service) checkMFA(ctx context.Context, userID, code string) error {
	var secret string
	var enabled int
	var last int64
	if err := s.C.DB.QueryRow(ctx, `SELECT COALESCE(totp_secret,''), mfa_enabled, totp_last FROM users WHERE user_id=?`, userID).Scan(&secret, &enabled, &last); err != nil {
		return err
	}
	if enabled == 0 {
		return nil
	}
	if code == "" {
		e := core.Errf(core.CodeUnauthenticated, "MFA code required")
		e.Info = map[string]any{"mfa_required": true}
		return e
	}
	c, ok := verifyTOTP(secret, code, time.Now(), last)
	if !ok {
		return ErrLogin
	}
	s.C.DB.Exec(ctx, `UPDATE users SET totp_last=? WHERE user_id=?`, c, userID)
	return nil
}
