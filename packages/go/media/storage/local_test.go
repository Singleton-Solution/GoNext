package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalDriver_PutGetStatDelete(t *testing.T) {
	t.Parallel()
	d, err := NewLocalDriver(LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	ctx := context.Background()
	body := []byte("hello, gonext")
	n, err := d.Put(ctx, "2026/05/test.txt", bytes.NewReader(body), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Put returned %d, want %d", n, len(body))
	}
	rc, err := d.Get(ctx, "2026/05/test.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q, want %q", got, body)
	}
	info, err := d.Stat(ctx, "2026/05/test.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(body)) {
		t.Fatalf("Stat size %d, want %d", info.Size, len(body))
	}
	if info.MimeType != "text/plain" {
		t.Fatalf("Stat mime %q, want text/plain", info.MimeType)
	}
	if err := d.Delete(ctx, "2026/05/test.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := d.Stat(ctx, "2026/05/test.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat after Delete: got %v, want ErrNotFound", err)
	}
	// Re-delete is idempotent.
	if err := d.Delete(ctx, "2026/05/test.txt"); err != nil {
		t.Fatalf("Delete (second): %v", err)
	}
}

func TestLocalDriver_RejectsPathEscape(t *testing.T) {
	t.Parallel()
	d, err := NewLocalDriver(LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	ctx := context.Background()
	badKeys := []string{"../etc/passwd", "a/../../b", "foo\\bar", ""}
	for _, k := range badKeys {
		if _, err := d.Put(ctx, k, bytes.NewReader([]byte("x")), ""); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q): got %v, want ErrInvalidKey", k, err)
		}
	}
}

func TestLocalDriver_PresignedPutAndGet(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d, err := NewLocalDriver(LocalConfig{Root: root, PublicBaseURL: "/_/media"})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	ctx := context.Background()

	// Presign upload.
	req, err := d.Presign(ctx, "uploads/test.bin", PresignPut, time.Minute, "application/octet-stream")
	if err != nil {
		t.Fatalf("Presign put: %v", err)
	}
	if !strings.Contains(req.URL, "op=put") {
		t.Fatalf("expected op=put in URL: %s", req.URL)
	}

	// Mount the handler at the same base path the URL embeds; we keep
	// the prefix in the request URL so the marker-based discriminator
	// inside the handler matches the same shape production hits.
	srv := httptest.NewServer(LocalUploadHandler(d))
	defer srv.Close()
	httpReq, _ := http.NewRequest(http.MethodPut, srv.URL+req.URL, bytes.NewReader([]byte("body")))
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status %d, want 204", resp.StatusCode)
	}
	// File should exist on disk.
	if _, err := d.Stat(ctx, "uploads/test.bin"); err != nil {
		t.Fatalf("Stat: %v", err)
	}

	// Presigned GET round-trip.
	greq, err := d.Presign(ctx, "uploads/test.bin", PresignGet, time.Minute, "")
	if err != nil {
		t.Fatalf("Presign get: %v", err)
	}
	getResp, err := http.Get(srv.URL + greq.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status %d, want 200", getResp.StatusCode)
	}
}

func TestLocalDriver_PresignedURLRejectsTampering(t *testing.T) {
	t.Parallel()
	d, err := NewLocalDriver(LocalConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	req, err := d.Presign(context.Background(), "secret.txt", PresignPut, time.Minute, "text/plain")
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	// Flip a character in the signature; verification must reject.
	tampered := strings.Replace(req.URL, "sig=", "sig=AAAA", 1)
	rawQ := tampered[strings.Index(tampered, "?")+1:]
	if _, _, err := d.VerifyPresignedURL(rawQ, "secret.txt"); err == nil {
		t.Fatalf("expected verify to fail on tampered signature")
	}
}

func TestLocalDriver_RootDirCreated(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "nested", "dir")
	d, err := NewLocalDriver(LocalConfig{Root: root})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	if d.Root() == "" {
		t.Fatalf("Root() empty")
	}
}

func TestNew_DefaultsToLocal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d, err := New(context.Background(), Options{
		Env: map[string]string{},
		Local: LocalConfig{Root: root},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := d.(*LocalDriver); !ok {
		t.Fatalf("got %T, want *LocalDriver", d)
	}
}

func TestNew_RespectsEnvVar(t *testing.T) {
	t.Parallel()
	d, err := New(context.Background(), Options{
		Env: map[string]string{"GONEXT_MEDIA_DRIVER": "gcs"},
		GCS: GCSConfig{Bucket: "bucket"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := d.(*GCSDriver); !ok {
		t.Fatalf("got %T, want *GCSDriver", d)
	}
}

func TestNew_RejectsUnknownDriver(t *testing.T) {
	t.Parallel()
	_, err := New(context.Background(), Options{
		Driver: DriverKind("azure"),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown driver") {
		t.Fatalf("expected unknown driver error, got %v", err)
	}
}

func TestGCSDriver_StubReturnsErrUnimplemented(t *testing.T) {
	t.Parallel()
	d, err := NewGCSDriver(GCSConfig{Bucket: "b"})
	if err != nil {
		t.Fatalf("NewGCSDriver: %v", err)
	}
	if _, err := d.Put(context.Background(), "k", strings.NewReader(""), ""); !errors.Is(err, ErrUnimplemented) {
		t.Fatalf("Put: got %v, want ErrUnimplemented", err)
	}
	if got := d.PublicURL("a/b"); got != "https://storage.googleapis.com/b/a/b" {
		t.Fatalf("PublicURL = %q", got)
	}
}
