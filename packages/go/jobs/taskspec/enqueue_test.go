package taskspec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// newTestClient spins up an in-memory miniredis, wraps it in a
// go-redis UniversalClient, and hands the asynq.Client back to the
// test. asynq's protocol is mostly plain Redis commands, so miniredis
// suffices for the enqueue path — we are asserting on the TaskInfo the
// client returns, not on what a real asynq worker would do with it.
//
// The returned cleanup closes the client and stops miniredis. Callers
// invoke it via t.Cleanup.
func newTestClient(t *testing.T) (*asynq.Client, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	client := asynq.NewClientFromRedisClient(rc)
	cleanup := func() {
		_ = client.Close()
		_ = rc.Close()
	}
	return client, cleanup
}

// TestEnqueue_Success enqueues a task whose payload matches the spec's
// schema. The TaskInfo returned by asynq must reflect the spec's
// declared Queue, MaxRetry, Timeout, and Name — those four fields are
// the contract Enqueue translates from the declarative spec into
// asynq.Option values.
func TestEnqueue_Success(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	schemaRaw := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)
	schema, err := jsonschemautil.Compile("https://gonext.test/ok.json", schemaRaw)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	spec := TaskSpec{
		Name:          "demo.task",
		Queue:         "critical",
		MaxRetry:      5,
		Timeout:       2 * time.Second,
		PayloadSchema: schema,
		Handler:       noopHandler,
	}
	if err := reg.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	info, err := Enqueue(context.Background(), client, reg, "demo.task", map[string]any{"name": "ada"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if info == nil {
		t.Fatal("Enqueue: TaskInfo is nil")
	}
	if info.Type != "demo.task" {
		t.Errorf("TaskInfo.Type = %q, want %q", info.Type, "demo.task")
	}
	if info.Queue != "critical" {
		t.Errorf("TaskInfo.Queue = %q, want %q", info.Queue, "critical")
	}
	if info.MaxRetry != 5 {
		t.Errorf("TaskInfo.MaxRetry = %d, want %d", info.MaxRetry, 5)
	}
	if info.Timeout != 2*time.Second {
		t.Errorf("TaskInfo.Timeout = %v, want %v", info.Timeout, 2*time.Second)
	}
}

// TestEnqueue_DefaultRegistryFallback covers the nil-registry path: a
// caller that passes nil should be served the singleton.
func TestEnqueue_DefaultRegistryFallback(t *testing.T) {
	// Sequential — mutates the Default() singleton.
	resetDefaultForTest()
	t.Cleanup(resetDefaultForTest)

	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	spec := TaskSpec{Name: "fallback.task", Queue: "default", Handler: noopHandler}
	if err := Default().Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	info, err := Enqueue(context.Background(), client, nil, "fallback.task", []byte(`{}`))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if info.Type != "fallback.task" {
		t.Errorf("Type = %q, want %q", info.Type, "fallback.task")
	}
}

// TestEnqueue_NilClient pins the ErrNilClient contract. Partial wiring
// (e.g. tests that forgot to install the client) must produce a typed
// error rather than a nil-dereference panic, so the error path can be
// asserted on and logged uniformly.
func TestEnqueue_NilClient(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(TaskSpec{Name: "x", Handler: noopHandler})
	_, err := Enqueue(context.Background(), nil, reg, "x", nil)
	if !errors.Is(err, ErrNilClient) {
		t.Fatalf("got %v, want ErrNilClient", err)
	}
}

// TestEnqueue_UnknownTask asserts the typed error path when the
// registry has no spec for the requested name.
func TestEnqueue_UnknownTask(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	_, err := Enqueue(context.Background(), client, reg, "missing.task", nil)
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("got %v, want ErrUnknownTask", err)
	}
}

// TestEnqueue_InvalidPayloadRejected exercises the schema-validation
// gate. A payload missing a required field must surface as
// ErrInvalidPayload — and importantly, it must NOT be enqueued: the
// whole point of validating producer-side is keeping garbage out of
// the queue.
func TestEnqueue_InvalidPayloadRejected(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	schemaRaw := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)
	schema, err := jsonschemautil.Compile("https://gonext.test/required.json", schemaRaw)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	spec := TaskSpec{Name: "needs.name", PayloadSchema: schema, Handler: noopHandler}
	if err := reg.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err = Enqueue(context.Background(), client, reg, "needs.name", map[string]any{})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("got %v, want ErrInvalidPayload", err)
	}
}

// TestEnqueue_NilSchemaSkipsValidation documents the "no schema means
// no constraint" path. A spec without a PayloadSchema must still
// enqueue successfully even with a structurally weird payload — the
// constraint is purely an opt-in producer-side gate.
func TestEnqueue_NilSchemaSkipsValidation(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	spec := TaskSpec{Name: "free.form", Queue: "default", Handler: noopHandler}
	if err := reg.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	info, err := Enqueue(context.Background(), client, reg, "free.form", []byte("any-bytes"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if info == nil || info.Type != "free.form" {
		t.Errorf("unexpected info: %+v", info)
	}
}

// TestEnqueue_BytesPassthrough verifies that a []byte payload is sent
// to asynq verbatim — no re-marshal. Webhook delivery relies on this
// because the body is HMAC-signed before enqueue; re-marshaling would
// invalidate the signature.
func TestEnqueue_BytesPassthrough(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	if err := reg.Register(TaskSpec{Name: "raw.task", Handler: noopHandler}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	raw := []byte(`{"presigned":true}`)
	info, err := Enqueue(context.Background(), client, reg, "raw.task", raw)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if string(info.Payload) != string(raw) {
		t.Errorf("payload mutated: got %q, want %q", info.Payload, raw)
	}
}

// TestOptionsFromSpec exercises the internal translator so a future
// refactor that changes the Option list (e.g. add Deadline) doesn't
// silently drop one.
func TestOptionsFromSpec(t *testing.T) {
	t.Parallel()
	spec := TaskSpec{Queue: "q", MaxRetry: 3, Timeout: 10 * time.Second}
	opts := optionsFromSpec(spec)
	// We expect at least the three options we configured. A more
	// brittle assertion on count would constrain a future addition;
	// asserting presence is what callers actually rely on.
	var seenQueue, seenRetry, seenTimeout bool
	for _, o := range opts {
		switch o.Type() {
		case asynq.QueueOpt:
			seenQueue = true
		case asynq.MaxRetryOpt:
			seenRetry = true
		case asynq.TimeoutOpt:
			seenTimeout = true
		}
	}
	if !seenQueue || !seenRetry || !seenTimeout {
		t.Errorf("missing option: queue=%v retry=%v timeout=%v", seenQueue, seenRetry, seenTimeout)
	}
}

// TestEnqueue_NonMarshalable produces a wrapped json.Marshal error
// rather than an ErrInvalidPayload, because the failure is a caller
// bug (passing a type the encoder can't handle), not a schema
// violation. Tests pin the contract so a future "wrap everything as
// invalid payload" refactor is loud.
func TestEnqueue_NonMarshalable(t *testing.T) {
	t.Parallel()
	client, cleanup := newTestClient(t)
	t.Cleanup(cleanup)

	reg := NewRegistry()
	_ = reg.Register(TaskSpec{Name: "marshal.fail", Handler: noopHandler})
	_, err := Enqueue(context.Background(), client, reg, "marshal.fail", make(chan int))
	if err == nil {
		t.Fatal("expected error for unmarshalable payload")
	}
	if errors.Is(err, ErrInvalidPayload) {
		t.Errorf("got ErrInvalidPayload for marshal failure; should be a plain marshal error: %v", err)
	}
}
