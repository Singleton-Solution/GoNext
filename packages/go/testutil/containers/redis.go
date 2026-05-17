package containers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Redis starts a Redis container and returns a redis:// URL ready for
// go-redis, redigo, or anything else that speaks the standard Redis URL
// format. The container is terminated by t.Cleanup at end of test.
//
// Default image is redis:7-alpine. Override via WithVersion or WithImage.
// WithDB is ignored — Redis doesn't have named databases the way Postgres
// does (it has numbered databases, which clients select at connect time
// via the URL path).
func Redis(t testing.TB, opts ...RedisOption) (url string) {
	t.Helper()
	if skipIfNoDocker(t) {
		return ""
	}

	cfg := apply(config{
		version: "7-alpine",
	}, opts)

	image := cfg.image
	if image == "" {
		image = "redis:" + cfg.version
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Redis logs "Ready to accept connections" once the server is fully
	// up. The module's default wait strategy also pings — we set our
	// own timeout to keep behaviour deterministic in slow CI runners.
	container, err := tcredis.Run(ctx, image,
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("containers.Redis: start %q: %v", image, err)
	}

	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("containers.Redis: terminate: %v", err)
		}
	})

	url, err = container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("containers.Redis: connection string: %v", fmt.Errorf("redis url: %w", err))
	}
	return url
}
