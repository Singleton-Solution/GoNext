package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// --- fixtures ----------------------------------------------------------------

// fixedClock returns a time.Time generator that always returns t. Used
// so the storage key is deterministic in tests.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// authedPrincipal returns a Principal with the editor role — which has
// every media capability by default.
func authedPrincipal() policy.Principal {
	return policy.Principal{
		UserID: "user-1",
		Roles:  []policy.Role{policy.RoleEditor},
	}
}

// withAuth wraps a request with a Principal-bearing context. Mirrors
// what the production auth middleware does, without dragging the real
// middleware into the unit tests.
func withAuth(r *http.Request, pr policy.Principal) *http.Request {
	return r.WithContext(policy.WithPrincipal(r.Context(), pr))
}

// newMux builds a fresh mux + Mount with the default deps. The
// store + putter are returned so individual tests can assert on
// their state.
//
// The id generator returns a monotonically incrementing string so two
// inserts in the same test land at distinct rows; the time source
// advances by 1 second per call so list ordering is deterministic but
// distinguishable.
func newMux(t *testing.T) (*http.ServeMux, *MemoryStore, *MemoryPutter) {
	t.Helper()
	var idSeq int
	idGen := func() string {
		idSeq++
		return "asset-" + strings.Repeat("0", 4-len(itoa(idSeq))) + itoa(idSeq)
	}
	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	var clockSeq int
	clock := func() time.Time {
		clockSeq++
		return base.Add(time.Duration(clockSeq) * time.Second)
	}
	store := NewMemoryStore(clock, idGen)
	putter := NewMemoryPutter()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/media", Deps{
		Store:    store,
		Putter:   putter,
		Policy:   policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Now:      func() time.Time { return base },
		MaxBytes: 1024 * 1024,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return mux, store, putter
}

// itoa is a zero-alloc integer-to-string for small positive ints. Used
// by the id generator above — avoids pulling in strconv only for the
// fixture.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// buildMultipart constructs a multipart/form-data body with a single
// "file" field. Returns the body bytes + the multipart Content-Type
// header value.
func buildMultipart(t *testing.T, filename string, contents []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// pngBytes returns a minimal valid PNG header + body. http.DetectContentType
// sniffs the leading bytes and emits "image/png" for this input.
func pngBytes(extra ...byte) []byte {
	out := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE,
	}
	return append(out, extra...)
}

// exeBytes returns bytes that http.DetectContentType identifies as a
// Windows executable. Used in the reject-exe test.
func exeBytes() []byte {
	// MZ header + minimal padding so the sniffer matches the PE
	// signature. http.DetectContentType returns
	// "application/vnd.microsoft.portable-executable" for this input.
	out := make([]byte, 64)
	out[0] = 'M'
	out[1] = 'Z'
	return out
}

// --- tests -------------------------------------------------------------------

func TestUpload_CreatesRowAndStoresBytes(t *testing.T) {
	mux, store, putter := newMux(t)

	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var asset Asset
	if err := json.Unmarshal(w.Body.Bytes(), &asset); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if asset.MimeType != "image/png" {
		t.Errorf("mime_type = %q, want image/png", asset.MimeType)
	}
	if asset.ByteSize != int64(len(pngBytes())) {
		t.Errorf("byte_size = %d, want %d", asset.ByteSize, len(pngBytes()))
	}
	if asset.Filename != "logo.png" {
		t.Errorf("filename = %q, want logo.png", asset.Filename)
	}
	if asset.PublicURL == "" {
		t.Errorf("public_url should be set")
	}
	if asset.UploaderID != "user-1" {
		t.Errorf("uploader_id = %q, want user-1", asset.UploaderID)
	}

	// The store has exactly one row.
	if _, err := store.GetByID(context.Background(), asset.ID); err != nil {
		t.Errorf("GetByID after insert: %v", err)
	}
	// The putter received exactly one PutObject call.
	if got := putter.PutCount(); got != 1 {
		t.Errorf("PutCount = %d, want 1", got)
	}
	if stored := putter.Stored(asset.StorageKey); !bytes.Equal(stored, pngBytes()) {
		t.Errorf("stored bytes mismatch (len got=%d want=%d)", len(stored), len(pngBytes()))
	}
}

func TestUpload_DedupesByContentHash(t *testing.T) {
	mux, _, putter := newMux(t)

	doUpload := func() *httptest.ResponseRecorder {
		body, ct := buildMultipart(t, "logo.png", pngBytes())
		req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	first := doUpload()
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}
	var firstAsset Asset
	_ = json.Unmarshal(first.Body.Bytes(), &firstAsset)

	second := doUpload()
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d (want 200 dedupe), body = %s", second.Code, second.Body.String())
	}
	var secondAsset Asset
	_ = json.Unmarshal(second.Body.Bytes(), &secondAsset)

	if firstAsset.ID != secondAsset.ID {
		t.Errorf("dedupe returned a different id: first=%s second=%s", firstAsset.ID, secondAsset.ID)
	}
	// Crucially, the S3 PUT was NOT issued a second time.
	if got := putter.PutCount(); got != 1 {
		t.Errorf("PutCount = %d, want 1 (second upload should dedupe before PutObject)", got)
	}
}

func TestUpload_RejectsExecutable(t *testing.T) {
	mux, _, putter := newMux(t)

	body, ct := buildMultipart(t, "evil.exe", exeBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := putter.PutCount(); got != 0 {
		t.Errorf("PutCount = %d, want 0 (executable should be rejected before storage)", got)
	}
}

func TestUpload_RejectsOversize(t *testing.T) {
	mux, _, _ := newMux(t)

	// MaxBytes in newMux is 1 MiB; build a 2 MiB payload.
	big := bytes.Repeat([]byte{0x42}, 2*1024*1024)
	body, ct := buildMultipart(t, "big.bin", big)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestUpload_RejectsUnauthenticated(t *testing.T) {
	mux, _, _ := newMux(t)
	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestUpload_RejectsForbidden(t *testing.T) {
	mux, _, _ := newMux(t)
	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), policy.Principal{
		UserID: "sub-1",
		Roles:  []policy.Role{policy.RoleSubscriber},
	})
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestList_PaginatesAndFiltersByType(t *testing.T) {
	mux, store, _ := newMux(t)

	// Seed two images and one document directly via the store. Doing it
	// at the store layer rather than via the upload handler keeps the
	// test focused on list semantics.
	ctx := context.Background()
	for i, mime := range []string{"image/png", "image/jpeg", "application/pdf"} {
		hash := bytes.Repeat([]byte{byte(i + 1)}, 32)
		if _, err := store.Insert(ctx, AssetCreate{
			Filename:   "f" + string(rune('a'+i)) + ".bin",
			MimeType:   mime,
			ByteSize:   100,
			StorageKey: "k/" + string(rune('a'+i)),
			SHA256:     hash,
			UploaderID: "user-1",
		}); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	doList := func(query string) (Page, int) {
		req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media?"+query, nil), authedPrincipal())
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var p Page
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		return p, w.Code
	}

	all, code := doList("")
	if code != 200 || len(all.Data) != 3 {
		t.Fatalf("unfiltered list: code=%d len=%d", code, len(all.Data))
	}

	images, code := doList("type=image")
	if code != 200 || len(images.Data) != 2 {
		t.Errorf("image filter: code=%d len=%d", code, len(images.Data))
	}
	for _, a := range images.Data {
		if !strings.HasPrefix(a.MimeType, "image/") {
			t.Errorf("image filter returned non-image: %q", a.MimeType)
		}
	}

	docs, _ := doList("type=document")
	if len(docs.Data) != 1 {
		t.Errorf("document filter: len=%d", len(docs.Data))
	}

	// limit=1 + cursor walks the list.
	page1, _ := doList("limit=1")
	if len(page1.Data) != 1 {
		t.Fatalf("page1 len=%d", len(page1.Data))
	}
	if page1.Pagination.NextCursor == "" {
		t.Fatal("page1 missing next_cursor")
	}
	page2, _ := doList("limit=1&cursor=" + page1.Pagination.NextCursor)
	if len(page2.Data) != 1 {
		t.Errorf("page2 len=%d", len(page2.Data))
	}
	if page2.Data[0].ID == page1.Data[0].ID {
		t.Errorf("page2 returned same row as page1")
	}
}

func TestList_RejectsBadType(t *testing.T) {
	mux, _, _ := newMux(t)
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media?type=spreadsheet", nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUpdate_AltTextOnly(t *testing.T) {
	mux, store, _ := newMux(t)
	asset, err := store.Insert(context.Background(), AssetCreate{
		Filename:   "logo.png",
		MimeType:   "image/png",
		ByteSize:   10,
		StorageKey: "k",
		SHA256:     bytes.Repeat([]byte{0x09}, 32),
		UploaderID: "user-1",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	patch := bytes.NewBufferString(`{"alt_text":"the company logo","filename":"hacked.png","storage_key":"hacked"}`)
	req := withAuth(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/media/"+asset.ID, patch), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out Asset
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.AltText != "the company logo" {
		t.Errorf("alt_text = %q", out.AltText)
	}
	if out.Filename != "logo.png" {
		t.Errorf("filename was mutated: %q", out.Filename)
	}
	if out.StorageKey != "k" {
		t.Errorf("storage_key was mutated: %q", out.StorageKey)
	}
}

func TestUpdate_RejectsEmptyBody(t *testing.T) {
	mux, store, _ := newMux(t)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "x", MimeType: "image/png", ByteSize: 1, StorageKey: "x",
		SHA256: bytes.Repeat([]byte{0x10}, 32), UploaderID: "user-1",
	})
	req := withAuth(httptest.NewRequest(http.MethodPatch, "/api/v1/admin/media/"+asset.ID, bytes.NewBufferString(`{}`)), authedPrincipal())
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDelete_SoftDeletesAndDisappearsFromList(t *testing.T) {
	mux, store, _ := newMux(t)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "x", MimeType: "image/png", ByteSize: 1, StorageKey: "x",
		SHA256: bytes.Repeat([]byte{0x11}, 32), UploaderID: "user-1",
	})

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/media/"+asset.ID, nil), authedPrincipal())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", w.Code)
	}

	// The list endpoint must not include the deleted asset.
	listReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media", nil), authedPrincipal())
	listW := httptest.NewRecorder()
	mux.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d", listW.Code)
	}
	var p Page
	_ = json.Unmarshal(listW.Body.Bytes(), &p)
	for _, a := range p.Data {
		if a.ID == asset.ID {
			t.Errorf("soft-deleted asset surfaced in list")
		}
	}

	// And the GET-by-id returns 404 by default (no trash flag).
	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/admin/media/"+asset.ID, nil), authedPrincipal())
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", getW.Code)
	}
}

func TestDelete_RejectsWithoutDeleteCapability(t *testing.T) {
	mux, store, _ := newMux(t)
	asset, _ := store.Insert(context.Background(), AssetCreate{
		Filename: "x", MimeType: "image/png", ByteSize: 1, StorageKey: "x",
		SHA256: bytes.Repeat([]byte{0x12}, 32), UploaderID: "user-1",
	})

	// Author role has upload+read but NOT delete (delete sits at editor+).
	authorPrincipal := policy.Principal{UserID: "auth-1", Roles: []policy.Role{policy.RoleAuthor}}
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/media/"+asset.ID, nil), authorPrincipal)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestMount_ValidatesDeps(t *testing.T) {
	cases := []struct {
		name string
		deps Deps
	}{
		{"no store", Deps{Putter: NewMemoryPutter(), Policy: policy.NewBasicPolicy(nil)}},
		{"no putter", Deps{Store: NewMemoryStore(nil, nil), Policy: policy.NewBasicPolicy(nil)}},
		{"no policy", Deps{Store: NewMemoryStore(nil, nil), Putter: NewMemoryPutter()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Mount(http.NewServeMux(), "/api/v1/admin/media", c.deps); err == nil {
				t.Errorf("Mount returned nil for missing dep")
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"logo.png", "logo.png"},
		{"../../etc/passwd", "passwd"},
		{"name with spaces.jpg", "name_with_spaces.jpg"},
		{"emoji_\U0001f600.png", "emoji__.png"},
		{strings.Repeat("a", 300) + ".png", ""},
	}
	for _, c := range cases {
		got := sanitizeFilename(c.in)
		if c.want == "" {
			// Just assert truncation kicked in, not the exact value.
			if len(got) > 200 {
				t.Errorf("sanitize(%q) = %q (len %d), want ≤200", c.in, got, len(got))
			}
			continue
		}
		if got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Ensure the MemoryPutter's PutObject error path returns 502 from the
// upload handler. The handler must NOT have inserted a row.
func TestUpload_StorageErrorReturns502AndDoesNotInsert(t *testing.T) {
	mux, store, putter := newMux(t)
	putter.SetPutError(errors.New("simulated storage outage"))

	body, ct := buildMultipart(t, "logo.png", pngBytes())
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", body), authedPrincipal())
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (got body %s)", w.Code, w.Body.String())
	}
	// And no row was inserted.
	page, _ := store.List(context.Background(), ListFilter{})
	if len(page.Data) != 0 {
		t.Errorf("expected 0 rows after storage failure, got %d", len(page.Data))
	}
}

// io.Discard is referenced indirectly by error paths in some
// configurations; keep a static import to silence linters that
// otherwise prune the dependency.
var _ = io.Discard
