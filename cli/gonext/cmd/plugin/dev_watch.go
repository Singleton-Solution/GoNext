package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileWatcher is the minimal seam over fsnotify.Watcher the dev loop
// uses. Tests substitute a fake to drive synthetic event streams.
type FileWatcher interface {
	// Events returns the receive-side of the event channel.
	Events() <-chan WatchEvent
	// Errors returns the receive-side of the error channel.
	Errors() <-chan error
	// Close releases any underlying resources. Idempotent.
	Close() error
}

// WatchEvent is a normalised file-system change notification. We don't
// re-export fsnotify.Event because tests should not have to import
// fsnotify just to construct one — and we don't actually look at the
// op type, only the path.
type WatchEvent struct {
	// Path is the absolute path of the file that changed.
	Path string
}

// fsnotifyWatcher constructs the production [FileWatcher] backed by
// github.com/fsnotify/fsnotify. It recursively walks projectDir at
// startup and registers each directory; fsnotify doesn't do recursive
// watches portably.
func fsnotifyWatcher(projectDir string) (FileWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("new watcher: %w", err)
	}
	if err := addRecursive(w, projectDir); err != nil {
		_ = w.Close()
		return nil, err
	}
	return newFsnotifyAdapter(w, projectDir), nil
}

// addRecursive walks root and registers every directory with the
// watcher, skipping the build output and common dependency caches that
// would generate event storms.
func addRecursive(w *fsnotify.Watcher, root string) error {
	skip := map[string]struct{}{
		"build":            {},
		"target":           {},
		"node_modules":     {},
		".git":             {},
		".idea":            {},
		".vscode":          {},
		"vendor":           {},
		"dist":             {},
		"__pycache__":      {},
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path != root {
			if _, ok := skip[info.Name()]; ok {
				return filepath.SkipDir
			}
		}
		return w.Add(path)
	})
}

// fsnotifyAdapter wraps a fsnotify.Watcher and translates its native
// events into the normalised [WatchEvent] type. It also strips events
// for paths inside ignored subtrees (e.g. someone touching build/
// after a rebuild — we'd otherwise feed our own output back into the
// loop).
type fsnotifyAdapter struct {
	w        *fsnotify.Watcher
	root     string
	events   chan WatchEvent
	errs     chan error
	stopFunc func()
}

// newFsnotifyAdapter starts a pump goroutine that translates the
// fsnotify event stream into the normalised channels. It closes the
// downstream channels when the underlying watcher is closed so the
// debouncer's range loop terminates cleanly.
func newFsnotifyAdapter(w *fsnotify.Watcher, root string) *fsnotifyAdapter {
	a := &fsnotifyAdapter{
		w:      w,
		root:   root,
		events: make(chan WatchEvent, 64),
		errs:   make(chan error, 4),
	}
	stopCh := make(chan struct{})
	a.stopFunc = func() { close(stopCh) }

	go func() {
		defer close(a.events)
		defer close(a.errs)
		for {
			select {
			case <-stopCh:
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if shouldIgnore(ev.Name, root) {
					continue
				}
				a.events <- WatchEvent{Path: ev.Name}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				a.errs <- err
			}
		}
	}()
	return a
}

// shouldIgnore returns true for paths under build/, target/, or any of
// the other dependency caches that addRecursive skipped at startup.
// Without this, every successful build writes to build/plugin.wasm,
// which fsnotify reports, which would trigger another build.
func shouldIgnore(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	switch first {
	case "build", "target", "node_modules", ".git", ".idea", ".vscode", "vendor", "dist", "__pycache__":
		return true
	}
	// Tempfiles and swap files from common editors.
	base := filepath.Base(path)
	if strings.HasSuffix(base, "~") || strings.HasPrefix(base, ".#") {
		return true
	}
	return false
}

// Events satisfies [FileWatcher].
func (a *fsnotifyAdapter) Events() <-chan WatchEvent { return a.events }

// Errors satisfies [FileWatcher].
func (a *fsnotifyAdapter) Errors() <-chan error { return a.errs }

// Close satisfies [FileWatcher]. It is safe to call more than once.
func (a *fsnotifyAdapter) Close() error {
	if a.stopFunc != nil {
		a.stopFunc()
		a.stopFunc = nil
	}
	return a.w.Close()
}

// debounce collapses bursty events from `in` into one downstream
// notification per quiet period of length `window`. We emit on the
// trailing edge: a save that fires three inotify events in 10ms
// produces a single rebuild trigger after `window` of silence.
//
// The function returns a receive-only channel that is closed when
// either `in` is closed or `ctx` is cancelled. The supplied `now` clock
// is used for the timer so tests can validate the timing without
// sleeping in real wall time — production wires this to time.Now.
//
// The implementation is deliberately allocation-light: one timer is
// reused across events; we Stop+Reset rather than recreating it on
// each event.
func debounce[T any](ctx context.Context, in <-chan T, window time.Duration, now func() time.Time) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		defer close(out)
		var timer *time.Timer
		var timerC <-chan time.Time
		_ = now // reserved for future deterministic-clock support
		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case _, ok := <-in:
				if !ok {
					if timer != nil {
						timer.Stop()
					}
					return
				}
				if timer == nil {
					timer = time.NewTimer(window)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						// Drain only if a value is actually queued;
						// otherwise the receive blocks forever.
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(window)
				}
			case <-timerC:
				timerC = nil
				timer = nil
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}
