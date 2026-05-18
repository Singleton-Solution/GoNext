package plugin

import (
	"context"
	"testing"
	"time"
)

// fakeWatcher is a FileWatcher implementation backed by buffered
// channels the test owns and feeds. It lets debounce and the watch
// loop be exercised with synthetic events.
type fakeWatcher struct {
	events chan WatchEvent
	errs   chan error
	closed bool
}

func newFakeWatcher(buf int) *fakeWatcher {
	return &fakeWatcher{
		events: make(chan WatchEvent, buf),
		errs:   make(chan error, buf),
	}
}

func (f *fakeWatcher) Events() <-chan WatchEvent { return f.events }
func (f *fakeWatcher) Errors() <-chan error      { return f.errs }
func (f *fakeWatcher) Close() error {
	if !f.closed {
		f.closed = true
		close(f.events)
		close(f.errs)
	}
	return nil
}

func TestDebounce_CollapsesBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan WatchEvent, 8)
	out := debounce(ctx, in, 30*time.Millisecond, time.Now)

	// Fire five events back-to-back. The debouncer should collapse
	// them into a single trailing-edge emission.
	for i := 0; i < 5; i++ {
		in <- WatchEvent{Path: "x"}
	}

	select {
	case <-out:
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("debounce never fired")
	}

	// Now wait long enough that a second emission would have happened
	// if the debouncer was buggy. Expect none.
	select {
	case <-out:
		t.Fatalf("debounce fired twice for a single burst")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestDebounce_TwoSeparateBurstsFireTwice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := make(chan WatchEvent, 4)
	out := debounce(ctx, in, 20*time.Millisecond, time.Now)

	in <- WatchEvent{Path: "a"}
	select {
	case <-out:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("first burst never fired")
	}

	// Quiet period: outside the debounce window.
	time.Sleep(60 * time.Millisecond)

	in <- WatchEvent{Path: "b"}
	select {
	case <-out:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("second burst never fired")
	}
}

func TestDebounce_ClosesOnInputClose(t *testing.T) {
	in := make(chan WatchEvent)
	out := debounce(context.Background(), in, 10*time.Millisecond, time.Now)
	close(in)
	select {
	case _, ok := <-out:
		if ok {
			t.Fatalf("expected closed channel; got value")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("out never closed after in closed")
	}
}

func TestDebounce_ClosesOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan WatchEvent, 1)
	out := debounce(ctx, in, 50*time.Millisecond, time.Now)
	in <- WatchEvent{Path: "x"}
	cancel()
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
			// A value may sneak through if the timer raced; keep
			// draining until the channel closes.
		case <-deadline:
			t.Fatalf("out never closed after context cancel")
		}
	}
}

func TestShouldIgnore(t *testing.T) {
	root := "/proj"
	cases := []struct {
		path string
		want bool
	}{
		{"/proj/build/plugin.wasm", true},
		{"/proj/target/wasm32-wasi/release/foo.wasm", true},
		{"/proj/node_modules/foo/index.js", true},
		{"/proj/.git/HEAD", true},
		{"/proj/src/main.go", false},
		{"/proj/manifest.json", false},
		{"/proj/src/.#vim-swap", true},
		{"/proj/src/main.go~", true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := shouldIgnore(tc.path, root)
			if got != tc.want {
				t.Errorf("shouldIgnore(%q) = %v; want %v", tc.path, got, tc.want)
			}
		})
	}
}
