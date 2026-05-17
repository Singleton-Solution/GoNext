package redis

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// pingTimeout is the budget for the boot-time Ping. Once Redis is
// reachable, a PING round-trip should be sub-millisecond; we leave
// 5s of headroom for cold connections, DNS, and TLS handshakes.
// Anything slower means something is wrong (network, auth, wrong host)
// and we want to find out fast rather than discover it on the first
// user request.
const pingTimeout = 5 * time.Second

// New builds a redis.Client from the supplied config.
//
// The returned client is ready to use: the URL has been parsed, the
// pool sizing applied, and a Ping has succeeded against the server.
// Caller is responsible for calling Close() at shutdown.
//
// ctx controls only the initial dial + Ping. Once New returns, the
// client manages its own connection lifecycle.
//
// logger is used for connection lifecycle events. Nil is tolerated —
// falls back to slog.Default.
func New(ctx context.Context, cfg config.RedisConfig, logger *slog.Logger) (*redis.Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("redis.New: REDIS_URL is required (got empty)")
	}
	if logger == nil {
		logger = slog.Default()
	}

	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		// Don't include cfg.URL in the error — DSN can contain password.
		return nil, fmt.Errorf("redis.New: parse REDIS_URL: %w", err)
	}

	// Apply pool/timeout overrides from config. go-redis has its own
	// defaults; we only stamp values when the operator explicitly
	// configured one.
	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}
	if cfg.MinIdleConns > 0 {
		opts.MinIdleConns = cfg.MinIdleConns
	}
	if cfg.DialTimeout > 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		opts.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		opts.WriteTimeout = cfg.WriteTimeout
	}

	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		// Surface the host/port so operators can verify connectivity
		// without us echoing the password back. opts.Addr is host:port.
		host, port := splitHostPort(opts.Addr)
		return nil, fmt.Errorf("redis.New: ping %s:%s: %w", host, port, err)
	}

	logger.Info("redis client ready",
		slog.Int("pool_size", opts.PoolSize),
		slog.Int("min_idle_conns", opts.MinIdleConns),
		slog.Duration("dial_timeout", opts.DialTimeout),
		slog.Duration("read_timeout", opts.ReadTimeout),
		slog.Duration("write_timeout", opts.WriteTimeout),
		slog.Int("db", opts.DB),
	)
	return client, nil
}

// splitHostPort extracts a (host, port) pair from a host:port string
// for use in error messages. Falls back to ("?", "?") on parse
// failure — we'd rather log something than nothing, and we don't want
// to bubble an extra error from an already-failing code path.
func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "?", "?"
	}
	return host, port
}
