package idempotency

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HeaderName is the canonical request header carrying the idempotency
// key. We follow the IETF draft
// (draft-ietf-httpapi-idempotency-key-header-06) exactly so SDKs that
// already implement the pattern interoperate without a custom shim.
const HeaderName = "Idempotency-Key"

// MaxKeyLength caps the header value at 255 bytes. The IETF draft
// recommends ≤ 255; we enforce it at both the Go layer (here) and the
// SQL CHECK constraint on idempotency_keys.key so a buggy client cannot
// blow out the column.
const MaxKeyLength = 255

// MinKeyLength is the lower bound for a key. We accept anything ≥ 1
// because the spec doesn't pin a minimum — clients pick the entropy
// they need. The Go layer rejects empty headers above this; sub-16
// chars is technically valid per RFC and we don't second-guess it.
const MinKeyLength = 1

// Status is the lifecycle state of an idempotency claim. It maps 1:1
// to the status TEXT column on idempotency_keys (migration 000014)
// and to the marker the Redis store returns from its Lua claim script.
type Status string

const (
	// StatusInProgress means the original handler is still running.
	// Replays during this window get 409 idempotency_key_pending —
	// the middleware does NOT block, the client is expected to retry
	// with exponential backoff.
	StatusInProgress Status = "in_progress"

	// StatusSucceeded means the original handler returned a 2xx and
	// the result is stored. Replays with the same request hash get
	// the stored result; replays with a different hash get 422.
	StatusSucceeded Status = "succeeded"

	// StatusFailed means the original handler returned a non-2xx and
	// the result is stored. We deliberately cache failures too — a
	// retry that re-runs business logic could turn a deterministic
	// 422 into a transient 500 just because the database moved. The
	// idempotent contract is "same key, same outcome".
	StatusFailed Status = "failed"
)

// Valid reports whether s is a recognised status. Used by the Postgres
// store when reading rows back so a corrupt status column (out-of-band
// admin edit, future migration) doesn't crash the middleware.
func (s Status) Valid() bool {
	switch s {
	case StatusInProgress, StatusSucceeded, StatusFailed:
		return true
	}
	return false
}

// Key is the parsed Idempotency-Key plus the SHA-256 of the canonical
// request it was first seen with. The middleware threads one of these
// through every claim and replay.
//
// Value is the raw header value, validated by [ValidateKeyValue].
// RequestHash is 32 bytes (sha256.Size) computed by [HashRequest].
type Key struct {
	// Value is the raw header value as the client sent it. We do NOT
	// hash this — operators need plaintext keys in their dashboards
	// to debug why a particular client keeps replaying. The column
	// on idempotency_keys is PK, so duplicate inserts are caught at
	// the DB layer.
	Value string

	// RequestHash is sha256(method || "\n" || path || "\n" || body).
	// When a client replays the same key with a different body, the
	// middleware compares hashes and returns 422 instead of a stale
	// cached response. Stored as raw bytes in Postgres BYTEA and hex
	// in Redis (Redis values are strings; we round-trip via hex so
	// the Lua script can compare with ==).
	RequestHash []byte
}

// ErrInvalidKey is returned by [NewKeyFromRequest] when the
// Idempotency-Key header is malformed (empty, oversize, control
// characters). The middleware translates this to 400.
var ErrInvalidKey = errors.New("idempotency: invalid key")

// ValidateKeyValue checks that v looks like a usable idempotency key:
// length within [MinKeyLength, MaxKeyLength], no control characters,
// no leading/trailing whitespace. We accept anything else because the
// spec is permissive — clients pick UUIDs, ULIDs, or whatever entropy
// scheme suits their stack, and second-guessing the alphabet here
// would only break correct clients.
func ValidateKeyValue(v string) error {
	if len(v) < MinKeyLength {
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	}
	if len(v) > MaxKeyLength {
		return fmt.Errorf("%w: length %d > %d", ErrInvalidKey, len(v), MaxKeyLength)
	}
	// Reject leading/trailing whitespace — they're almost always a
	// client-side bug (someone pasted "  uuid  " into a header field).
	if strings.TrimSpace(v) != v {
		return fmt.Errorf("%w: surrounding whitespace", ErrInvalidKey)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		// Bytes < 0x20 are C0 controls; 0x7F is DEL. Both are header-
		// smuggling vectors and have no legitimate use in an opaque
		// identifier.
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: control byte 0x%02x at offset %d", ErrInvalidKey, c, i)
		}
	}
	return nil
}

// HashRequest computes the canonical SHA-256 of an HTTP request for
// the idempotency replay-detection check. The canonical form is
//
//	method + "\n" + path + "\n" + body
//
// Headers are excluded by design: the same client might send slightly
// different headers on a retry (Accept-Encoding, User-Agent, request
// ID) without changing the semantic operation. Path includes query
// string because a retry to a different URL is, by construction, a
// different operation.
//
// body is the raw request body. The caller is responsible for
// preserving the bytes for the downstream handler (typically by
// reading into a buffer and replacing r.Body — see middleware.go).
func HashRequest(method, path string, body []byte) []byte {
	h := sha256.New()
	// We use \n as the separator and never base64-encode the body.
	// The components are length-prefixed implicitly: method has a
	// fixed alphabet (uppercase letters), path starts with "/", and
	// the body is the remainder of the digest. No ambiguity.
	h.Write([]byte(method))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write(body)
	return h.Sum(nil)
}

// NewKeyFromRequest extracts the Idempotency-Key header, reads the
// request body (replacing r.Body with a fresh reader so the downstream
// handler sees the same bytes), and returns the parsed [Key]. The
// caller is the middleware; handlers do NOT call this directly.
//
// If the header is absent, ok is false and err is nil — the caller
// should bypass idempotency entirely for that request. If the header
// is present but malformed, err wraps [ErrInvalidKey] and the
// middleware should reject with 400.
//
// maxBodySize caps the body bytes read into memory. Anything larger
// is treated as a malformed request — the middleware can't compute a
// canonical hash without buffering the full body, and a 10 GiB upload
// behind an Idempotency-Key header is a denial-of-service vector.
// Callers pass [DefaultMaxBodySize] when they don't have a stricter
// per-route bound.
func NewKeyFromRequest(r *http.Request, maxBodySize int64) (key Key, ok bool, err error) {
	v := r.Header.Get(HeaderName)
	if v == "" {
		return Key{}, false, nil
	}
	if err := ValidateKeyValue(v); err != nil {
		return Key{}, true, err
	}

	body, err := readAndReplaceBody(r, maxBodySize)
	if err != nil {
		return Key{}, true, err
	}

	hash := HashRequest(r.Method, r.URL.RequestURI(), body)
	return Key{Value: v, RequestHash: hash}, true, nil
}

// DefaultMaxBodySize is the buffering ceiling [NewKeyFromRequest]
// applies when the caller doesn't pass an explicit bound. 1 MiB
// matches what most JSON APIs already cap at the gateway; bodies that
// big behind an Idempotency-Key are essentially always a bug.
const DefaultMaxBodySize int64 = 1 << 20

// ErrBodyTooLarge is returned by [NewKeyFromRequest] when the request
// body exceeds the configured ceiling. The middleware maps this to
// 413 Payload Too Large.
var ErrBodyTooLarge = errors.New("idempotency: body exceeds max size")

// readAndReplaceBody buffers r.Body up to maxBodySize+1 bytes (so we
// can distinguish "exactly maxBodySize" from "overflow"), then swaps
// r.Body for a fresh reader over the buffered bytes so the downstream
// handler sees the same content. A nil body is treated as empty —
// the canonical hash still covers method + path so different routes
// don't collide.
func readAndReplaceBody(r *http.Request, maxBodySize int64) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	// io.LimitReader caps the read; reading exactly maxBodySize+1
	// bytes tells us "the body was at least that big" without
	// having to buffer the full overflow.
	limited := io.LimitReader(r.Body, maxBodySize+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("idempotency: read body: %w", err)
	}
	if int64(len(buf)) > maxBodySize {
		// We deliberately don't drain the remaining body — the caller
		// is going to reject the request anyway, and draining a hostile
		// uploader's tens of GiB is itself a DoS.
		return nil, fmt.Errorf("%w (%d > %d)", ErrBodyTooLarge, len(buf), maxBodySize)
	}
	// Replace the body so the downstream handler reads the same
	// bytes. Closing the original is best-effort — net/http will
	// also close it at the end of the request.
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytesReader(buf))
	return buf, nil
}

// bytesReader is a tiny helper that returns an io.Reader over b.
// Pulled out so the middleware tests can spy on the body-swap path
// without depending on bytes.NewReader's identity.
func bytesReader(b []byte) io.Reader {
	return &sliceReader{b: b}
}

// sliceReader is a minimal io.Reader over a byte slice. We avoid
// bytes.NewReader so this file doesn't pull in "bytes" — the package
// is intentionally small and this is the only place that needs one.
type sliceReader struct {
	b []byte
	i int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.i >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.i:])
	s.i += n
	return n, nil
}
