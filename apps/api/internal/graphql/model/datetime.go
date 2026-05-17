package model

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// DateTime is the GraphQL DateTime scalar. We wrap time.Time so we
// can install the gqlgen Marshal/Unmarshal hooks without polluting the
// stdlib type. Always serialized RFC3339Nano, in UTC.
type DateTime struct {
	time.Time
}

// NewDateTime is a small constructor used by resolver tests to build
// fixtures without having to remember the wrapping.
func NewDateTime(t time.Time) DateTime { return DateTime{Time: t.UTC()} }

// MarshalGQL implements graphql.Marshaler. We write a JSON-quoted
// RFC3339Nano string — the same shape every other date scalar in our
// API uses (REST handlers and webhook payloads alike).
func (d DateTime) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(d.Time.UTC().Format(time.RFC3339Nano)))
}

// UnmarshalGQL implements graphql.Unmarshaler. Accepts a string in
// RFC3339Nano (or RFC3339; time.Parse is happy with either). Numeric
// epoch values are rejected — being strict on input format prevents
// the "did the client mean millis or seconds?" class of bug.
func (d *DateTime) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("DateTime must be a string, got %T", v)
	}
	if s == "" {
		return errors.New("DateTime cannot be empty")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("DateTime: %w", err)
	}
	d.Time = t.UTC()
	return nil
}
