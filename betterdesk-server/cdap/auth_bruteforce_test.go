package cdap

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/ratelimit"
)

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	gateway := &Gateway{cfg: config.DefaultConfig()}
	req := httptest.NewRequest("GET", "/cdap", nil)
	req.RemoteAddr = "198.51.100.20:40000"
	req.Header.Set("X-Real-IP", "203.0.113.7")
	req.Header.Set("X-Forwarded-For", "203.0.113.8")

	if got := gateway.clientIP(req); got != "198.51.100.20" {
		t.Fatalf("clientIP = %q, want the direct peer", got)
	}
}

func TestClientIPHonorsTrustedProxy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TrustProxy = true
	nets, err := config.ParseTrustedProxies("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProxies = nets
	gateway := &Gateway{cfg: cfg}

	req := httptest.NewRequest("GET", "/cdap", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Real-IP", "203.0.113.7")
	if got := gateway.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want X-Real-IP from the trusted proxy", got)
	}

	req.Header.Set("X-Real-IP", "not-an-ip")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := gateway.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the first X-Forwarded-For hop", got)
	}

	req.Header.Set("X-Forwarded-For", "garbage")
	if got := gateway.clientIP(req); got != "127.0.0.1" {
		t.Fatalf("clientIP = %q, want the direct peer for unparseable headers", got)
	}
}

func TestUserPasswordAttemptsAreLimitedPerAccount(t *testing.T) {
	gateway, database := newAPIKeyAuthGateway(t)
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateUser(&db.User{Username: "Admin", PasswordHash: hash, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	gateway.SetLoginLimiter(ratelimit.NewIPLimiter(2, time.Minute, time.Minute))

	// Each guess comes from a different address, so only an account-level
	// budget can stop the third attempt.
	for i, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		if _, _, err := gateway.authUserPassword(AuthPayload{Username: "admin", Password: "wrong"}, ip); err == nil {
			t.Fatalf("attempt %d with a wrong password succeeded", i+1)
		}
	}
	_, _, err = gateway.authUserPassword(AuthPayload{Username: "ADMIN", Password: "correct-password"}, "203.0.113.3")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("third attempt err = %v, want the account rate limit", err)
	}
}

func TestUnknownUserComparisonUsesCurrentHashFormat(t *testing.T) {
	if hash := dummyPasswordHash(); !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Fatalf("dummy hash %q is not in the current PBKDF2 format", hash)
	}
}
