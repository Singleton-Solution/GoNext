package csp

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Report is the normalized, flat shape we hand to the logger and
// counter. Both the legacy (application/csp-report) and modern
// (application/reports+json) wire formats are decoded into this
// type so downstream code does not need to fork on protocol version.
//
// Unknown / missing fields default to the zero value; callers should
// treat empty strings as "not provided by the browser".
type Report struct {
	DocumentURI        string
	Referrer           string
	BlockedURI         string
	ViolatedDirective  string
	EffectiveDirective string
	OriginalPolicy     string
	Disposition        string
	SourceFile         string
	LineNumber         int
	ColumnNumber       int
	StatusCode         string
}

// legacyEnvelope models the application/csp-report wire shape:
//
//	{ "csp-report": { ... } }
//
// Each browser supplies a slightly different field-name set; we use
// json.RawMessage + permissive decoding to stay compatible.
type legacyEnvelope struct {
	CSPReport json.RawMessage `json:"csp-report"`
}

// reportFields holds the union of legacy and modern report fields with
// json tags for the legacy spelling. The modern Reporting API uses the
// same fields under a different envelope; we decode it separately
// (see decodeModernBody) and reuse this struct.
type reportFields struct {
	DocumentURI        string          `json:"document-uri,omitempty"`
	Referrer           string          `json:"referrer,omitempty"`
	BlockedURI         string          `json:"blocked-uri,omitempty"`
	ViolatedDirective  string          `json:"violated-directive,omitempty"`
	EffectiveDirective string          `json:"effective-directive,omitempty"`
	OriginalPolicy     string          `json:"original-policy,omitempty"`
	Disposition        string          `json:"disposition,omitempty"`
	SourceFile         string          `json:"source-file,omitempty"`
	LineNumber         json.RawMessage `json:"line-number,omitempty"`
	ColumnNumber       json.RawMessage `json:"column-number,omitempty"`
	StatusCode         json.RawMessage `json:"status-code,omitempty"`
}

// modernUnderscoreFields mirrors reportFields with underscore-separated
// json keys, which the Reporting API uses inside its "body" object.
type modernUnderscoreFields struct {
	DocumentURL        string          `json:"documentURL,omitempty"`
	DocumentURI        string          `json:"document_uri,omitempty"`
	Referrer           string          `json:"referrer,omitempty"`
	BlockedURL         string          `json:"blockedURL,omitempty"`
	BlockedURI         string          `json:"blocked_uri,omitempty"`
	ViolatedDirective  string          `json:"violatedDirective,omitempty"`
	EffectiveDirective string          `json:"effectiveDirective,omitempty"`
	OriginalPolicy     string          `json:"originalPolicy,omitempty"`
	Disposition        string          `json:"disposition,omitempty"`
	SourceFile         string          `json:"sourceFile,omitempty"`
	LineNumber         json.RawMessage `json:"lineNumber,omitempty"`
	ColumnNumber       json.RawMessage `json:"columnNumber,omitempty"`
	StatusCode         json.RawMessage `json:"statusCode,omitempty"`
}

// modernEnvelope models a single entry in the Reporting API JSON array:
//
//	[ { "type": "csp-violation", "age": …, "url": …, "body": { ... } }, … ]
//
// Only csp-violation entries are decoded; other report types (e.g.
// "deprecation", "intervention") are silently skipped.
type modernEnvelope struct {
	Type string          `json:"type"`
	URL  string          `json:"url"`
	Body json.RawMessage `json:"body"`
}

// parseReportBody decodes body in either legacy or modern shape,
// returning the normalized reports and the kind label ("legacy" /
// "modern") for the metric counter.
//
// kindHint comes from the Content-Type classifier. "legacy" and
// "modern" select the matching decoder directly. "auto" tries legacy
// first then modern.
func parseReportBody(body []byte, kindHint string) ([]Report, string, error) {
	switch kindHint {
	case "legacy":
		reports, err := decodeLegacyBody(body)
		return reports, "legacy", err
	case "modern":
		reports, err := decodeModernBody(body)
		return reports, "modern", err
	case "auto":
		// Heuristic: a body starting with "[" is the modern array;
		// otherwise try the legacy envelope first.
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			if reports, err := decodeModernBody(body); err == nil && len(reports) > 0 {
				return reports, "modern", nil
			}
		}
		if reports, err := decodeLegacyBody(body); err == nil && len(reports) > 0 {
			return reports, "legacy", nil
		}
		// Fall through to a final attempt at the other format in case
		// the heuristic mis-guessed.
		if len(trimmed) > 0 && trimmed[0] == '{' {
			if reports, err := decodeModernBody(body); err == nil && len(reports) > 0 {
				return reports, "modern", nil
			}
		}
		return nil, "", errMalformed
	}
	return nil, "", errMalformed
}

func decodeLegacyBody(body []byte) ([]Report, error) {
	var env legacyEnvelope
	if err := jsonDecode(body, &env); err != nil {
		return nil, err
	}
	if len(env.CSPReport) == 0 {
		return nil, errMalformed
	}
	var fields reportFields
	if err := json.Unmarshal(env.CSPReport, &fields); err != nil {
		return nil, err
	}
	r := Report{
		DocumentURI:        fields.DocumentURI,
		Referrer:           fields.Referrer,
		BlockedURI:         fields.BlockedURI,
		ViolatedDirective:  fields.ViolatedDirective,
		EffectiveDirective: fields.EffectiveDirective,
		OriginalPolicy:     fields.OriginalPolicy,
		Disposition:        fields.Disposition,
		SourceFile:         fields.SourceFile,
		LineNumber:         decodeIntField(fields.LineNumber),
		ColumnNumber:       decodeIntField(fields.ColumnNumber),
		StatusCode:         decodeStringOrIntField(fields.StatusCode),
	}
	if r.EffectiveDirective == "" {
		// Many browsers only set violated-directive; mirror it.
		r.EffectiveDirective = r.ViolatedDirective
	}
	// Reject reports that contain no recognizable field — those are
	// almost certainly malformed clients.
	if r.DocumentURI == "" && r.BlockedURI == "" && r.ViolatedDirective == "" {
		return nil, errMalformed
	}
	return []Report{r}, nil
}

func decodeModernBody(body []byte) ([]Report, error) {
	// Modern Reporting API wraps reports in a top-level array, but a
	// few intermediaries also POST a single object. Accept either.
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errMalformed
	}

	var envelopes []modernEnvelope
	if trimmed[0] == '[' {
		if err := json.Unmarshal(body, &envelopes); err != nil {
			return nil, err
		}
	} else {
		var one modernEnvelope
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, err
		}
		envelopes = []modernEnvelope{one}
	}

	out := make([]Report, 0, len(envelopes))
	for _, env := range envelopes {
		if env.Type != "" && env.Type != "csp-violation" && env.Type != "csp" {
			continue
		}
		// The Reporting API uses camelCase keys inside body; the
		// legacy spelling also occasionally leaks through proxies.
		// Decode the body twice and merge — whichever set the
		// browser populated wins.
		var modern modernUnderscoreFields
		_ = json.Unmarshal(env.Body, &modern)
		var legacy reportFields
		_ = json.Unmarshal(env.Body, &legacy)

		r := Report{
			DocumentURI:        pickString(modern.DocumentURL, modern.DocumentURI, legacy.DocumentURI, env.URL),
			Referrer:           pickString(modern.Referrer, legacy.Referrer),
			BlockedURI:         pickString(modern.BlockedURL, modern.BlockedURI, legacy.BlockedURI),
			ViolatedDirective:  pickString(modern.ViolatedDirective, legacy.ViolatedDirective),
			EffectiveDirective: pickString(modern.EffectiveDirective, legacy.EffectiveDirective),
			OriginalPolicy:     pickString(modern.OriginalPolicy, legacy.OriginalPolicy),
			Disposition:        pickString(modern.Disposition, legacy.Disposition),
			SourceFile:         pickString(modern.SourceFile, legacy.SourceFile),
			LineNumber:         pickInt(modern.LineNumber, legacy.LineNumber),
			ColumnNumber:       pickInt(modern.ColumnNumber, legacy.ColumnNumber),
			StatusCode:         pickStringOrInt(modern.StatusCode, legacy.StatusCode),
		}
		if r.EffectiveDirective == "" {
			r.EffectiveDirective = r.ViolatedDirective
		}
		if r.DocumentURI == "" && r.BlockedURI == "" && r.ViolatedDirective == "" {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errMalformed
	}
	return out, nil
}

// decodeIntField unmarshals a JSON value that may be either a number or
// a quoted-number string into an int. Returns 0 on failure (matching
// the Report struct's zero-value semantic).
func decodeIntField(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	// Trim surrounding quotes if present.
	s := string(bytes.Trim(raw, "\""))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// decodeStringOrIntField decodes a JSON value that may be either a
// quoted string or an unquoted number, returning a canonical string.
// Used for status-code which Chrome emits as a number but Firefox as a
// string.
func decodeStringOrIntField(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
		return ""
	}
	return string(trimmed)
}

// pickString returns the first non-empty string from candidates.
func pickString(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// pickInt returns the first non-zero int decoded from the raw fields.
func pickInt(candidates ...json.RawMessage) int {
	for _, c := range candidates {
		if n := decodeIntField(c); n != 0 {
			return n
		}
	}
	return 0
}

// pickStringOrInt returns the first non-empty value decoded by
// decodeStringOrIntField.
func pickStringOrInt(candidates ...json.RawMessage) string {
	for _, c := range candidates {
		if v := decodeStringOrIntField(c); v != "" {
			return v
		}
	}
	return ""
}
