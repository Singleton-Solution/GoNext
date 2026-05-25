// Package safehttp provides a hardened outbound HTTP client that
// defends against SSRF (Server-Side Request Forgery) and other
// outbound-network risks common to multi-tenant servers that issue
// HTTP calls on behalf of users or plugins.
//
// Why this package exists
//
// Any code path that takes a user- or plugin-supplied URL and issues
// an HTTP request against it is a potential SSRF vector: an attacker
// can target the host's private network (cloud-metadata services,
// internal admin endpoints, databases bound to localhost) by pointing
// the URL at a private IP — or, more sneakily, at a hostname that
// resolves to a private IP. This package centralizes the defenses so
// every outbound caller in the GoNext codebase gets the same
// protections without re-implementing them.
//
// Three protections, layered
//
//  1. Domain allowlist — the caller declares the exact set of hosts
//     that are reachable. The client refuses any URL whose hostname is
//     not on that list. This is the strongest filter because it admits
//     only known-good destinations rather than trying to enumerate
//     bad ones.
//
//  2. SSRF guard — every resolved IP is checked against a denylist of
//     non-public address space (RFC1918, loopback, link-local,
//     multicast, unspecified, IPv6 ULA, CGNAT). The check runs on the
//     initial URL AND on every redirect target so a DNS-rebinding
//     attack — where the hostname returns a public IP at first lookup
//     and a private IP a moment later — can't slip through.
//
//  3. Resource caps — a 30s request timeout, a 3-hop redirect cap, and
//     a 10 MiB response body cap mean an upstream cannot consume
//     unbounded host resources via slowloris-style attacks, redirect
//     loops, or response-body bombs.
//
// Surface
//
// The package's main type is Client, constructed via New(opts...). The
// options pattern composes:
//
//	c, err := safehttp.New(
//	    safehttp.WithAllowlist("api.example.com", "hooks.example.com"),
//	    safehttp.WithTimeout(15 * time.Second),
//	    safehttp.WithMaxResponseBytes(2 * 1024 * 1024),
//	)
//	if err != nil { ... }
//	resp, err := c.Do(ctx, req)
//
// The returned *Client wraps net/http.Client; callers issue requests
// with c.Do(req), c.Get(url), c.Post(url, ...). Response.Body is a
// LimitReader bounded by the configured MaxResponseBytes — readers
// that need to detect "we hit the cap" should compare bytes read to
// the cap, since LimitReader returns io.EOF silently at the boundary.
//
// Test seam
//
// SSRF resolution defaults to net.DefaultResolver but can be replaced
// via WithResolver — tests use this to mock a host whose name resolves
// to 127.0.0.1 (which would otherwise be blocked by the SSRF guard).
// Similarly, WithDialContext lets tests pin connections to an
// httptest.Server. The package's _test.go files exercise both seams.
//
// What this package does NOT do
//
//   - It does not authenticate or sign outbound requests. Callers are
//     responsible for Authorization / X-Signature headers etc.
//   - It does not retry transient failures. Callers (e.g.
//     webhooks/delivery) own retry policy.
//   - It does not cache DNS. Every fresh request re-resolves and
//     re-validates — that is intentional, because a cached lookup
//     undermines the SSRF guard for long-lived clients.
package safehttp
