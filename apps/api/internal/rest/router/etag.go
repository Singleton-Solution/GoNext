package router

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ErrInvalidIfMatch is returned by [ParseIfMatchVersion] when the
// If-Match header is present but malformed (not a quoted integer).
// Callers translate this into a 400 with code "invalid_if_match".
var ErrInvalidIfMatch = errors.New("router: invalid If-Match header")

// FormatETag wraps a value in the strong-ETag double-quote form per
// RFC 9110 §8.8.3. The value is taken as-is — callers pass either a
// hex-encoded content hash (for read ETags) or a decimal version
// integer (for write ETags); both are RFC-valid token characters and
// don't need escaping.
//
// Returns the empty string when value is empty, so handlers can write
// FormatETag(post.Hash) unconditionally and skip setting the header
// when no hash is yet present.
func FormatETag(value string) string {
	if value == "" {
		return ""
	}
	return `"` + value + `"`
}

// ParseETag returns the inner value of a quoted ETag, stripping the
// surrounding double quotes and the optional weak-ETag W/ prefix. An
// unquoted input is returned verbatim — some clients send the value
// without quotes and there's no security reason to be strict.
//
// Returns the empty string for an empty input.
func ParseETag(etag string) string {
	if etag == "" {
		return ""
	}
	etag = strings.TrimSpace(etag)
	etag = strings.TrimPrefix(etag, "W/")
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1]
	}
	return etag
}

// HashETag returns the strong-ETag form of a raw byte hash. Used for
// read-side ETags on posts (the content_blocks_hash bytea). nil or
// empty input returns the empty string.
//
// We hex-encode rather than base64 because content_blocks_hash is
// already a binary digest; hex round-trips through the header layer
// without surprises (no =/+ chars to escape).
func HashETag(hash []byte) string {
	if len(hash) == 0 {
		return ""
	}
	return FormatETag(hex.EncodeToString(hash))
}

// VersionETag returns the strong-ETag form of an integer version. Used
// for write-side conditional checks (If-Match) on posts. Quoting the
// integer matches the strong-ETag form and lets clients store a single
// header value rather than separate hash + version fields.
func VersionETag(version int) string {
	return FormatETag(strconv.Itoa(version))
}

// ParseIfMatchVersion returns the integer version supplied by the
// client in the If-Match header. Returns:
//
//   - (0, false, nil)            — header absent. Caller decides
//     whether to require it.
//   - (n, true, nil)             — header present and parsed.
//   - (0, true, ErrInvalidIfMatch) — header present but malformed.
//
// Wildcard "*" is accepted and reported as (0, true, nil) with the
// caller responsible for treating it as "match any version". Posts
// CRUD does not currently use wildcard If-Match, but the parser
// tolerates it so future endpoints (e.g. "upsert if exists") can
// adopt the standard semantics without touching this helper.
func ParseIfMatchVersion(r *http.Request) (int, bool, error) {
	raw := r.Header.Get("If-Match")
	if raw == "" {
		return 0, false, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return 0, true, nil
	}
	value := ParseETag(raw)
	if value == "" {
		return 0, true, ErrInvalidIfMatch
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, true, ErrInvalidIfMatch
	}
	return n, true, nil
}

// MatchesIfNoneMatch reports whether the request's If-None-Match
// header matches the given current ETag (already in quoted form).
// Handlers call this on read paths; a true result means the client
// already has the latest representation and a 304 is appropriate.
//
// Per RFC 9110 §13.1.2, If-None-Match may be a comma-separated list of
// ETags or a single "*" wildcard. Both forms are honored. The
// comparison is byte-exact on the quoted form (weak comparison) —
// strong vs weak doesn't matter for our use case because we only ever
// generate strong ETags.
func MatchesIfNoneMatch(r *http.Request, current string) bool {
	if current == "" {
		return false
	}
	raw := r.Header.Get("If-None-Match")
	if raw == "" {
		return false
	}
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return true
	}
	for _, candidate := range strings.Split(raw, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == current {
			return true
		}
		// Tolerate the case where the client sent the inner value
		// without quotes — strip both sides and compare.
		if ParseETag(candidate) == ParseETag(current) && ParseETag(current) != "" {
			return true
		}
	}
	return false
}

// SetETag sets the ETag response header if etag is non-empty. A no-op
// for the empty string so handlers can call it unconditionally with
// HashETag/VersionETag output.
func SetETag(w http.ResponseWriter, etag string) {
	if etag == "" {
		return
	}
	w.Header().Set("ETag", etag)
}
