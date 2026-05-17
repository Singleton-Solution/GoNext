// Package router provides shared HTTP utilities that every REST package in
// the API mounts against: JSON response writers, RFC 7807 problem-details,
// cursor pagination helpers, and ETag/If-Match/If-None-Match parsing.
//
// The package owns no routes of its own. It is a small "framework" layer
// that lives alongside the route packages (posts, pages, taxonomies, …)
// so the same problem-details JSON shape and the same cursor encoding
// are produced across every endpoint without each package re-implementing
// the wheel.
//
// # Why a separate package?
//
// Issue #76 introduces the first REST surface in the API binary. Every
// subsequent REST issue (#77 taxonomies, #78 media, etc.) will want the
// same building blocks: a structured error body, cursor pagination, ETag
// helpers. Pulling them out now means later mounts compose against a
// stable surface; pulling them out later means each issue re-invents
// something subtly different.
//
// # Response shape
//
// All success responses are JSON with status 2xx. The package's
// [WriteJSON] writes the Content-Type header, marshals the body, and
// writes the status code in one call. Empty bodies are permitted
// (status only); pass a nil body.
//
// All error responses follow RFC 7807 Problem Details — application/
// problem+json with the canonical fields (type, title, status, detail,
// instance). [WriteError] builds the body from a code + message and
// [WriteProblem] takes a fully-formed [ProblemDetails].
//
// # Cursor pagination
//
// Cursors are opaque base64url-encoded strings. The on-wire form is
// intentionally opaque so the encoding can evolve (today: just a UUID;
// tomorrow: a tuple of (sort_key, id) when we add non-id sort orders)
// without breaking clients. See [EncodeCursor] / [ParseCursor].
//
// # Conditional GET
//
// REST routes participate in conditional-GET / optimistic-concurrency
// via two HTTP headers:
//
//   - ETag: a quoted string derived from the underlying row's
//     content_blocks_hash (posts) or its version (post_types, settings).
//     [FormatETag] / [ParseETag] handle the surrounding quotes.
//
//   - If-None-Match: client supplies the last-known ETag on GET; if it
//     matches, the handler returns 304 Not Modified instead of the
//     body. [MatchesIfNoneMatch] does the comparison.
//
//   - If-Match: client supplies the last-known version on PATCH/DELETE;
//     if it doesn't match the current row version, the handler returns
//     412 Precondition Failed. [ParseIfMatchVersion] / [VersionETag]
//     handle the (version → ETag) round-trip.
//
// The package is concurrency-safe: no package-level mutable state.
package router
