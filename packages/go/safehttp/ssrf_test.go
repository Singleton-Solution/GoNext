package safehttp

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// fakeResolver lets tests drive the SSRF guard without DNS.
type fakeResolver struct {
	mapping map[string][]netip.Addr
	err     error
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.mapping[host], nil
}

func TestIsPublicAddr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		// Public IPv4 and IPv6 — should pass.
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},

		// IPv4 SSRF targets.
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false}, // AWS metadata
		{"169.254.0.1", false},     // link-local
		{"0.0.0.0", false},
		{"100.64.0.1", false}, // CGNAT
		{"100.127.255.255", false},

		// Multicast / unspecified.
		{"224.0.0.1", false},
		{"239.255.255.255", false},

		// IPv6 SSRF targets.
		{"::1", false},
		{"fe80::1", false},
		{"fc00::1", false}, // ULA
		{"fd00::1", false}, // ULA
		{"ff00::1", false}, // multicast
		{"::", false},      // unspecified
	}
	for _, tc := range cases {
		ip, err := netip.ParseAddr(tc.ip)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.ip, err)
		}
		if got := isPublicAddr(ip); got != tc.want {
			t.Errorf("isPublicAddr(%s)=%v want %v", tc.ip, got, tc.want)
		}
	}
}

func TestAssertHostPublic_IPLiteral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// IP literal — no DNS, decision is direct.
	if err := assertHostPublic(ctx, nil, "8.8.8.8"); err != nil {
		t.Errorf("public IP literal rejected: %v", err)
	}
	if err := assertHostPublic(ctx, nil, "127.0.0.1"); err == nil {
		t.Errorf("loopback IP literal accepted")
	} else if !errors.Is(err, ErrBlocked) {
		t.Errorf("wrong error type: %v", err)
	}
}

func TestAssertHostPublic_DNS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := &fakeResolver{mapping: map[string][]netip.Addr{
		"public.example.com":  {netip.MustParseAddr("8.8.8.8")},
		"private.example.com": {netip.MustParseAddr("10.0.0.1")},
		"mixed.example.com": {
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("127.0.0.1"),
		},
	}}

	if err := assertHostPublic(ctx, r, "public.example.com"); err != nil {
		t.Errorf("public host rejected: %v", err)
	}
	if err := assertHostPublic(ctx, r, "private.example.com"); err == nil {
		t.Errorf("private host accepted")
	}
	// Mixed answers: fail closed.
	if err := assertHostPublic(ctx, r, "mixed.example.com"); err == nil {
		t.Errorf("host with both public+private answers accepted")
	}
}

func TestAssertHostPublic_Empty(t *testing.T) {
	t.Parallel()
	if err := assertHostPublic(context.Background(), nil, ""); err == nil {
		t.Errorf("empty host accepted")
	}
}

func TestAssertHostPublic_IPv6Brackets(t *testing.T) {
	t.Parallel()
	// URL hostnames for IPv6 come with brackets; assertHostPublic
	// strips them.
	if err := assertHostPublic(context.Background(), nil, "[::1]"); err == nil {
		t.Errorf("[::1] accepted")
	}
}
