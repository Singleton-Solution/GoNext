package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// --- test doubles -----------------------------------------------------

// fakeStore is an in-memory SessionStore. It records every Delete so
// tests can assert on the exact tokens revoked and in what order, and
// lets a test inject failures (listErr, deleteErr) to drive the
// error-path branches.
type fakeStore struct {
	mu        sync.Mutex
	sessions  map[string][]session.SessionInfo // userID -> infos
	listErr   error
	deleteErr error
	deletes   []string // tokens passed to Delete, in call order
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: make(map[string][]session.SessionInfo)}
}

func (f *fakeStore) addSession(userID string, info session.SessionInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[userID] = append(f.sessions[userID], info)
}

func (f *fakeStore) List(_ context.Context, userID string) ([]session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	src := f.sessions[userID]
	out := make([]session.SessionInfo, len(src))
	copy(out, src)
	return out, nil
}

func (f *fakeStore) Delete(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, token)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	// Mirror the manager's behavior: drop the matching entry from
	// every user's list so a follow-up List sees the post-delete state.
	for uid, infos := range f.sessions {
		kept := infos[:0]
		for _, info := range infos {
			if info.Token != token {
				kept = append(kept, info)
			}
		}
		f.sessions[uid] = kept
	}
	return nil
}

// fakeEmitter records every Emit call without touching a store. We use
// it to assert audit emissions one-to-one with the revocations the
// handler performed.
type fakeEmitter struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func newFakeEmitter() *fakeEmitter { return &fakeEmitter{} }

func (e *fakeEmitter) Emit(_ context.Context, eventType string, opts ...audit.EmitOption) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	evt := audit.Event{EventType: eventType}
	for _, opt := range opts {
		opt(&evt)
	}
	e.events = append(e.events, evt)
	return nil
}

func (e *fakeEmitter) snapshot() []audit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]audit.Event, len(e.events))
	copy(out, e.events)
	return out
}

// --- helpers ---------------------------------------------------------

// authedRequest builds an http.Request with the given method/path, a
// principal carrying userID stashed on the context, and (optionally) a
// sid cookie set to currentToken.
func authedRequest(t *testing.T, method, path, userID, currentToken string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if userID != "" {
		req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: userID}))
	}
	if currentToken != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: currentToken})
	}
	return req
}

// makeSession returns a SessionInfo populated with the given token and
// reasonable defaults for the time fields. Tests that need specific
// timestamps overwrite the returned struct.
func makeSession(token, userID string, ageMinutes int) session.SessionInfo {
	now := time.Now().UTC()
	return session.SessionInfo{
		Token:      token,
		UserID:     userID,
		CreatedAt:  now.Add(-time.Duration(ageMinutes) * time.Minute),
		LastSeenAt: now,
	}
}

// decodeList parses the GET response body into a listResponse.
func decodeList(t *testing.T, body string) listResponse {
	t.Helper()
	var resp listResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode list response: %v\nbody: %s", err, body)
	}
	return resp
}

// findByID returns the session view with the given ID, or fails the
// test if it isn't present. Useful because list response order is
// unspecified.
func findByID(t *testing.T, views []SessionView, id string) SessionView {
	t.Helper()
	for _, v := range views {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("session ID %q not found among %d views", id, len(views))
	return SessionView{}
}

// newHandlers builds Handlers wired to the supplied fakes. Centralizing
// the constructor here keeps each test focused on behavior rather than
// dependency plumbing.
func newHandlers(store *fakeStore, emitter *fakeEmitter) *Handlers {
	return NewHandlers(store, emitter)
}

// --- List ------------------------------------------------------------

func TestList_ReturnsUserSessions(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.addSession("u1", makeSession("tok-b", "u1", 10))
	store.addSession("u2", makeSession("tok-c", "u2", 5)) // belongs to another user

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodGet, "/", "u1", "tok-a")
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control: got %q, want no-store", cc)
	}
	resp := decodeList(t, rec.Body.String())
	if len(resp.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(resp.Sessions), resp.Sessions)
	}
	idA := sessionIDFromToken("tok-a")
	idB := sessionIDFromToken("tok-b")
	// Both sessions should appear by ID; foreign-user session must not.
	a := findByID(t, resp.Sessions, idA)
	b := findByID(t, resp.Sessions, idB)
	if !a.Current {
		t.Errorf("session tok-a should be current (the sid cookie matches)")
	}
	if b.Current {
		t.Errorf("session tok-b must NOT be current")
	}
	// Verify u2's session is not in the response.
	for _, v := range resp.Sessions {
		if v.ID == sessionIDFromToken("tok-c") {
			t.Errorf("session from another user leaked into the response")
		}
	}
	// Audit log MUST be silent for reads — auth.session.revoked is a
	// write event.
	if events := emitter.snapshot(); len(events) != 0 {
		t.Errorf("List should not emit audit events, got %d", len(events))
	}
}

func TestList_NoCurrentCookie_NoneFlagged(t *testing.T) {
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.addSession("u1", makeSession("tok-b", "u1", 10))

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodGet, "/", "u1", "") // no cookie
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	resp := decodeList(t, rec.Body.String())
	for _, v := range resp.Sessions {
		if v.Current {
			t.Errorf("no sid cookie means no session can be 'current', got %+v", v)
		}
	}
}

func TestList_Unauthenticated_Returns401(t *testing.T) {
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no principal on ctx
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
}

func TestList_EmptyPrincipalUserID_Returns401(t *testing.T) {
	// Anonymous-principal case: policy.WithPrincipal called with the
	// zero Principal (UserID == ""). Our principalFromContext rejects
	// it so we fail closed.
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{}))
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
}

func TestList_StoreFailure_Returns500(t *testing.T) {
	store := newFakeStore()
	store.listErr = errors.New("redis: down")

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodGet, "/", "u1", "tok-a")
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("expected error JSON, got %s", rec.Body.String())
	}
}

func TestList_EmptyResult(t *testing.T) {
	// A user with no live sessions gets an empty array, NOT null.
	// The JSON shape matters for SPA clients that iterate the slice
	// without a nil check.
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodGet, "/", "u1", "")
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"sessions":[]`) {
		t.Errorf("empty result must serialize as [], got %s", rec.Body.String())
	}
}

// --- DeleteOne -------------------------------------------------------

func TestDeleteOne_OwnSession_Succeeds(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.addSession("u1", makeSession("tok-b", "u1", 10))

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	target := sessionIDFromToken("tok-b")
	req := authedRequest(t, http.MethodDelete, "/"+target, "u1", "tok-a")
	req.SetPathValue("id", target)
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.deletes) != 1 || store.deletes[0] != "tok-b" {
		t.Errorf("deletes: got %v, want [tok-b]", store.deletes)
	}
	// One auth.session.revoked event for the single revoked session.
	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(events))
	}
	ev := events[0]
	if ev.EventType != EventSessionRevoked {
		t.Errorf("event type: got %q, want %q", ev.EventType, EventSessionRevoked)
	}
	if ev.ActorUserID != "u1" {
		t.Errorf("actor: got %q, want u1", ev.ActorUserID)
	}
	if ev.ResourceType != "session" || ev.ResourceID != target {
		t.Errorf("resource: got %s/%s, want session/%s", ev.ResourceType, ev.ResourceID, target)
	}
	if bulk, _ := ev.Metadata["bulk"].(bool); bulk {
		t.Errorf("targeted revoke should have bulk=false, got %+v", ev.Metadata)
	}
}

func TestDeleteOne_ForeignSession_Returns404(t *testing.T) {
	// The spec is explicit: deleting a session that belongs to another
	// user MUST return 404, not 403, so we don't reveal that the
	// session ID exists for some other principal.
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.addSession("u2", makeSession("tok-foreign", "u2", 5))

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	target := sessionIDFromToken("tok-foreign")
	req := authedRequest(t, http.MethodDelete, "/"+target, "u1", "tok-a")
	req.SetPathValue("id", target)
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (NOT 403, to avoid leaking existence)", rec.Code)
	}
	if len(store.deletes) != 0 {
		t.Errorf("a foreign session was actually deleted: %v", store.deletes)
	}
	if events := emitter.snapshot(); len(events) != 0 {
		t.Errorf("no audit event should be emitted for a 404, got %d", len(events))
	}
}

func TestDeleteOne_UnknownID_Returns404(t *testing.T) {
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 30))

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "tok-a")
	req.SetPathValue("id", "deadbeef0000000000000000000000ff")
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
	if len(store.deletes) != 0 {
		t.Errorf("delete should not be called for unknown ID, got %v", store.deletes)
	}
}

func TestDeleteOne_EmptyPathValue_Returns404(t *testing.T) {
	// Defensive path: in production ServeMux's `DELETE /{id}` pattern
	// will not match an empty id segment, but a direct call from
	// outside the mux (or a future router) might. The handler must
	// fail closed.
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "")
	// Intentionally do NOT call SetPathValue.
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestDeleteOne_Unauthenticated_Returns401(t *testing.T) {
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/some-id", nil)
	req.SetPathValue("id", "some-id")
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestDeleteOne_StoreListFailure_Returns500(t *testing.T) {
	store := newFakeStore()
	store.listErr = errors.New("redis: connection refused")

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/some-id", "u1", "tok-a")
	req.SetPathValue("id", "some-id")
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestDeleteOne_StoreDeleteFailure_Returns500(t *testing.T) {
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.deleteErr = errors.New("redis: write error")

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	target := sessionIDFromToken("tok-a")
	req := authedRequest(t, http.MethodDelete, "/"+target, "u1", "")
	req.SetPathValue("id", target)
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestDeleteOne_AuditEmitFailure_StillReturns204(t *testing.T) {
	// Audit emit is best-effort: once the session is gone, returning
	// anything other than 204 would lie to the client. We log a
	// warning instead; the warning is exercised by the slog default
	// logger and not asserted on here.
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	emitter := newFakeEmitter()
	emitter.err = errors.New("audit: postgres down")

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	target := sessionIDFromToken("tok-a")
	req := authedRequest(t, http.MethodDelete, "/"+target, "u1", "")
	req.SetPathValue("id", target)
	h.DeleteOne(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204 even when audit emit fails", rec.Code)
	}
	if len(store.deletes) != 1 {
		t.Errorf("the session should still be deleted: %v", store.deletes)
	}
}

// --- DeleteAll -------------------------------------------------------

func TestDeleteAll_PreservesCurrentRevokesRest(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-current", "u1", 60))
	store.addSession("u1", makeSession("tok-other-1", "u1", 30))
	store.addSession("u1", makeSession("tok-other-2", "u1", 5))

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "tok-current")
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// Exactly the two non-current tokens should be deleted.
	if got := store.deletes; len(got) != 2 {
		t.Fatalf("delete count: got %d, want 2 (%v)", len(got), got)
	}
	for _, tok := range store.deletes {
		if tok == "tok-current" {
			t.Errorf("current session was revoked: deletes=%v", store.deletes)
		}
	}
	// Both non-current tokens should appear in the delete list.
	hasOther1, hasOther2 := false, false
	for _, tok := range store.deletes {
		if tok == "tok-other-1" {
			hasOther1 = true
		}
		if tok == "tok-other-2" {
			hasOther2 = true
		}
	}
	if !hasOther1 || !hasOther2 {
		t.Errorf("missing expected revocations: got=%v", store.deletes)
	}

	// One audit event per revoked session, all with bulk=true.
	events := emitter.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events: got %d, want 2", len(events))
	}
	for _, ev := range events {
		if ev.EventType != EventSessionRevoked {
			t.Errorf("event type: got %q", ev.EventType)
		}
		if ev.ActorUserID != "u1" {
			t.Errorf("actor: got %q", ev.ActorUserID)
		}
		if ev.ResourceType != "session" {
			t.Errorf("resource type: got %q", ev.ResourceType)
		}
		if bulk, _ := ev.Metadata["bulk"].(bool); !bulk {
			t.Errorf("bulk flag missing on bulk-revoke event: %+v", ev.Metadata)
		}
	}
}

func TestDeleteAll_NoCookie_RevokesAll(t *testing.T) {
	// Edge case: the caller is authenticated via some path that didn't
	// set the sid cookie (or it was stripped by an intermediary). We
	// have no way to identify a "current" session, so we revoke every
	// session — strictly more conservative for security purposes
	// (no live session left dangling).
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 60))
	store.addSession("u1", makeSession("tok-b", "u1", 30))

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "") // no cookie
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d", rec.Code)
	}
	if len(store.deletes) != 2 {
		t.Errorf("delete count: got %d, want 2", len(store.deletes))
	}
	if got := len(emitter.snapshot()); got != 2 {
		t.Errorf("audit count: got %d, want 2", got)
	}
}

func TestDeleteAll_OnlyCurrent_NoDeletes(t *testing.T) {
	// User has just one session, and it's the current one. Nothing to
	// revoke; the endpoint is a no-op that still returns 204.
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-only", "u1", 30))

	h := newHandlers(store, emitter)
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "tok-only")
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if len(store.deletes) != 0 {
		t.Errorf("nothing should be revoked, got %v", store.deletes)
	}
	if len(emitter.snapshot()) != 0 {
		t.Errorf("no audit events should be emitted")
	}
}

func TestDeleteAll_Unauthenticated_Returns401(t *testing.T) {
	h := newHandlers(newFakeStore(), newFakeEmitter())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestDeleteAll_ListFailure_Returns500(t *testing.T) {
	store := newFakeStore()
	store.listErr = errors.New("redis: down")

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "tok-a")
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestDeleteAll_DeleteFailureMidLoop_Returns500(t *testing.T) {
	// First Delete succeeds (the fake will append to deletes), the
	// second fails — but our fake doesn't change deleteErr mid-call,
	// so we set it from the start. Either branch must surface as 500.
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 60))
	store.addSession("u1", makeSession("tok-b", "u1", 30))
	store.deleteErr = errors.New("redis: write conflict")

	h := newHandlers(store, newFakeEmitter())
	rec := httptest.NewRecorder()
	req := authedRequest(t, http.MethodDelete, "/", "u1", "")
	h.DeleteAll(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// --- Routes ---------------------------------------------------------

func TestRoutes_GETListsSessions(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 5))

	h := newHandlers(store, emitter)
	srv := httptest.NewServer(injectPrincipal("u1", h.Routes()))
	defer srv.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok-a"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestRoutes_DELETEByIDRevokes(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-a", "u1", 30))
	store.addSession("u1", makeSession("tok-b", "u1", 5))

	h := newHandlers(store, emitter)
	srv := httptest.NewServer(injectPrincipal("u1", h.Routes()))
	defer srv.Close()

	target := sessionIDFromToken("tok-b")
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/"+target, nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok-a"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /{id}: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
	if len(store.deletes) != 1 || store.deletes[0] != "tok-b" {
		t.Errorf("deletes: got %v, want [tok-b]", store.deletes)
	}
}

func TestRoutes_DELETERootRevokesOthers(t *testing.T) {
	store := newFakeStore()
	emitter := newFakeEmitter()
	store.addSession("u1", makeSession("tok-current", "u1", 60))
	store.addSession("u1", makeSession("tok-other", "u1", 5))

	h := newHandlers(store, emitter)
	srv := httptest.NewServer(injectPrincipal("u1", h.Routes()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: "tok-current"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", resp.StatusCode)
	}
	if len(store.deletes) != 1 || store.deletes[0] != "tok-other" {
		t.Errorf("deletes: got %v, want [tok-other]", store.deletes)
	}
}

// --- ID derivation --------------------------------------------------

func TestSessionIDFromToken_DeterministicAndStable(t *testing.T) {
	a := sessionIDFromToken("abc")
	b := sessionIDFromToken("abc")
	if a != b {
		t.Errorf("ID derivation must be deterministic: %q vs %q", a, b)
	}
	c := sessionIDFromToken("xyz")
	if a == c {
		t.Errorf("distinct tokens collided: %q == %q", a, c)
	}
	// 32 hex chars = 16 bytes = 128 bits.
	if len(a) != 32 {
		t.Errorf("ID length: got %d, want 32", len(a))
	}
}

func TestSessionIDFromToken_EmptyTokenReturnsEmpty(t *testing.T) {
	if got := sessionIDFromToken(""); got != "" {
		t.Errorf("empty token must produce empty id, got %q", got)
	}
}

// --- helpers (test-only) --------------------------------------------

// injectPrincipal is the test substitute for the
// auth.RequireSession middleware. It attaches a fixed Principal so
// the routes-level tests can exercise the full mux without a real
// session.Manager.
func injectPrincipal(userID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := policy.WithPrincipal(r.Context(), policy.Principal{UserID: userID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TestNewHandlers_NilStorePanics confirms the constructor crashes
// early on a wiring bug rather than letting nil propagate to a request
// that 500s with no useful log line.
func TestNewHandlers_NilStorePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil store")
		}
	}()
	_ = NewHandlers(nil, newFakeEmitter())
}

func TestNewHandlers_NilEmitterPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil emitter")
		}
	}()
	_ = NewHandlers(newFakeStore(), nil)
}

// TestWithCookieName_Overrides confirms the option seam works. We
// don't need to exercise WithLogger here — slog is well-tested and
// our handler's only logger touch is on the error paths, which other
// tests already drive.
func TestWithCookieName_Overrides(t *testing.T) {
	store := newFakeStore()
	store.addSession("u1", makeSession("tok-a", "u1", 30))

	h := NewHandlers(store, newFakeEmitter(), WithCookieName("admin_sid"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(policy.WithPrincipal(req.Context(), policy.Principal{UserID: "u1"}))
	// Use the override name, not the default — the default cookie
	// should NOT mark the session as current under this configuration.
	req.AddCookie(&http.Cookie{Name: "admin_sid", Value: "tok-a"})
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	resp := decodeList(t, rec.Body.String())
	if len(resp.Sessions) != 1 || !resp.Sessions[0].Current {
		t.Errorf("custom cookie name should be honored, resp=%+v", resp)
	}
}
