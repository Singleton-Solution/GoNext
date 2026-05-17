// Package csp provides a typed Content-Security-Policy (CSP) builder,
// the HTTP middleware that emits the policy as a response header, and the
// `/_/csp-report` violation report endpoint described in
// docs/13-security-baseline.md §3.
//
// The package is intentionally split into three concerns:
//
//   - Policy — a strongly-typed record of CSP directives. SourceExpr values
//     (Self(), Nonce("..."), Host("https://example.com"), …) describe each
//     directive's source list without stringly-typed coupling. Policy.String
//     serializes the record to a valid CSP header value.
//
//   - Middleware — wraps an http.Handler and emits Content-Security-Policy
//     (or Content-Security-Policy-Report-Only) on every response. The
//     middleware reads the per-request nonce from r.Context (placed there by
//     security.WithNonce) and folds it into script-src/style-src before
//     serialization. Callers MUST wire security.WithNonce earlier in the
//     middleware chain — see the Middleware godoc for the canonical ordering.
//
//   - ReportHandler — an http.Handler that accepts browser-emitted CSP
//     violation reports at /_/csp-report. Both the legacy
//     application/csp-report body shape and the modern Reporting API
//     (application/reports+json) are decoded. Violations are emitted at WARN
//     via slog with structured fields, counted via an injected Counter, and
//     rate-limited by client IP (100 reports/min by default per
//     docs/13-security-baseline.md §11).
//
// Presets:
//
//   - PublicSitePolicy() — strict policy for theme-rendered HTML; script-src
//     uses 'self' + per-request nonce + 'strict-dynamic'.
//
//   - AdminPolicy() — stricter; no 'strict-dynamic', frame-ancestors 'none',
//     and require-trusted-types-for 'script' for the admin/block editor.
//
// Both presets accept a PolicyOptions struct so callers can supply extra
// media hosts, oEmbed providers, report endpoints, etc., without
// hand-building the Policy.
//
// Wiring example (Go HTTP server):
//
//	mux := http.NewServeMux()
//	mux.Handle("/_/csp-report", csp.ReportHandler(csp.ReportConfig{
//	    Logger:  logger,
//	    Counter: counter,
//	}))
//	mux.Handle("/", pageHandler)
//
//	policy := csp.PublicSitePolicy(csp.PolicyOptions{
//	    Site:         "https://example.com",
//	    MediaHosts:   []string{"https://media.example.com"},
//	    OEmbedHosts:  []string{"https://www.youtube.com", "https://player.vimeo.com"},
//	    ReportURI:    "/_/csp-report",
//	    ReportTo:     "default",
//	})
//
//	handler := security.WithNonce()(
//	    csp.Middleware(policy, csp.Options{})(
//	        security.Headers(security.PublicSite())(mux),
//	    ),
//	)
//
// See docs/13-security-baseline.md §3 for the canonical policies that the
// presets implement and §11 for the rate-limit budgets the report endpoint
// honors.
package csp
