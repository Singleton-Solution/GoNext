// Package redis is a thin wrapper around github.com/redis/go-redis/v9
// for GoNext binaries that need a vanilla Redis client.
//
// It mirrors the shape of packages/go/db.New: read the typed
// config.RedisConfig, dial, ping with a short boot-time budget, and
// return a ready *redis.Client. Caller is responsible for calling
// Close() at shutdown.
//
// The wrapper exists for code paths that just need a connected,
// verified client — healthz, future cron jobs, ad-hoc operational
// scripts. Higher-level packages with their own Redis access patterns
// (packages/go/session, packages/go/ratelimit) import
// github.com/redis/go-redis/v9 directly and keep their own clients;
// this wrapper is NOT meant to replace those.
//
// Two design constraints worth calling out:
//
//   - Boot-time Ping with a 5s budget. Failing fast at startup is
//     preferable to failing the first user request. The budget is
//     deliberately short — once Redis is reachable, a PING should be
//     sub-millisecond.
//
//   - Error messages never contain the DSN. Redis URLs commonly carry
//     a password (redis://:pass@host:port), and operators paste error
//     messages into chat all the time. Surface host:port for
//     debuggability without leaking secrets.
//
// Typical wiring in cmd/server/main.go:
//
//	rdb, err := redisclient.New(ctx, cfg.Redis, logger)
//	if err != nil {
//	    return fmt.Errorf("redis: %w", err)
//	}
//	defer rdb.Close()
//
// See packages/go/db/doc.go for the sister pattern on Postgres.
package redis
