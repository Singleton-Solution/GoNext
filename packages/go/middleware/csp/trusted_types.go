// Package csp — Trusted Types enforcement helpers.
//
// Plugin-contributed frontend JavaScript in the admin is a high-value XSS
// vector: a malicious manifest could ship a script that does
// `someEl.innerHTML = attackerControlled` and bypass any per-request
// sanitization. Trusted Types is the browser-side mitigation — once
// `require-trusted-types-for 'script'` is in force, calls to the DOM XSS
// sinks (`innerHTML`, `outerHTML`, `document.write`, `eval`, …) THROW
// unless the assigned value was produced by a named, allowlisted policy.
//
// This file adds an ergonomic Go-side surface for declaring those policy
// names and folding them into the existing `Policy` so the rest of the
// CSP builder does not need to grow new directive-emit logic.
//
// Background reading:
//   - W3C Trusted Types: https://w3c.github.io/trusted-types/dist/spec/
//   - CSP3 §11.3 (require-trusted-types-for):
//     https://www.w3.org/TR/CSP3/#directive-require-trusted-types-for
//   - GoNext docs/13-security-baseline.md §3.2 (admin CSP)
//
// The package-level `Policy` struct already carries the `RequireTrustedTypesFor`
// and `TrustedTypes` fields and emits the spec-shaped directives. This file
// adds three things on top:
//
//  1. `TrustedTypesOptions` — a tiny option bag for declarative wiring.
//  2. `Policy.WithTrustedTypes` — a non-mutating helper that returns a
//     CLONE of the receiver with the policy names merged in. Mirrors the
//     `WithNonce` ergonomics so it composes naturally inside the
//     `Middleware` hot path.
//  3. `Options.RequireTrustedTypes` — a `Middleware`-level shortcut for
//     callers who want to enable Trusted Types without hand-building a
//     `Policy`. When set, the middleware merges the names into every
//     emitted CSP header.
//
// Wiring example:
//
//	policy := csp.AdminPolicy(csp.PolicyOptions{
//	    TrustedTypePolicies: []string{"default", "nextjs#bundler", "dompurify"},
//	})
//	csp.Middleware(policy, csp.Options{
//	    RequireTrustedTypes: []string{"gn-plugin"}, // appended per request
//	})
//
// produces a header containing:
//
//	require-trusted-types-for 'script'; trusted-types default nextjs#bundler dompurify gn-plugin
package csp

import "strings"

// TrustedTypesOptions describes a Trusted Types enforcement shape. The
// zero value disables Trusted Types entirely; a value with `Sinks` set
// to []string{"script"} matches the only sink the spec currently defines.
//
// TrustedTypesOptions is intentionally value-typed and immutable so it
// can be shared across goroutines without locking.
type TrustedTypesOptions struct {
	// Sinks lists the abstract sink names enforced by
	// `require-trusted-types-for`. The only spec-defined value is
	// "script"; future versions of the spec may add "style". An empty
	// slice DISABLES the directive (the most permissive shape).
	Sinks []string

	// Policies lists the Trusted Types policy names that documents may
	// instantiate. The token "default" refers to the default policy;
	// "'allow-duplicates'" permits multiple creations of a policy with
	// the same name (useful in dev). All other tokens are policy names
	// (e.g. "gn-plugin", "nextjs#bundler", "dompurify").
	//
	// Policy names are emitted verbatim — callers are responsible for
	// passing CSP-shaped tokens (no surrounding quotes for unquoted
	// names).
	Policies []string
}

// IsEnabled reports whether the options carry any enforcement directive.
// Callers can use this to early-out before invoking `Apply`.
func (o TrustedTypesOptions) IsEnabled() bool {
	return len(o.Sinks) > 0 || len(o.Policies) > 0
}

// Apply merges the receiver into p (in place). p must be non-nil; pass a
// fresh `Clone()` if the caller wants immutability.
//
// Merging semantics:
//   - Sinks are de-duplicated against p.RequireTrustedTypesFor (case-sensitive,
//     after trimming surrounding quotes so 'script' and "script" collapse).
//   - Policies are appended in declared order, de-duplicated against
//     p.TrustedTypes.
//
// Apply does NOT validate token shapes; CSP is forgiving about extra
// tokens and the spec's behavior for malformed policy names is "ignore
// the unknown token", which matches what we'd want here anyway.
func (o TrustedTypesOptions) Apply(p *Policy) {
	if p == nil {
		return
	}
	if len(o.Sinks) > 0 {
		p.RequireTrustedTypesFor = mergeUnique(p.RequireTrustedTypesFor, o.Sinks, trimQuotes)
	}
	if len(o.Policies) > 0 {
		p.TrustedTypes = mergeUnique(p.TrustedTypes, o.Policies, strings.TrimSpace)
	}
}

// WithTrustedTypes returns a CLONE of p with the given options merged in.
// The receiver is not modified. Mirrors `Policy.WithNonce` so call sites
// can compose policies in a single expression without ever holding a
// mutable reference.
//
// If the options are disabled (IsEnabled() == false) the receiver is
// cloned unchanged, matching the WithNonce("") no-op behavior.
func (p *Policy) WithTrustedTypes(opts TrustedTypesOptions) *Policy {
	if p == nil {
		c := &Policy{}
		opts.Apply(c)
		return c
	}
	c := p.Clone()
	opts.Apply(c)
	return c
}

// mergeUnique appends `next` onto `prior`, skipping entries whose
// `normalize`d form already appears in `prior`. The output preserves
// insertion order; the returned slice never aliases `prior`.
//
// Empty / whitespace-only entries are skipped (the spec ignores them and
// the directive serializer would drop them anyway).
func mergeUnique(prior, next []string, normalize func(string) string) []string {
	seen := make(map[string]struct{}, len(prior)+len(next))
	out := make([]string, 0, len(prior)+len(next))
	for _, v := range prior {
		k := normalize(v)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	for _, v := range next {
		k := normalize(v)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	return out
}

// trimQuotes normalizes a CSP token by stripping surrounding single
// quotes and ASCII whitespace. Used by `mergeUnique` so e.g. `script`
// and `'script'` are treated as the same sink name.
func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "'")
	return strings.TrimSpace(s)
}
