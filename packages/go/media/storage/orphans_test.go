package storage_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Singleton-Solution/GoNext/packages/go/media/storage"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

func setupS3DriverForOrphans(t *testing.T) *storage.S3Driver {
	t.Helper()
	endpoint, accessKey, secretKey := containers.MinIO(t)
	if endpoint == "" {
		t.Skip("docker unavailable")
	}
	bucket := "orphans-" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	if len(bucket) > 60 {
		bucket = bucket[:60]
	}
	cli, _ := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cli.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket: %v", err)
	}
	driver, err := storage.NewS3Driver(storage.S3Config{
		Endpoint:  endpoint,
		Region:    "us-east-1",
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
		UseSSL:    false,
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Driver: %v", err)
	}
	return driver
}

func TestAbortOrphans_NoDriverNoOp(t *testing.T) {
	t.Parallel()
	// LocalDriver does not implement MultipartDriver — sweep should
	// return zero result and no error.
	d, err := storage.NewLocalDriver(storage.LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	res, err := storage.AbortOrphanedMultiparts(context.Background(), d, storage.AbortOrphansOptions{})
	if err != nil {
		t.Fatalf("AbortOrphans: %v", err)
	}
	if res.Scanned != 0 || res.Aborted != 0 || res.Errors != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestAbortOrphans_S3_AbortsOldOnly(t *testing.T) {
	driver := setupS3DriverForOrphans(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create one "old" upload by initiating + sleeping. The default
	// OlderThan is 24h but we override to 0 so anything found counts
	// as old.
	oldUploadID, err := driver.InitMultipart(ctx, "old/file.bin", "application/octet-stream")
	if err != nil {
		t.Fatalf("InitMultipart: %v", err)
	}

	// Sweep. Use Now that is 1h in the future and OlderThan = 30min
	// so the upload (initiated ~now) falls in the "older than" window.
	res, err := storage.AbortOrphanedMultiparts(ctx, driver, storage.AbortOrphansOptions{
		OlderThan: 30 * time.Minute,
		Now:       func() time.Time { return time.Now().Add(1 * time.Hour) },
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("AbortOrphans: %v", err)
	}
	if res.Aborted < 1 {
		t.Fatalf("expected at least 1 aborted, got %+v", res)
	}

	// Re-listing must now omit the aborted upload.
	uploads, err := driver.ListIncompleteUploads(ctx, time.Now().Add(2*time.Hour), 100)
	if err != nil {
		t.Fatalf("ListIncompleteUploads: %v", err)
	}
	for _, u := range uploads {
		if u.UploadID == oldUploadID {
			t.Fatalf("upload %s not aborted", oldUploadID)
		}
	}
}

func TestAbortOrphans_S3_LeavesRecentUploadsAlone(t *testing.T) {
	driver := setupS3DriverForOrphans(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Recent upload — sweep with default 24h threshold must NOT abort.
	uploadID, err := driver.InitMultipart(ctx, "recent/file.bin", "application/octet-stream")
	if err != nil {
		t.Fatalf("InitMultipart: %v", err)
	}
	res, err := storage.AbortOrphanedMultiparts(ctx, driver, storage.AbortOrphansOptions{})
	if err != nil {
		t.Fatalf("AbortOrphans: %v", err)
	}
	if res.Aborted != 0 {
		t.Fatalf("expected 0 aborted (recent upload), got %+v", res)
	}
	// Confirm the upload is still listable.
	uploads, _ := driver.ListIncompleteUploads(ctx, time.Now().Add(1*time.Hour), 100)
	found := false
	for _, u := range uploads {
		if u.UploadID == uploadID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recent upload disappeared")
	}
	// Cleanup.
	_ = driver.AbortMultipart(ctx, "recent/file.bin", uploadID)
}

func TestNewAbortOrphansSpec_RequiresDriver(t *testing.T) {
	t.Parallel()
	if _, err := storage.NewAbortOrphansSpec(storage.AbortOrphansSpecOptions{}); err == nil {
		t.Fatalf("expected error for nil driver")
	}
}

// TestAbortOrphans_S3_BodyDoesNotInterfere keeps an unrelated PutObject
// in place during the sweep and verifies the object survives. The
// orphan-sweep only touches multipart machinery; completed objects
// must not be collateral damage.
func TestAbortOrphans_S3_BodyDoesNotInterfere(t *testing.T) {
	driver := setupS3DriverForOrphans(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Put a regular object.
	body := bytes.Repeat([]byte("x"), 32)
	if _, err := driver.Put(ctx, "intact/object.bin", bytes.NewReader(body), "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Initiate a multipart upload that becomes orphan-eligible.
	if _, err := driver.InitMultipart(ctx, "orphan/file.bin", "application/octet-stream"); err != nil {
		t.Fatalf("InitMultipart: %v", err)
	}
	// Sweep with future "now".
	if _, err := storage.AbortOrphanedMultiparts(ctx, driver, storage.AbortOrphansOptions{
		OlderThan: 1 * time.Minute,
		Now:       func() time.Time { return time.Now().Add(1 * time.Hour) },
	}); err != nil {
		t.Fatalf("AbortOrphans: %v", err)
	}
	// Object still readable.
	rc, err := driver.Get(ctx, "intact/object.bin")
	if err != nil {
		t.Fatalf("Get after sweep: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch after sweep")
	}
}

// keep net/http linked for future presign assertions; pinning so
// future contributors can extend this file without dancing imports.
var _ = http.MethodGet
