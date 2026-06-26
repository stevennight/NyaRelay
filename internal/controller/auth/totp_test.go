package auth

import (
	"testing"
	"time"
)

func TestTOTPVerifyCurrentCode(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	code := GenerateTOTPCode(secret, now)
	if !VerifyTOTP(secret, code, now) {
		t.Fatal("expected current totp code to verify")
	}
	if VerifyTOTP(secret, "000000", now) {
		t.Fatal("expected wrong code to fail")
	}
}
