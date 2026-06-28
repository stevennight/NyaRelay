package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nyarelay/internal/controller/auth"
)

func TestLoginLimitKeyIgnoresRemotePort(t *testing.T) {
	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	reqA.RemoteAddr = "198.51.100.10:40001"
	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	reqB.RemoteAddr = "198.51.100.10:40002"

	if gotA, gotB := loginLimitKey(reqA, "admin"), loginLimitKey(reqB, "admin"); gotA != gotB {
		t.Fatalf("limit key mismatch: %q != %q", gotA, gotB)
	}
}

func TestLoginLimitKeyPrefersForwardedAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.10:40001"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.10")

	if got := loginLimitKey(req, "admin"); got != "203.0.113.9:admin" {
		t.Fatalf("limit key = %q, want forwarded client ip", got)
	}
}

func TestSetSessionCookieUsesSecureWhenForwardedHTTPS(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel.example/api/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	session := auth.Session{
		ID:        "session-1",
		UserID:    1,
		Username:  "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	setSessionCookie(rec, req, session)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatal("session cookie should be secure for https requests")
	}
}

func TestFirstHostHandlesIPv6(t *testing.T) {
	if got := firstHost("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("firstHost = %q, want IPv6 host", got)
	}
}
