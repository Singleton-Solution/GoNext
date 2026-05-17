// Package audit is the GoNext audit-log emit plumbing.
//
// Every privileged action in the platform — auth.login.*, plugin.activated,
// settings.updated, policy.denied, etc. — should produce one append-only
// audit_log row that captures who acted, on what, from where, when, and
// (optionally) why. See docs/06-auth-permissions.md §13 for the full data
// model and the catalog of events.
//
// What's here:
//
//   - Event is the value-typed audit row. The shape is locked even though
//     the SQL migration that creates the audit_log table ships separately;
//     the Postgres store writes via INSERT against the documented column
//     list so the contract is fixed.
//
//   - Store is the persistence interface (Emit + List + filter). Two
//     implementations: MemoryStore for tests and PostgresStore for
//     production. The Postgres backend accepts a *pgxpool.Pool — bring
//     your own pool, no internal connection management.
//
//   - Emitter is the ergonomic helper. Handlers don't want to thread
//     actor / IP / plugin into every call site, so Emitter captures
//     them once (typically per request, in middleware) and offers a
//     short Emit(ctx, eventType, opts...) call.
//
//   - Middleware (httpx-compatible) auto-emits an http.request audit row
//     for state-changing methods (POST/PUT/PATCH/DELETE). Safe methods
//     (GET/HEAD/OPTIONS) are skipped — they shouldn't be mutating state.
//
// Tamper-evidence: an opaque PrevHash field is reserved on Event so a
// follow-up issue can land an HMAC-chain implementation without a schema
// change. v1 leaves PrevHash nil and relies on the SIEM-export path
// documented in docs/06-auth-permissions.md §13.3.
//
// Trusted proxies and X-Forwarded-For: by default the Emitter trusts
// no proxies and ignores X-Forwarded-For entirely — clientIP comes
// from r.RemoteAddr. This prevents an attacker from spoofing their
// source IP in the audit log by setting the XFF header. If the server
// runs behind a known set of reverse proxies, install their CIDR
// ranges via emitter.WithTrustedProxies(); XFF is then consulted only
// when the immediate peer is a trusted proxy, and the walk is
// rightmost-to-leftmost, stopping at the first address NOT in the
// trusted set (the standard "trust chain" pattern).
//
// Typical wiring in an HTTP handler:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	store := audit.NewPostgresStore(pool)
//	emitter := audit.NewEmitter(store).WithTrustedProxies([]netip.Prefix{
//	    netip.MustParsePrefix("10.0.0.0/8"), // internal LB range
//	})
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("POST /api/v1/posts", createPost)
//
//	root := audit.Middleware(emitter, audit.WithEmitFailureRecorder(metrics))(mux)
//
// Inside a handler, attach the per-request actor/IP and emit a custom event:
//
//	e := audit.WithRequest(emitter, r, currentUserID)
//	e.Emit(ctx, "post.published",
//	    audit.WithTarget("post", postID),
//	    audit.WithSeverity(audit.SeverityInfo),
//	    audit.WithMetadata(map[string]any{"slug": slug}),
//	)
//
// See docs/06-auth-permissions.md §13 (data model) and §13.1 (event catalog).
package audit
