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
//     attempts to lock an account after N consecutive failures. Lockout
//     state is intentionally not surfaced on wrong-password responses
//     (oracle avoidance, per docs/06-auth-permissions.md §12.2).
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
//
// Typical wiring in cmd/server/main.go:
//
//	rdb := redis.NewClient(opts)
//	ipLimiter, _ := ratelimit.NewRedisLimiter(rdb, ratelimit.Policy{
//	    Capacity:   100,
//	    RefillRate: 60.0 / 60.0, // 60 tokens per minute
//	    Prefix:     "api:unauth:ip",
//	})
//	mux := http.NewServeMux()
//	handler := ratelimit.Middleware(ipLimiter, ratelimit.KeyByIP)(mux)
//
// The Limiter interface is intentionally minimal; consumers compose
// multiple limiters (per-IP + per-user, per-route + per-tenant) by
// calling Allow in sequence, first to deny wins.
package ratelimit
