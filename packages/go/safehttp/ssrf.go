package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// ErrBlocked is the sentinel returned when a URL is rejected by the
// SSRF guard or the domain allowlist. Callers can errors.Is against it
// to distinguish "we refused to send" from "the upstream refused us".
var ErrBlocked = errors.New("safehttp: request blocked")

// Resolver is the subset of net.Resolver we depend on. Exposing the
// interface (rather than taking *net.Resolver) lets tests substitute a
// fake resolver that, for example, claims "evil.com" resolves to
// 127.0.0.1 so the SSRF guard can be exercised end-to-end against an
// httptest.Server.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// netResolver is the wrapper around net.DefaultResolver used when no
// resolver is supplied via WithResolver.
type netResolver struct {
	r *net.Resolver
}

func (n *netResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return n.r.LookupNetIP(ctx, network, host)
}

// defaultResolver is the package-default Resolver. Wraps
// net.DefaultResolver so it picks up the operator's /etc/resolv.conf
// without ceremony.
var defaultResolver Resolver = &netResolver{r: net.DefaultResolver}

// AssertHostPublic is the exported entry point to the SSRF check.
// Resolves host through net.DefaultResolver and reports an error if
// any resolved address is in the SSRF denylist. Use this from callers
// that aren't going through a full safehttp.Client (e.g. the plugin
// runtime, where the per-plugin allowlist lives elsewhere but the
// IP-classification logic should be shared).
//
// Returns a wrapped ErrBlocked on any failure.
func AssertHostPublic(ctx context.Context, host string) error {
	return assertHostPublic(ctx, defaultResolver, host)
}

// IsPublicAddr is the exported version of the IP classifier. Callers
// that have already resolved a host (or are inspecting a literal IP)
// can call this directly.
func IsPublicAddr(ip netip.Addr) bool { return isPublicAddr(ip) }

// assertHostPublic verifies that host (a URL hostname, possibly with
// brackets for IPv6) resolves only to public IP addresses. Returns a
// wrapped ErrBlocked on any failure.
//
// Single resolved address that is private => block.
// Multiple addresses, ANY of which is private => block. We deliberately
// fail closed: a DNS that returns both a public and a private answer
// (whether by accident or by attacker design) is treated as untrusted.
func assertHostPublic(ctx context.Context, r Resolver, host string) error {
	host = strings.Trim(host, "[]")
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrBlocked)
	}

	// IP-literal short-circuit: callers may pass http://10.0.0.1/ — no
	// DNS lookup necessary, check the literal directly.
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(ip) {
			return fmt.Errorf("%w: %s is non-public", ErrBlocked, ip)
		}
		return nil
	}

	if r == nil {
		r = defaultResolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("%w: resolve %s: %v", ErrBlocked, host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%w: resolve %s: no addresses", ErrBlocked, host)
	}
	for _, a := range addrs {
		if !isPublicAddr(a) {
			return fmt.Errorf("%w: %s resolves to non-public %s", ErrBlocked, host, a)
		}
	}
	return nil
}

// isPublicAddr reports whether ip is safe to talk to from a multi-tenant
// server. The denylist is the union of common SSRF targets:
//
//   - RFC1918 (10/8, 172.16/12, 192.168/16) — typical private LAN
//   - 127/8 IPv4 loopback
//   - ::1 IPv6 loopback
//   - 169.254/16 link-local (covers AWS cloud-metadata 169.254.169.254)
//   - fe80::/10 IPv6 link-local
//   - Multicast (224/4, ff00::/8)
//   - Unspecified (0.0.0.0, ::)
//   - 100.64/10 CGNAT — used inside cloud-provider networks
//   - fc00::/7 IPv6 unique-local addresses (private ULA)
//
// The function is conservative: anything it isn't sure about is
// treated as non-public. That is the right posture for a client whose
// caller hasn't yet validated the URL through an allowlist.
func isPublicAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	// netip's own classifiers cover most cases.
	if ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		// 169.254/16 (link-local — already covered, but spell it out).
		if b[0] == 169 && b[1] == 254 {
			return false
		}
		// 100.64/10 — CGNAT, often used inside cloud LANs.
		if b[0] == 100 && (b[1]&0xc0) == 64 {
			return false
		}
		// 0.0.0.0/8 reserved for "this network".
		if b[0] == 0 {
			return false
		}
	}
	// IPv6 ULA — fc00::/7. netip.IsPrivate covers fec0::/10 deprecated
	// site-local but not fc00::/7 in older Go releases; check
	// explicitly so we never have to find out which release we're on.
	if ip.Is6() {
		b := ip.As16()
		if b[0]&0xfe == 0xfc {
			return false
		}
	}
	return true
}
