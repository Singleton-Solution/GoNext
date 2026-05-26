package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

// EnvAuditHMACKey is the environment variable name from which the
// audit-log HMAC key is read. The variable is read once at process
// boot (via HMACKeyFromEnv) and never mutated; rotating the key
// requires an operator-driven snapshot+restart flow, not a runtime
// reload — chain verification depends on the same key being used end
// to end.
const EnvAuditHMACKey = "GONEXT_AUDIT_HMAC_KEY"

// minHMACKeyBytes is the floor for an audit HMAC key. 32 bytes (256
// bits) matches SHA-256's output and is the smallest sensible value;
// shorter keys are functional but defeat the threat model (an
// attacker who can guess a 16-byte key can forge any row's prev_hash).
//
// The check is enforced at process boot rather than at first emit so
// a mis-set deployment fails fast.
const minHMACKeyBytes = 32

// ErrInvalidHMACKey is returned by HMACKeyFromEnv (or the value's
// validation path) when the configured key fails the minimum-length
// check or the env var is unset. The error is intentionally
// distinguishable from a generic "audit init failed" so the operator
// can act on it.
var ErrInvalidHMACKey = fmt.Errorf("audit: invalid HMAC key (env %s)", EnvAuditHMACKey)

// HMACKeyFromEnv reads the chain HMAC key from EnvAuditHMACKey.
//
// The env value is treated as raw bytes (after the hex-decode
// fallback). Two encodings are accepted: a hex string ("abc123...")
// or a raw ASCII passphrase. The hex form is preferred — it lets the
// operator generate a key with `openssl rand -hex 32` and pin
// exactly 256 bits of entropy.
//
// Returns ErrInvalidHMACKey if the env var is unset or the resulting
// byte slice is shorter than minHMACKeyBytes.
func HMACKeyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(EnvAuditHMACKey))
	if raw == "" {
		return nil, fmt.Errorf("%w: env var is unset", ErrInvalidHMACKey)
	}
	// Try hex decode first; fall back to raw bytes if that fails.
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) >= minHMACKeyBytes {
		return decoded, nil
	}
	if len(raw) < minHMACKeyBytes {
		return nil, fmt.Errorf("%w: key is %d bytes, want >= %d", ErrInvalidHMACKey, len(raw), minHMACKeyBytes)
	}
	return []byte(raw), nil
}

// CanonicalBytes returns the byte sequence the HMAC is computed over
// for one event. The format is intentionally deterministic across Go
// versions and platforms:
//
//   <event_type> '\x1f' <actor_user_id> '\x1f' <actor_plugin_slug> '\x1f'
//   <resource_type> '\x1f' <resource_id> '\x1f' <ip> '\x1f'
//   <user_agent> '\x1f' <severity> '\x1f' <occurred_at_rfc3339nano> '\x1f'
//   <metadata_canonical>
//
// 0x1f is the ASCII unit-separator, chosen because it cannot appear
// in any UTF-8-encoded printable string (it's a control byte) — that
// keeps the encoding injection-proof without an escaping pass.
//
// metadata_canonical is the keys of e.Metadata sorted lexically, then
// concatenated as "k=v;" pairs with %v rendering. Maps are not order-
// stable in Go; without the explicit sort the hash would be
// non-deterministic.
//
// We deliberately exclude e.ID from the canonical bytes — the
// database assigns IDs on insert and they're not known at the moment
// the emitter computes prev_hash. The chain links event N's
// prev_hash to event N-1's full canonical bytes, which is enough to
// detect any tampering with the prior row's content.
func CanonicalBytes(e Event) []byte {
	var b strings.Builder
	const sep = "\x1f"
	b.WriteString(e.EventType)
	b.WriteString(sep)
	b.WriteString(e.ActorUserID)
	b.WriteString(sep)
	b.WriteString(e.ActorPluginSlug)
	b.WriteString(sep)
	b.WriteString(e.ResourceType)
	b.WriteString(sep)
	b.WriteString(e.ResourceID)
	b.WriteString(sep)
	b.WriteString(e.IP)
	b.WriteString(sep)
	b.WriteString(e.UserAgent)
	b.WriteString(sep)
	b.WriteString(string(e.Severity))
	b.WriteString(sep)
	if !e.Time.IsZero() {
		b.WriteString(e.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	b.WriteString(sep)
	b.WriteString(canonicalMetadata(e.Metadata))
	return []byte(b.String())
}

// canonicalMetadata renders e.Metadata as a sorted "k=v;" string.
// Nested maps are not supported in v1 — they'd require a recursive
// encoder and the audit table doesn't use them today. If a caller
// passes a nested map, the inner map renders via %v (the stdlib's
// fmt default), which is stable per-Go-release but not guaranteed
// stable across releases. The chain verifier handles this by
// reporting the cutover row.
func canonicalMetadata(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%v", m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// ChainHash returns HMAC-SHA256(key, canonical(prev)). Returns nil
// when prev is the zero Event (i.e. this is the chain root).
//
// The HMAC is keyed: an attacker with the audit_log table but without
// the key cannot recompute hashes to forge a row's predecessor link.
// Verification is a constant-time hmac.Equal check.
func ChainHash(key []byte, prev Event) []byte {
	// Event holds a map and isn't comparable; detect "zero" by
	// checking the fields the chain hash depends on. EventType is
	// required for any non-empty event so an empty EventType is a
	// reliable "zero" signal.
	if prev.EventType == "" && prev.Time.IsZero() && len(prev.Metadata) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(CanonicalBytes(prev))
	return mac.Sum(nil)
}

// ChainConfig wires a Store with the HMAC key + the "fetch previous
// row" callback the emitter needs to compute prev_hash. The callback
// is store-specific because the lookup is "most recent row, regardless
// of actor/event/severity" — Filter.Limit=1 with no other filters.
//
// A nil ChainConfig (or a nil Key) disables the chain: prev_hash is
// left as the caller set it (typically nil). That's how tests and
// dev mode opt out without a separate code path.
type ChainConfig struct {
	Key []byte
	// PrevFetcher returns the most-recent event in the store, or the
	// zero Event if the store is empty (signalling "this is the
	// chain root"). The implementation is expected to be cheap — the
	// emit-hot-path runs it on every state-changing call.
	PrevFetcher func() (Event, error)
}

// Valid reports whether c is wired enough to compute a chain hash.
// A nil c or nil c.Key disables the chain.
func (c *ChainConfig) Valid() bool {
	return c != nil && len(c.Key) >= minHMACKeyBytes && c.PrevFetcher != nil
}
