package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestNoopStore_Get(t *testing.T) {
	s := NewNoopStore()
	cases := []string{"FOO", "BAR_BAZ", "anything-at-all", ""}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			v, err := s.Get(k)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get(%q): err = %v, want errors.Is ErrNotFound", k, err)
			}
			if v != "" {
				t.Errorf("Get(%q): value = %q, want empty", k, v)
			}
		})
	}
}

func TestNoopStore_MustGetPanics(t *testing.T) {
	s := NewNoopStore()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustGet did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %T %v", r, r)
		}
		if !strings.HasPrefix(msg, "secrets: MustGet(") {
			t.Errorf("panic message prefix unexpected: %q", msg)
		}
		if !strings.Contains(msg, `"DATABASE_URL"`) {
			t.Errorf("panic message missing key: %q", msg)
		}
	}()
	s.MustGet("DATABASE_URL")
}
