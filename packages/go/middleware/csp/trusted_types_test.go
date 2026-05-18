package csp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTrustedTypesOptions_IsEnabled exercises the small predicate that
// drives the early-out in Middleware. Both shapes (sinks alone, policies
// alone, both) must count as enabled.
func TestTrustedTypesOptions_IsEnabled(t *testing.T) {
	if (TrustedTypesOptions{}).IsEnabled() {
		t.Errorf("zero value should be disabled")
	}
	if !(TrustedTypesOptions{Sinks: []string{"script"}}).IsEnabled() {
		t.Errorf("sinks-only should be enabled")
	}
	if !(TrustedTypesOptions{Policies: []string{"gn-plugin"}}).IsEnabled() {
		t.Errorf("policies-only should be enabled")
	}
	if !(TrustedTypesOptions{
		Sinks:    []string{"script"},
		Policies: []string{"gn-plugin"},
	}).IsEnabled() {
		t.Errorf("both should be enabled")
	}
}

// TestTrustedTypesOptions_Apply_EmptyPolicyIsSafe pins the Apply
// behavior against a nil/empty receiver.
func TestTrustedTypesOptions_Apply_NilReceiver(t *testing.T) {
	// Should not panic.
	(TrustedTypesOptions{Sinks: []string{"script"}}).Apply(nil)
}

// TestPolicy_WithTrustedTypes_AddsBothDirectives is the happy path: a
// fresh policy gets both the sink directive and the policy list.
func TestPolicy_WithTrustedTypes_AddsBothDirectives(t *testing.T) {
	base := &Policy{ScriptSrc: []SourceExpr{Self()}}
	got := base.WithTrustedTypes(TrustedTypesOptions{
		Sinks:    []string{"script"},
		Policies: []string{"gn-plugin", "dompurify"},
	}).String()

	for _, m := range []string{
		"require-trusted-types-for 'script'",
		"trusted-types gn-plugin dompurify",
	} {
		if !strings.Contains(got, m) {
			t.Errorf("output missing %q\nfull: %s", m, got)
		}
	}

	// Original must not be mutated.
	if strings.Contains(base.String(), "trusted-types") {
		t.Errorf("source policy mutated: %s", base.String())
	}
}

// TestPolicy_WithTrustedTypes_MergesUniqueWithExistingPolicy verifies
// that the merge folds in entries already declared on the Policy
// without duplicating them.
func TestPolicy_WithTrustedTypes_MergesUnique(t *testing.T) {
	base := &Policy{
		RequireTrustedTypesFor: []string{"script"},
		TrustedTypes:           []string{"default", "nextjs#bundler"},
	}
	got := base.WithTrustedTypes(TrustedTypesOptions{
		Sinks:    []string{"script"},                // duplicate, must collapse
		Policies: []string{"nextjs#bundler", "gn-plugin"}, // first duplicate, second new
	}).String()

	// One sink occurrence only.
	if cnt := strings.Count(got, "'script'"); cnt != 1 {
		t.Errorf("expected exactly one 'script' sink occurrence, got %d\nfull: %s", cnt, got)
	}
	// One nextjs#bundler occurrence only — and gn-plugin must appear once.
	if cnt := strings.Count(got, "nextjs#bundler"); cnt != 1 {
		t.Errorf("nextjs#bundler duplicated: count=%d\nfull: %s", cnt, got)
	}
	if !strings.Contains(got, "gn-plugin") {
		t.Errorf("gn-plugin missing: %s", got)
	}
}

// TestPolicy_WithTrustedTypes_HandlesQuotedSinkDuplicates exercises the
// trimQuotes normalizer that collapses 'script' and script as the same
// token.
func TestPolicy_WithTrustedTypes_TrimsQuotedSinks(t *testing.T) {
	base := &Policy{RequireTrustedTypesFor: []string{"'script'"}}
	got := base.WithTrustedTypes(TrustedTypesOptions{
		Sinks: []string{"script"}, // bare — must NOT add a second sink token
	}).String()
	if cnt := strings.Count(got, "'script'"); cnt != 1 {
		t.Errorf("quoted/bare dedup failed, got count=%d\nfull: %s", cnt, got)
	}
}

// TestPolicy_WithTrustedTypes_SkipsEmptyTokens ensures whitespace-only or
// empty entries are dropped (matches the directive serializer).
func TestPolicy_WithTrustedTypes_SkipsEmpty(t *testing.T) {
	base := &Policy{}
	got := base.WithTrustedTypes(TrustedTypesOptions{
		Sinks:    []string{"  ", "script", ""},
		Policies: []string{"", "gn-plugin", "   "},
	}).String()
	if !strings.Contains(got, "require-trusted-types-for 'script'") {
		t.Errorf("expected require-trusted-types-for 'script', got: %s", got)
	}
	if !strings.Contains(got, "trusted-types gn-plugin") {
		t.Errorf("expected trusted-types gn-plugin, got: %s", got)
	}
}

// TestPolicy_WithTrustedTypes_NilReceiver verifies the documented safe
// behavior — a nil receiver returns a fresh Policy carrying the merged
// values.
func TestPolicy_WithTrustedTypes_NilReceiver(t *testing.T) {
	var p *Policy
	got := p.WithTrustedTypes(TrustedTypesOptions{
		Sinks:    []string{"script"},
		Policies: []string{"gn-plugin"},
	}).String()
	if !strings.Contains(got, "require-trusted-types-for 'script'") {
		t.Errorf("nil receiver did not produce sink directive: %s", got)
	}
	if !strings.Contains(got, "trusted-types gn-plugin") {
		t.Errorf("nil receiver did not produce policy directive: %s", got)
	}
}

// TestPolicy_WithTrustedTypes_DisabledIsNoop pins the documented no-op
// behavior so callers can call WithTrustedTypes unconditionally.
func TestPolicy_WithTrustedTypes_DisabledIsNoop(t *testing.T) {
	base := &Policy{ScriptSrc: []SourceExpr{Self()}}
	got := base.WithTrustedTypes(TrustedTypesOptions{}).String()
	want := base.String()
	if got != want {
		t.Errorf("disabled options should be no-op\n got: %s\nwant: %s", got, want)
	}
}

// TestMiddleware_RequireTrustedTypesOption is the integration test for
// the middleware-level shortcut. The emitted CSP must carry both the
// sink directive and the merged policy list on every response.
func TestMiddleware_RequireTrustedTypesOption(t *testing.T) {
	p := AdminPolicy(PolicyOptions{
		TrustedTypePolicies: []string{"default", "dompurify"},
	})
	opts := Options{
		RequireTrustedTypes: []string{"gn-plugin"},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	Middleware(p, opts)(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "require-trusted-types-for 'script'") {
		t.Errorf("missing require-trusted-types-for: %s", got)
	}
	if !strings.Contains(got, "trusted-types default dompurify gn-plugin") {
		t.Errorf("merged policy list wrong: %s", got)
	}
}

// TestMiddleware_RequireTrustedTypesEmptyIsNoop verifies the documented
// no-op shape: empty Options.RequireTrustedTypes leaves the underlying
// Policy directives untouched.
func TestMiddleware_RequireTrustedTypesEmptyIsNoop(t *testing.T) {
	p := AdminPolicy(PolicyOptions{})
	wantUnderlying := p.String()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	Middleware(p, Options{})(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Content-Security-Policy") != wantUnderlying {
		t.Errorf("empty RequireTrustedTypes mutated policy")
	}
}

// TestMiddleware_RequireTrustedTypesWithNonce verifies the per-request
// nonce path still works when Options.RequireTrustedTypes is set.
// Plugin-frontend hardening must NOT break the existing nonce wiring.
func TestMiddleware_RequireTrustedTypesWithNonce(t *testing.T) {
	p := AdminPolicy(PolicyOptions{})
	opts := Options{
		RequireTrustedTypes: []string{"gn-plugin"},
		NonceFromContext:    func(r *http.Request) string { return "NONCEABC" },
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	Middleware(p, opts)(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "'nonce-NONCEABC'") {
		t.Errorf("nonce missing from CSP: %s", got)
	}
	if !strings.Contains(got, "trusted-types") || !strings.Contains(got, "gn-plugin") {
		t.Errorf("trusted-types not folded in: %s", got)
	}
}

// TestTrimQuotes covers the small helper directly so its branches are
// exercised even if higher-level tests change shape later.
func TestTrimQuotes(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"  ":        "",
		"script":    "script",
		"'script'":  "script",
		" 'script' ":"script",
	}
	for in, want := range cases {
		if got := trimQuotes(in); got != want {
			t.Errorf("trimQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMergeUnique_PreservesOrder pins the documented "preserve insertion
// order" contract.
func TestMergeUnique_PreservesOrder(t *testing.T) {
	got := mergeUnique([]string{"a", "b"}, []string{"b", "c", "a", "d"}, strings.TrimSpace)
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("at %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
