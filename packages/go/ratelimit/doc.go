// Package ratelimit provides token-bucket rate limiting primitives and
// higher-level helpers for authentication brute-force mitigation.
//
// What's here:
//
//   - Limiter: the core interface. One method, Allow, returning whether
//     the request should proceed, the time to wait before retrying when
//     denied, and any backend error.
//
//   - MemoryLimiter: a sync.Map-backed token bucket suitable for single-
//     instance and dev workloads. Lock-free fast path, no external deps.
//
//   - RedisLimiter: an INCR + TTL token-bucket implementation that runs
//     atomically on the Redis server. Use this in multi-instance
//     production where counters must be shared across replicas.
//
//   - LoginAttemptLimiter: a two-bucket helper for login flows that
//     enforces per-IP and per-email burst limits AND tracks failed
//     attempts to lock an account after N consecutive failures.
//
//     Two oracles are explicitly defended against, per issue #195:
//
//     1. Lockout state is intentionally not surfaced on wrong-password
//     responses — the IsLocked query is callable only AFTER a
//     successful password match. See LoginAttemptLimiter docstring.
//
//     2. The per-email bucket is applied ONLY when the caller has
//     confirmed the email exists in their user store
//     (CheckInput.EmailExists = true). Applying it unconditionally
//     would let an attacker probe email existence by watching for the
//     429 response that only known emails could trigger.
//
//   - FailureStore: the interface backing the lockout counter. Two
//     implementations ship: MemoryFailureStore (tests + single-instance
//     dev) and PostgresFailureStore (production, writes to
//     users.failed_login_count and users.locked_until). The latter is
//     what makes lockouts survive process restarts and a multi-replica
//     fleet — without it, an attacker simply waits out a restart or
//     retries on another replica.
//
//   - AuditEmitter: the interface LoginAttemptLimiter uses to log the
//     three AC-required events (auth.login.locked, auth.login.unlocked,
//     auth.ratelimit.exceeded). NopAuditEmitter is the zero-cost
//     default; production callers should plug in packages/go/audit's
//     emitter — that is the intended consumer of these events.
//
//   - IPLimiter: a per-IP general API rate limit helper.
//
//   - Middleware: a stdlib-shaped HTTP middleware that consults a Limiter
//     keyed by a caller-supplied function. On denial it returns 429 with
//     a Retry-After header populated from the limiter's hint.
//
// Design references:
//
//   - docs/06-auth-permissions.md §12 (Brute Force & Abuse Mitigation)
//   - docs/13-security-baseline.md §11 (Rate limiting and abuse)
//   - Issue #195 (the AC that the LoginAttemptLimiter implements)
//
// Typical wiring in cmd/server/main.go:
//
//	rdb := redis.NewClient(opts)
//	ipLimiter, _ := ratelimit.NewRedisLimiter(rdb, ratelimit.Policy{
//	    Capacity:   100,
//	    RefillRate: 60.0 / 60.0,
//	    Prefix:     "api:unauth:ip",
//	})
//	mux := http.NewServeMux()
//	handler := ratelimit.Middleware(ipLimiter, ratelimit.KeyByIP)(mux)
//
// Login wiring (the hardened path; both oracles closed):
//
//	store, _ := ratelimit.NewPostgresFailureStore(db)
//	audit := audit.NewEmitter(...) // implements ratelimit.AuditEmitter
//	lal, _ := ratelimit.NewLoginAttemptLimiter(ratelimit.LoginAttemptOptions{
//	    IPLimiter:    ipLoginLimiter,    // 20/5min
//	    EmailLimiter: emailLoginLimiter, // 5/15min
//	    FailureStore: store,
//	    Audit:        audit,
//	})
//
//	// In the login handler:
//	user, err := db.UserByEmail(ctx, req.Email)
//	emailExists := err == nil && user != nil
//	res, err := lal.Check(ctx, ratelimit.CheckInput{
//	    IP: clientIP, Email: req.Email, EmailExists: emailExists,
//	})
//	if !res.Allowed {
//	    writeRateLimited(w, res.RetryAfter); return
//	}
//	if !emailExists || !user.PasswordMatches(req.Password) {
//	    if emailExists {
//	        _, _ = lal.RecordFailure(ctx, user.ID)
//	    }
//	    writeGenericAuthError(w); return
//	}
//	locked, _, _ := lal.IsLocked(ctx, user.ID)
//	if locked { writeAccountLocked(w); return }
//	_ = lal.RecordSuccess(ctx, user.ID)
//	issueSession(w, user)
//
// The Limiter interface is intentionally minimal; consumers compose
// multiple limiters (per-IP + per-user, per-route + per-tenant) by
// calling Allow in sequence, first to deny wins.
package ratelimit
