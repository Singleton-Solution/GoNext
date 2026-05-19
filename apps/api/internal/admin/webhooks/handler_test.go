package webhooks

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/webhooks/delivery"
)

// fixedClock returns a function returning the same UTC instant on
// every call. Used to make signatures deterministic across runs.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// counterIDGen produces sequential string IDs ("sub-1", "sub-2", …).
// The store needs only "unique enough"; sequential IDs are easier to
// assert against than UUIDs.
func counterIDGen() func() string {
	var n atomic.Int64
	return func() string {
		v := n.Add(1)
		return "sub-" + strconv.FormatInt(v, 10)
	}
}

type testHarness struct {
	mux      *http.ServeMux
	store    *MemoryStore
	policy   policy.Policy
	clock    time.Time
	httpDo   *recordingClient
	deliverT *httptest.Server // optional subscriber target the test endpoint hits
}

// recordingClient is an http.RoundTripper that snapshots every
// request body before delegating to the real transport. Used by the
// Test endpoint case to assert on what we put on the wire.
type recordingClient struct {
	mu        sync.Mutex
	bodies    [][]byte
	transport http.RoundTripper
}

func (c *recordingClient) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return c.transport.RoundTrip(req)
}

func newTestHarness(t *testing.T, target http.Handler) *testHarness {
	t.Helper()
	clock := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore(WithClock(fixedClock(clock)), WithIDGen(counterIDGen()))
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	var server *httptest.Server
	if target != nil {
		server = httptest.NewServer(target)
		t.Cleanup(server.Close)
	}
	rc := &recordingClient{transport: http.DefaultTransport}
	httpClient := &http.Client{Transport: rc, Timeout: 5 * time.Second}
	if err := Mount(mux, "/api/v1/admin/webhooks", Deps{
		Store:      store,
		Policy:     pol,
		HTTPClient: httpClient,
		Now:        fixedClock(clock),
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &testHarness{
		mux:      mux,
		store:    store,
		policy:   pol,
		clock:    clock,
		httpDo:   rc,
		deliverT: server,
	}
}

func adminPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:1", Roles: []policy.Role{policy.RoleAdmin}}
}

func authorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleAuthor}}
}

func (h *testHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// -----------------------------------------------------------------------------
// CREATE
// -----------------------------------------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	h := newTestHarness(t, nil)
	body := []byte(`{"name":"orders","url":"https://example.com/hook","events":["post.published"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/webhooks", bytes.NewReader(body))
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got SubscriptionWithSecret
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "orders" {
		t.Errorf("name: got %q, want orders", got.Name)
	}
	if !got.Active {
		t.Errorf("active: got false, want true (default)")
	}
	if got.Secret == "" {
		t.Error("secret: want non-empty hex string on first reveal")
	}
	if _, err := hex.DecodeString(got.Secret); err != nil {
		t.Errorf("secret: not hex: %v", err)
	}
	if got.CreatedBy != "user:1" {
		t.Errorf("created_by: got %q, want user:1", got.CreatedBy)
	}
}

func TestCreate_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing name", `{"url":"https://example.com","events":["post.published"]}`},
		{"missing url", `{"name":"x","events":["post.published"]}`},
		{"bad scheme", `{"name":"x","url":"ftp://example.com","events":[]}`},
		{"relative url", `{"name":"x","url":"/relative","events":[]}`},
		{"unknown event", `{"name":"x","url":"https://example.com","events":["not.real"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, nil)
			req := httptest.NewRequest("POST", "/api/v1/admin/webhooks", strings.NewReader(tc.body))
			rec := h.do(req, ptr(adminPrincipal()))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// -----------------------------------------------------------------------------
// LIST / GET / UPDATE / DELETE
// -----------------------------------------------------------------------------

func seedSub(t *testing.T, h *testHarness, name string) Subscription {
	t.Helper()
	body, _ := json.Marshal(SubscriptionCreate{
		Name:   name,
		URL:    "https://example.com/" + name,
		Events: []string{"post.published"},
	})
	req := httptest.NewRequest("POST", "/api/v1/admin/webhooks", bytes.NewReader(body))
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed %s: status %d; body=%s", name, rec.Code, rec.Body.String())
	}
	var sub SubscriptionWithSecret
	if err := json.Unmarshal(rec.Body.Bytes(), &sub); err != nil {
		t.Fatalf("seed unmarshal: %v", err)
	}
	return sub.Subscription
}

func TestList_ReturnsCreatedSubs(t *testing.T) {
	h := newTestHarness(t, nil)
	seedSub(t, h, "alpha")
	seedSub(t, h, "beta")
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Subscription]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 2 {
		t.Errorf("data: got %d, want 2", len(page.Data))
	}
}

func TestGet_HappyPath(t *testing.T) {
	h := newTestHarness(t, nil)
	sub := seedSub(t, h, "alpha")
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks/"+sub.ID, nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGet_NotFound(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks/missing", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdate_PartialPatch(t *testing.T) {
	h := newTestHarness(t, nil)
	sub := seedSub(t, h, "alpha")
	body := []byte(`{"name":"renamed"}`)
	req := httptest.NewRequest("PATCH", "/api/v1/admin/webhooks/"+sub.ID, bytes.NewReader(body))
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got Subscription
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("name: got %q, want renamed", got.Name)
	}
	if got.URL != sub.URL {
		t.Errorf("url should be unchanged: got %q, want %q", got.URL, sub.URL)
	}
}

func TestDelete_HappyPath(t *testing.T) {
	h := newTestHarness(t, nil)
	sub := seedSub(t, h, "alpha")
	req := httptest.NewRequest("DELETE", "/api/v1/admin/webhooks/"+sub.ID, nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// Now GET should 404.
	getReq := httptest.NewRequest("GET", "/api/v1/admin/webhooks/"+sub.ID, nil)
	getRec := h.do(getReq, ptr(adminPrincipal()))
	if getRec.Code != http.StatusNotFound {
		t.Errorf("after delete, GET: got %d, want 404", getRec.Code)
	}
}

func TestDisableEnable_FlipActive(t *testing.T) {
	h := newTestHarness(t, nil)
	sub := seedSub(t, h, "alpha")
	disable := httptest.NewRequest("POST", "/api/v1/admin/webhooks/"+sub.ID+"/disable", nil)
	rec := h.do(disable, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d; body=%s", rec.Code, rec.Body.String())
	}
	var afterDisable Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &afterDisable)
	if afterDisable.Active {
		t.Errorf("active: still true after disable")
	}
	enable := httptest.NewRequest("POST", "/api/v1/admin/webhooks/"+sub.ID+"/enable", nil)
	rec = h.do(enable, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d; body=%s", rec.Code, rec.Body.String())
	}
	var afterEnable Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &afterEnable)
	if !afterEnable.Active {
		t.Errorf("active: still false after enable")
	}
}

// -----------------------------------------------------------------------------
// TEST ENDPOINT
// -----------------------------------------------------------------------------

func TestTest_DeliversAndSigns(t *testing.T) {
	var (
		gotSig       string
		gotEventType string
		gotBody      []byte
	)
	target := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(delivery.SignatureHeader)
		gotEventType = r.Header.Get(delivery.EventTypeHeader)
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	h := newTestHarness(t, target)

	// Seed a subscription whose URL points at the httptest target.
	createBody, _ := json.Marshal(SubscriptionCreate{
		Name:   "test-target",
		URL:    h.deliverT.URL,
		Events: []string{"post.published"},
	})
	createReq := httptest.NewRequest("POST", "/api/v1/admin/webhooks", bytes.NewReader(createBody))
	createRec := h.do(createReq, ptr(adminPrincipal()))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("seed: %d; body=%s", createRec.Code, createRec.Body.String())
	}
	var seed SubscriptionWithSecret
	if err := json.Unmarshal(createRec.Body.Bytes(), &seed); err != nil {
		t.Fatalf("seed unmarshal: %v", err)
	}

	// Trigger the Test endpoint.
	testReq := httptest.NewRequest("POST", "/api/v1/admin/webhooks/"+seed.ID+"/test", nil)
	testRec := h.do(testReq, ptr(adminPrincipal()))
	if testRec.Code != http.StatusOK {
		t.Fatalf("test: %d; body=%s", testRec.Code, testRec.Body.String())
	}
	var res TestResult
	if err := json.Unmarshal(testRec.Body.Bytes(), &res); err != nil {
		t.Fatalf("result unmarshal: %v", err)
	}
	if !res.Delivered {
		t.Errorf("delivered: got false")
	}
	if res.ResponseCode != http.StatusOK {
		t.Errorf("response_code: got %d, want 200", res.ResponseCode)
	}

	// Verify the signature header round-trips through the delivery
	// package's Verify so the contract with subscribers stays whole.
	if gotSig == "" {
		t.Fatal("signature header was not set on the request")
	}
	secret, _ := hex.DecodeString(seed.Secret)
	if err := delivery.Verify(secret, gotBody, gotSig, h.clock, 5*time.Minute); err != nil {
		t.Errorf("verify: %v", err)
	}
	if gotEventType != EventTypeTest {
		t.Errorf("event type header: got %q, want %q", gotEventType, EventTypeTest)
	}
}

func TestTest_SubscriptionMissing(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("POST", "/api/v1/admin/webhooks/missing/test", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// DELIVERIES
// -----------------------------------------------------------------------------

func TestDeliveries_PaginatesNewestFirst(t *testing.T) {
	h := newTestHarness(t, nil)
	sub := seedSub(t, h, "alpha")
	// Seed 5 delivery rows directly via the store.
	base := h.clock
	for i := 0; i < 5; i++ {
		_ = h.store.RecordDelivery(context.Background(), Delivery{
			SubscriptionID: sub.ID,
			EventID:        "evt-" + strconv.Itoa(i),
			EventType:      "post.published",
			Attempt:        1,
			Status:         "success",
			ResponseCode:   200,
			DurationMs:     42,
			DeliveredAt:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks/"+sub.ID+"/deliveries?limit=2", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[Delivery]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 2 {
		t.Errorf("data: got %d, want 2", len(page.Data))
	}
	if page.Pagination.NextCursor == "" {
		t.Error("next_cursor: want non-empty (more pages)")
	}
	// newest first
	if !page.Data[0].DeliveredAt.After(page.Data[1].DeliveredAt) {
		t.Errorf("order: want newest first, got %v before %v", page.Data[0].DeliveredAt, page.Data[1].DeliveredAt)
	}
}

func TestDeliveries_UnknownSubscription(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks/missing/deliveries", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// AUTH
// -----------------------------------------------------------------------------

func TestAuth_AnonymousIsUnauthorized(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestAuth_NonAdminForbidden(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
}

func TestAuth_AdminCanList(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

// -----------------------------------------------------------------------------
// EVENT CATALOG
// -----------------------------------------------------------------------------

func TestEvents_ReturnsCatalog(t *testing.T) {
	h := newTestHarness(t, nil)
	req := httptest.NewRequest("GET", "/api/v1/admin/webhooks/events", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data []EventDescriptor `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Data) == 0 {
		t.Fatal("data: empty catalog")
	}
	// The reserved test event must always be present.
	var hasTest bool
	for _, e := range got.Data {
		if e.Name == EventTypeTest {
			hasTest = true
			break
		}
	}
	if !hasTest {
		t.Error("catalog missing reserved webhook.test event")
	}
}

func ptr[T any](v T) *T { return &v }
