package csp

import (
	"strings"
	"testing"
)

// TestSourceExpr_StringRendersAllKinds verifies each typed source
// constructor renders to the exact CSP token shape mandated by the
// spec. The asserted strings double as documentation; if a constructor
// is ever changed, this test will fail and the change will be visible
// in the diff.
func TestSourceExpr_StringRendersAllKinds(t *testing.T) {
	cases := []struct {
		name string
		s    SourceExpr
		want string
	}{
		{"Self", Self(), "'self'"},
		{"None", None(), "'none'"},
		{"UnsafeInline", UnsafeInline(), "'unsafe-inline'"},
		{"UnsafeEval", UnsafeEval(), "'unsafe-eval'"},
		{"WasmUnsafeEval", WasmUnsafeEval(), "'wasm-unsafe-eval'"},
		{"StrictDynamic", StrictDynamic(), "'strict-dynamic'"},
		{"ReportSample", ReportSample(), "'report-sample'"},
		{"UnsafeHashes", UnsafeHashes(), "'unsafe-hashes'"},
		{"Nonce", Nonce("abc123"), "'nonce-abc123'"},
		{"Sha256", Sha256("aaaa"), "'sha256-aaaa'"},
		{"Sha384", Sha384("bbbb"), "'sha384-bbbb'"},
		{"Sha512", Sha512("cccc"), "'sha512-cccc'"},
		{"Host", Host("https://example.com"), "https://example.com"},
		{"Scheme", Scheme("data:"), "data:"},
		{"Raw", Raw("'foo'"), "'foo'"},
		{"Zero", SourceExpr{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSourceExpr_IsZero exercises the zero-value sentinel used by the
// directive serializer to skip placeholders.
func TestSourceExpr_IsZero(t *testing.T) {
	if !(SourceExpr{}).IsZero() {
		t.Errorf("zero value should report IsZero=true")
	}
	if Self().IsZero() {
		t.Errorf("Self() should not report IsZero=true")
	}
}

// TestPolicy_StringEmitsCanonicalSyntax sanity-checks a hand-built
// Policy through Policy.String, verifying directive order, separator,
// and source-expression joining.
func TestPolicy_StringEmitsCanonicalSyntax(t *testing.T) {
	p := &Policy{
		DefaultSrc:              []SourceExpr{Self()},
		ScriptSrc:               []SourceExpr{Self(), StrictDynamic()},
		StyleSrc:                []SourceExpr{Self()},
		ImgSrc:                  []SourceExpr{Self(), Scheme("data:")},
		ObjectSrc:               []SourceExpr{None()},
		BaseURI:                 []SourceExpr{Self()},
		FormAction:              []SourceExpr{Self()},
		FrameAncestors:          []SourceExpr{Self()},
		UpgradeInsecureRequests: true,
		ReportURI:               "/_/csp-report",
	}
	got := p.String()
	want := "default-src 'self'; " +
		"script-src 'self' 'strict-dynamic'; " +
		"style-src 'self'; " +
		"img-src 'self' data:; " +
		"frame-ancestors 'self'; " +
		"form-action 'self'; " +
		"base-uri 'self'; " +
		"object-src 'none'; " +
		"upgrade-insecure-requests; " +
		"report-uri /_/csp-report"
	if got != want {
		t.Errorf("\n got:  %s\nwant:  %s", got, want)
	}
}

// TestPolicy_StringOmitsEmptyDirectives ensures we don't accidentally
// emit "name ;" for slices that are empty or contain only zero values.
func TestPolicy_StringOmitsEmptyDirectives(t *testing.T) {
	p := &Policy{
		DefaultSrc: []SourceExpr{Self()},
		ScriptSrc:  []SourceExpr{}, // present but empty — must be omitted
		StyleSrc:   []SourceExpr{{}}, // contains only zero — must be omitted
	}
	got := p.String()
	if got != "default-src 'self'" {
		t.Errorf("got %q, want exactly the default-src directive", got)
	}
	if strings.Contains(got, "script-src") || strings.Contains(got, "style-src") {
		t.Errorf("empty directives leaked: %s", got)
	}
}

// TestPolicy_StringHandlesAllFlags exercises the boolean directives
// (upgrade-insecure-requests, block-all-mixed-content) and the sandbox
// + trusted-types families.
func TestPolicy_StringHandlesAllFlags(t *testing.T) {
	p := &Policy{
		BlockAllMixedContent:    true,
		UpgradeInsecureRequests: true,
		Sandbox:                 []string{"allow-scripts", "allow-same-origin"},
		RequireTrustedTypesFor:  []string{"script"},
		TrustedTypes:            []string{"default", "nextjs#bundler"},
		ReportTo:                "default",
	}
	got := p.String()
	for _, must := range []string{
		"upgrade-insecure-requests",
		"block-all-mixed-content",
		"sandbox allow-scripts allow-same-origin",
		"require-trusted-types-for 'script'",
		"trusted-types default nextjs#bundler",
		"report-to default",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("output missing %q\nfull: %s", must, got)
		}
	}
}

// TestPolicy_StringBareSandbox covers the edge case where Sandbox is
// non-nil but empty: the spec mandates a bare `sandbox` directive
// (strictest sandbox, no allow-tokens).
func TestPolicy_StringBareSandbox(t *testing.T) {
	p := &Policy{Sandbox: []string{}}
	got := p.String()
	if got != "sandbox" {
		t.Errorf("got %q, want exactly %q", got, "sandbox")
	}
}

// TestPolicy_StringRequireTrustedTypesAutoQuotes verifies bare "script"
// is quoted to 'script' as the spec requires, while already-quoted
// values pass through unchanged.
func TestPolicy_StringRequireTrustedTypesAutoQuotes(t *testing.T) {
	p := &Policy{RequireTrustedTypesFor: []string{"script", "'style'"}}
	got := p.String()
	if !strings.Contains(got, "require-trusted-types-for 'script' 'style'") {
		t.Errorf("auto-quote failed: %s", got)
	}
}

// TestPolicy_NilReceiverIsSafe documents that calling String / WithNonce
// on a nil *Policy does not panic — useful for defensive call sites.
func TestPolicy_NilReceiverIsSafe(t *testing.T) {
	var p *Policy
	if got := p.String(); got != "" {
		t.Errorf("nil String() should be empty, got %q", got)
	}
	if got := p.WithNonce("xyz"); got == nil {
		t.Errorf("nil WithNonce should return non-nil empty Policy")
	}
}

// TestPolicy_WithNonceAddsToScriptAndStyle verifies the documented
// behavior: nonce is added to both script-src and style-src.
func TestPolicy_WithNonceAddsToScriptAndStyle(t *testing.T) {
	base := &Policy{
		ScriptSrc: []SourceExpr{Self()},
		StyleSrc:  []SourceExpr{Self()},
	}
	derived := base.WithNonce("ABCDEF")
	got := derived.String()
	if !strings.Contains(got, "script-src 'self' 'nonce-ABCDEF'") {
		t.Errorf("script-src missing nonce: %s", got)
	}
	if !strings.Contains(got, "style-src 'self' 'nonce-ABCDEF'") {
		t.Errorf("style-src missing nonce: %s", got)
	}

	// Original must not be mutated.
	if strings.Contains(base.String(), "nonce-") {
		t.Errorf("original policy mutated: %s", base.String())
	}
}

// TestPolicy_WithNonceMirrorsElemDirectives verifies that the per-element
// directives (script-src-elem, style-src-elem), when present, also
// receive the nonce.
func TestPolicy_WithNonceMirrorsElemDirectives(t *testing.T) {
	base := &Policy{
		ScriptSrcElem: []SourceExpr{Self()},
		StyleSrcElem:  []SourceExpr{Self()},
	}
	got := base.WithNonce("XYZ").String()
	if !strings.Contains(got, "script-src-elem 'self' 'nonce-XYZ'") {
		t.Errorf("script-src-elem missing nonce: %s", got)
	}
	if !strings.Contains(got, "style-src-elem 'self' 'nonce-XYZ'") {
		t.Errorf("style-src-elem missing nonce: %s", got)
	}
}

// TestPolicy_WithNonceEmptyStringIsNoop verifies the documented opt-out:
// when called with "" the policy is returned as-is so chains can call
// WithNonce unconditionally.
func TestPolicy_WithNonceEmptyStringIsNoop(t *testing.T) {
	base := &Policy{ScriptSrc: []SourceExpr{Self()}}
	got := base.WithNonce("").String()
	want := "script-src 'self'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestPolicy_WithNonceSkipsEmptyDirectives verifies a nonce is NOT
// added to directives the caller left empty — adding a nonce there
// would emit a directive that the caller never declared.
func TestPolicy_WithNonceSkipsEmptyDirectives(t *testing.T) {
	base := &Policy{ScriptSrc: []SourceExpr{Self()}}
	got := base.WithNonce("Q").String()
	if strings.Contains(got, "style-src") {
		t.Errorf("nonce leaked into undeclared style-src: %s", got)
	}
}

// TestPolicy_CloneIsDeep verifies Clone returns a deep copy so mutating
// the clone does not affect the source.
func TestPolicy_CloneIsDeep(t *testing.T) {
	src := &Policy{
		ScriptSrc:    []SourceExpr{Self()},
		Sandbox:      []string{"allow-scripts"},
		TrustedTypes: []string{"default"},
	}
	c := src.Clone()
	c.ScriptSrc = append(c.ScriptSrc, UnsafeInline())
	c.Sandbox = append(c.Sandbox, "allow-forms")
	c.TrustedTypes = append(c.TrustedTypes, "dompurify")

	if strings.Contains(src.String(), "unsafe-inline") {
		t.Errorf("src mutated via clone: %s", src.String())
	}
	if strings.Contains(src.String(), "allow-forms") {
		t.Errorf("src.Sandbox mutated via clone: %s", src.String())
	}
	if strings.Contains(src.String(), "dompurify") {
		t.Errorf("src.TrustedTypes mutated via clone: %s", src.String())
	}
}

// TestSortedClone is a tiny coverage tickle for the helper that may
// otherwise drop coverage below the 90% target if presets stop using it
// directly.
func TestSortedClone(t *testing.T) {
	in := []string{"b", "a", "c"}
	got := sortedClone(in)
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("sortedClone failed: %v", got)
	}
	if &got[0] == &in[0] {
		t.Errorf("sortedClone returned the input slice, not a copy")
	}
}
