package sdk

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// codec_test.go validates the wire-format codec the SDK uses to talk
// to the host. The host-side counterpart is exercised by
// packages/go/plugins/abi/hooks/abi_test.go; these tests pin the
// guest side of the same envelope so a regression in either half
// fails its own CI.
//
// We deliberately test the Marshal -> raw bytes path AND the
// host_marshal -> SDK Unmarshal round trip, because the contract is
// symmetric: every byte the host emits must round-trip through our
// decoder, and vice versa.

func TestPackUnpackResult(t *testing.T) {
	cases := []struct {
		name   string
		ptr    uint32
		length int32
	}{
		{"zero", 0, 0},
		{"small_positive", 100, 42},
		{"large_pointer", 0xFFFFFF00, 1},
		{"negative_status", 0, -3},
		{"negative_status_with_max_neg", 0, -2147483648},
		{"max_length", 0x1000, 0x7FFFFFFF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packed := PackResult(tc.ptr, tc.length)
			ptr, length := UnpackResult(packed)
			if ptr != tc.ptr || length != tc.length {
				t.Fatalf("round-trip mismatch: (%d, %d) -> %#x -> (%d, %d)",
					tc.ptr, tc.length, packed, ptr, length)
			}
		})
	}
}

func TestMarshalActionPayloadEmpty(t *testing.T) {
	// nil args should marshal to an empty-array envelope, NOT null —
	// the host's decoder always expects a JSON array.
	buf, err := MarshalActionPayload(nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"kind":"action","args":[]}`
	if string(buf) != want {
		t.Fatalf("got %q, want %q", buf, want)
	}
}

func TestMarshalActionPayloadWithArgs(t *testing.T) {
	buf, err := MarshalActionPayload([]any{"hello", 42.0, true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"kind":"action"`) {
		t.Errorf("missing kind field: %s", buf)
	}
	if !strings.Contains(string(buf), `"hello",42,true`) {
		t.Errorf("args not encoded: %s", buf)
	}
}

func TestUnmarshalActionPayloadRoundTrip(t *testing.T) {
	original := []any{"hello", 42.0, true, map[string]any{"k": "v"}}
	buf, err := MarshalActionPayload(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p, err := UnmarshalActionPayload(buf)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Kind != PayloadKindAction {
		t.Errorf("kind: got %q, want %q", p.Kind, PayloadKindAction)
	}
	if len(p.Args) != 4 {
		t.Fatalf("args length: got %d, want 4", len(p.Args))
	}
	if p.Args[0].(string) != "hello" {
		t.Errorf("args[0]: got %v, want hello", p.Args[0])
	}
	if p.Args[1].(float64) != 42.0 {
		t.Errorf("args[1]: got %v, want 42", p.Args[1])
	}
}

func TestUnmarshalActionPayloadRejectsBadKind(t *testing.T) {
	buf := []byte(`{"kind":"filter","args":[]}`)
	_, err := UnmarshalActionPayload(buf)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("expected ErrBadPayload, got %v", err)
	}
}

func TestUnmarshalActionPayloadRejectsEmpty(t *testing.T) {
	_, err := UnmarshalActionPayload(nil)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("expected ErrBadPayload for empty, got %v", err)
	}
	_, err = UnmarshalActionPayload([]byte(``))
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("expected ErrBadPayload for empty string, got %v", err)
	}
}

func TestUnmarshalActionPayloadRejectsInvalidJSON(t *testing.T) {
	_, err := UnmarshalActionPayload([]byte(`{not json`))
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("expected ErrBadPayload for invalid JSON, got %v", err)
	}
}

func TestMarshalFilterPayloadEmptyValue(t *testing.T) {
	buf, err := MarshalFilterPayload(nil, nil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"kind":"filter","value":null,"args":[]}`
	if string(buf) != want {
		t.Fatalf("got %q, want %q", buf, want)
	}
}

func TestMarshalFilterPayloadWithValueAndArgs(t *testing.T) {
	value := json.RawMessage(`"hello"`)
	args := []any{42.0}
	buf, err := MarshalFilterPayload(value, args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"kind":"filter","value":"hello","args":[42]}`
	if string(buf) != want {
		t.Fatalf("got %q, want %q", buf, want)
	}
}

func TestUnmarshalFilterPayloadRoundTrip(t *testing.T) {
	// encoding/json HTML-escapes <, >, & in raw messages — that's
	// fine on the wire (the host's decoder accepts both) but the
	// round-trip assertion has to compare values after a parse,
	// not against the raw bytes.
	original := json.RawMessage(`"<p>hi</p>"`)
	buf, err := MarshalFilterPayload(original, []any{42.0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p, err := UnmarshalFilterPayload(buf)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Kind != PayloadKindFilter {
		t.Errorf("kind: got %q, want %q", p.Kind, PayloadKindFilter)
	}
	var got string
	if err := json.Unmarshal(p.Value, &got); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}
	if got != "<p>hi</p>" {
		t.Errorf("value: got %q, want '<p>hi</p>'", got)
	}
	if len(p.Args) != 1 || p.Args[0].(float64) != 42 {
		t.Errorf("args: got %v, want [42]", p.Args)
	}
}

func TestUnmarshalFilterPayloadRejectsBadKind(t *testing.T) {
	buf := []byte(`{"kind":"action","value":null,"args":[]}`)
	_, err := UnmarshalFilterPayload(buf)
	if !errors.Is(err, ErrBadPayload) {
		t.Fatalf("expected ErrBadPayload, got %v", err)
	}
}

func TestMarshalFilterResultRoundTrip(t *testing.T) {
	value := json.RawMessage(`{"title":"updated"}`)
	buf, err := MarshalFilterResult(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalFilterResult(buf)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(out) != `{"title":"updated"}` {
		t.Errorf("got %s, want %s", out, value)
	}
}

func TestUnmarshalFilterResultEmpty(t *testing.T) {
	out, err := UnmarshalFilterResult(nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if out != nil {
		t.Errorf("expected nil result for empty input, got %s", out)
	}
}

func TestUnmarshalFilterResultMissingValue(t *testing.T) {
	// An envelope with a missing Value field should decode as
	// "null" — the SDK's normalisation of "no body".
	out, err := UnmarshalFilterResult([]byte(`{}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(out) != `null` {
		t.Errorf("got %s, want null", out)
	}
}

func TestHostErrorIs(t *testing.T) {
	e := &HostError{Function: "gn_kv_get", Status: -3}
	if !errors.Is(e, ErrHostFailure) {
		t.Errorf("HostError should match ErrHostFailure")
	}
	if errors.Is(e, ErrBadPayload) {
		t.Errorf("HostError should NOT match ErrBadPayload")
	}
}

func TestHostErrorError(t *testing.T) {
	e := &HostError{Function: "gn_kv_get", Status: -3}
	want := `sdk: host call "gn_kv_get" failed (status=-3)`
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}
