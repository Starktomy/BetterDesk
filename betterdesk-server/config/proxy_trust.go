package config

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of CIDRs or single IPs
// into IPNet entries. Bare IPs become /32 (IPv4) or /128 (IPv6).
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy IP %q", p)
			}
			if ip4 := ip.To4(); ip4 != nil {
				p = ip4.String() + "/32"
			} else {
				p = ip.String() + "/128"
			}
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", p, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// RemoteAddrIsTrustedProxy reports whether the direct TCP peer (r.RemoteAddr)
// is in TrustedProxies. Returns false when the allowlist is empty so
// TRUST_PROXY=Y alone cannot honor spoofable X-Forwarded-* headers (#276).
func (c *Config) RemoteAddrIsTrustedProxy(remoteAddr string) bool {
	if c == nil || len(c.TrustedProxies) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return c.ipIsTrustedProxy(net.ParseIP(host))
}

func (c *Config) ipIsTrustedProxy(ip net.IP) bool {
	if c == nil || ip == nil {
		return false
	}
	for _, n := range c.TrustedProxies {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// IPIsPanelSignalProxy reports whether ip is in PanelSignalProxyCIDRs
// (Node panel → hbbs TCP proxy for Web Remote). Empty allowlist → false.
func (c *Config) IPIsPanelSignalProxy(ip net.IP) bool {
	if c == nil || ip == nil || len(c.PanelSignalProxyCIDRs) == 0 {
		return false
	}
	for _, n := range c.PanelSignalProxyCIDRs {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// ShouldHonorForwardedHeaders is true only when TrustProxy is set and the
// direct connection comes from a configured trusted proxy CIDR.
func (c *Config) ShouldHonorForwardedHeaders(remoteAddr string) bool {
	return c != nil && c.TrustProxy && c.RemoteAddrIsTrustedProxy(remoteAddr)
}

// WarnProxyTrustMisconfig logs when TRUST_PROXY is enabled without an allowlist.
func (c *Config) WarnProxyTrustMisconfig() {
	if c == nil || !c.TrustProxy {
		return
	}
	if len(c.TrustedProxies) == 0 {
		log.Printf("[config] TRUST_PROXY=Y but TRUSTED_PROXIES is empty — X-Forwarded-For/X-Real-IP headers will be IGNORED. Set TRUSTED_PROXIES to your reverse proxy CIDR(s), e.g. 127.0.0.1/32,10.0.0.0/8 (#276)")
	}
}

// ForwardedClient returns the client address reported by trusted proxies, as
// the raw header value (it may carry a port), or false when forwarded headers
// do not apply.
//
// Proxies append to X-Forwarded-For, so its left part is whatever the client
// sent. Walk it from the right past further trusted proxies and take the
// first untrusted hop: the address the outermost trusted proxy saw. This is
// the proxy-addr rule the console uses. X-Real-IP is used only without
// X-Forwarded-For.
func (c *Config) ForwardedClient(r *http.Request) (string, bool) {
	if r == nil || !c.ShouldHonorForwardedHeaders(r.RemoteAddr) {
		return "", false
	}
	var hops []string
	for _, value := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(value, ",") {
			if hop := strings.TrimSpace(part); hop != "" {
				hops = append(hops, hop)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip := forwardedHopIP(hops[i])
		if ip == nil {
			return "", false
		}
		if i == 0 || !c.ipIsTrustedProxy(ip) {
			return hops[i], true
		}
	}
	if len(hops) == 0 {
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); forwardedHopIP(xri) != nil {
			return xri, true
		}
	}
	return "", false
}

// ClientIP returns the client IP for rate limits, blocklists and audit
// records: the forwarded client when a trusted proxy reported one, otherwise
// the direct peer.
func (c *Config) ClientIP(r *http.Request) string {
	if value, ok := c.ForwardedClient(r); ok {
		return forwardedHopIP(value).String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func forwardedHopIP(hop string) net.IP {
	if host, _, err := net.SplitHostPort(hop); err == nil {
		hop = host
	}
	return net.ParseIP(strings.Trim(hop, "[]"))
}
