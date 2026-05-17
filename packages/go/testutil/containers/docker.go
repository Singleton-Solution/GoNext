package containers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// dockerProbeOnce caches the docker-availability result for the lifetime
// of the test binary. Probing once avoids spending several seconds per
// test on a machine without Docker — the suite spends that time exactly
// once and every test gets a fast skip after that.
var (
	dockerProbeOnce sync.Once
	dockerAvailable bool
)

// skipIfNoDocker calls t.Skip with a clear message when no Docker daemon
// is reachable. Returns true when it skipped (caller should return) so
// the helper short-circuits cleanly without trying to start a container
// that's guaranteed to fail.
//
// The probe uses testcontainers-go's own provider detection so it picks
// up Docker, Podman, or whatever the user has configured via
// DOCKER_HOST / TESTCONTAINERS_HOST_OVERRIDE.
func skipIfNoDocker(t testing.TB) bool {
	t.Helper()
	dockerProbeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		provider, err := testcontainers.NewDockerProvider()
		if err != nil {
			return
		}
		defer func() { _ = provider.Close() }()
		if err := provider.Health(ctx); err != nil {
			return
		}
		dockerAvailable = true
	})
	if !dockerAvailable {
		t.Skip("docker not available")
		return true
	}
	return false
}
