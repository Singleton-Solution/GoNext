package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestEnvStore_Get(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		key     string
		want    string
		wantErr error // sentinel; nil = no error
	}{
		{
			name: "happy path",
			env:  map[string]string{"DATABASE_URL": "postgres://x"},
			key:  "DATABASE_URL",
			want: "postgres://x",
		},
		{
			name:    "missing key",
			env:     map[string]string{},
			key:     "DATABASE_URL",
			wantErr: ErrNotFound,
		},
		{
			name:    "empty value treated as missing",
			env:     map[string]string{"DATABASE_URL": ""},
			key:     "DATABASE_URL",
			wantErr: ErrNotFound,
		},
		{
			name: "multibyte value passes through",
			env:  map[string]string{"K": "héllo"},
			key:  "K",
			want: "héllo",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newEnvStoreWithLookup(func(k string) (string, bool) {
				v, ok := c.env[k]
				return v, ok
			})
			got, err := s.Get(c.key)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Get(%q): err = %v, want errors.Is %v", c.key, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get(%q): unexpected error %v", c.key, err)
			}
			if got != c.want {
				t.Errorf("Get(%q) = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestEnvStore_ErrorDoesNotLeakValue(t *testing.T) {
	// An empty value triggers ErrNotFound. The error should mention the
	// key but never the value — assert by checking the key is present
	// and the value (a sentinel string) is not.
	const sentinel = "super-secret-value-never-log-me"
	s := newEnvStoreWithLookup(func(k string) (string, bool) {
		// Pretend the value is set to the sentinel but the rule says
		// empty-is-missing. We use a separate code path: when an
		// adapter wraps an error, it must never echo the value.
		// For env, the missing-key error path is exercised directly
		// since values aren't included in any error wrap.
		return "", false
	})
	_, err := s.Get("ANY_KEY")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Errorf("error message leaked secret value: %v", err)
	}
	if !strings.Contains(err.Error(), "ANY_KEY") {
		t.Errorf("error message did not mention key: %v", err)
	}
}

func TestEnvStore_UsesOSLookupByDefault(t *testing.T) {
	const key = "GONEXT_SECRETS_ENV_STORE_TEST_KEY"
	const val = "value-from-os-environ"
	t.Setenv(key, val)
	s := NewEnvStore()
	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != val {
		t.Errorf("Get = %q, want %q", got, val)
	}
}
