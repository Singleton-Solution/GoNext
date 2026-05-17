package containers

import "testing"

// TestApply_Defaults checks that an empty options list leaves the
// defaults baseline untouched. Trivial, but it guards against future
// refactors of apply that might accidentally clobber defaults.
func TestApply_Defaults(t *testing.T) {
	t.Parallel()
	got := apply(config{version: "16-alpine", db: "gonext_test"}, nil)
	if got.version != "16-alpine" {
		t.Errorf("version: got %q, want %q", got.version, "16-alpine")
	}
	if got.db != "gonext_test" {
		t.Errorf("db: got %q, want %q", got.db, "gonext_test")
	}
	if got.image != "" {
		t.Errorf("image: got %q, want empty", got.image)
	}
}

// TestApply_Overrides exercises every Option constructor end-to-end so
// rename refactors get caught at unit-test time, before they need a
// Docker daemon to surface.
func TestApply_Overrides(t *testing.T) {
	t.Parallel()
	got := apply(config{version: "16-alpine", db: "gonext_test"}, []Option{
		WithVersion("15-alpine"),
		WithDB("orders_test"),
		WithImage("ghcr.io/example/pg:custom"),
	})
	if got.version != "15-alpine" {
		t.Errorf("version: got %q, want %q", got.version, "15-alpine")
	}
	if got.db != "orders_test" {
		t.Errorf("db: got %q, want %q", got.db, "orders_test")
	}
	if got.image != "ghcr.io/example/pg:custom" {
		t.Errorf("image: got %q, want override", got.image)
	}
}

// TestApply_NilOptionsSkipped guards apply against accidental nil
// entries — callers sometimes build option slices conditionally
// (`opts = append(opts, maybeOpt())` where maybeOpt may return nil)
// and we don't want that to panic.
func TestApply_NilOptionsSkipped(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("apply panicked on nil option: %v", r)
		}
	}()
	got := apply(config{version: "x"}, []Option{nil, WithVersion("y"), nil})
	if got.version != "y" {
		t.Errorf("version: got %q, want %q", got.version, "y")
	}
}
