package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// pingTimeout is the budget for the boot-time Ping. It's deliberately
// short — the connection itself dials with cfg.DialTimeout under the hood,
// but the application of statement_timeout + a SELECT 1 should be sub-second
// on a healthy DB. Anything slower means something is wrong (network, DNS,
// auth) and we want to find out fast.
const pingTimeout = 5 * time.Second

// New builds a pgxpool.Pool from the supplied config.
//
// The returned pool is ready to use: it has been Ping'd, statement_timeout
// is set on every checked-out connection, and the pool size limits are
// applied. Caller is responsible for calling Close() at shutdown.
//
// ctx controls only the initial dial + Ping. Once New returns, the pool's
// own context governs ongoing connections.
//
// logger is used for connection lifecycle events (afterConnect, acquire
// failures). Nil is tolerated — falls back to slog.Default.
func New(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("db.New: DATABASE_URL is required (got empty)")
	}
	if logger == nil {
		logger = slog.Default()
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Don't include cfg.URL in the error — DSN contains password.
		return nil, fmt.Errorf("db.New: parse DATABASE_URL: %w", err)
	}

	// Pool sizing. Defaults from config are reasonable for a single API
	// replica @ ~100 RPS; tune via env vars per docs/09-deployment-ops.md §17.
	if cfg.MaxOpenConns > 0 {
		poolCfg.MaxConns = int32(cfg.MaxOpenConns) //nolint:gosec // sane bound enforced by config validation
	}
	if cfg.MaxIdleConns > 0 {
		poolCfg.MinConns = int32(cfg.MaxIdleConns) //nolint:gosec // sane bound enforced by config validation
	}
	if cfg.ConnMaxLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.ConnMaxLifetime
	}
	if cfg.ConnMaxIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.ConnMaxIdleTime
	}

	// Periodic health-check so dead connections are recycled before they
	// land a user request. pgx defaults to 1 minute; we leave it.

	// Per-connection statement_timeout. This is the single most important
	// production safeguard in the pool: without it, a runaway query
	// wedges a slot indefinitely. Applied as an AfterConnect hook so it
	// runs once per physical connection, not per checkout.
	if cfg.StatementTimeout > 0 {
		stmt := fmt.Sprintf("SET statement_timeout = %d", cfg.StatementTimeout.Milliseconds())
		poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("set statement_timeout: %w", err)
			}
			return nil
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db.New: open pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		// Surface the host/port so operators can verify connectivity
		// without us logging the password. pgxpool.Config has a Host()/Port()
		// equivalent in ConnConfig.
		host, port := dsnHostPort(poolCfg)
		return nil, fmt.Errorf("db.New: ping %s:%d: %w", host, port, err)
	}

	logger.Info("database pool ready",
		slog.Int("max_conns", int(poolCfg.MaxConns)),
		slog.Int("min_conns", int(poolCfg.MinConns)),
		slog.Duration("max_lifetime", poolCfg.MaxConnLifetime),
		slog.Duration("statement_timeout", cfg.StatementTimeout),
	)
	return pool, nil
}

// dsnHostPort extracts host:port from a parsed pgxpool config for error
// messages. Returns ("?", 0) on unexpected nil — defensive.
func dsnHostPort(cfg *pgxpool.Config) (string, uint16) {
	if cfg == nil || cfg.ConnConfig == nil {
		return "?", 0
	}
	return cfg.ConnConfig.Host, cfg.ConnConfig.Port
}
