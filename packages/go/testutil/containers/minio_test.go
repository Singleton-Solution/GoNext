package containers_test

import (
	"context"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// TestMinIO_AcceptsConnections starts a real MinIO container, creates a
// bucket, and lists buckets to confirm both control-plane and data-plane
// paths work. If Docker isn't available the helper skips.
func TestMinIO_AcceptsConnections(t *testing.T) {
	endpoint, accessKey, secretKey := containers.MinIO(t)
	if endpoint == "" {
		return
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("minio.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const bucket = "containers-test"
	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket(%q): %v", bucket, err)
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("BucketExists: %v", err)
	}
	if !exists {
		t.Fatalf("BucketExists(%q): false, want true", bucket)
	}
}
