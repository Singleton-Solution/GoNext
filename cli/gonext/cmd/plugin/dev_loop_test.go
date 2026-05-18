package plugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncBuf is a thread-safe wrapper around bytes.Buffer so the
// test-side observer can poll for substrings while the dev-loop
// goroutine writes diagnostics. bytes.Buffer is not safe for
// concurrent use.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// stubUploader records the number of Upload calls and the host each
// was made against. It satisfies [Uploader].
type stubUploader struct {
	N    atomic.Int32
	Host atomic.Value
	Err  error
}

func (s *stubUploader) Upload(_ context.Context, host, _ string) error {
	s.N.Add(1)
	s.Host.Store(host)
	return s.Err
}

// writeDevProjectWithGoMod plants a tinygo-shaped project at a fresh
// temp dir and returns the path. The fake runner is responsible for
// actually producing the artifact.
func writeDevProjectWithGoMod(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// frozenTime is a deterministic clock used by tprintf-driven tests.
func frozenTime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestRunDevLoop_BuildOnly(t *testing.T) {
	dir := writeDevProjectWithGoMod(t, `{"capabilities":["a","b"]}`)
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			// Write a fake artifact at the -o path.
			out := c.Args[len(c.Args)-3]
			return os.WriteFile(out, []byte{0x00}, 0o644)
		},
	}
	up := &stubUploader{}

	opts := devOptions{
		ProjectDir: dir,
		Host:       "http://nowhere.invalid",
		BuildOnly:  true,
		Lang:       "auto",
		Runner:     r,
		Uploader:   up,
		Watcher:    func(_ string) (FileWatcher, error) { t.Fatal("watcher should not be called"); return nil, nil },
		Now:        frozenTime,
	}
	var stdout, stderr bytes.Buffer
	if err := runDevLoop(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("runDevLoop: %v; stderr=%q", err, stderr.String())
	}
	if up.N.Load() != 0 {
		t.Errorf("uploader should not run in --build-only; calls=%d", up.N.Load())
	}
	if !strings.Contains(stdout.String(), "build-only") {
		t.Errorf("stdout missing build-only marker: %q", stdout.String())
	}
}

func TestRunDevLoop_BuildAndUpload_NoWatch(t *testing.T) {
	dir := writeDevProjectWithGoMod(t, `{"capabilities":["http.fetch"]}`)
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			out := c.Args[len(c.Args)-3]
			return os.WriteFile(out, []byte{0x00}, 0o644)
		},
	}
	up := &stubUploader{}
	opts := devOptions{
		ProjectDir: dir,
		Host:       "http://localhost:9999",
		Watch:      false,
		Lang:       "auto",
		Runner:     r,
		Uploader:   up,
		Watcher:    func(_ string) (FileWatcher, error) { t.Fatal("watcher should not be called"); return nil, nil },
		Now:        frozenTime,
	}
	var stdout, stderr bytes.Buffer
	if err := runDevLoop(context.Background(), opts, &stdout, &stderr); err != nil {
		t.Fatalf("runDevLoop: %v; stderr=%q", err, stderr.String())
	}
	if got := up.N.Load(); got != 1 {
		t.Errorf("want 1 upload; got %d", got)
	}
	if !strings.Contains(stdout.String(), "= http.fetch") {
		t.Errorf("expected first-build capability listing; got %q", stdout.String())
	}
}

func TestRunDevLoop_BuildFails_FirstPassReturnsError(t *testing.T) {
	dir := writeDevProjectWithGoMod(t, `{}`)
	r := &fakeRunner{Err: errors.New("toolchain boom")}
	up := &stubUploader{}
	opts := devOptions{
		ProjectDir: dir, Host: "http://localhost", Watch: false,
		Lang: "auto", Runner: r, Uploader: up, Now: frozenTime,
	}
	err := runDevLoop(context.Background(), opts, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("want error on first-pass build failure")
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("error %q missing build prefix", err)
	}
}

func TestRunDevLoop_WatchRebuildsOnEvent(t *testing.T) {
	dir := writeDevProjectWithGoMod(t, `{"capabilities":["a"]}`)
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			out := c.Args[len(c.Args)-3]
			return os.WriteFile(out, []byte{0x00}, 0o644)
		},
	}
	up := &stubUploader{}
	fake := newFakeWatcher(8)
	opts := devOptions{
		ProjectDir: dir, Host: "http://x", Watch: true,
		Lang: "auto", Runner: r, Uploader: up,
		Watcher: func(_ string) (FileWatcher, error) { return fake, nil },
		Now:     frozenTime,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- runDevLoop(ctx, opts, &stdout, &stderr)
	}()

	// Wait for the first build/upload to complete.
	waitFor(t, func() bool { return up.N.Load() >= 1 }, time.Second, "first upload")

	// Fire an event burst — the debouncer collapses to one rebuild.
	for i := 0; i < 4; i++ {
		fake.events <- WatchEvent{Path: filepath.Join(dir, "src.go")}
	}
	waitFor(t, func() bool { return up.N.Load() >= 2 }, time.Second, "rebuild upload")

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("loop returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("loop did not exit after cancel")
	}

	if r.Calls[0].Name != "tinygo" {
		t.Errorf("first call should be tinygo; got %q", r.Calls[0].Name)
	}
}

func TestRunDevLoop_WatchSurfacesBuildErrorsButKeepsRunning(t *testing.T) {
	dir := writeDevProjectWithGoMod(t, `{}`)
	var callCount atomic.Int32
	r := &fakeRunner{
		Hook: func(c fakeCall) error {
			n := callCount.Add(1)
			if n == 2 {
				return errors.New("transient toolchain failure")
			}
			out := c.Args[len(c.Args)-3]
			return os.WriteFile(out, []byte{0x00}, 0o644)
		},
	}
	up := &stubUploader{}
	fake := newFakeWatcher(8)
	opts := devOptions{
		ProjectDir: dir, Host: "http://x", Watch: true,
		Lang: "auto", Runner: r, Uploader: up,
		Watcher: func(_ string) (FileWatcher, error) { return fake, nil },
		Now:     frozenTime,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	stderr := &syncBuf{}
	go func() {
		stdout := &syncBuf{}
		done <- runDevLoop(ctx, opts, stdout, stderr)
	}()
	waitFor(t, func() bool { return up.N.Load() >= 1 }, time.Second, "first upload")

	// Trigger the failing rebuild.
	fake.events <- WatchEvent{Path: filepath.Join(dir, "src.go")}
	waitFor(t, func() bool { return callCount.Load() >= 2 }, time.Second, "second build attempt")
	waitFor(t, func() bool { return strings.Contains(stderr.String(), "transient") }, time.Second, "error message")

	// Now trigger a successful rebuild — the loop must have kept running.
	fake.events <- WatchEvent{Path: filepath.Join(dir, "src.go")}
	waitFor(t, func() bool { return up.N.Load() >= 2 }, time.Second, "second upload after recovery")

	cancel()
	<-done
}

// waitFor polls cond until it returns true or timeout elapses. It is
// the test-side complement to debounce: production has a 200ms window,
// tests override the watcher altogether so we just need a polling
// helper to wait for the goroutines to settle.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunDev_MissingArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := Run([]string{"dev"}, &stdout, &stderr)
	if got != ExitUsage {
		t.Errorf("exit = %d; want %d", got, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "missing project directory") {
		t.Errorf("stderr missing diagnostic: %q", stderr.String())
	}
}

func TestRunDev_ExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := Run([]string{"dev", "/a", "/b"}, &stdout, &stderr)
	if got != ExitUsage {
		t.Errorf("exit = %d; want %d", got, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "extra argument") {
		t.Errorf("stderr missing diagnostic: %q", stderr.String())
	}
}

func TestRunDev_NotADirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := Run([]string{"dev", "/no/such/path/at/all"}, &stdout, &stderr)
	if got != ExitFail {
		t.Errorf("exit = %d; want %d", got, ExitFail)
	}
	if !strings.Contains(stderr.String(), "is not a directory") {
		t.Errorf("stderr missing diagnostic: %q", stderr.String())
	}
}

func TestRunDev_HelpFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := Run([]string{"dev", "--help"}, &stdout, &stderr)
	if got != ExitOK {
		t.Errorf("exit = %d; want %d", got, ExitOK)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "--host") {
		t.Errorf("help missing --host: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
