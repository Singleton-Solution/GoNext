package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	hostbus "github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
	pluginruntime "github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
)

// theContentWasm is defined in fixturedata_test.go — a code-generated
// byte slice from wat/the_content.wat. See the comment on that variable
// for regeneration steps.
//
// Two filename quirks shape the name:
//   - Anything with a `testdata` directory or `testdata*` prefix is
//     ignored by go list — that rules out the natural `testdata_*` name.
//   - Anything matching `*_wasm.go` or `*_wasm_test.go` is interpreted
//     as a GOOS=wasm-only file — that rules out names like
//     `fixture_wasm_test.go`. The current name dodges both.

// loadFixture loads the the_content fixture and returns a fully-wired
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

	mod, err := rt.LoadModule(ctx, name, theContentWasm)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	return NewDispatcher(mod), mod
}

// TestPackUnpackRoundTrip pins the bit-level invariant the ABI relies on.
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

func TestMarshalActionPayload_EmptyArgs(t *testing.T) {
	// nil args must encode as "args":[] (not null), so the guest's
	// decoder always sees a JSON array.
	buf, err := MarshalActionPayload(nil)
	if err != nil {
		t.Fatalf("MarshalActionPayload: %v", err)
	}
	got := string(buf)
	want := `{"kind":"action","args":[]}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarshalFilterPayload_NilValue(t *testing.T) {
	buf, err := MarshalFilterPayload(nil, nil)
	if err != nil {
		t.Fatalf("MarshalFilterPayload: %v", err)
	}
	got := string(buf)
	want := `{"kind":"filter","value":null,"args":[]}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestInvokeFilter_TheContent is the headline integration test: the
// guest receives a payload and returns the transformed value.
func TestInvokeFilter_TheContent(t *testing.T) {
	d, _ := loadFixture(t, "the_content_filter")
	ctx := context.Background()

	out, err := d.InvokeFilter(ctx, "the_content", json.RawMessage(`"hello, world"`))
	if err != nil {
		t.Fatalf("InvokeFilter: %v", err)
	}
	want := json.RawMessage(`"HELLO, WORLD"`)
	if string(out) != string(want) {
		t.Errorf("filter result = %s, want %s", out, want)
	}
}

// TestInvokeAction_NoPayloadResult covers the "(ptr=0, len=0) success"
// shape: the guest accepts an empty action and returns OK no-body.
func TestInvokeAction_NoPayloadResult(t *testing.T) {
	d, _ := loadFixture(t, "action_no_payload")
	ctx := context.Background()

	// No args — the payload envelope is {"kind":"action","args":[]}
	if err := d.InvokeAction(ctx, "on_event"); err != nil {
		t.Errorf("InvokeAction empty: %v", err)
	}
}

// TestInvokeAction_WithArgs verifies an action with args also passes
// through (the guest doesn't inspect them, but the host must marshal
// them and allocate guest memory for them).
func TestInvokeAction_WithArgs(t *testing.T) {
	d, _ := loadFixture(t, "action_args")
	ctx := context.Background()

	if err := d.InvokeAction(ctx, "on_event", "user-42", 7, map[string]string{"k": "v"}); err != nil {
		t.Errorf("InvokeAction args: %v", err)
	}
}

// TestInvokeAction_UnknownHook covers the guest-reported error path:
// gn_handle_hook returned ResultStatusUnknownHook.
func TestInvokeAction_UnknownHook(t *testing.T) {
	d, _ := loadFixture(t, "unknown_hook")
	ctx := context.Background()

	err := d.InvokeAction(ctx, "no.such.hook")
	if err == nil {
		t.Fatal("expected error for unknown hook, got nil")
	}
	if !errors.Is(err, ErrUnknownHook) {
		t.Errorf("err = %v, want errors.Is ErrUnknownHook", err)
	}
	var he *HookError
	if !errors.As(err, &he) {
		t.Fatalf("err type = %T, want *HookError", err)
	}
	if he.Status != ResultStatusUnknownHook {
		t.Errorf("status = %s, want unknown_hook", he.Status)
	}
}

// TestInvokeAction_Trap covers the trap path: the guest calls gn_panic
// and the host wraps the resulting *TrapError as a HookError tagged
// ResultStatusTrap.
func TestInvokeAction_Trap(t *testing.T) {
	d, _ := loadFixture(t, "trap_plugin")
	ctx := context.Background()

	err := d.InvokeAction(ctx, "trip_panic")
	if err == nil {
		t.Fatal("expected trap error, got nil")
	}
	if !errors.Is(err, ErrTrapped) {
		t.Errorf("err = %v, want errors.Is ErrTrapped", err)
	}
	var he *HookError
	if !errors.As(err, &he) {
		t.Fatalf("err type = %T, want *HookError", err)
	}
	if he.Status != ResultStatusTrap {
		t.Errorf("status = %s, want trap", he.Status)
	}
	// The underlying *TrapError must be reachable for callers that want
	// the trap reason.
	var trap *pluginruntime.TrapError
	if !errors.As(err, &trap) {
		t.Errorf("trap not unwrappable from HookError: %v", err)
	}
}

// TestInvokeFilter_OOM forces the guest's bump allocator past memory
// end so the next gn_alloc returns 0. We invoke through the dispatcher
// AFTER the force_oom to confirm the host surfaces it as ErrOutOfMemory.
func TestInvokeFilter_OOM(t *testing.T) {
	d, mod := loadFixture(t, "oom_plugin")
	ctx := context.Background()

	// Drive the guest's bump pointer past memory end.
	if _, err := mod.Call(ctx, "force_oom"); err != nil {
		t.Fatalf("force_oom: %v", err)
	}

	_, err := d.InvokeFilter(ctx, "the_content", json.RawMessage(`"abc"`))
	if err == nil {
		t.Fatal("expected OOM error, got nil")
	}
	if !errors.Is(err, ErrOutOfMemory) {
		t.Errorf("err = %v, want errors.Is ErrOutOfMemory", err)
	}
}

// TestInvokeFilter_PayloadTooLarge verifies the host-side payload cap
// kicks in before any guest call.
func TestInvokeFilter_PayloadTooLarge(t *testing.T) {
	d, _ := loadFixture(t, "big_payload")
	ctx := context.Background()

	// Build a JSON string longer than MaxPayloadBytes once envelopedd
	// in the FilterPayload. Use 1.5 MiB worth so the envelope total
	// definitely exceeds 1 MiB.
	bigStr := strings.Repeat("x", 1<<21)
	value := json.RawMessage(`"` + bigStr + `"`)

	_, err := d.InvokeFilter(ctx, "the_content", value)
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
func TestDispatcher_ConcurrentInvocations(t *testing.T) {
	d, _ := loadFixture(t, "concurrent_plugin")
	ctx := context.Background()

	const N = 100
	var wg sync.WaitGroup
	var bad atomic.Int64
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			input := json.RawMessage(fmt.Sprintf(`"hello-%d"`, i))
			out, err := d.InvokeFilter(ctx, "the_content", input)
			if err != nil {
				bad.Add(1)
				t.Errorf("InvokeFilter %d: %v", i, err)
				return
			}
			want := fmt.Sprintf(`"HELLO-%d"`, i)
			if string(out) != want {
				bad.Add(1)
				t.Errorf("InvokeFilter %d: got %s, want %s", i, out, want)
			}
		}(i)
	}
	wg.Wait()
	if n := bad.Load(); n > 0 {
		t.Fatalf("%d/%d invocations failed", n, N)
	}
}

// TestBridge_RegistersFromManifest walks the manifest's hooks list and
// confirms each becomes a live registration on the bus.
func TestBridge_RegistersFromManifest(t *testing.T) {
	d, _ := loadFixture(t, "bridge_basic")
	bus := hostbus.NewBus()

	bridge, err := NewBridge("test-plugin", d, bus)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}

	m := &manifest.Manifest{
		Hooks: &manifest.Hooks{
			Actions: []string{"on_event"},
			Filters: []string{"the_content"},
		},
	}
	n, err := bridge.Register(context.Background(), m)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if n != 2 {
		t.Errorf("Register returned %d, want 2", n)
	}

	// Fire the action — it should reach the plugin and succeed.
	if err := bus.Do(context.Background(), "on_event"); err != nil {
		t.Errorf("bus.Do on_event: %v", err)
	}

	// Apply the filter — it should transform via the plugin.
	got, err := bus.ApplyFilters(context.Background(), "the_content", json.RawMessage(`"bridged"`))
	if err != nil {
		t.Fatalf("bus.ApplyFilters: %v", err)
	}
	gotBytes, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("filter result type = %T, want json.RawMessage", got)
	}
	if string(gotBytes) != `"BRIDGED"` {
		t.Errorf("filter result = %s, want %q", gotBytes, `"BRIDGED"`)
	}

	// After Unregister, the bus no longer routes to the plugin.
	bridge.Unregister()
	got2, err := bus.ApplyFilters(context.Background(), "the_content", json.RawMessage(`"after"`))
	if err != nil {
		t.Fatalf("bus.ApplyFilters after unreg: %v", err)
	}
	if rm, ok := got2.(json.RawMessage); ok && string(rm) != `"after"` {
		t.Errorf("after unregister, filter still ran: got %s", rm)
	}
}

// TestBridge_NilManifestHooks confirms a manifest with no hooks
// installs no registrations and produces no error.
func TestBridge_NilManifestHooks(t *testing.T) {
	d, _ := loadFixture(t, "bridge_nil")
	bus := hostbus.NewBus()
	bridge, err := NewBridge("plug", d, bus)
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
	bus := hostbus.NewBus()
	if _, err := NewBridge("", nil, bus); err == nil {
		t.Error("NewBridge with nil dispatcher: want error, got nil")
	}
	if _, err := NewBridge("", &Dispatcher{}, nil); err == nil {
		t.Error("NewBridge with nil bus: want error, got nil")
	}
}

// TestBridge_PropagatesError covers the error path through the bridge:
// the guest returned ResultStatusUnknownHook, the bus's Do call must
// surface the error.
func TestBridge_PropagatesError(t *testing.T) {
	d, _ := loadFixture(t, "bridge_err")
	bus := hostbus.NewBus()
	bridge, err := NewBridge("plug", d, bus)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	defer bridge.Unregister()

	// Register an action the guest doesn't recognise.
	m := &manifest.Manifest{
		Hooks: &manifest.Hooks{Actions: []string{"unknown.thing"}},
	}
	if _, err := bridge.Register(context.Background(), m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := bus.Do(context.Background(), "unknown.thing"); err == nil {
		t.Fatal("expected error from bus.Do, got nil")
	} else if !errors.Is(err, ErrUnknownHook) {
		t.Errorf("err = %v, want errors.Is ErrUnknownHook", err)
	}
}

// TestHookError_IsTraversal exercises the errors.Is matrix.
func TestHookError_IsTraversal(t *testing.T) {
	cases := []struct {
		status ResultStatus
		want   error
	}{
		{ResultStatusOutOfMemory, ErrOutOfMemory},
		{ResultStatusBadPayload, ErrBadPayload},
		{ResultStatusUnknownHook, ErrUnknownHook},
		{ResultStatusError, ErrGuestError},
		{ResultStatusTrap, ErrTrapped},
	}
	for _, c := range cases {
		he := &HookError{Hook: "x", Status: c.status}
		if !errors.Is(he, c.want) {
			t.Errorf("HookError{status=%s} is not %v", c.status, c.want)
		}
	}
	// Non-matching sentinel.
	he := &HookError{Hook: "x", Status: ResultStatusOK}
	if errors.Is(he, ErrOutOfMemory) {
		t.Error("HookError{OK} should not match ErrOutOfMemory")
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

	// Use a minimal module that exports nothing the ABI needs.
	mod, err := rt.LoadModule(ctx, "minimal", minimalWasm)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	d := NewDispatcher(mod)
	err = d.InvokeAction(ctx, "anything")
	if err == nil {
		t.Fatal("expected ErrMissingExport, got nil")
	}
	if !errors.Is(err, ErrMissingExport) {
		t.Errorf("err = %v, want errors.Is ErrMissingExport", err)
	}
}

// minimalWasm is a tiny module that exports nothing the ABI needs.
// Section-by-section layout follows the same conventions as
// runtime/testdata_test.go.
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
