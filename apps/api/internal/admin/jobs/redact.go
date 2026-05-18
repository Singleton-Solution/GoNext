package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
)

// applyRedaction returns a copy of the payload with the redaction
// mask applied. The function is total — it never returns an error;
// payloads that aren't a JSON object are wholesale-redacted to the
// sentinel string when ANY field redaction is requested, on the
// principle that a non-object payload may still carry sensitive
// material and a partial mask isn't possible.
//
// The implementation walks the payload as a streaming JSON decoder
// rather than json.Unmarshal-to-map → json.Marshal because:
//
//   - Streaming preserves the original ordering of fields, which
//     operators rely on when diffing the masked preview against an
//     unmasked one in their head.
//   - It preserves any duplicate-key oddities (rare but possible from
//     legacy producers) rather than silently collapsing.
//
// The output is always a freshly-allocated slice; the caller can hand
// it to a JSON encoder without worrying about aliasing the input.
func applyRedaction(payload []byte, red Redaction) []byte {
	if len(red.Fields) == 0 {
		// No redaction record — return as-is. We do NOT defensively
		// copy here; callers treat the result as read-only.
		return payload
	}
	if !looksLikeJSONObject(payload) {
		// Non-object payload (string, number, array, garbage). We can't
		// surgically mask a top-level field if there is no top-level
		// field — fall back to the sentinel.
		return []byte(`"` + redactedSentinel + `"`)
	}

	masked, err := redactObjectFields(payload, red.fieldSet())
	if err != nil {
		// JSON parse failure mid-stream. Rather than return the raw
		// (possibly-sensitive) payload, fall back to the sentinel.
		return []byte(`"` + redactedSentinel + `"`)
	}
	return masked
}

// looksLikeJSONObject reports whether the payload is most likely a
// top-level JSON object. We trim leading whitespace and check the
// first non-whitespace byte. This is a cheap pre-flight; the proper
// decoder catches malformed inputs.
func looksLikeJSONObject(b []byte) bool {
	trimmed := bytes.TrimLeft(b, " \t\n\r")
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// fieldSet returns the redaction fields as a map for O(1) membership
// tests. Built once per redact call; the slice is small enough that
// the allocation is dwarfed by the JSON work.
func (r Redaction) fieldSet() map[string]struct{} {
	out := make(map[string]struct{}, len(r.Fields))
	for _, f := range r.Fields {
		out[f] = struct{}{}
	}
	return out
}

// redactObjectFields rewrites the JSON object in payload, replacing the
// value of every top-level field named in mask with the redacted
// sentinel string. Nested objects are left untouched — operators want
// a focused mask, not a sweeping one.
func redactObjectFields(payload []byte, mask map[string]struct{}) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("admin/jobs: payload is not a JSON object")
	}

	var out bytes.Buffer
	out.WriteByte('{')
	first := true

	for dec.More() {
		// Decode the field name.
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, errors.New("admin/jobs: non-string object key")
		}
		// Decode the value preserving its raw JSON form.
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		// Always quote-escape the key — the JSON spec mandates
		// double-quoted keys regardless of content.
		encoded, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		out.Write(encoded)
		out.WriteByte(':')
		if _, redacted := mask[key]; redacted {
			out.WriteByte('"')
			out.WriteString(redactedSentinel)
			out.WriteByte('"')
		} else {
			out.Write(raw)
		}
	}

	out.WriteByte('}')
	return out.Bytes(), nil
}

// sortedFields returns the redaction fields sorted lexicographically.
// Used by the audit emission so the event metadata is deterministic
// regardless of insert order. Not used on the hot path of redact.
func sortedFields(fields []string) []string {
	out := make([]string, len(fields))
	copy(out, fields)
	sort.Strings(out)
	return out
}
