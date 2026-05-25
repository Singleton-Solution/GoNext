package render

import (
	"errors"
	"html/template"
	"testing"
)

func nopRenderer(_ Block, _ template.HTML, _ Context) (template.HTML, error) {
	return "", nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("core/paragraph", BlockSpec{Render: nopRenderer}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := r.Get("core/paragraph"); !ok {
		t.Fatal("Get returned false")
	}
	if !r.Has("core/paragraph") {
		t.Fatal("Has returned false")
	}
}

func TestRegistry_DuplicateReturnsError(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	spec := BlockSpec{Render: nopRenderer}
	if err := r.Register("core/heading", spec); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("core/heading", spec)
	if !errors.Is(err, ErrDuplicateBlockType) {
		t.Fatalf("expected ErrDuplicateBlockType, got %v", err)
	}
}

func TestRegistry_NilRendererRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("core/heading", BlockSpec{}); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRegistry_EmptyTypeRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register("", BlockSpec{Render: nopRenderer}); err == nil {
		t.Fatal("expected error for empty type, got nil")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register("core/heading", BlockSpec{Render: nopRenderer})
	if !r.Unregister("core/heading") {
		t.Fatal("Unregister returned false")
	}
	if r.Has("core/heading") {
		t.Fatal("Has returned true after Unregister")
	}
	if r.Unregister("missing") {
		t.Fatal("Unregister returned true for missing block")
	}
}

func TestRegistry_NamesLexicographic(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register("zeta/zz", BlockSpec{Render: nopRenderer})
	_ = r.Register("alpha/aa", BlockSpec{Render: nopRenderer})
	_ = r.Register("midd/mm", BlockSpec{Render: nopRenderer})
	got := r.Names()
	want := []string{"alpha/aa", "midd/mm", "zeta/zz"}
	if len(got) != len(want) {
		t.Fatalf("len(Names) = %d, want %d", len(got), len(want))
	}
	for i, n := range got {
		if n != want[i] {
			t.Fatalf("Names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestRegistry_MustRegisterPanicsOnDup(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("core/heading", BlockSpec{Render: nopRenderer})
	defer func() {
		if got := recover(); got == nil {
			t.Fatal("expected panic, got none")
		}
	}()
	r.MustRegister("core/heading", BlockSpec{Render: nopRenderer})
}

func TestRegistry_Len(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("Len = %d, want 0", r.Len())
	}
	_ = r.Register("a", BlockSpec{Render: nopRenderer})
	_ = r.Register("b", BlockSpec{Render: nopRenderer})
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want 2", r.Len())
	}
}
