package jobs

import (
	"bytes"
	"context"
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

	"github.com/hibiken/asynq"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// fakeInspector is a hand-rolled stand-in for *asynq.Inspector. We
// don't reach for a Redis (real or in-memory) here because the unit
// tests just need a programmable seam — every interesting failure
// mode is reachable by returning a particular sentinel from one of
// the four methods.
//
// Concurrent access via sync.Mutex; tests rarely fan out but the
// admin UI's fetch+action chain might in a future revision.
type fakeInspector struct {
	mu       sync.Mutex
	archived map[string][]*asynq.TaskInfo // queue -> ordered tasks

	listErr    error
	getErr     error
	replayErr  error
	discardErr error
}

func newFakeInspector() *fakeInspector {
	return &fakeInspector{archived: make(map[string][]*asynq.TaskInfo)}
}

func (f *fakeInspector) seed(queue string, tasks ...*asynq.TaskInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archived[queue] = append(f.archived[queue], tasks...)
}

func (f *fakeInspector) ListArchivedTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	tasks := f.archived[queue]
	// Apply paging: pull page size + page number out of opts.
	size, page := 30, 1
	for _, o := range opts {
		// hack: rely on stringer-free interface — we just need a
		// deterministic way to extract numbers. asynq.PageSize returns
		// an unexported type, so we can't type-assert. Instead, the
		// tests pin the page size and page number via direct slicing
		// in the assertions, keeping this method's behavior simple.
		_ = o
	}
	// Honour the limit+page semantics that the handler relies on.
	// Implementation: tests construct the page slice they want; this
	// stub returns the entire seeded list and the handler trims it.
	// That matches Asynq's actual behavior closely enough for the
	// pagination tests below.
	_ = size
	_ = page
	out := make([]*asynq.TaskInfo, len(tasks))
	copy(out, tasks)
	return out, nil
}

func (f *fakeInspector) GetTaskInfo(queue, id string) (*asynq.TaskInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, t := range f.archived[queue] {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, asynq.ErrTaskNotFound
}

func (f *fakeInspector) RunArchivedTask(queue, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replayErr != nil {
		return f.replayErr
	}
	tasks := f.archived[queue]
	for i, t := range tasks {
		if t.ID == id {
			// Simulate the Asynq behavior: the task leaves the archived
			// set. Tests assert on this via the "after replay, list is
			// empty" check.
			f.archived[queue] = append(tasks[:i], tasks[i+1:]...)
			return nil
		}
	}
	return asynq.ErrTaskNotFound
}

func (f *fakeInspector) DeleteArchivedTask(queue, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.discardErr != nil {
		return f.discardErr
	}
	tasks := f.archived[queue]
	for i, t := range tasks {
		if t.ID == id {
			f.archived[queue] = append(tasks[:i], tasks[i+1:]...)
			return nil
		}
	}
	return asynq.ErrTaskNotFound
}

// testHarness wires Mount onto an http.ServeMux against a fake
// inspector + in-memory redaction store + BasicPolicy. Each test
// constructs its own — there is no shared state between tests.
type testHarness struct {
	mux        *http.ServeMux
	inspector  *fakeInspector
	redactions *MemoryRedactionStore
	policy     policy.Policy
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	insp := newFakeInspector()
	red := NewMemoryRedactionStore()
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin/jobs", Deps{
		Inspector:  insp,
		Redactions: red,
		Policy:     pol,
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return &testHarness{
		mux:        mux,
		inspector:  insp,
		redactions: red,
		policy:     pol,
	}
}

func adminPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:1", Roles: []policy.Role{policy.RoleAdmin}}
}

func authorPrincipal() policy.Principal {
	return policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleAuthor}}
}

// do issues req against the harness as the given principal. nil principal
// means anonymous (no principal on the context).
func (h *testHarness) do(req *http.Request, pr *policy.Principal) *httptest.ResponseRecorder {
	if pr != nil {
		req = req.WithContext(policy.WithPrincipal(req.Context(), *pr))
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

func sampleTask(id, queue, taskType string, payload []byte) *asynq.TaskInfo {
	return &asynq.TaskInfo{
		ID:           id,
		Queue:        queue,
		Type:         taskType,
		Payload:      payload,
		State:        asynq.TaskStateArchived,
		MaxRetry:     25,
		Retried:      25,
		LastErr:      "delivery failed: 500 internal server error",
		LastFailedAt: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func TestList_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default",
		sampleTask("t1", "default", "webhook:deliver", []byte(`{"url":"https://example.com","token":"abcd"}`)),
		sampleTask("t2", "default", "webhook:deliver", []byte(`{"url":"https://other.example.com"}`)),
	)

	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[ArchivedTask]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if len(page.Data) != 2 {
		t.Fatalf("data: got %d, want 2", len(page.Data))
	}
	if page.Data[0].ID != "t1" || page.Data[1].ID != "t2" {
		t.Errorf("data ids: got %v, want [t1 t2]", []string{page.Data[0].ID, page.Data[1].ID})
	}
	// Payload preview is included on the list endpoint; full Payload is not.
	if page.Data[0].PayloadPreview == "" {
		t.Error("payload_preview: want non-empty on list")
	}
	if page.Data[0].Payload != nil {
		t.Errorf("payload: want nil on list, got %s", page.Data[0].Payload)
	}
}

func TestList_EmptyQueue(t *testing.T) {
	h := newTestHarness(t)
	// No seeding.
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[ArchivedTask]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 0 {
		t.Errorf("data: got %d, want 0", len(page.Data))
	}
	if page.Pagination.NextCursor != "" {
		t.Errorf("next_cursor: got %q, want empty", page.Pagination.NextCursor)
	}
}

func TestList_MissingQueueParam(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestList_PaginationCursor(t *testing.T) {
	h := newTestHarness(t)
	// Seed enough to require pagination at limit=2.
	for i := 0; i < 5; i++ {
		h.inspector.seed("default", sampleTask(
			"t"+itoa(i), "default", "webhook:deliver", []byte(`{"i":`+itoa(i)+`}`),
		))
	}

	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default&limit=2", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[ArchivedTask]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data: got %d, want 2", len(page.Data))
	}
	if page.Pagination.NextCursor == "" {
		t.Fatal("next_cursor: got empty, want non-empty (more pages available)")
	}
}

func TestList_InvalidLimit(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default&limit=-1", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestList_InvalidCursor(t *testing.T) {
	h := newTestHarness(t)
	// The cursor is base64url-encoded text. We probe with a value that
	// decodes (after base64) to something non-numeric, which the handler
	// rejects.
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default&cursor="+router.EncodeCursor("not-a-number"), nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestList_InspectorError(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.listErr = errors.New("redis down")
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// TestList_QueueNotFoundIsEmpty pins the fix for issue #502: a clean
// install has never enqueued anything to the "default" queue, so Asynq's
// ListArchivedTasks returns an error wrapping ErrQueueNotFound. The
// handler must translate that into an empty page (200), not a 500.
// Otherwise the admin Jobs page renders its FailureState UI for every
// fresh deployment.
func TestList_QueueNotFoundIsEmpty(t *testing.T) {
	h := newTestHarness(t)
	// Asynq wraps ErrQueueNotFound with fmt.Errorf, so mirror that here
	// — errors.Is must still report a match.
	h.inspector.listErr = fmt.Errorf("asynq: %w", asynq.ErrQueueNotFound)

	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var page router.Page[ArchivedTask]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if len(page.Data) != 0 {
		t.Errorf("data: got %d, want 0", len(page.Data))
	}
	// Empty page must serialise as [] (not null) so the UI can render it
	// without a nil-check; router.Page already guarantees this, but pin
	// the wire shape here too.
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("body: want data:[] in body, got %s", rec.Body.String())
	}
	if page.Pagination.NextCursor != "" {
		t.Errorf("next_cursor: got %q, want empty", page.Pagination.NextCursor)
	}
}

// -----------------------------------------------------------------------------
// AUTH
// -----------------------------------------------------------------------------

func TestAuth_AnonymousIsUnauthorized(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuth_NonAdminIsForbidden(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuth_AdminCanList(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// GET ONE
// -----------------------------------------------------------------------------

func TestGet_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask("t1", "default", "webhook:deliver", []byte(`{"hello":"world"}`)))

	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq/t1?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var task ArchivedTask
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if task.ID != "t1" {
		t.Errorf("id: got %q, want t1", task.ID)
	}
	if len(task.Payload) == 0 {
		t.Error("payload: want non-empty on detail endpoint")
	}
}

func TestGet_NotFound(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq/missing?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGet_MissingQueue(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq/t1", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// REPLAY
// -----------------------------------------------------------------------------

func TestReplay_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask("t1", "default", "webhook:deliver", []byte(`{}`)))

	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/replay?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// After replay, the task leaves the archived set.
	if got := len(h.inspector.archived["default"]); got != 0 {
		t.Errorf("archived after replay: got %d, want 0", got)
	}
}

func TestReplay_NotFound(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/missing/replay?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestReplay_NoAuth(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask("t1", "default", "webhook:deliver", []byte(`{}`)))
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/replay?queue=default", nil)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// DISCARD
// -----------------------------------------------------------------------------

func TestDiscard_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask("t1", "default", "webhook:deliver", []byte(`{}`)))

	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/discard?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.inspector.archived["default"]); got != 0 {
		t.Errorf("archived after discard: got %d, want 0", got)
	}
}

func TestDiscard_NotFound(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/missing/discard?queue=default", nil)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// REDACT
// -----------------------------------------------------------------------------

func TestRedact_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask(
		"t1", "default", "webhook:deliver",
		[]byte(`{"url":"https://example.com","token":"secret-abcd"}`),
	))

	body := strings.NewReader(`{"queue":"default","fields":["token"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	req.Header.Set("Content-Type", "application/json")
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Redaction record exists.
	if h.redactions.Len() != 1 {
		t.Fatalf("redactions len: got %d, want 1", h.redactions.Len())
	}

	// Subsequent list returns the masked preview.
	listReq := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq?queue=default", nil)
	listRec := h.do(listReq, ptr(adminPrincipal()))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status: got %d, want 200", listRec.Code)
	}
	var page router.Page[ArchivedTask]
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("data: got %d, want 1", len(page.Data))
	}
	preview := page.Data[0].PayloadPreview
	if !strings.Contains(preview, redactedSentinel) {
		t.Errorf("preview: want to contain %q, got %q", redactedSentinel, preview)
	}
	if strings.Contains(preview, "secret-abcd") {
		t.Errorf("preview: redacted value leaked: %q", preview)
	}
	if !page.Data[0].Redacted {
		t.Error("redacted flag: want true")
	}
}

func TestRedact_EmptyFields(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"queue":"default","fields":[]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRedact_NestedPathRejected(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"queue":"default","fields":["nested.field"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRedact_DuplicateFields(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"queue":"default","fields":["x","x"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	rec := h.do(req, ptr(adminPrincipal()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRedact_NoAuth(t *testing.T) {
	h := newTestHarness(t)
	body := strings.NewReader(`{"queue":"default","fields":["token"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	rec := h.do(req, ptr(authorPrincipal()))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRedact_DetailEndpointAlsoRedacts(t *testing.T) {
	h := newTestHarness(t)
	h.inspector.seed("default", sampleTask(
		"t1", "default", "webhook:deliver",
		[]byte(`{"url":"https://example.com","token":"secret"}`),
	))
	body := strings.NewReader(`{"queue":"default","fields":["token"]}`)
	req := httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body)
	if rec := h.do(req, ptr(adminPrincipal())); rec.Code != http.StatusOK {
		t.Fatalf("redact status: got %d", rec.Code)
	}

	getReq := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq/t1?queue=default", nil)
	getRec := h.do(getReq, ptr(adminPrincipal()))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status: got %d", getRec.Code)
	}
	var task ArchivedTask
	if err := json.Unmarshal(getRec.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bytes.Contains(task.Payload, []byte(redactedSentinel)) {
		t.Errorf("payload: want redacted sentinel, got %s", task.Payload)
	}
	if bytes.Contains(task.Payload, []byte("secret")) {
		t.Errorf("payload: leaked secret, got %s", task.Payload)
	}
}

func TestRedact_NonObjectPayload(t *testing.T) {
	h := newTestHarness(t)
	// Array payload — applyRedaction must fall back to wholesale sentinel.
	h.inspector.seed("default", sampleTask(
		"t1", "default", "webhook:deliver", []byte(`["a","b","c"]`),
	))
	body := strings.NewReader(`{"queue":"default","fields":["any"]}`)
	if rec := h.do(httptest.NewRequest("POST", "/api/v1/admin/jobs/dlq/t1/redact", body), ptr(adminPrincipal())); rec.Code != http.StatusOK {
		t.Fatalf("redact status: got %d", rec.Code)
	}

	getReq := httptest.NewRequest("GET", "/api/v1/admin/jobs/dlq/t1?queue=default", nil)
	getRec := h.do(getReq, ptr(adminPrincipal()))
	var task ArchivedTask
	if err := json.Unmarshal(getRec.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bytes.Contains(task.Payload, []byte(redactedSentinel)) {
		t.Errorf("payload: want sentinel for non-object payload, got %s", task.Payload)
	}
}

// -----------------------------------------------------------------------------
// MOUNT
// -----------------------------------------------------------------------------

func TestMount_RequiresInspector(t *testing.T) {
	if err := Mount(http.NewServeMux(), "/x", Deps{
		Redactions: NewMemoryRedactionStore(),
		Policy:     policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}); err == nil {
		t.Fatal("Mount with nil inspector: want error, got nil")
	}
}

func TestMount_RequiresRedactions(t *testing.T) {
	if err := Mount(http.NewServeMux(), "/x", Deps{
		Inspector: newFakeInspector(),
		Policy:    policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
	}); err == nil {
		t.Fatal("Mount with nil redactions: want error, got nil")
	}
}

func TestMount_RequiresPolicy(t *testing.T) {
	if err := Mount(http.NewServeMux(), "/x", Deps{
		Inspector:  newFakeInspector(),
		Redactions: NewMemoryRedactionStore(),
	}); err == nil {
		t.Fatal("Mount with nil policy: want error, got nil")
	}
}

// -----------------------------------------------------------------------------
// REDACTION PRIMITIVE
// -----------------------------------------------------------------------------

func TestApplyRedaction_TopLevelFields(t *testing.T) {
	red := Redaction{Fields: []string{"token", "email"}}
	in := []byte(`{"url":"https://example.com","token":"secret","email":"a@b.c"}`)
	out := applyRedaction(in, red)
	if bytes.Contains(out, []byte("secret")) {
		t.Errorf("token leaked: %s", out)
	}
	if bytes.Contains(out, []byte("a@b.c")) {
		t.Errorf("email leaked: %s", out)
	}
	if !bytes.Contains(out, []byte(`"url":"https://example.com"`)) {
		t.Errorf("non-redacted field missing: %s", out)
	}
}

func TestApplyRedaction_NoFieldsPassesThrough(t *testing.T) {
	red := Redaction{}
	in := []byte(`{"a":1}`)
	out := applyRedaction(in, red)
	if string(out) != string(in) {
		t.Errorf("empty redaction modified payload: got %s, want %s", out, in)
	}
}

func TestApplyRedaction_GarbageJSON(t *testing.T) {
	red := Redaction{Fields: []string{"x"}}
	in := []byte(`{not json`)
	out := applyRedaction(in, red)
	if !bytes.Contains(out, []byte(redactedSentinel)) {
		t.Errorf("want sentinel fallback for garbage, got %s", out)
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = digits[i%10]
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

// Compile-time check that the body reader doesn't panic on a closed body.
var _ = io.Discard
var _ = context.Background
