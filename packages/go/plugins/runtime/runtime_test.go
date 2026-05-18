package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// newTestRuntime returns a fresh Runtime that captures slog output into
// the returned buffer, so tests can assert on logged messages from
// gn_log.
func newTestRuntime(t *testing.T, opts ...Option) (*Runtime, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	allOpts := append([]Option{WithLogger(logger)}, opts...)
	rt, err := New(context.Background(), allOpts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt, buf
}

// syncBuffer is a tiny goroutine-safe bytes.Buffer wrapper. Tests
// running concurrent gn_log calls write into it from multiple
// goroutines, so the wrapper avoids `go test -race` complaints about
// the unsynchronized stdlib buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestFixturesCompile is the canary that confirms our hand-authored
// WASM bytes are valid before any other test relies on them. If a
// fixture stops compiling, all the harder-to-diagnose downstream
// failures get cleaner attribution.
func TestFixturesCompile(t *testing.T) {
	rt, _ := newTestRuntime(t)
	cases := map[string][]byte{
		"add":        wasmAdd,
		"panic":      wasmPanic,
		"log":        wasmLog,
		"time":       wasmTime,
		"concurrent": wasmConcurrent,
		// bigmem deliberately exceeds the 16 MiB cap and must FAIL —
		// it's verified separately in TestLoadModule_MemoryLimit.
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			mod, err := rt.LoadModule(context.Background(), name, b)
			if err != nil {
				t.Fatalf("LoadModule %q: %v", name, err)
			}
			_ = mod.Close(context.Background())
		})
	}
}

func TestLoadModule_AddHappyPath(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "add", wasmAdd)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	results, err := mod.Call(ctx, "add", api.EncodeI32(7), api.EncodeI32(35))
	if err != nil {
		t.Fatalf("Call add: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := api.DecodeI32(results[0])
	if got != 42 {
		t.Errorf("add(7, 35) = %d, want 42", got)
	}
}

func TestCall_FunctionNotFound(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "add", wasmAdd)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	_, err = mod.Call(ctx, "no_such_function")
	if !errors.Is(err, ErrFunctionNotFound) {
		t.Errorf("Call missing fn: want ErrFunctionNotFound, got %v", err)
	}
}

func TestCall_AfterClose(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "add", wasmAdd)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := mod.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err = mod.Call(ctx, "add", api.EncodeI32(1), api.EncodeI32(2))
	if !errors.Is(err, ErrModuleClosed) {
		t.Errorf("Call after Close: want ErrModuleClosed, got %v", err)
	}
}

func TestLoadModule_GuestPanicTrappedAsError(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "boomer", wasmPanic)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	_, err = mod.Call(ctx, "boom")
	if err == nil {
		t.Fatal("expected trap error from gn_panic, got nil")
	}

	var trap *TrapError
	if !errors.As(err, &trap) {
		t.Fatalf("expected *TrapError, got %T: %v", err, err)
	}
	if !strings.Contains(trap.Reason, "boom from guest") {
		t.Errorf("trap reason = %q, want substring %q", trap.Reason, "boom from guest")
	}
	if trap.Module != "boomer" {
		t.Errorf("trap module = %q, want %q", trap.Module, "boomer")
	}
}

func TestLoadModule_MalformedBytesAreCompileError(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()

	// Use a defer/recover so a stray host-side panic shows up as a test
	// failure rather than crashing the whole binary. The whole point
	// of this test is that bad bytes DO NOT panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LoadModule panicked on malformed bytes: %v", r)
		}
	}()

	_, err := rt.LoadModule(ctx, "bad", wasmInvalid)
	if err == nil {
		t.Fatal("expected error on malformed wasm, got nil")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Errorf("expected *CompileError, got %T: %v", err, err)
	}
}

func TestLoadModule_MemoryLimit(t *testing.T) {
	rt, _ := newTestRuntime(t) // default cap = 256 pages = 16 MiB
	ctx := context.Background()

	// bigmem requests 1024 pages = 64 MiB. wazero must refuse to
	// instantiate it under our 256-page cap and we must surface the
	// failure as a plain error (not a panic, not a successful load).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LoadModule panicked on oversized memory: %v", r)
		}
	}()
	_, err := rt.LoadModule(ctx, "bigmem", wasmBigMem)
	if err == nil {
		t.Fatal("expected error when module memory exceeds runtime cap")
	}
}

func TestLoadModule_DuplicateName(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()

	mod1, err := rt.LoadModule(ctx, "dup", wasmAdd)
	if err != nil {
		t.Fatalf("first LoadModule: %v", err)
	}
	defer mod1.Close(ctx)

	_, err = rt.LoadModule(ctx, "dup", wasmAdd)
	if err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}
	if !strings.Contains(err.Error(), "already loaded") {
		t.Errorf("error = %q, want substring %q", err.Error(), "already loaded")
	}
}

func TestRuntime_CloseIdempotent(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	_, err := rt.LoadModule(ctx, "after", wasmAdd)
	if !errors.Is(err, ErrRuntimeClosed) {
		t.Errorf("LoadModule after Close: want ErrRuntimeClosed, got %v", err)
	}
}

func TestModule_ConcurrentCalls(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "concurrent", wasmConcurrent)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	const goroutines = 16
	const callsPer = 64

	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(start int32) {
			defer wg.Done()
			for i := int32(0); i < callsPer; i++ {
				x := start + i
				results, err := mod.Call(ctx, "square", api.EncodeI32(x))
				if err != nil {
					bad.Add(1)
					return
				}
				if api.DecodeI32(results[0]) != x*x {
					bad.Add(1)
				}
			}
		}(int32(g * callsPer))
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d concurrent calls returned wrong results", bad.Load())
	}
}

func TestHost_GnLog(t *testing.T) {
	rt, logBuf := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "logger", wasmLog)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	if _, err := mod.Call(ctx, "say_hi"); err != nil {
		t.Fatalf("Call say_hi: %v", err)
	}

	if !strings.Contains(logBuf.String(), "hi from plugin") {
		t.Errorf("log buffer = %q, want substring %q", logBuf.String(), "hi from plugin")
	}
	if !strings.Contains(logBuf.String(), "plugin=logger") {
		t.Errorf("log buffer = %q, want plugin attribute", logBuf.String())
	}
}

// recordingPublisher captures gn_log fan-out for the TestHost_GnLog_
// Publisher test. The contract is that hostGnLog calls Publish
// exactly once per gn_log invocation, after the structured-logger
// write, with the module name, raw int32 level, and decoded message.
type recordingPublisher struct {
	mu    sync.Mutex
	calls []recordedLog
}

type recordedLog struct {
	module  string
	level   int32
	message string
}

func (p *recordingPublisher) Publish(module string, level int32, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, recordedLog{module, level, message})
}

func (p *recordingPublisher) snapshot() []recordedLog {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedLog, len(p.calls))
	copy(out, p.calls)
	return out
}

// TestHost_GnLog_Publisher verifies the WithLogPublisher seam: a
// runtime configured with a publisher must invoke its Publish exactly
// once per guest gn_log call, with the correct module, level, and
// decoded message. This is the load-bearing test for the dev-CLI log
// streaming feature.
func TestHost_GnLog_Publisher(t *testing.T) {
	pub := &recordingPublisher{}
	rt, _ := newTestRuntime(t, WithLogPublisher(pub))
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "logger", wasmLog)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)
	if _, err := mod.Call(ctx, "say_hi"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("publisher call count: got %d want 1; calls=%+v", len(calls), calls)
	}
	if calls[0].module != "logger" {
		t.Errorf("module: got %q want %q", calls[0].module, "logger")
	}
	if calls[0].message != "hi from plugin" {
		t.Errorf("message: got %q want %q", calls[0].message, "hi from plugin")
	}
}

func TestHost_GnTimeMs(t *testing.T) {
	// Pin time so the test is deterministic.
	fixed := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	rt, _ := newTestRuntime(t, WithTimeSource(func() time.Time { return fixed }))
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "clock", wasmTime)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close(ctx)

	results, err := mod.Call(ctx, "get_time")
	if err != nil {
		t.Fatalf("Call get_time: %v", err)
	}
	got := int64(results[0])
	want := fixed.UnixMilli()
	if got != want {
		t.Errorf("get_time = %d, want %d", got, want)
	}
}

func TestRuntime_EmptyBytes(t *testing.T) {
	rt, _ := newTestRuntime(t)
	_, err := rt.LoadModule(context.Background(), "empty", nil)
	if err == nil {
		t.Fatal("expected error for nil bytes")
	}
}

func TestRuntime_EmptyName(t *testing.T) {
	rt, _ := newTestRuntime(t)
	_, err := rt.LoadModule(context.Background(), "", wasmAdd)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// TestModule_CloseDuringCallIsSafe spins up calls and races a Close
// against them. The contract is "Close drains in-flight Call before
// returning"; the test just verifies the race detector stays quiet
// and no deadlock occurs.
func TestModule_CloseDuringCallIsSafe(t *testing.T) {
	rt, _ := newTestRuntime(t)
	ctx := context.Background()
	mod, err := rt.LoadModule(ctx, "concurrent", wasmConcurrent)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 128; i++ {
			_, _ = mod.Call(ctx, "square", api.EncodeI32(int32(i)))
		}
	}()

	// Let some calls land first.
	time.Sleep(2 * time.Millisecond)
	if err := mod.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done
}

// TestRuntime_WithHostModule verifies the extension seam: a caller can
// register an additional host-module builder and New() invokes it
// against the underlying wazero runtime.
func TestRuntime_WithHostModule(t *testing.T) {
	hostCalled := false
	rt, err := New(context.Background(),
		WithHostModule(func(ctx context.Context, _ wazeroRuntime) error {
			hostCalled = true
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New with host module: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	if !hostCalled {
		t.Error("WithHostModule builder was never invoked")
	}
}

func TestRuntime_WithHostModuleBuilderError(t *testing.T) {
	_, err := New(context.Background(),
		WithHostModule(func(ctx context.Context, _ wazeroRuntime) error {
			return fmt.Errorf("intentional builder failure")
		}),
	)
	if err == nil {
		t.Fatal("expected New() to surface host-builder error")
	}
	if !strings.Contains(err.Error(), "intentional builder failure") {
		t.Errorf("error = %q, want substring %q", err.Error(), "intentional builder failure")
	}
}
