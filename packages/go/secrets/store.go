package secrets

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned by Store.Get when a key has no value in the
// backend. Callers should check with errors.Is(err, ErrNotFound) — not by
// string comparison — because adapters may wrap it with redacted context.
var ErrNotFound = errors.New("secret not found")

// Store is the system-wide secret backend. Implementations must be safe
// for concurrent use by multiple goroutines: GoNext fetches secrets on
// the request path (for OAuth client lookups, for example), and the
// boot-time fetch and request-time fetch can overlap.
//
// Get returns the secret value for key. If the key has no value, Get
// returns ErrNotFound, possibly wrapped with redacted context — never
// the value itself. An empty string is treated as "not found" so that
// `${FOO:-}` style docker-compose substitution doesn't silently pass.
//
// MustGet returns the value or panics. The panic message contains the
// key but never the value. Reserved for boot-time constants the
// process can't run without; for request-time reads, use Get.
type Store interface {
	Get(key string) (value string, err error)
	MustGet(key string) string
}

// mustGet is the shared MustGet implementation. Adapters call it so that
// every Store panics with the same, predictable, redacted message shape.
// Tests assert on this exact prefix.
func mustGet(s Store, key string) string {
	v, err := s.Get(key)
	if err != nil {
		// Deliberately no %v on err: it's already redacted, but doubling up
		// keeps callers honest if a future adapter forgets.
		panic(fmt.Sprintf("secrets: MustGet(%q) failed: %s", key, err))
	}
	return v
}
