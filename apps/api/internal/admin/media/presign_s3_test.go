package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/admin/media"
	"github.com/Singleton-Solution/GoNext/packages/go/media/storage"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// setupPresignS3 wires the presign routes against a real S3-backed
// LocalDriver replacement — i.e. an actual S3Driver pointed at a
// MinIO testcontainer. Used to exercise the multipart presign flow
// end-to-end.
func setupPresignS3(t *testing.T) (*http.ServeMux, *storage.S3Driver) {
	t.Helper()
	endpoint, accessKey, secretKey := containers.MinIO(t)
	if endpoint == "" {
		t.Skip("docker unavailable")
	}
	bucket := "presign-test-" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
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
	store := media.NewMemoryStore(nil, nil)
	mux := http.NewServeMux()
	deps := media.Deps{
		Store:  store,
		Putter: media.NewMemoryPutter(),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}
	if err := media.Mount(mux, "/api/v1/admin/media", deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := media.MountPresign(mux, "/api/v1/admin/media", deps, media.PresignDeps{
		Driver:   driver,
		MaxBytes: 100 * 1024 * 1024,
	}); err != nil {
		t.Fatalf("MountPresign: %v", err)
	}
	return mux, driver
}

func TestPresignMultipart_S3_EndToEnd(t *testing.T) {
	mux, driver := setupPresignS3(t)
	// 1. Presign multipart upload.
	const totalSize = 6 * 1024 * 1024 // 6 MiB → two parts at 8 MiB partSize → actually 1 part
	body := media.PresignRequest{
		Filename:  "movie.bin",
		MimeType:  "application/octet-stream",
		Size:      totalSize,
		Multipart: true,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req = withAuthPrincipal(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("presign status %d body %s", rec.Code, rec.Body.String())
	}
	var resp media.PresignMultipartResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UploadID == "" || len(resp.PartURLs) == 0 {
		t.Fatalf("bad response: %+v", resp)
	}

	// 2. PUT each part.
	manifest := make([]media.FinalizeMultipartPart, 0, len(resp.PartURLs))
	remaining := int64(totalSize)
	for _, part := range resp.PartURLs {
		size := resp.PartSize
		if size > remaining {
			size = remaining
		}
		// The S3 multipart spec requires every non-last part to be
		// >=5 MiB. Our single-part case here has size = totalSize,
		// which satisfies the rule trivially.
		buf := bytes.Repeat([]byte("A"), int(size))
		httpReq, _ := http.NewRequest(http.MethodPut, part.URL, bytes.NewReader(buf))
		httpReq.ContentLength = int64(len(buf))
		r, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("PUT part %d: %v", part.PartNumber, err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode/100 != 2 {
			t.Fatalf("PUT part %d status %d", part.PartNumber, r.StatusCode)
		}
		manifest = append(manifest, media.FinalizeMultipartPart{
			PartNumber: part.PartNumber,
			ETag:       r.Header.Get("ETag"),
		})
		remaining -= size
	}

	// 3. Finalize.
	finBody := media.FinalizeMultipartRequest{
		UploadID: resp.UploadID,
		Key:      resp.Key,
		Filename: "movie.bin",
		MimeType: "application/octet-stream",
		Size:     totalSize,
		Parts:    manifest,
	}
	finRaw, _ := json.Marshal(finBody)
	finReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/finalize-multipart", bytes.NewReader(finRaw))
	finReq = withAuthPrincipal(finReq)
	finRec := httptest.NewRecorder()
	mux.ServeHTTP(finRec, finReq)
	if finRec.Code != http.StatusCreated {
		t.Fatalf("finalize-multipart status %d body %s", finRec.Code, finRec.Body.String())
	}

	// 4. Object lands in the bucket.
	info, err := driver.Stat(context.Background(), resp.Key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(totalSize) {
		t.Fatalf("Stat size %d, want %d", info.Size, totalSize)
	}
}

// withAuthPrincipal mirrors withAuth from the media package's internal
// tests but lives in the _test (external) package, so we duplicate the
// minimum machinery here.
func withAuthPrincipal(r *http.Request) *http.Request {
	pr := policy.Principal{UserID: "user-1", Roles: []policy.Role{policy.RoleEditor}}
	return r.WithContext(policy.WithPrincipal(r.Context(), pr))
}
