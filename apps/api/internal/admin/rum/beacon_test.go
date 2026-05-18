package rum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubStore is the test seam for tests that want to inspect what
// the beacon handler wrote, or to force an Insert failure to
// exercise the 500 path.
type stubStore struct {
	inserted   []Event
	insertErr  error
	insertCall int
}

func (s *stubStore) Insert(_ context.Context, _ time.Time, events []Event) error {
	s.insertCall++
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted = append(s.inserted, events...)
	return nil
}

func (s *stubStore) Percentiles(context.Context, string, string, time.Time, time.Time) (PercentileResult, error) {
	return PercentileResult{}, nil
}

func (s *stubStore) SlowestRoutes(context.Context, string, time.Time, time.Time, int) ([]RouteSlowRow, error) {
	return nil, nil
}

func TestBeacon_RejectsGET(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	req := httptest.NewRequest(http.MethodGet, "/_/rum/beacon", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405; got %d", rec.Code)
	}
	if rec.Header().Get("Allow") != "POST" {
		t.Fatalf("expected Allow: POST; got %q", rec.Header().Get("Allow"))
	}
}

func TestBeacon_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestBeacon_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestBeacon_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", strings.NewReader(`{"events":[],"bogus":true}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestBeacon_RejectsOversizeBody(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	body := make([]byte, MaxBodyBytes+1)
	for i := range body {
		body[i] = 'a'
	}
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413; got %d", rec.Code)
	}
}

func TestBeacon_RejectsEmptyBatch(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	body, _ := json.Marshal(Batch{Events: nil})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestBeacon_RejectsInvalidEvent(t *testing.T) {
	t.Parallel()
	h := newBeaconForTest(t, &stubStore{})
	bad := goodEvent()
	bad.Metric = "ZZZ"
	body, _ := json.Marshal(Batch{Events: []Event{bad}})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rec.Code)
	}
}

func TestBeacon_HappyPath_Persists(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	h := newBeaconForTest(t, store)
	body, _ := json.Marshal(Batch{Events: []Event{
		goodEvent(),
		{Metric: "CLS", Value: 0.05, Rating: "good", PagePath: "/x", SessionID: "h"},
	}})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204; got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(store.inserted) != 2 {
		t.Fatalf("expected 2 inserted; got %d", len(store.inserted))
	}
}

func TestBeacon_StoreErrorIs500(t *testing.T) {
	t.Parallel()
	store := &stubStore{insertErr: errors.New("boom")}
	h := newBeaconForTest(t, store)
	body, _ := json.Marshal(Batch{Events: []Event{goodEvent()}})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500; got %d", rec.Code)
	}
}

func TestBeacon_BatchAtMaxSize(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	h := newBeaconForTest(t, store)
	events := make([]Event, MaxBatchSize)
	for i := range events {
		events[i] = goodEvent()
	}
	body, _ := json.Marshal(Batch{Events: events})
	if int64(len(body)) >= MaxBodyBytes {
		t.Fatalf("test fixture exceeds body cap: %d", len(body))
	}
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for max batch; got %d", rec.Code)
	}
	if len(store.inserted) != MaxBatchSize {
		t.Fatalf("expected %d inserted; got %d", MaxBatchSize, len(store.inserted))
	}
}

func TestBeacon_BatchOverflowRejected(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	h := newBeaconForTest(t, store)
	events := make([]Event, MaxBatchSize+1)
	for i := range events {
		events[i] = goodEvent()
	}
	body, _ := json.Marshal(Batch{Events: events})
	req := httptest.NewRequest(http.MethodPost, "/_/rum/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on batch overflow; got %d", rec.Code)
	}
	if store.insertCall != 0 {
		t.Fatalf("expected store.Insert never called; got %d", store.insertCall)
	}
}

func TestBeacon_NewRequiresStore(t *testing.T) {
	t.Parallel()
	if _, err := NewBeaconHandler(nil, nil, nil); err == nil {
		t.Fatal("expected error when store is nil")
	}
}

func TestTrimSpace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"a", "a"},
		{"  abc  ", "abc"},
		{"\t\nab\r ", "ab"},
	}
	for _, tc := range cases {
		got := string(trimSpace([]byte(tc.in)))
		if got != tc.want {
			t.Fatalf("trimSpace(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func newBeaconForTest(t *testing.T, store EventStore) *BeaconHandler {
	t.Helper()
	h, err := NewBeaconHandler(store, func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }, nil)
	if err != nil {
		t.Fatalf("NewBeaconHandler: %v", err)
	}
	return h
}
