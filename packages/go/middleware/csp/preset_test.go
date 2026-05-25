package csp

import (
	"strings"
	"testing"
)

// TestPublicSitePolicy_MatchesBaseline pins the public-site preset to
// the canonical shape in docs/13-security-baseline.md §3.1.
func TestPublicSitePolicy_MatchesBaseline(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{
		MediaHosts:  []string{"https://media.example.com"},
		OEmbedHosts: []string{"https://www.youtube.com", "https://player.vimeo.com"},
		ReportURI:   "/_/csp-report",
		ReportTo:    "default",
	})
	got := p.String()

	mustContain := []string{
		"default-src 'self'",
		"script-src 'self' 'strict-dynamic'",
		"style-src 'self'",
		"img-src 'self' data: https://media.example.com",
		"font-src 'self' data:",
		"connect-src 'self' https://media.example.com",
		"frame-src 'self' https://www.youtube.com https://player.vimeo.com",
		"media-src 'self' https://media.example.com",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'self'",
		"worker-src 'self'",
		"manifest-src 'self'",
		"upgrade-insecure-requests",
		"report-uri /_/csp-report",
		"report-to default",
	}
	for _, m := range mustContain {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}

	// MUST NOT contain unsafe-inline / unsafe-eval per baseline.
	for _, banned := range []string{"'unsafe-inline'", "'unsafe-eval'"} {
		if strings.Contains(got, banned) {
			t.Errorf("public preset leaked %s: %s", banned, got)
		}
	}
}

// TestPublicSitePolicy_AcceptsExtras exercises the lesser-used options
// (ScriptHosts, StyleHosts, FontHosts, ExtraImgSchemes,
// ExtraMediaSchemes, FrameAncestors) so they don't regress silently.
func TestPublicSitePolicy_AcceptsExtras(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{
		ScriptHosts:       []string{"https://cdn.example.com"},
		StyleHosts:        []string{"https://fonts.googleapis.com"},
		FontHosts:         []string{"https://fonts.gstatic.com"},
		ExtraImgSchemes:   []SourceExpr{Scheme("blob:")},
		ExtraMediaSchemes: []SourceExpr{Scheme("mediastream:")},
		FrameAncestors:    []SourceExpr{None()},
		ConnectHosts:      []string{"https://api.example.com"},
	})
	got := p.String()
	for _, m := range []string{
		"script-src 'self' https://cdn.example.com",
		"style-src 'self' https://fonts.googleapis.com",
		"font-src 'self' data: https://fonts.gstatic.com",
		"img-src 'self' data: blob:",
		"media-src 'self' mediastream:",
		"frame-ancestors 'none'",
		"connect-src 'self' https://api.example.com",
	} {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}
}

// TestPublicSitePolicy_RespectsIncludeStrictDynamicFalse pins the
// behavior of the pointer-shaped opt-out.
func TestPublicSitePolicy_RespectsIncludeStrictDynamicFalse(t *testing.T) {
	f := false
	p := PublicSitePolicy(PolicyOptions{IncludeStrictDynamic: &f})
	if strings.Contains(p.String(), "strict-dynamic") {
		t.Errorf("strict-dynamic should have been suppressed: %s", p.String())
	}
}

// TestPublicSitePolicy_RespectsUpgradeInsecureFalse exercises the
// pointer-shaped suppression of upgrade-insecure-requests.
func TestPublicSitePolicy_RespectsUpgradeInsecureFalse(t *testing.T) {
	f := false
	p := PublicSitePolicy(PolicyOptions{IncludeUpgradeInsecureRequests: &f})
	if strings.Contains(p.String(), "upgrade-insecure-requests") {
		t.Errorf("upgrade-insecure-requests should have been suppressed: %s", p.String())
	}
}

// TestPublicSitePolicy_WithNonceAddsToScriptAndStyle verifies the
// middleware-equivalent flow: emit nonce from context, call WithNonce,
// nonce ends up in BOTH script-src and style-src.
func TestPublicSitePolicy_WithNonceAddsToScriptAndStyle(t *testing.T) {
	p := PublicSitePolicy(PolicyOptions{})
	got := p.WithNonce("NONCE123").String()
	if !strings.Contains(got, "'nonce-NONCE123'") {
		t.Errorf("nonce missing: %s", got)
	}
	// Both directives must carry the nonce, not just one.
	if cnt := strings.Count(got, "'nonce-NONCE123'"); cnt < 2 {
		t.Errorf("expected at least 2 nonce occurrences (script-src + style-src), got %d", cnt)
	}
}

// TestAdminPolicy_MatchesBaseline pins the admin preset to §3.2.
func TestAdminPolicy_MatchesBaseline(t *testing.T) {
	p := AdminPolicy(PolicyOptions{
		MediaHosts: []string{"https://media.example.com"},
		ReportURI:  "/_/csp-report",
	})
	got := p.String()

	for _, m := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data: blob: https://media.example.com",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-src 'self'",
		"media-src 'self' blob: https://media.example.com",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"worker-src 'self' blob:",
		"upgrade-insecure-requests",
		"require-trusted-types-for 'script'",
		"trusted-types default nextjs#bundler dompurify",
		"report-uri /_/csp-report",
	} {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}
	// Admin must NOT include strict-dynamic by default — admin has a
	// fixed allowlist of scripts.
	if strings.Contains(got, "'strict-dynamic'") {
		t.Errorf("admin preset leaked strict-dynamic: %s", got)
	}
}

// TestAdminPolicy_AcceptsExtras exercises the lesser-used options on
// the admin preset to ensure the option-handling branches don't rot.
func TestAdminPolicy_AcceptsExtras(t *testing.T) {
	f := false
	p := AdminPolicy(PolicyOptions{
		ScriptHosts:                    []string{"https://admin-cdn.example.com"},
		StyleHosts:                     []string{"https://styles.example.com"},
		FontHosts:                      []string{"https://fonts.gstatic.com"},
		ExtraImgSchemes:                []SourceExpr{Scheme("filesystem:")},
		ExtraMediaSchemes:              []SourceExpr{Scheme("mediastream:")},
		FrameAncestors:                 []SourceExpr{Self()},
		IncludeUpgradeInsecureRequests: &f,
	})
	got := p.String()
	for _, m := range []string{
		"script-src 'self' https://admin-cdn.example.com",
		"style-src 'self' https://styles.example.com",
		"font-src 'self' data: https://fonts.gstatic.com",
		"img-src 'self' data: blob: filesystem:",
		"media-src 'self' blob: mediastream:",
		"frame-ancestors 'self'",
	} {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}
	if strings.Contains(got, "upgrade-insecure-requests") {
		t.Errorf("upgrade-insecure-requests should have been suppressed: %s", got)
	}
}

// TestAdminPolicy_TrustedTypePoliciesOverride verifies callers can
// substitute their own policy names.
func TestAdminPolicy_TrustedTypePoliciesOverride(t *testing.T) {
	p := AdminPolicy(PolicyOptions{
		TrustedTypePolicies: []string{"my-policy"},
	})
	got := p.String()
	if !strings.Contains(got, "trusted-types my-policy") {
		t.Errorf("override missed: %s", got)
	}
	if strings.Contains(got, "nextjs#bundler") {
		t.Errorf("override should have replaced default policies: %s", got)
	}
}

// TestAdminPolicy_IncludeStrictDynamicTrue exercises the explicit
// opt-in for callers who need it (rare; default is off).
func TestAdminPolicy_IncludeStrictDynamicTrue(t *testing.T) {
	tr := true
	p := AdminPolicy(PolicyOptions{IncludeStrictDynamic: &tr})
	if !strings.Contains(p.String(), "'strict-dynamic'") {
		t.Errorf("opt-in did not enable strict-dynamic: %s", p.String())
	}
}

// TestAdminStrictPolicy_MatchesBaseline pins the admin-strict preset to
// the canonical shape required by issue #59. The shape is:
//
//	default-src 'self'; script-src 'self' 'strict-dynamic' [nonce];
//	require-trusted-types-for 'script';
//	trusted-types gn-admin gn-editor 'allow-duplicates'; …
//
// All other directives mirror AdminPolicy.
func TestAdminStrictPolicy_MatchesBaseline(t *testing.T) {
	p := AdminStrictPolicy(PolicyOptions{
		ReportURI: "/_/csp-report",
	})
	got := p.String()

	for _, m := range []string{
		"default-src 'self'",
		"script-src 'self' 'strict-dynamic'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors 'none'",
		"require-trusted-types-for 'script'",
		"trusted-types gn-admin gn-editor 'allow-duplicates'",
		"report-uri /_/csp-report",
	} {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}

	// Admin-strict MUST NOT include unsafe-inline / unsafe-eval.
	for _, banned := range []string{"'unsafe-inline'", "'unsafe-eval'"} {
		if strings.Contains(got, banned) {
			t.Errorf("admin-strict preset leaked %s: %s", banned, got)
		}
	}
}

// TestAdminStrictPolicy_AppliesNonceTransitively verifies WithNonce
// folds the per-request nonce into script-src alongside 'strict-dynamic'.
// The nonce-from-context shape is what the Next.js middleware mirrors
// in apps/admin/middleware.ts.
func TestAdminStrictPolicy_AppliesNonceTransitively(t *testing.T) {
	p := AdminStrictPolicy(PolicyOptions{})
	got := p.WithNonce("ABC123").String()
	if !strings.Contains(got, "'nonce-ABC123'") {
		t.Errorf("nonce missing: %s", got)
	}
	if !strings.Contains(got, "'strict-dynamic'") {
		t.Errorf("strict-dynamic missing: %s", got)
	}
}

// TestAdminStrictPolicy_HonorsCallerOverrides verifies the caller can
// substitute their own TrustedTypePolicies (e.g. a hardened deployment
// that disallows 'allow-duplicates') and disable strict-dynamic.
func TestAdminStrictPolicy_HonorsCallerOverrides(t *testing.T) {
	f := false
	p := AdminStrictPolicy(PolicyOptions{
		TrustedTypePolicies:  []string{"gn-admin"},
		IncludeStrictDynamic: &f,
	})
	got := p.String()
	if !strings.Contains(got, "trusted-types gn-admin") {
		t.Errorf("override missed: %s", got)
	}
	if strings.Contains(got, "gn-editor") {
		t.Errorf("caller override should have dropped gn-editor: %s", got)
	}
	if strings.Contains(got, "'strict-dynamic'") {
		t.Errorf("caller suppressed strict-dynamic but it leaked: %s", got)
	}
}

// TestHostsToSourcesSkipsEmpty verifies the helper drops empty strings
// so callers can freely append optional lists.
func TestHostsToSourcesSkipsEmpty(t *testing.T) {
	got := hostsToSources([]string{"", "https://a", "", "https://b"})
	if len(got) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(got))
	}
	if got[0].String() != "https://a" || got[1].String() != "https://b" {
		t.Errorf("wrong sources: %+v", got)
	}
	if hostsToSources(nil) != nil {
		t.Errorf("nil input should return nil")
	}
}

// TestBoolPtrHelper exercises the tiny helper used by callers building
// PolicyOptions in literal form.
func TestBoolPtrHelper(t *testing.T) {
	if *boolPtr(true) != true {
		t.Errorf("boolPtr(true) != true")
	}
	if *boolPtr(false) != false {
		t.Errorf("boolPtr(false) != false")
	}
}
