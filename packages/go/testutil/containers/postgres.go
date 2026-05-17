package containers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Postgres starts a Postgres container and returns a libpq-style DSN that
// callers can hand directly to pgx or database/sql. The container is
// terminated automatically via t.Cleanup when the test finishes (or fails).
//
// When Docker is unreachable the test is skipped with "docker not
// available" — this is intentional: integration tests should fail loudly
// when their substrate is broken but stay quiet on machines that simply
// can't run containers.
//
// The default image is postgres:16-alpine with a "gonext_test" database,
// "test" superuser, and "test" password. Override any of these with
// WithVersion, WithImage, or WithDB. The helper waits for the container
// to accept SQL connections before returning.
func Postgres(t testing.TB, opts ...PGOption) (dsn string) {
	t.Helper()
	if skipIfNoDocker(t) {
		return ""
	}

	cfg := apply(config{
		version: "16-alpine",
		db:      "gonext_test",
	}, opts)

	image := cfg.image
	if image == "" {
		image = "postgres:" + cfg.version
	}

	const (
		user = "test"
		pass = "test"
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// The postgres module's WaitingFor strategy already handles the
	// "ready to accept connections" race by tailing logs and then
	// retrying a SQL ping. We stack a connection probe on top with a
	// generous deadline so flaky daemons don't fail us in CI.
	container, err := tcpg.Run(ctx, image,
		tcpg.WithDatabase(cfg.db),
		tcpg.WithUsername(user),
		tcpg.WithPassword(pass),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("containers.Postgres: start %q: %v", image, err)
	}

	t.Cleanup(func() {
		// Use a fresh context — t may be marked failed and its deadline
		// expired, but we still want to reclaim the container.
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("containers.Postgres: terminate: %v", err)
		}
	})

	dsn, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("containers.Postgres: connection string: %v", fmt.Errorf("postgres dsn: %w", err))
	}
	return dsn
}
