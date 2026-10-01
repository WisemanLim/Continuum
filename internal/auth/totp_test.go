package auth

import (
	"testing"
	"time"
)

// RFC 6238 Appendix B (SHA-1), last 6 digits.
func TestTOTPVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for _, c := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}} {
		got, err := TOTPCode(secret, c.unix/30)
		if err != nil || got != c.want {
			t.Fatalf("T=%d: %s %v want %s", c.unix, got, err, c.want)
		}
	}
	now := time.Unix(1234567890, 0)
	if _, ok := verifyTOTP(secret, "005924", now, 0); !ok {
		t.Fatal("current step")
	}
	prev, _ := TOTPCode(secret, now.Unix()/30-1)
	if _, ok := verifyTOTP(secret, prev, now, 0); !ok {
		t.Fatal("previous step within window")
	}
	old, _ := TOTPCode(secret, now.Unix()/30-3)
	if _, ok := verifyTOTP(secret, old, now, 0); ok {
		t.Fatal("old code accepted")
	}
	if _, ok := verifyTOTP(secret, "005924", now, now.Unix()/30); ok {
		t.Fatal("replay accepted")
	}
}
