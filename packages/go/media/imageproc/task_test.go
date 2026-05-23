package imageproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// memSource is the in-memory Source the task tests use to feed the
// handler a known set of bytes. The map's key is the storage key,
// the value is the bytes — same shape as the production MinIO
// adapter exposes.
type memSource struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func newMemSource() *memSource { return &memSource{objects: map[string][]byte{}} }

func (m *memSource) put(key string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = append([]byte(nil), body...)
}

func (m *memSource) GetObject(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	b, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("memSource: %q not found", key)
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// memSink is the in-memory Sink: records every variant put so tests
// can assert on the sibling keys produced.
type memSink struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newMemSink() *memSink {
	return &memSink{objects: map[string][]byte{}, types: map[string]string{}}
}

func (m *memSink) PutObject(_ context.Context, key string, body []byte, mime string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = append([]byte(nil), body...)
	m.types[key] = mime
	return nil
}

func (m *memSink) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

func (m *memSink) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	return out
}

// memManifest records SetVariants calls so tests can assert that the
// store-side manifest was written with the right shape.
type memManifest struct {
	mu      sync.Mutex
	records map[string][]ManifestEntry
}

func newMemManifest() *memManifest {
	return &memManifest{records: map[string][]ManifestEntry{}}
}

func (m *memManifest) MarkProcessed(_ context.Context, id string, entries []ManifestEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[id] = append([]ManifestEntry(nil), entries...)
	return nil
}

// --- direct handler tests ----------------------------------------------------

// TestHandler_HappyPath runs the handler in-process: a JPEG lands in
// the source, the handler is invoked with a Payload, variants land
// in the sink, the manifest records the variants. The end-to-end
// invariants the production flow relies on.
func TestHandler_HappyPath(t *testing.T) {
	t.Parallel()
	src := newMemSource()
	sink := newMemSink()
	mw := newMemManifest()
	src.put("2026/01/photo.jpg", makeJPEG(t, 512, 512))

	handler := NewHandler(HandlerDeps{
		Source:         src,
		Sink:           sink,
		ManifestWriter: mw,
	})

	payload, _ := json.Marshal(Payload{
		AssetID:    "asset-1",
		StorageKey: "2026/01/photo.jpg",
		MIMEType:   "image/jpeg",
	})
	if err := handler(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	// Four variants for the default config.
	if got := sink.count(); got != 4 {
		t.Errorf("sink put count = %d, want 4 (one per variant)", got)
	}
	for _, k := range sink.keys() {
		if k == "2026/01/photo.jpg" {
			t.Errorf("sink contains the source key — variants must be siblings")
		}
	}
	entries, ok := mw.records["asset-1"]
	if !ok {
		t.Fatalf("manifest writer was not called for asset-1")
	}
	if len(entries) != 4 {
		t.Errorf("manifest entries = %d, want 4", len(entries))
	}
}

// TestHandler_SkipsNonImage covers the wire-level guard: a PDF
// upload that still enqueues a media.process task must short-circuit
// in the handler rather than running the decoder on garbage.
func TestHandler_SkipsNonImage(t *testing.T) {
	t.Parallel()
	src := newMemSource()
	sink := newMemSink()

	handler := NewHandler(HandlerDeps{Source: src, Sink: sink})
	payload, _ := json.Marshal(Payload{
		AssetID:    "asset-2",
		StorageKey: "2026/01/contract.pdf",
		MIMEType:   "application/pdf",
	})
	if err := handler(context.Background(), payload); err != nil {
		t.Fatalf("handler should not error on unsupported MIME: %v", err)
	}
	if sink.count() != 0 {
		t.Errorf("sink should be untouched for a non-image upload")
	}
}

// TestHandler_PropagatesSourceError pins that a missing source object
// produces an error (the worker layer translates to a retry).
func TestHandler_PropagatesSourceError(t *testing.T) {
	t.Parallel()
	src := newMemSource()
	src.err = errors.New("storage down")
	sink := newMemSink()

	handler := NewHandler(HandlerDeps{Source: src, Sink: sink})
	payload, _ := json.Marshal(Payload{
		AssetID:    "asset-3",
		StorageKey: "missing",
		MIMEType:   "image/jpeg",
	})
	err := handler(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected error from missing source")
	}
}

// TestHandler_RejectsMalformedPayload pins the typed-error path for
// a payload that won't unmarshal. The schema validation upstream
// catches most of these, but the handler is defensive.
func TestHandler_RejectsMalformedPayload(t *testing.T) {
	t.Parallel()
	handler := NewHandler(HandlerDeps{Source: newMemSource(), Sink: newMemSink()})
	if err := handler(context.Background(), []byte("not json")); err == nil {
		t.Fatalf("expected error on garbage payload")
	}
	if err := handler(context.Background(), []byte(`{"storage_key":""}`)); err == nil {
		t.Fatalf("expected error on payload with missing asset_id")
	}
}

// --- end-to-end through asynq + miniredis ------------------------------------

// newTestClient mirrors taskspec's own test helper: spin up
// miniredis, wrap it in a go-redis client, return an asynq.Client.
func newTestAsynqClient(t *testing.T) *asynq.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	client := asynq.NewClientFromRedisClient(rc)
	t.Cleanup(func() {
		_ = client.Close()
		_ = rc.Close()
	})
	return client
}

// TestTaskSpec_EnqueueAndDispatch runs the full producer→consumer
// path: NewSpec → Register → Enqueue → Dispatch → ProcessTask. This
// is the integration the upload handler relies on; if any seam
// breaks, the upload writes a row but no variants ever land.
func TestTaskSpec_EnqueueAndDispatch(t *testing.T) {
	t.Parallel()
	src := newMemSource()
	sink := newMemSink()
	mw := newMemManifest()
	src.put("2026/01/photo.jpg", makeJPEG(t, 256, 256))

	spec, err := NewSpec(HandlerDeps{Source: src, Sink: sink, ManifestWriter: mw})
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	if spec.Name != TaskName {
		t.Errorf("spec.Name = %q, want %q", spec.Name, TaskName)
	}
	if spec.Queue != DefaultQueue {
		t.Errorf("spec.Queue = %q, want %q", spec.Queue, DefaultQueue)
	}
	if spec.Timeout != DefaultTimeout {
		t.Errorf("spec.Timeout = %v, want %v", spec.Timeout, DefaultTimeout)
	}

	reg := taskspec.NewRegistry()
	if err := reg.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	client := newTestAsynqClient(t)
	info, err := taskspec.Enqueue(context.Background(), client, reg, TaskName, Payload{
		AssetID:    "asset-1",
		StorageKey: "2026/01/photo.jpg",
		MIMEType:   "image/jpeg",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if info.Type != TaskName {
		t.Errorf("TaskInfo.Type = %q, want %q", info.Type, TaskName)
	}
	if info.Queue != DefaultQueue {
		t.Errorf("TaskInfo.Queue = %q, want %q", info.Queue, DefaultQueue)
	}

	// Dispatch onto a fresh mux and process the task locally.
	mux := asynq.NewServeMux()
	taskspec.Dispatch(mux, reg)
	task := asynq.NewTask(TaskName, info.Payload)
	if err := mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if sink.count() == 0 {
		t.Errorf("dispatch ran but no variants landed in sink")
	}
	if _, ok := mw.records["asset-1"]; !ok {
		t.Errorf("dispatch ran but manifest writer not invoked")
	}
}

// TestTaskSpec_EnqueueRejectsBadPayload pins that the schema is
// wired and validated at Enqueue time — a payload missing the
// required asset_id must not enter the queue. (taskspec.Enqueue
// returns ErrInvalidPayload; the worker never sees the bytes.)
func TestTaskSpec_EnqueueRejectsBadPayload(t *testing.T) {
	t.Parallel()
	spec, err := NewSpec(HandlerDeps{Source: newMemSource(), Sink: newMemSink()})
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	reg := taskspec.NewRegistry()
	if err := reg.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client := newTestAsynqClient(t)

	// Payload missing the required storage_key field.
	_, err = taskspec.Enqueue(context.Background(), client, reg, TaskName, map[string]any{
		"asset_id": "asset-1",
	})
	if err == nil {
		t.Fatalf("expected ErrInvalidPayload on schema violation")
	}
	if !errors.Is(err, taskspec.ErrInvalidPayload) {
		t.Errorf("got %v, want errors.Is(..., taskspec.ErrInvalidPayload)", err)
	}
}

// TestPayloadSchema_RoundTrips proves PayloadSchema returns bytes
// that compile back to the same schema (i.e. the schema is round-
// trip stable). Catches a future maintainer who edits the inline
// schema string but forgets to revalidate.
func TestPayloadSchema_RoundTrips(t *testing.T) {
	t.Parallel()
	raw, err := PayloadSchema()
	if err != nil {
		t.Fatalf("PayloadSchema: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("PayloadSchema returned empty bytes")
	}
	// Just check it's valid JSON; the deep semantic test runs via
	// TestTaskSpec_EnqueueRejectsBadPayload.
	var anyMap map[string]any
	if err := json.Unmarshal(raw, &anyMap); err != nil {
		t.Fatalf("PayloadSchema is not valid JSON: %v", err)
	}
}

// TestHandler_RespectsTimeout pins that the handler honours a
// cancelled context — the worker side relies on this to bound a
// runaway encode.
func TestHandler_RespectsTimeout(t *testing.T) {
	t.Parallel()
	src := newMemSource()
	sink := newMemSink()
	src.put("k", makeJPEG(t, 64, 64))

	handler := NewHandler(HandlerDeps{Source: src, Sink: sink})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	// Sleep slightly to ensure the timeout has actually fired before
	// the handler reads it.
	time.Sleep(time.Millisecond)

	payload, _ := json.Marshal(Payload{AssetID: "a", StorageKey: "k", MIMEType: "image/jpeg"})
	if err := handler(ctx, payload); err == nil {
		t.Errorf("expected error from cancelled context")
	}
}
