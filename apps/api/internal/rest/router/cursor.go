package router

import (
	"encoding/base64"
	"errors"
)

// ErrInvalidCursor is returned by [ParseCursor] when the cursor cannot
// be decoded. Callers translate this into a 400 with code
// "invalid_cursor"; the underlying bytes are not surfaced to clients
// (they're useless for forging anyway).
var ErrInvalidCursor = errors.New("router: invalid cursor")

// cursorEncoding is the unpadded URL-safe base64 alphabet. Unpadded so
// the cursor reads cleanly in a query string without %= escapes.
var cursorEncoding = base64.RawURLEncoding

// EncodeCursor returns an opaque cursor that encodes raw. The current
// implementation is a thin base64url wrapper because issue #76's
// cursors are UUIDs (Posts.ID). When non-id sort orders land in a
// follow-up issue, we'll change the inner bytes to a (sort_key, id)
// tuple — the on-wire opaque-base64url contract is what stays stable.
//
// Empty raw returns the empty string (no cursor), which is the natural
// "no more pages" sentinel.
func EncodeCursor(raw string) string {
	if raw == "" {
		return ""
	}
	return cursorEncoding.EncodeToString([]byte(raw))
}

// ParseCursor decodes a cursor produced by [EncodeCursor]. The empty
// string yields ("", nil) — clients pass no cursor on the first page.
// A malformed cursor returns [ErrInvalidCursor].
func ParseCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	decoded, err := cursorEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvalidCursor
	}
	return string(decoded), nil
}

// PageInfo is the envelope returned with paginated responses. The shape
// mirrors what the issue spec requires:
//
//	{ "data": [...], "pagination": { "next_cursor": "...", "prev_cursor": "..." } }
//
// Both cursors are omitted when empty so a final page renders as
// {"next_cursor": ""} rather than a missing key — that's the natural
// "no more pages" signal for clients.
type PageInfo struct {
	NextCursor string `json:"next_cursor"`
	PrevCursor string `json:"prev_cursor"`
}

// Page wraps a typed data slice with PageInfo. Used as the JSON body
// for paginated list endpoints. Callers construct one and pass it to
// [WriteJSON].
type Page[T any] struct {
	Data       []T      `json:"data"`
	Pagination PageInfo `json:"pagination"`
}
