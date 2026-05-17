package secrets

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// staticStore is a tiny in-memory Store for testing Require / MustGet
// behaviour without depending on Env or File adapters. It mimics the real
// adapter convention of wrapping ErrNotFound with the key so that the
// aggregated error from Require names every missing key.
type staticStore struct {
	values map[string]string
}

func (s *staticStore) Get(key string) (string, error) {
	v, ok := s.values[key]
	if !ok || v == "" {
		return "", fmt.Errorf("static %q: %w", key, ErrNotFound)
	}
	return v, nil
}

func (s *staticStore) MustGet(key string) string { return mustGet(s, key) }

func TestRequire(t *testing.T) {
	cases := []struct {
		name        string
		store       Store
		keys        []string
		wantErr     bool
		wantInError []string // substrings the joined error must contain
		notInError  []string // substrings the joined error must NOT contain
	}{
		{
			name:    "all keys present",
			store:   &staticStore{values: map[string]string{"A": "1", "B": "2"}},
			keys:    []string{"A", "B"},
			wantErr: false,
		},
		{
			name:        "single missing key",
			store:       &staticStore{values: map[string]string{"A": "1"}},
			keys:        []string{"A", "MISSING"},
			wantErr:     true,
			wantInError: []string{"MISSING"},
			notInError:  []string{`"A"`}, // present keys don't appear
		},
		{
			name:        "multiple missing keys aggregated",
			store:       &staticStore{values: map[string]string{}},
			keys:        []string{"DATABASE_URL", "PEPPER", "CSRF"},
			wantErr:     true,
			wantInError: []string{"DATABASE_URL", "PEPPER", "CSRF"},
		},
		{
			name:    "no keys is a no-op",
			store:   &staticStore{values: nil},
			keys:    nil,
			wantErr: false,
		},
		{
			name:        "value never appears in aggregated error",
			store:       &staticStore{values: map[string]string{"K": "secret-value-keep-quiet"}},
			keys:        []string{"K", "MISSING"},
			wantErr:     true,
			wantInError: []string{"MISSING"},
			notInError:  []string{"secret-value-keep-quiet"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Require(c.store, c.keys...)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				// Aggregated error keeps ErrNotFound semantics for missing keys.
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("want errors.Is(err, ErrNotFound), got %v", err)
				}
				msg := err.Error()
				for _, s := range c.wantInError {
					if !strings.Contains(msg, s) {
						t.Errorf("aggregated error missing %q:\n%s", s, msg)
					}
				}
				for _, s := range c.notInError {
					if strings.Contains(msg, s) {
						t.Errorf("aggregated error contained forbidden %q:\n%s", s, msg)
					}
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequire_NilStore(t *testing.T) {
	err := Require(nil, "ANY")
	if err == nil {
		t.Fatal("expected error for nil store")
	}
	if !strings.Contains(err.Error(), "nil store") {
		t.Errorf("error doesn't explain the cause: %v", err)
	}
}

func TestStore_MustGetSuccess(t *testing.T) {
	s := &staticStore{values: map[string]string{"K": "v"}}
	got := s.MustGet("K")
	if got != "v" {
		t.Errorf("MustGet = %q, want %q", got, "v")
	}
}
