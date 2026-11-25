package config

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()
	nets, err := ParseTrustedProxies("127.0.0.1/32, 10.0.0.0/8, 2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 {
		t.Fatalf("len=%d, want 3", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("127.0.0.1")) {
		t.Fatal("expected 127.0.0.1 in first net")
	}
	if !nets[2].Contains(net.ParseIP("2001:db8::1")) {
		t.Fatal("expected bare IPv6 to become /128")
	}
}

func TestParseTrustedProxiesInvalid(t *testing.T) {
	t.Parallel()
	if _, err := ParseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldHonorForwardedHeaders(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		TrustProxy: true,
		TrustedProxies: []*net.IPNet{
			mustParseCIDR(t, "10.0.0.0/8"),
		},
	}
	if !cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("trusted proxy should honor headers")
	}
	if cfg.ShouldHonorForwardedHeaders("203.0.113.1:443") {
		t.Fatal("untrusted remote must not honor headers")
	}
	cfg.TrustedProxies = nil
	if cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("empty allowlist must not honor headers")
	}
	cfg.TrustProxy = false
	cfg.TrustedProxies = []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}
	if cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("TrustProxy=false must not honor headers")
	}
}

func TestIPIsPanelSignalProxy(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("127.0.0.1 should match default loopback allowlist")
	}
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("::1")) {
		t.Fatal("::1 should match default loopback allowlist")
	}
	if cfg.IPIsPanelSignalProxy(net.ParseIP("198.51.100.1")) {
		t.Fatal("public IP must not match default panel proxy allowlist")
	}

	cfg.PanelSignalProxyCIDRs = nil
	if cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("empty allowlist must reject")
	}

	nets, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PanelSignalProxyCIDRs = nets
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("10.1.2.3")) {
		t.Fatal("10.1.2.3 should match 10.0.0.0/8")
	}
	if cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback should not match custom 10.0.0.0/8-only allowlist")
	}
}

func mustParseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestClientIPTakesTheHopTheTrustedProxyAdded(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		TrustProxy:     true,
		TrustedProxies: []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")},
	}
	cases := []struct {
		name, remote, xff, realIP, want string
	}{
		{"untrusted peer ignores headers", "203.0.113.1:443", "6.6.6.6", "7.7.7.7", "203.0.113.1"},
		{"nginx appends the real client", "10.0.0.2:5000", "6.6.6.6, 198.51.100.4", "198.51.100.4", "198.51.100.4"},
		{"chained trusted proxies are skipped", "10.0.0.2:5000", "6.6.6.6, 198.51.100.4, 10.0.0.9", "", "198.51.100.4"},
		{"repeated headers keep their order", "10.0.0.2:5000", "", "", "198.51.100.5"},
		{"X-Real-IP only without X-Forwarded-For", "10.0.0.2:5000", "", "198.51.100.6", "198.51.100.6"},
		{"hop with a port", "10.0.0.2:5000", "198.51.100.7:4321", "", "198.51.100.7"},
		{"malformed hop falls back to the peer", "10.0.0.2:5000", "1.2.3.4, garbage", "", "10.0.0.2"},
		{"all hops trusted keeps the leftmost", "10.0.0.2:5000", "10.0.0.3, 10.0.0.4", "", "10.0.0.3"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tc.remote
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.name == "repeated headers keep their order" {
			req.Header.Add("X-Forwarded-For", "6.6.6.6")
			req.Header.Add("X-Forwarded-For", "198.51.100.5")
		}
		if tc.realIP != "" {
			req.Header.Set("X-Real-IP", tc.realIP)
		}
		if got := cfg.ClientIP(req); got != tc.want {
			t.Errorf("%s: ClientIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}
