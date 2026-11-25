package cdap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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

	// nginx $proxy_add_x_forwarded_for appends the address it saw after
	// whatever the client sent, so the left part is the client's to choose.
	req.Header.Set("X-Real-IP", "not-an-ip")
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := gateway.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the hop the trusted proxy added", got)
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

// Each connection is charged once against the IP limit, at the upgrade.
func TestAuthDoesNotChargeTheIPLimitAgain(t *testing.T) {
	gateway, database := newAPIKeyAuthGateway(t)
	gateway.ctx = context.Background()
	gateway.SetJWTManager(auth.NewJWTManager("cdap-test-secret-key-32-bytes-min!", time.Hour))
	hash, err := auth.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateUser(&db.User{Username: "agent", PasswordHash: hash, Role: "operator"}); err != nil {
		t.Fatal(err)
	}
	gateway.SetRateLimiter(ratelimit.NewIPLimiter(1, time.Minute, time.Minute))

	server := httptest.NewServer(http.HandlerFunc(gateway.handleWebSocket))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/cdap",
		&websocket.DialOptions{Subprotocols: []string{"cdap-v1"}})
	if err != nil {
		t.Fatalf("upgrade within the limit failed: %v", err)
	}
	defer conn.CloseNow()

	payload, _ := json.Marshal(AuthPayload{Method: "user_password", Username: "agent", Password: "pw", DeviceID: "cdapdev01"})
	frame, _ := json.Marshal(Message{Type: "auth", Payload: payload})
	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reply Message
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "auth_result" {
		t.Fatalf("reply = %s %s, want auth_result", reply.Type, reply.Payload)
	}
}
