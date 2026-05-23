package marketplace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	mp "github.com/Singleton-Solution/GoNext/packages/go/plugins/marketplace"
)

// =============================================================================
// Fake store
// =============================================================================

// fakeStore is a programmable in-memory Store stand-in. We hand-roll
// it (rather than reach for a SQL mock) so tests can express:
//
//   - listings indexed by slug,
//   - per-version aggregates pinned to deterministic values,
//   - install events recorded into an append-only slice the test can
//     inspect.
//
// Concurrent access via a single mutex; the handler doesn't fan out
// per-request, but the test harness's t.Parallel() pattern still
// benefits from the safety net.
type fakeStore struct {
	mu sync.Mutex

	listings    map[string]mp.Listing      // slug -> listing
	versionsFor map[uuid.UUID][]mp.Version // listing id -> versions
	compatFor   map[uuid.UUID][]mp.CompatRange
	ratingsFor  map[uuid.UUID][]mp.Rating
	aggregates  map[uuid.UUID]mp.Aggregate
	installs    map[uuid.UUID]int64
	events      []mp.InstallEvent

	// failure injection
	listErr   error
	getErr    error
	submitErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		listings:    map[string]mp.Listing{},
		versionsFor: map[uuid.UUID][]mp.Version{},
		compatFor:   map[uuid.UUID][]mp.CompatRange{},
		ratingsFor:  map[uuid.UUID][]mp.Rating{},
		aggregates:  map[uuid.UUID]mp.Aggregate{},
		installs:    map[uuid.UUID]int64{},
	}
}

func (s *fakeStore) seedListing(l mp.Listing) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listings[l.Slug] = l
}

func (s *fakeStore) seedVersions(listingID uuid.UUID, v ...mp.Version) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versionsFor[listingID] = append(s.versionsFor[listingID], v...)
}

func (s *fakeStore) seedAggregate(versionID uuid.UUID, avg float64, count int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aggregates[versionID] = mp.Aggregate{PluginVersionID: versionID, Average: avg, Count: count}
}

func (s *fakeStore) seedInstalls(listingID uuid.UUID, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installs[listingID] = n
}

func (s *fakeStore) ListListings(_ context.Context, filter ListFilter) ([]mp.Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]mp.Listing, 0, len(s.listings))
	for _, l := range s.listings {
		if filter.Category != "" && l.PrimaryCategory != filter.Category {
			continue
		}
		out = append(out, l)
	}
	out = filterByQuery(out, filter.Query)
	return out, nil
}

func (s *fakeStore) GetListingBySlug(_ context.Context, slug string) (mp.Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return mp.Listing{}, s.getErr
	}
	l, ok := s.listings[slug]
	if !ok {
		return mp.Listing{}, fmt.Errorf("%w: slug %q", mp.ErrNotFound, slug)
	}
	return l, nil
}

func (s *fakeStore) ListVersions(_ context.Context, listingID uuid.UUID) ([]mp.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mp.Version, len(s.versionsFor[listingID]))
	copy(out, s.versionsFor[listingID])
	return out, nil
}

func (s *fakeStore) ListCompat(_ context.Context, versionID uuid.UUID) ([]mp.CompatRange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mp.CompatRange, len(s.compatFor[versionID]))
	copy(out, s.compatFor[versionID])
	return out, nil
}

func (s *fakeStore) ListRatings(_ context.Context, versionID uuid.UUID) ([]mp.Rating, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mp.Rating, len(s.ratingsFor[versionID]))
	copy(out, s.ratingsFor[versionID])
	return out, nil
}

func (s *fakeStore) SubmitRating(_ context.Context, in mp.Rating) (mp.Rating, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.submitErr != nil {
		return mp.Rating{}, s.submitErr
	}
	if in.Stars < 1 || in.Stars > 5 {
		return mp.Rating{}, fmt.Errorf("%w: stars %d", mp.ErrInvalidInput, in.Stars)
	}
	in.CreatedAt = time.Now().UTC()
	s.ratingsFor[in.PluginVersionID] = append(s.ratingsFor[in.PluginVersionID], in)
	// Re-aggregate so subsequent reads pick up the fresh value.
	agg := s.aggregates[in.PluginVersionID]
	total := agg.Average*float64(agg.Count) + float64(in.Stars)
	agg.Count++
	agg.Average = total / float64(agg.Count)
	agg.PluginVersionID = in.PluginVersionID
	s.aggregates[in.PluginVersionID] = agg
	return in, nil
}

func (s *fakeStore) AggregateRatings(_ context.Context, versionID uuid.UUID) (mp.Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agg, ok := s.aggregates[versionID]; ok {
		return agg, nil
	}
	return mp.Aggregate{PluginVersionID: versionID}, nil
}

func (s *fakeStore) RecordInstallEvent(_ context.Context, in mp.InstallEvent) (mp.InstallEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.ID = int64(len(s.events) + 1)
	in.CreatedAt = time.Now().UTC()
	s.events = append(s.events, in)
	if in.EventType == mp.EventInstalled {
		s.installs[in.ListingID]++
	}
	return in, nil
}

func (s *fakeStore) CountInstalls(_ context.Context, listingID uuid.UUID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installs[listingID], nil
}

// =============================================================================
// Fake installer + bundle fetcher
// =============================================================================

type fakeInstaller struct {
	mu       sync.Mutex
	installs []string
	err      error
	slug     string
}

func (f *fakeInstaller) Install(_ context.Context, bundle io.Reader) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	// Drain the bundle to mimic the production path; this also
	// surfaces "the fetcher fed us nothing" as a clean failure when
	// the test forgets to seed bytes.
	data, _ := io.ReadAll(bundle)
	f.installs = append(f.installs, string(data))
	if f.slug != "" {
		return f.slug, nil
	}
	return "plugin-from-bundle", nil
}

type memoryBundleFetcher struct {
	mu    sync.Mutex
	bytes map[string][]byte
	err   error
}

func newMemoryFetcher() *memoryBundleFetcher {
	return &memoryBundleFetcher{bytes: map[string][]byte{}}
}

func (f *memoryBundleFetcher) put(digest string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bytes[digest] = b
}

func (f *memoryBundleFetcher) Fetch(_ context.Context, digest string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.bytes[digest]
	if !ok {
		return nil, ErrBundleNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// =============================================================================
// Harness
// =============================================================================

type harness struct {
	mux       *http.ServeMux
	store     *fakeStore
	installer *fakeInstaller
	bundles   *memoryBundleFetcher
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := newFakeStore()
	insp := &fakeInstaller{}
	bundles := newMemoryFetcher()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/marketplace", Deps{
		Store:     store,
		Installer: insp,
		Bundles:   bundles,
		Policy:    policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		HostID:    NewStaticHostID("test-host"),
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &harness{mux: mux, store: store, installer: insp, bundles: bundles}
}

func adminPrincipal() policy.Principal {
	return policy.Principal{
		UserID: "11111111-1111-1111-1111-111111111111",
		Roles:  []policy.Role{policy.RoleAdmin},
	}
}

func subscriberPrincipal() policy.Principal {
	return policy.Principal{
		UserID: "22222222-2222-2222-2222-222222222222",
		Roles:  []policy.Role{policy.RoleSubscriber},
	}
}

func (h *harness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func seedListing(t *testing.T, store *fakeStore, slug, name, category string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	listingID := uuid.New()
	versionID := uuid.New()
	store.seedListing(mp.Listing{
		ID:              listingID,
		Slug:            slug,
		Name:            name,
		Summary:         "Summary for " + name,
		PrimaryCategory: category,
		Status:          mp.ListingListed,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	})
	wasm := []byte("wasm:" + slug)
	digest := sha256.Sum256(wasm)
	store.seedVersions(listingID, mp.Version{
		ID:          versionID,
		ListingID:   listingID,
		Version:     "1.0.0",
		WasmSHA256:  digest[:],
		Manifest:    json.RawMessage(`{"apiVersion":"gonext.io/v1","name":"` + slug + `","version":"1.0.0","abi_version":1}`),
		PublishedAt: time.Now().UTC(),
	})
	return listingID, versionID
}

// =============================================================================
// Tests
// =============================================================================

func TestListListings_Happy(t *testing.T) {
	h := newHarness(t)
	seedListing(t, h.store, "akismet", "Akismet", "antispam")
	seedListing(t, h.store, "seo-helper", "SEO Helper", "seo")

	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []ListingCard `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("want 2 listings, got %d", len(resp.Data))
	}
}

func TestListListings_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListListings_FilterByCategory(t *testing.T) {
	h := newHarness(t)
	seedListing(t, h.store, "akismet", "Akismet", "antispam")
	seedListing(t, h.store, "seo-helper", "SEO Helper", "seo")
	seedListing(t, h.store, "spam-blocker", "Spam Blocker", "antispam")

	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings?category=antispam", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []ListingCard `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("want 2 listings under antispam, got %d", len(resp.Data))
	}
	for _, c := range resp.Data {
		if c.PrimaryCategory != "antispam" {
			t.Errorf("got listing with category %q, want antispam", c.PrimaryCategory)
		}
	}
}

func TestListListings_FilterByQuery(t *testing.T) {
	h := newHarness(t)
	seedListing(t, h.store, "akismet", "Akismet", "antispam")
	seedListing(t, h.store, "seo-helper", "SEO Helper", "seo")

	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings?q=seo", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data []ListingCard `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 listing matching seo, got %d", len(resp.Data))
	}
	if resp.Data[0].Slug != "seo-helper" {
		t.Errorf("got slug %q, want seo-helper", resp.Data[0].Slug)
	}
}

func TestListListings_SortByStars(t *testing.T) {
	h := newHarness(t)
	_, vA := seedListing(t, h.store, "alpha", "Alpha", "")
	_, vB := seedListing(t, h.store, "bravo", "Bravo", "")
	h.store.seedAggregate(vA, 3.5, 10)
	h.store.seedAggregate(vB, 4.8, 4)

	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings?sort=stars", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []ListingCard `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) < 2 {
		t.Fatalf("want >= 2 listings, got %d", len(resp.Data))
	}
	if resp.Data[0].Slug != "bravo" {
		t.Errorf("first slug = %q, want bravo (highest stars)", resp.Data[0].Slug)
	}
}

func TestListListings_SortRejectsUnknown(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings?sort=garbage", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetListing_Happy(t *testing.T) {
	h := newHarness(t)
	_, _ = seedListing(t, h.store, "akismet", "Akismet", "antispam")
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings/akismet", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var detail ListingDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Slug != "akismet" {
		t.Errorf("slug = %q, want akismet", detail.Slug)
	}
	if detail.LatestVersion == nil {
		t.Errorf("expected latest_version to be populated")
	}
}

func TestGetListing_NotFound(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings/nope", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListVersions_Happy(t *testing.T) {
	h := newHarness(t)
	_, _ = seedListing(t, h.store, "akismet", "Akismet", "antispam")
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings/akismet/versions", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data []struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 version, got %d", len(resp.Data))
	}
	if resp.Data[0].Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", resp.Data[0].Version)
	}
}

func TestInstall_DispatchesToLifecycle(t *testing.T) {
	h := newHarness(t)
	listingID, versionID := seedListing(t, h.store, "akismet", "Akismet", "antispam")
	// Seed the bundle bytes keyed by the version's digest.
	wasm := []byte("wasm:akismet")
	digest := sha256.Sum256(wasm)
	h.bundles.put(toHex(digest[:]), wasm)

	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/install", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp InstallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Slug != "akismet" || resp.Version != "1.0.0" {
		t.Errorf("response = %+v, want akismet@1.0.0", resp)
	}
	if len(h.installer.installs) != 1 {
		t.Fatalf("installer called %d times, want 1", len(h.installer.installs))
	}
	if h.installer.installs[0] != string(wasm) {
		t.Errorf("installer received %q, want %q", h.installer.installs[0], string(wasm))
	}
	// One install event should be recorded.
	if got := h.store.events; len(got) != 1 || got[0].EventType != mp.EventInstalled {
		t.Errorf("events = %+v, want one installed event", got)
	}
	if h.store.events[0].ListingID != listingID || h.store.events[0].VersionID != versionID {
		t.Errorf("event keys = (%s, %s), want (%s, %s)",
			h.store.events[0].ListingID, h.store.events[0].VersionID, listingID, versionID)
	}
}

func TestInstall_BundleMissing(t *testing.T) {
	h := newHarness(t)
	seedListing(t, h.store, "akismet", "Akismet", "antispam")
	// Skip seeding the bundle — fetch returns ErrBundleNotFound.
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/install", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestInstall_CapabilityGate(t *testing.T) {
	h := newHarness(t)
	seedListing(t, h.store, "akismet", "Akismet", "antispam")
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/install", nil)
	pr := subscriberPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// Verify no event was recorded.
	if len(h.store.events) != 0 {
		t.Errorf("events = %+v, want none", h.store.events)
	}
}

func TestInstall_LifecycleFailureRecordsErroredEvent(t *testing.T) {
	h := newHarness(t)
	_, _ = seedListing(t, h.store, "akismet", "Akismet", "antispam")
	wasm := []byte("wasm:akismet")
	digest := sha256.Sum256(wasm)
	h.bundles.put(toHex(digest[:]), wasm)
	h.installer.err = errors.New("manifest rejected")

	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/install", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "manifest rejected") {
		t.Errorf("body = %s, want to include lifecycle error", rec.Body.String())
	}
	if len(h.store.events) != 1 || h.store.events[0].EventType != mp.EventErrored {
		t.Errorf("events = %+v, want one errored event", h.store.events)
	}
}

func TestInstall_RequiresExistingListing(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/nope/install", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSubmitRating_Happy(t *testing.T) {
	h := newHarness(t)
	_, versionID := seedListing(t, h.store, "akismet", "Akismet", "antispam")
	body, _ := json.Marshal(SubmitRatingRequest{
		VersionID:  versionID,
		Stars:      5,
		ReviewText: "Excellent.",
	})
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/ratings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// The aggregate should be visible via GET.
	getReq := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings/akismet/ratings", nil)
	getRec := h.do(getReq, &pr)
	if getRec.Code != http.StatusOK {
		t.Fatalf("ratings GET status = %d, want 200", getRec.Code)
	}
	var resp RatingsResponse
	_ = json.Unmarshal(getRec.Body.Bytes(), &resp)
	if resp.Aggregate.Count != 1 {
		t.Errorf("aggregate.count = %d, want 1", resp.Aggregate.Count)
	}
	if resp.Aggregate.Average != 5.0 {
		t.Errorf("aggregate.average = %f, want 5", resp.Aggregate.Average)
	}
}

func TestSubmitRating_RejectsOutOfRange(t *testing.T) {
	h := newHarness(t)
	_, versionID := seedListing(t, h.store, "akismet", "Akismet", "antispam")
	body, _ := json.Marshal(SubmitRatingRequest{
		VersionID: versionID,
		Stars:     9,
	})
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/ratings", bytes.NewReader(body))
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitRating_CapabilityGate(t *testing.T) {
	h := newHarness(t)
	_, versionID := seedListing(t, h.store, "akismet", "Akismet", "antispam")
	body, _ := json.Marshal(SubmitRatingRequest{VersionID: versionID, Stars: 4})
	req := httptest.NewRequest("POST", "/api/v1/admin/marketplace/listings/akismet/ratings", bytes.NewReader(body))
	pr := subscriberPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRatings_ListEmptyWhenNoVersions(t *testing.T) {
	h := newHarness(t)
	h.store.seedListing(mp.Listing{
		ID:        uuid.New(),
		Slug:      "lonely",
		Name:      "Lonely",
		Status:    mp.ListingListed,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	req := httptest.NewRequest("GET", "/api/v1/admin/marketplace/listings/lonely/ratings", nil)
	pr := adminPrincipal()
	rec := h.do(req, &pr)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp RatingsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Aggregate.Count != 0 {
		t.Errorf("aggregate.count = %d, want 0", resp.Aggregate.Count)
	}
}

func TestMount_RejectsMissingDeps(t *testing.T) {
	mux := http.NewServeMux()
	err := Mount(mux, "/x", Deps{})
	if err == nil {
		t.Fatalf("Mount with empty deps should return error")
	}
}

// toHex is a test-local mirror of the handler's hex.EncodeToString call,
// kept independent so a change in either spot fails the test loudly.
func toHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexChars[v>>4]
		out[i*2+1] = hexChars[v&0x0f]
	}
	return string(out)
}
