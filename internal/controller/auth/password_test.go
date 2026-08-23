package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRejectsOversizedPassword(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("x", maxPasswordBytes+1)); err == nil {
		t.Fatal("expected oversized password to be rejected")
	}
}

func TestVerifyPasswordRejectsMalformedHashParameters(t *testing.T) {
	if VerifyPassword("pbkdf2-sha256$210000$AA$AA", "correct horse battery staple") {
		t.Fatal("malformed salt/key lengths must not verify")
	}
	if VerifyPassword("pbkdf2-sha256$1000001$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "correct horse battery staple") {
		t.Fatal("excessive PBKDF2 iterations must not verify")
	}
}
