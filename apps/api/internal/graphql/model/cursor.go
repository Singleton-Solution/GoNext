package model

import (
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
)

// Cursor is the GraphQL Cursor scalar — an opaque base64 string used
// for Relay-style cursor pagination. Encodes a stable position
// (typically `{created_at}|{id}`) so that pagination is consistent
// across inserts; the wire format is intentionally hidden from
// clients so we can evolve it without breaking persisted queries.
type Cursor string

// String returns the wire form. Implemented so Cursor can be used as
// a map key or struct field without further conversion.
func (c Cursor) String() string { return string(c) }

// EncodeCursor takes a raw position string and returns the wire-form
// Cursor. Callers in resolvers do not construct Cursor values
// directly — they call EncodeCursor with whatever position string the
// underlying repository hands them.
func EncodeCursor(raw string) Cursor {
	return Cursor(base64.StdEncoding.EncodeToString([]byte(raw)))
}

// DecodeCursor reverses EncodeCursor. Returns an error for malformed
// input so resolvers can convert it into a GraphQL user-facing error
// rather than crashing.
func DecodeCursor(c Cursor) (string, error) {
	if c == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(string(c))
	if err != nil {
		return "", fmt.Errorf("cursor: %w", err)
	}
	return string(raw), nil
}

// MarshalGQL implements graphql.Marshaler. Writes the cursor as a
// JSON-quoted string.
func (c Cursor) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(string(c)))
}

// UnmarshalGQL implements graphql.Unmarshaler. Accepts a string and
// stores it verbatim — the resolver decodes it via DecodeCursor.
func (c *Cursor) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("Cursor must be a string, got %T", v)
	}
	*c = Cursor(s)
	return nil
}
