package secrets

import "fmt"

// NoopStore returns ErrNotFound for every key. It exists for unit tests
// that want to assert a code path doesn't depend on a real secret backend,
// and for boot-time checks where a developer wants to see which keys the
// process will request before wiring up Vault or AWS-SM.
//
// NoopStore's MustGet is the same as any other Store's: it panics. If you
// want a non-panicking placeholder, hand callers a Store you can stub with
// a static map instead.
type NoopStore struct{}

// NewNoopStore returns a zero-value NoopStore.
func NewNoopStore() *NoopStore { return &NoopStore{} }

// Get always returns ErrNotFound, wrapped with the key.
func (s *NoopStore) Get(key string) (string, error) {
	return "", fmt.Errorf("noop %q: %w", key, ErrNotFound)
}

// MustGet always panics with a redacted message.
func (s *NoopStore) MustGet(key string) string { return mustGet(s, key) }
