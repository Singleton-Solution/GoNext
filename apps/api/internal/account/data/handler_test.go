package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// --- fakes ------------------------------------------------------------

type fakeVerifier struct {
	ok  bool
	err error
}

func (f *fakeVerifier) Verify(_ context.Context, _, _ string) (bool, error) {
	return f.ok, f.err
}

type fakeAnonymizer struct {
	mu     sync.Mutex
	called []string
	err    error
}

func (f *fakeAnonymizer) Anonymize(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, userID)
	return f.err
}

type fakeEnqueuer struct {
	mu     sync.Mutex
	called []struct{ UserID, JobID string }
	err    error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, userID, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, struct{ UserID, JobID string }{userID, jobID})
	return f.err
}

type fakeAudit struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeAudit) Emit(_ context.Context, eventType string, _ ...audit.EmitOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, eventType)
	return nil
}

// --- helpers ----------------------------------------------------------

func newTestHandlers(t *testing.T, v PasswordVerifier, a Anonymizer, e ExportEnqueuer) (*Handlers, *fakeAudit) {
	t.Helper()
	au := &fakeAudit{}
	h := NewHandlers(Deps{
		Verifier:    v,
		Anonymizer:  a,
		Enqueuer:    e,
		Audit:       au,
		PollURLBase: "https://api.example.com",
	})
	return h, au
}

func withPrincipal(r *http.Request, userID string) *http.Request {
	ctx := policy.WithPrincipal(r.Context(), policy.Principal{UserID: userID})
	return r.WithContext(ctx)
}

// --- tests ------------------------------------------------------------

func TestExport_Happy_EnqueuesAndReturns202(t *testing.T) {
	enq := &fakeEnqueuer{}
	h, au := newTestHandlers(t, &fakeVerifier{}, &fakeAnonymizer{}, enq)

	req := httptest.NewRequest(http.MethodGet, "/export", nil)
	req = withPrincipal(req, "user-42")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want %d (body=%s)", rr.Code, http.StatusAccepted, rr.Body.String())
	}
	var resp ExportResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID == "" {
		t.Errorf("empty job_id")
	}
	if resp.Status != "queued" {
		t.Errorf("status = %q, want queued", resp.Status)
	}
	if !strings.Contains(resp.PollURL, resp.JobID) {
		t.Errorf("poll_url %q does not contain job_id %q", resp.PollURL, resp.JobID)
	}

	if len(enq.called) != 1 {
		t.Fatalf("enqueue called %d times, want 1", len(enq.called))
	}
	if enq.called[0].UserID != "user-42" {
		t.Errorf("enqueue user_id = %q, want user-42", enq.called[0].UserID)
	}
	if len(au.events) != 1 || au.events[0] != EventDataExportRequested {
		t.Errorf("audit events = %v, want [%s]", au.events, EventDataExportRequested)
	}
}

func TestExport_NoPrincipal_401(t *testing.T) {
	h, _ := newTestHandlers(t, &fakeVerifier{}, &fakeAnonymizer{}, &fakeEnqueuer{})
	req := httptest.NewRequest(http.MethodGet, "/export", nil)
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestExport_EnqueueFails_503(t *testing.T) {
	enq := &fakeEnqueuer{err: errors.New("redis down")}
	h, _ := newTestHandlers(t, &fakeVerifier{}, &fakeAnonymizer{}, enq)

	req := httptest.NewRequest(http.MethodGet, "/export", nil)
	req = withPrincipal(req, "user-1")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestDelete_Happy(t *testing.T) {
	anon := &fakeAnonymizer{}
	h, au := newTestHandlers(t, &fakeVerifier{ok: true}, anon, &fakeEnqueuer{})

	body := bytes.NewBufferString(`{"password":"sekret","password_confirm":"sekret"}`)
	req := httptest.NewRequest(http.MethodPost, "/delete", body)
	req = withPrincipal(req, "user-42")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if len(anon.called) != 1 || anon.called[0] != "user-42" {
		t.Errorf("anonymize called with %v, want [user-42]", anon.called)
	}
	if len(au.events) != 1 || au.events[0] != EventDataDeleteSucceeded {
		t.Errorf("audit events = %v, want [%s]", au.events, EventDataDeleteSucceeded)
	}
}

func TestDelete_PasswordMismatch_400(t *testing.T) {
	h, _ := newTestHandlers(t, &fakeVerifier{ok: true}, &fakeAnonymizer{}, &fakeEnqueuer{})
	body := bytes.NewBufferString(`{"password":"a","password_confirm":"b"}`)
	req := httptest.NewRequest(http.MethodPost, "/delete", body)
	req = withPrincipal(req, "u")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestDelete_WrongPassword_401(t *testing.T) {
	anon := &fakeAnonymizer{}
	h, au := newTestHandlers(t, &fakeVerifier{ok: false}, anon, &fakeEnqueuer{})
	body := bytes.NewBufferString(`{"password":"x","password_confirm":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/delete", body)
	req = withPrincipal(req, "u")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if len(anon.called) != 0 {
		t.Errorf("anonymizer must NOT be called on wrong password; got calls=%v", anon.called)
	}
	if len(au.events) != 1 || au.events[0] != EventDataDeleteFailed {
		t.Errorf("audit events = %v, want [%s]", au.events, EventDataDeleteFailed)
	}
}

func TestDelete_MissingBody_400(t *testing.T) {
	h, _ := newTestHandlers(t, &fakeVerifier{ok: true}, &fakeAnonymizer{}, &fakeEnqueuer{})
	req := httptest.NewRequest(http.MethodPost, "/delete", bytes.NewBufferString(`{}`))
	req = withPrincipal(req, "u")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
