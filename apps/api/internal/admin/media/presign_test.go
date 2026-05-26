package media

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/media/storage"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// newPresignMux wires the four presign-flow routes against a LocalDriver
// rooted in a temp dir, so the URL the server hands the client lands in
// a writable place when followed.
func newPresignMux(t *testing.T) (*http.ServeMux, *MemoryStore, *storage.LocalDriver) {
	t.Helper()
	store := NewMemoryStore(nil, nil)
	driver, err := storage.NewLocalDriver(storage.LocalConfig{
		Root:          t.TempDir(),
		PublicBaseURL: "/_/media",
	})
	if err != nil {
		t.Fatalf("NewLocalDriver: %v", err)
	}
	mux := http.NewServeMux()
	deps := Deps{
		Store:  store,
		Putter: NewMemoryPutter(),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}
	if err := Mount(mux, "/api/v1/admin/media", deps); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := MountPresign(mux, "/api/v1/admin/media", deps, PresignDeps{
		Driver:   driver,
		MaxBytes: 50 * 1024 * 1024,
	}); err != nil {
		t.Fatalf("MountPresign: %v", err)
	}
	// Mount the LocalUploadHandler so presigned URLs actually land.
	mux.Handle("/_/media/", storage.LocalUploadHandler(driver))
	return mux, store, driver
}

func TestPresign_Single_HappyPath(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := PresignRequest{Filename: "logo.png", MimeType: "image/png", Size: 1024}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp PresignSingleResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UploadURL == "" || resp.Key == "" || resp.ExpiresAt.IsZero() {
		t.Fatalf("bad response: %+v", resp)
	}
	if resp.MaxBytes != 50*1024*1024 {
		t.Fatalf("MaxBytes = %d", resp.MaxBytes)
	}
}

func TestPresign_RejectsExeMime(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := PresignRequest{Filename: "evil.exe", MimeType: "application/octet-stream", Size: 100}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415; body %s", rec.Code, rec.Body.String())
	}
}

func TestPresign_RejectsOversize(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := PresignRequest{Filename: "huge.png", MimeType: "image/png", Size: 999_999_999}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
}

func TestPresign_Multipart_FallsBackForSmallFiles(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	// Size below the multipart threshold — server must reject as
	// invalid_request because multipart is meaningless for tiny files.
	body := PresignRequest{Filename: "small.png", MimeType: "image/png", Size: 1024, Multipart: true}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body.String())
	}
}

func TestPresign_Multipart_FallsBackToSingleWhenDriverLacksSupport(t *testing.T) {
	t.Parallel()
	// LocalDriver does NOT implement MultipartDriver; the handler must
	// degrade gracefully and return a single-shot envelope.
	mux, _, _ := newPresignMux(t)
	body := PresignRequest{Filename: "video.bin", MimeType: "video/mp4", Size: 10 * 1024 * 1024, Multipart: true}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	// Body must NOT contain "upload_id" — that key is only on the
	// multipart response shape; the fallback returns the single-shot
	// shape.
	if strings.Contains(rec.Body.String(), `"upload_id"`) {
		t.Fatalf("expected single-shot fallback, got %s", rec.Body.String())
	}
}

func TestPresign_RequiresAuth(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := PresignRequest{Filename: "a.png", MimeType: "image/png", Size: 1}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestPresignAndFinalize_EndToEnd_LocalDriver(t *testing.T) {
	t.Parallel()
	mux, _, driver := newPresignMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Presign.
	body := PresignRequest{Filename: "logo.png", MimeType: "image/png", Size: 64}
	raw, _ := json.Marshal(body)
	preReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/presign", bytes.NewReader(raw))
	preReq = withAuth(preReq, authedPrincipal())
	preRec := httptest.NewRecorder()
	mux.ServeHTTP(preRec, preReq)
	if preRec.Code != http.StatusOK {
		t.Fatalf("presign status %d body %s", preRec.Code, preRec.Body.String())
	}
	var preResp PresignSingleResponse
	if err := json.NewDecoder(preRec.Body).Decode(&preResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 2. PUT the bytes against the returned URL (uses the
	//    LocalUploadHandler mounted at /_/media/).
	pngBody := bytes.Repeat([]byte{0x89, 0x50, 0x4E, 0x47}, 16)
	putReq, _ := http.NewRequest(http.MethodPut, srv.URL+preResp.UploadURL, bytes.NewReader(pngBody))
	for k, v := range preResp.Headers {
		putReq.Header.Set(k, v)
	}
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status %d", putResp.StatusCode)
	}

	// 3. Finalize.
	finBody := FinalizeRequest{
		Key:      preResp.Key,
		Filename: "logo.png",
		MimeType: "image/png",
		Size:     int64(len(pngBody)),
	}
	finRaw, _ := json.Marshal(finBody)
	finReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/finalize", bytes.NewReader(finRaw))
	finReq = withAuth(finReq, authedPrincipal())
	finRec := httptest.NewRecorder()
	mux.ServeHTTP(finRec, finReq)
	if finRec.Code != http.StatusCreated {
		t.Fatalf("finalize status %d body %s", finRec.Code, finRec.Body.String())
	}

	// Verify the bytes are actually on disk via the driver.
	info, err := driver.Stat(context.Background(), preResp.Key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(pngBody)) {
		t.Fatalf("Stat size %d, want %d", info.Size, len(pngBody))
	}
}

func TestFinalize_RejectsMissingObject(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := FinalizeRequest{
		Key:      "phantom/key.png",
		Filename: "p.png",
		MimeType: "image/png",
		Size:     10,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/finalize", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestFinalizeMultipart_DriverWithoutSupport(t *testing.T) {
	t.Parallel()
	mux, _, _ := newPresignMux(t)
	body := FinalizeMultipartRequest{
		Key:      "k",
		UploadID: "u",
		Filename: "v.mp4",
		MimeType: "video/mp4",
		Size:     1,
		Parts:    []FinalizeMultipartPart{{PartNumber: 1, ETag: "x"}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media/finalize-multipart", bytes.NewReader(raw))
	req = withAuth(req, authedPrincipal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (local driver lacks multipart support)", rec.Code)
	}
}

// TestMintStorageKey verifies the key shape is yyyy/mm/<uuid>-<safe>.
func TestMintStorageKey_Shape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	k := mintStorageKey(now, "Hello World.png")
	if !strings.HasPrefix(k, "2026/05/") {
		t.Fatalf("key %q missing yyyy/mm/ prefix", k)
	}
	if !strings.HasSuffix(k, "Hello_World.png") {
		t.Fatalf("key %q missing sanitized filename", k)
	}
}
