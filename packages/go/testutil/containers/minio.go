package containers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MinIO starts a MinIO container (S3-compatible object storage) and
// returns the endpoint plus root access credentials. The endpoint is a
// "host:port" string suitable for passing to the AWS SDK as a custom
// endpoint, or to the minio-go client directly.
//
// The container is terminated by t.Cleanup at end of test.
//
// Default image is the upstream minio/minio "latest" tag pinned via the
// testcontainers module — override with WithVersion or WithImage when
// reproducibility matters more than tracking upstream. Default root
// credentials are "minioadmin"/"minioadmin"; tests that need different
// credentials can re-set them at the bucket level after connecting.
//
// Returned values are positional rather than a struct because every
// caller wires all three into a client constructor — a struct would
// just add a layer of dereferencing for no gain.
func MinIO(t testing.TB, opts ...MinIOOption) (endpoint, accessKey, secretKey string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skip under -short (covered by nightly-full-tests workflow)")
	}
	if skipIfNoDocker(t) {
		return "", "", ""
	}

	cfg := apply(config{
		// Pinning a recent stable MinIO RELEASE tag — MinIO releases
		// rapidly and "latest" drifts. The "RELEASE.<date>" form is
		// MinIO's official versioning scheme.
		version: "RELEASE.2024-10-13T13-34-11Z",
	}, opts)

	image := cfg.image
	if image == "" {
		image = "minio/minio:" + cfg.version
	}

	const (
		rootUser = "minioadmin"
		rootPass = "minioadmin"
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// MinIO logs an "API:" line containing the listening address once
	// it's ready to accept S3 traffic. Wait for that plus an HTTP probe
	// against the readiness endpoint so we don't race the first call.
	container, err := tcminio.Run(ctx, image,
		tcminio.WithUsername(rootUser),
		tcminio.WithPassword(rootPass),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/minio/health/live").
				WithPort("9000/tcp").
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("containers.MinIO: start %q: %v", image, err)
	}

	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := container.Terminate(termCtx); err != nil {
			t.Logf("containers.MinIO: terminate: %v", err)
		}
	})

	endpoint, err = container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("containers.MinIO: connection string: %v", fmt.Errorf("minio endpoint: %w", err))
	}
	return endpoint, rootUser, rootPass
}
