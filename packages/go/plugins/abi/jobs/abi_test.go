package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
	pluginruntime "github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"

	asynqlib "github.com/hibiken/asynq"
)

// theJobWasm is defined in fixturedata_test.go (code-generated from
// wat/the_job.wat). Two filename quirks shape the name:
//   - Anything with a `testdata` directory or `testdata*` prefix is
//     ignored by go list — that rules out the natural `testdata_*` name.
//   - Anything matching `*_wasm.go` or `*_wasm_test.go` is interpreted
//     as a GOOS=wasm-only file.

// loadFixture loads the the_job fixture and returns a fully-wired
// Dispatcher. The test cleanup closes the runtime, which closes the
// module.
func loadFixture(t *testing.T, name string) (*Dispatcher, *pluginruntime.Module) {
	t.Helper()
	ctx := context.Background()
	rt, err := pluginruntime.New(ctx)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	mod, err := rt.LoadModule(ctx, name, theJobWasm)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	return NewDispatcher(mod), mod
}

// TestPackUnpackRoundTrip pins the bit-level invariant the ABI relies
// on. Same shape as the hooks ABI test — the packed-return encoding
// is intentionally identical.
func TestPackUnpackRoundTrip(t *testing.T) {
	cases := []struct {
		ptr uint32
		len int32
	}{
		{0, 0},
		{0, -1},
		{0, -4},
		{1024, 16},
		{0xFFFFFFFF, 0x7FFFFFFF},
	}
	for _, c := range cases {
		packed := packResult(c.ptr, c.len)
		ptr, length := unpackResult(packed)
		if ptr != c.ptr || length != c.len {
			t.Errorf("pack/unpack (%d, %d) -> (%d, %d)", c.ptr, c.len, ptr, length)
		}
	}
}

// TestMarshalJobEnvelope_FullShape verifies the envelope JSON contains
// the idempotency key, retry count, and raw payload in the documented
// shape. The guest's decoder relies on the field ordering being stable.
func TestMarshalJobEnvelope_FullShape(t *testing.T) {
	buf, err := MarshalJobEnvelope("task-abc", 3, json.RawMessage(`{"to":"a@b"}`))
	if err != nil {
		t.Fatalf("MarshalJobEnvelope: %v", err)
	}
	// Decode and assert per field — JSON object ordering isn't load-
	// bearing for the guest, but the fields must all be present.
	var got JobEnvelope
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.IdempotencyKey != "task-abc" {
		t.Errorf("IdempotencyKey = %q, want task-abc", got.IdempotencyKey)
	}
	if got.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", got.RetryCount)
	}
	if string(got.Payload) != `{"to":"a@b"}` {
		t.Errorf("Payload = %s, want {\"to\":\"a@b\"}", got.Payload)
	}
}

// TestMarshalJobEnvelope_NilPayload covers the "no body" path. asynq
// allows enqueuing an empty payload; the envelope must still produce
// valid JSON so the guest decoder doesn't choke.
func TestMarshalJobEnvelope_NilPayload(t *testing.T) {
	buf, err := MarshalJobEnvelope("", 0, nil)
	if err != nil {
		t.Fatalf("MarshalJobEnvelope: %v", err)
	}
	// payload must be JSON null, not omitted.
	want := `{"idempotency_key":"","retry_count":0,"payload":null}`
	if string(buf) != want {
		t.Errorf("got %s, want %s", buf, want)
	}
}

// TestMarshalJobEnvelope_NegativeRetryCount confirms the bridge
// normalizes negative retry counts to 0. asynq's API guarantees
// non-negative, but the marshal API defends.
func TestMarshalJobEnvelope_NegativeRetryCount(t *testing.T) {
	buf, err := MarshalJobEnvelope("k", -5, nil)
	if err != nil {
		t.Fatalf("MarshalJobEnvelope: %v", err)
	}
	var got JobEnvelope
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (normalized)", got.RetryCount)
	}
}

// TestInvokeJob_HappyPath is the headline integration test: the guest
// receives an envelope and returns OK.
func TestInvokeJob_HappyPath(t *testing.T) {
	d, _ := loadFixture(t, "job_happy")
	ctx := context.Background()

	env, err := MarshalJobEnvelope("task-1", 0, json.RawMessage(`{"to":"x@y"}`))
	if err != nil {
		t.Fatalf("MarshalJobEnvelope: %v", err)
	}
	if err := d.InvokeJob(ctx, "email.send", env); err != nil {
		t.Errorf("InvokeJob email.send: %v", err)
	}
}

// TestInvokeJob_UnknownJob covers the guest-reported error path:
// gn_handle_job returned ResultStatusUnknownJob.
func TestInvokeJob_UnknownJob(t *testing.T) {
	d, _ := loadFixture(t, "job_unknown")
	ctx := context.Background()

	env, _ := MarshalJobEnvelope("k", 0, nil)
	err := d.InvokeJob(ctx, "no.such.job", env)
	if err == nil {
		t.Fatal("expected error for unknown job, got nil")
	}
	if !errors.Is(err, ErrUnknownJob) {
		t.Errorf("err = %v, want errors.Is ErrUnknownJob", err)
	}
	var je *JobError
	if !errors.As(err, &je) {
		t.Fatalf("err type = %T, want *JobError", err)
	}
	if je.Status != ResultStatusUnknownJob {
		t.Errorf("status = %s, want unknown_job", je.Status)
	}
}

// TestInvokeJob_Trap covers the trap path: the guest calls gn_panic
// and the host wraps the resulting *TrapError as a JobError tagged
// ResultStatusTrap. This is the load-bearing "asynq sees this as an
// error and retries per policy" property — see Bridge.handler for the
// retry contract.
func TestInvokeJob_Trap(t *testing.T) {
	d, _ := loadFixture(t, "job_trap")
	ctx := context.Background()

	env, _ := MarshalJobEnvelope("k", 0, nil)
	err := d.InvokeJob(ctx, "trap.job", env)
	if err == nil {
		t.Fatal("expected trap error, got nil")
	}
	if !errors.Is(err, ErrTrapped) {
		t.Errorf("err = %v, want errors.Is ErrTrapped", err)
	}
	var je *JobError
	if !errors.As(err, &je) {
		t.Fatalf("err type = %T, want *JobError", err)
	}
	if je.Status != ResultStatusTrap {
		t.Errorf("status = %s, want trap", je.Status)
	}
	// The underlying *TrapError must be reachable for callers that want
	// the trap reason.
	var trap *pluginruntime.TrapError
	if !errors.As(err, &trap) {
		t.Errorf("trap not unwrappable from JobError: %v", err)
	}
}

// TestInvokeJob_OOM forces the guest's bump allocator past memory end
// so the next gn_alloc returns 0. We invoke through the dispatcher
// AFTER the force_oom to confirm the host surfaces it as
// ErrOutOfMemory.
func TestInvokeJob_OOM(t *testing.T) {
	d, mod := loadFixture(t, "job_oom")
	ctx := context.Background()

	// Drive the guest's bump pointer past memory end.
	if _, err := mod.Call(ctx, "force_oom"); err != nil {
		t.Fatalf("force_oom: %v", err)
	}

	env, _ := MarshalJobEnvelope("k", 0, nil)
	err := d.InvokeJob(ctx, "email.send", env)
	if err == nil {
		t.Fatal("expected OOM error, got nil")
	}
	if !errors.Is(err, ErrOutOfMemory) {
		t.Errorf("err = %v, want errors.Is ErrOutOfMemory", err)
	}
}

// TestInvokeJob_PayloadTooLarge verifies the host-side payload cap
// kicks in before any guest call.
func TestInvokeJob_PayloadTooLarge(t *testing.T) {
	d, _ := loadFixture(t, "job_big")
	ctx := context.Background()

	bigStr := strings.Repeat("x", 1<<21)
	env := []byte(`{"idempotency_key":"k","retry_count":0,"payload":"` + bigStr + `"}`)

	err := d.InvokeJob(ctx, "email.send", env)
	if err == nil {
		t.Fatal("expected ErrPayloadTooLarge, got nil")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("err = %v, want errors.Is ErrPayloadTooLarge", err)
	}
}

// TestDispatcher_ConcurrentInvocations is the race-detector workout —
// 100 concurrent invokes on the same Module. Module.Call is documented
// to serialize internally, so the goal is to confirm the dispatcher's
// own logic (cached exports, alloc/call interleave) is also race-free.
// This is the "100 concurrent task pickups for the same plugin" test
// from the issue.
func TestDispatcher_ConcurrentInvocations(t *testing.T) {
	d, _ := loadFixture(t, "job_concurrent")
	ctx := context.Background()

	const N = 100
	var wg sync.WaitGroup
	var bad atomic.Int64
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			env, _ := MarshalJobEnvelope(fmt.Sprintf("task-%d", i), 0, nil)
			if err := d.InvokeJob(ctx, "email.send", env); err != nil {
				bad.Add(1)
				t.Errorf("InvokeJob %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if n := bad.Load(); n > 0 {
		t.Fatalf("%d/%d invocations failed", n, N)
	}
}

// TestBridge_RegistersFromManifest walks the manifest's jobs list and
// confirms each becomes a live TaskSpec on the registry, with the
// correct queue.
func TestBridge_RegistersFromManifest(t *testing.T) {
	d, _ := loadFixture(t, "bridge_basic")
	reg := taskspec.NewRegistry()

	// Construct a Checker with the jobs.enqueue cap granted so the
	// capability gate passes.
	capReg := capabilities.NewRegistry()
	_ = capReg.Register(capabilities.CapabilityDef{ID: CapabilityID})
	checker := capabilities.NewChecker(capReg, capabilities.NewGrantSet(CapabilityID))

	bridge, err := NewBridge("test-plugin", d, reg, checker)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	m := &manifest.Manifest{
		Jobs: []string{"email.send"},
	}
	n, err := bridge.Register(context.Background(), m)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n != 1 {
		t.Errorf("Register returned %d, want 1", n)
	}

	// The TaskSpec must be present with the correct queue.
	spec, ok := reg.Get("email.send")
	if !ok {
		t.Fatal("email.send not found in registry")
	}
	if spec.Queue != "plugin" {
		t.Errorf("spec.Queue = %q, want plugin", spec.Queue)
	}
	if spec.MaxRetry != DefaultMaxRetry {
		t.Errorf("spec.MaxRetry = %d, want %d", spec.MaxRetry, DefaultMaxRetry)
	}
	if spec.Handler == nil {
		t.Fatal("spec.Handler is nil")
	}

	// Invoke the handler with a bare context — asynq.GetTaskID returns
	// ok=false outside of an asynq dispatch, which is the documented
	// test-path behavior (envelope IdempotencyKey is then empty).
	// End-to-end task-ID propagation is covered by the asynq library's
	// own integration tests; here we verify the bridge plumbing reaches
	// the dispatcher and the guest returns OK.
	ctx := context.Background()
	if err := spec.Handler(ctx, []byte(`{}`)); err != nil {
		t.Errorf("Handler: %v", err)
	}

	// After Unregister, the same handler must short-circuit with an
	// error so asynq retries (and the next pickup hits the new bridge
	// after hot reload).
	bridge.Unregister()
	err = spec.Handler(ctx, []byte(`{}`))
	if err == nil {
		t.Error("expected error from handler after Unregister, got nil")
	}
}

// TestBridge_CapabilityDenied confirms a manifest declaring jobs with
// NO jobs.enqueue grant is rejected at Register time. This is the
// load-bearing security check.
func TestBridge_CapabilityDenied(t *testing.T) {
	d, _ := loadFixture(t, "bridge_capdeny")
	reg := taskspec.NewRegistry()

	capReg := capabilities.NewRegistry()
	_ = capReg.Register(capabilities.CapabilityDef{ID: CapabilityID})
	// Empty grant set — no capability.
	checker := capabilities.NewChecker(capReg, capabilities.NewGrantSet())

	bridge, err := NewBridge("test-plugin", d, reg, checker)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	m := &manifest.Manifest{Jobs: []string{"email.send"}}
	n, err := bridge.Register(context.Background(), m)
	if err == nil {
		t.Fatal("expected ErrCapabilityDenied, got nil")
	}
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Errorf("err = %v, want errors.Is ErrCapabilityDenied", err)
	}
	if !errors.Is(err, capabilities.ErrCapabilityDenied) {
		t.Errorf("err = %v, want errors.Is capabilities.ErrCapabilityDenied", err)
	}
	if n != 0 {
		t.Errorf("Register installed %d TaskSpecs despite cap denial, want 0", n)
	}
	// Confirm the registry is empty.
	if reg.Has("email.send") {
		t.Error("registry contains email.send despite cap denial")
	}
}

// TestBridge_NilCheckerSkipsCapCheck documents the test-path contract:
// a nil checker disables the cap gate. Production wiring always passes
// one. Without this affordance, every test would need to wire a Checker
// just to exercise unrelated code paths.
func TestBridge_NilCheckerSkipsCapCheck(t *testing.T) {
	d, _ := loadFixture(t, "bridge_nilchecker")
	reg := taskspec.NewRegistry()

	bridge, err := NewBridge("test-plugin", d, reg, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	m := &manifest.Manifest{Jobs: []string{"email.send"}}
	n, err := bridge.Register(context.Background(), m)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n != 1 {
		t.Errorf("Register returned %d, want 1", n)
	}
}

// TestBridge_EmptyJobsManifest confirms a manifest with no jobs[]
// installs no TaskSpecs and produces no error.
func TestBridge_EmptyJobsManifest(t *testing.T) {
	d, _ := loadFixture(t, "bridge_empty")
	reg := taskspec.NewRegistry()
	bridge, err := NewBridge("plug", d, reg, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	n, err := bridge.Register(context.Background(), &manifest.Manifest{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 registrations, got %d", n)
	}
}

// TestBridge_NilArgs covers defensive guards.
func TestBridge_NilArgs(t *testing.T) {
	reg := taskspec.NewRegistry()
	if _, err := NewBridge("", nil, reg, nil); err == nil {
		t.Error("NewBridge with nil dispatcher: want error, got nil")
	}
	if _, err := NewBridge("", &Dispatcher{}, nil, nil); err == nil {
		t.Error("NewBridge with nil registry: want error, got nil")
	}
}

// TestBridge_TrapRetries documents the contract: a plugin trap inside
// a job returns an error from the registered TaskSpec.Handler WITHOUT
// wrapping in asynq.SkipRetry. The asynq runner is therefore free to
// retry per the spec's MaxRetry policy.
func TestBridge_TrapRetries(t *testing.T) {
	d, _ := loadFixture(t, "bridge_trap")
	reg := taskspec.NewRegistry()
	bridge, err := NewBridge("plug", d, reg, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer bridge.Unregister()

	m := &manifest.Manifest{Jobs: []string{"trap.job"}}
	if _, err := bridge.Register(context.Background(), m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	spec, _ := reg.Get("trap.job")
	if spec.MaxRetry != DefaultMaxRetry {
		t.Errorf("MaxRetry = %d, want %d", spec.MaxRetry, DefaultMaxRetry)
	}

	// The handler MUST return an error so asynq retries. It MUST NOT
	// wrap with asynq.SkipRetry — operators want the retry budget to
	// surface trapping plugins.
	err = spec.Handler(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error from trap.job handler, got nil")
	}
	if errors.Is(err, asynqlib.SkipRetry) {
		t.Error("trap error wrapped with SkipRetry — bridge should let asynq retry")
	}
	if !errors.Is(err, ErrTrapped) {
		t.Errorf("err = %v, want errors.Is ErrTrapped", err)
	}
}

// TestBridge_HotReload exercises the G1/G2 hot-reload flow: an old
// bridge is Unregistered, a fresh module is loaded into a fresh
// runtime, a fresh bridge is wired against a SAME-NAME job using a
// fresh registry (which is how the lifecycle Manager does swap), and
// the new closure points at the new dispatcher.
//
// The test asserts:
//
//   - The OLD spec's Handler returns an error after Unregister.
//   - A fresh registry accepts the same job name (first-writer-wins
//     applies per-registry; the lifecycle Manager constructs fresh
//     registries on reload).
//   - The NEW spec's Handler succeeds (it reaches the new dispatcher).
//
// This mirrors the symmetry of the hooks bridge's Unregister contract.
func TestBridge_HotReload(t *testing.T) {
	ctx := context.Background()

	// --- Generation 1 ---
	d1, _ := loadFixture(t, "reload_gen1")
	reg1 := taskspec.NewRegistry()
	br1, err := NewBridge("plug", d1, reg1, nil)
	if err != nil {
		t.Fatalf("NewBridge gen1: %v", err)
	}
	m := &manifest.Manifest{Jobs: []string{"email.send"}}
	if _, err := br1.Register(ctx, m); err != nil {
		t.Fatalf("Register gen1: %v", err)
	}
	spec1, _ := reg1.Get("email.send")
	if err := spec1.Handler(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("gen1 Handler: %v", err)
	}
	br1.Unregister()
	if err := spec1.Handler(ctx, []byte(`{}`)); err == nil {
		t.Error("gen1 Handler after Unregister: want error, got nil")
	}

	// --- Generation 2 (fresh module, fresh registry, fresh bridge) ---
	d2, _ := loadFixture(t, "reload_gen2")
	reg2 := taskspec.NewRegistry()
	br2, err := NewBridge("plug", d2, reg2, nil)
	if err != nil {
		t.Fatalf("NewBridge gen2: %v", err)
	}
	defer br2.Unregister()
	if _, err := br2.Register(ctx, m); err != nil {
		t.Fatalf("Register gen2: %v", err)
	}
	spec2, _ := reg2.Get("email.send")
	if err := spec2.Handler(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("gen2 Handler: %v", err)
	}
	if names := br2.Registered(); len(names) != 1 || names[0] != "email.send" {
		t.Errorf("br2.Registered = %v, want [email.send]", names)
	}
}

// TestBridge_DuplicateJobInRegistry covers the conflict path: two
// bridges trying to register the same job name into the same registry
// surface ErrAlreadyRegistered.
func TestBridge_DuplicateJobInRegistry(t *testing.T) {
	d, _ := loadFixture(t, "bridge_dup")
	reg := taskspec.NewRegistry()
	// Pre-register a colliding spec.
	if err := reg.Register(taskspec.TaskSpec{Name: "email.send", Handler: noopHandler}); err != nil {
		t.Fatalf("pre-Register: %v", err)
	}
	bridge, err := NewBridge("plug", d, reg, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer bridge.Unregister()
	m := &manifest.Manifest{Jobs: []string{"email.send"}}
	_, err = bridge.Register(context.Background(), m)
	if err == nil {
		t.Fatal("expected ErrAlreadyRegistered, got nil")
	}
	if !errors.Is(err, taskspec.ErrAlreadyRegistered) {
		t.Errorf("err = %v, want errors.Is taskspec.ErrAlreadyRegistered", err)
	}
}

// TestDispatcher_MissingExports verifies that a module without the ABI
// exports returns ErrMissingExport on first invoke.
func TestDispatcher_MissingExports(t *testing.T) {
	ctx := context.Background()
	rt, err := pluginruntime.New(ctx)
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	mod, err := rt.LoadModule(ctx, "minimal", minimalWasm)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	d := NewDispatcher(mod)
	env, _ := MarshalJobEnvelope("k", 0, nil)
	err = d.InvokeJob(ctx, "anything", env)
	if err == nil {
		t.Fatal("expected ErrMissingExport, got nil")
	}
	if !errors.Is(err, ErrMissingExport) {
		t.Errorf("err = %v, want errors.Is ErrMissingExport", err)
	}
}

// TestJobError_IsTraversal exercises the errors.Is matrix.
func TestJobError_IsTraversal(t *testing.T) {
	cases := []struct {
		status ResultStatus
		want   error
	}{
		{ResultStatusOutOfMemory, ErrOutOfMemory},
		{ResultStatusBadPayload, ErrBadPayload},
		{ResultStatusUnknownJob, ErrUnknownJob},
		{ResultStatusError, ErrGuestError},
		{ResultStatusTrap, ErrTrapped},
	}
	for _, c := range cases {
		je := &JobError{Job: "x", Status: c.status}
		if !errors.Is(je, c.want) {
			t.Errorf("JobError{status=%s} is not %v", c.status, c.want)
		}
	}
	// Non-matching sentinel.
	je := &JobError{Job: "x", Status: ResultStatusOK}
	if errors.Is(je, ErrOutOfMemory) {
		t.Error("JobError{OK} should not match ErrOutOfMemory")
	}
}

// TestResultStatus_String pins the human-readable rendering used in
// observer labels and error messages.
func TestResultStatus_String(t *testing.T) {
	cases := []struct {
		s    ResultStatus
		want string
	}{
		{ResultStatusOK, "ok"},
		{ResultStatusError, "error"},
		{ResultStatusOutOfMemory, "out_of_memory"},
		{ResultStatusBadPayload, "bad_payload"},
		{ResultStatusUnknownJob, "unknown_job"},
		{ResultStatusTrap, "trap"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("ResultStatus(%d).String() = %q, want %q", int32(c.s), got, c.want)
		}
	}
	// Unknown status renders with the numeric form.
	got := ResultStatus(-99).String()
	if !strings.HasPrefix(got, "status(") {
		t.Errorf("unknown status rendered as %q, want status(...) prefix", got)
	}
}

// TestBridge_EnvelopeMissingTaskIDIsTolerated asserts that the bridge
// handler accepts a bare context (no asynq metadata) — asynq.GetTaskID
// returns ok=false, the envelope IdempotencyKey field is empty, and
// the call proceeds without error. This is the documented test-path
// affordance.
func TestBridge_EnvelopeMissingTaskIDIsTolerated(t *testing.T) {
	d, _ := loadFixture(t, "envelope_notaskid")
	reg := taskspec.NewRegistry()
	bridge, err := NewBridge("plug", d, reg, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer bridge.Unregister()
	if _, err := bridge.Register(context.Background(), &manifest.Manifest{Jobs: []string{"email.send"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	spec, _ := reg.Get("email.send")
	// Bare context — no asynq metadata.
	if err := spec.Handler(context.Background(), []byte(`{}`)); err != nil {
		t.Errorf("Handler: %v", err)
	}
}

// noopHandler is a stand-in handler used by the duplicate-registration
// test where the pre-existing TaskSpec needs a non-nil Handler so it's
// realistic.
func noopHandler(_ context.Context, _ []byte) error { return nil }

// minimalWasm is a tiny module that exports nothing the ABI needs.
// Section-by-section layout follows the same conventions as
// runtime/testdata_test.go and the hooks sibling test.
var minimalWasm = []byte{
	// header
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// type: () -> ()
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	// function: 1 func of type 0
	0x03, 0x02, 0x01, 0x00,
	// export: "noop" func 0
	0x07, 0x08, 0x01, 0x04, 'n', 'o', 'o', 'p', 0x00, 0x00,
	// code: 1 body, body size 2, 0 locals, end
	0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
}
