package storage_test

import (
	"bytes"
	"context"
	"errors"
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

// setupS3 starts a MinIO container, creates a fresh bucket, and
// returns a configured S3Driver plus the bucket name. Skips when
// Docker is unavailable (the testutil helper already handles that).
func setupS3(t *testing.T) (*storage.S3Driver, string) {
	t.Helper()
	endpoint, accessKey, secretKey := containers.MinIO(t)
	if endpoint == "" {
		t.Skip("docker unavailable")
	}
	bucket := "gonext-test-" + strings.ToLower(t.Name())
	// Bucket names cannot contain "/" or "_"; replace anything bad.
	bucket = strings.NewReplacer("/", "-", "_", "-").Replace(bucket)
	if len(bucket) > 60 {
		bucket = bucket[:60]
	}

	// Create the bucket via a one-shot client; the driver itself does
	// not expose bucket creation.
	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("minio.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cli.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket: %v", err)
	}

	d, err := storage.NewS3Driver(storage.S3Config{
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
	return d, bucket
}

func TestS3Driver_PutGetStatDelete(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body := []byte("hello s3")
	n, err := d.Put(ctx, "k/test.txt", bytes.NewReader(body), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n <= 0 {
		t.Fatalf("Put returned %d bytes", n)
	}
	rc, err := d.Get(ctx, "k/test.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q, want %q", got, body)
	}
	info, err := d.Stat(ctx, "k/test.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(body)) {
		t.Fatalf("Stat size %d, want %d", info.Size, len(body))
	}
	if err := d.Delete(ctx, "k/test.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := d.Stat(ctx, "k/test.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat after delete: %v, want ErrNotFound", err)
	}
}

func TestS3Driver_PresignPutAndGet(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := d.Presign(ctx, "presigned.txt", storage.PresignPut, 5*time.Minute, "text/plain")
	if err != nil {
		t.Fatalf("Presign put: %v", err)
	}
	if req.URL == "" || time.Until(req.ExpiresAt) <= 0 {
		t.Fatalf("bad presigned request: %+v", req)
	}
	// Perform the PUT.
	httpReq, _ := http.NewRequest(http.MethodPut, req.URL, strings.NewReader("hello"))
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT status %d", resp.StatusCode)
	}
	// Confirm object lands.
	if _, err := d.Stat(ctx, "presigned.txt"); err != nil {
		t.Fatalf("Stat: %v", err)
	}

	// Presigned GET.
	gReq, err := d.Presign(ctx, "presigned.txt", storage.PresignGet, time.Minute, "")
	if err != nil {
		t.Fatalf("Presign get: %v", err)
	}
	getResp, err := http.Get(gReq.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	got, _ := io.ReadAll(getResp.Body)
	if string(got) != "hello" {
		t.Fatalf("body got %q", got)
	}
}

func TestS3Driver_MultipartLifecycle(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	key := "multi/test.bin"
	uploadID, err := d.InitMultipart(ctx, key, "application/octet-stream")
	if err != nil {
		t.Fatalf("InitMultipart: %v", err)
	}
	if uploadID == "" {
		t.Fatalf("empty uploadID")
	}

	// Each part must be at least 5 MiB except the last. Smallest legal
	// two-part upload is 5 MiB + 1 byte.
	const partSize = 5 * 1024 * 1024
	part1 := bytes.Repeat([]byte("A"), partSize)
	part2 := []byte("Z")
	parts := make([]storage.CompletedPart, 0, 2)
	for i, body := range [][]byte{part1, part2} {
		partNumber := i + 1
		u, err := d.PresignPart(ctx, key, uploadID, partNumber, 5*time.Minute)
		if err != nil {
			t.Fatalf("PresignPart %d: %v", partNumber, err)
		}
		httpReq, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(body))
		httpReq.ContentLength = int64(len(body))
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("PUT part %d: %v", partNumber, err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("PUT part %d status %d", partNumber, resp.StatusCode)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("PUT part %d: missing ETag header", partNumber)
		}
		parts = append(parts, storage.CompletedPart{PartNumber: partNumber, ETag: etag})
	}

	if err := d.CompleteMultipart(ctx, key, uploadID, parts); err != nil {
		t.Fatalf("CompleteMultipart: %v", err)
	}

	info, err := d.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat after complete: %v", err)
	}
	if info.Size != int64(partSize+len(part2)) {
		t.Fatalf("Stat size %d, want %d", info.Size, partSize+len(part2))
	}
}

func TestS3Driver_AbortMultipart_Idempotent(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Aborting an unknown upload must not error.
	if err := d.AbortMultipart(ctx, "ghost.bin", "no-such-id"); err != nil {
		t.Fatalf("AbortMultipart on missing: %v", err)
	}
}

func TestS3Driver_ListIncompleteUploads(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	uploadID, err := d.InitMultipart(ctx, "abandoned/big.bin", "application/octet-stream")
	if err != nil {
		t.Fatalf("InitMultipart: %v", err)
	}
	// Wait a tick so the initiated timestamp is comfortably in the past.
	time.Sleep(50 * time.Millisecond)
	uploads, err := d.ListIncompleteUploads(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("ListIncompleteUploads: %v", err)
	}
	found := false
	for _, u := range uploads {
		if u.UploadID == uploadID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected uploadID %s in list, got %+v", uploadID, uploads)
	}
	// Now abort, then re-list — abandoned upload should be gone.
	if err := d.AbortMultipart(ctx, "abandoned/big.bin", uploadID); err != nil {
		t.Fatalf("AbortMultipart: %v", err)
	}
}

func TestS3Driver_NotFoundDistinguished(t *testing.T) {
	d, _ := setupS3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := d.Get(ctx, "does/not/exist.bin"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get on missing: got %v, want ErrNotFound", err)
	}
}
