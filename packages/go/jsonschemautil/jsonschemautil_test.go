package jsonschemautil

import (
	"errors"
	"strings"
	"testing"
)

// TestCompile_AcceptsValid2020Schema covers the happy path: a schema
// declaring the pinned dialect compiles cleanly and the returned
// *jsonschema.Schema validates instances correctly.
func TestCompile_AcceptsValid2020Schema(t *testing.T) {
	raw := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)

	schema, err := Compile("https://gonext.test/ok.json", raw)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if schema == nil {
		t.Fatal("Compile returned nil schema with nil error")
	}
	// A quick instance check — proves we actually got back a usable
	// validator, not just a Compile() that silently no-ops.
	if err := schema.Validate(map[string]any{"name": "ok"}); err != nil {
		t.Errorf("valid instance rejected: %v", err)
	}
	if err := schema.Validate(map[string]any{}); err == nil {
		t.Error("missing required field accepted; schema not enforced")
	}
}

// TestCompile_AcceptsMissingDollarSchema documents the "absent $schema
// is fine" decision: the compiler pins the default, so we don't force
// authors to type the URL every time.
func TestCompile_AcceptsMissingDollarSchema(t *testing.T) {
	raw := []byte(`{"type":"string","minLength":1}`)
	if _, err := Compile("https://gonext.test/no-schema.json", raw); err != nil {
		t.Errorf("Compile without $schema: %v", err)
	}
}

// TestCompile_RejectsDraft07 is the headline behavior of this package.
// A draft-07 schema must be rejected with an error that names the
// declared draft and the required draft, so the operator-facing
// message in the API response can guide the plugin author.
func TestCompile_RejectsDraft07(t *testing.T) {
	raw := []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object"
	}`)
	_, err := Compile("https://gonext.test/draft07.json", raw)
	if err == nil {
		t.Fatal("Compile accepted a draft-07 schema; expected rejection")
	}
	if !errors.Is(err, ErrUnsupportedDialect) {
		t.Errorf("error chain missing ErrUnsupportedDialect: %v", err)
	}
	if !strings.Contains(err.Error(), "draft-07") {
		t.Errorf("error message should name the offending draft: %v", err)
	}
}

// TestCompile_RejectsDraft06 covers the other historical draft we'll
// see in plugin manifests authored against old WordPress conventions.
func TestCompile_RejectsDraft06(t *testing.T) {
	raw := []byte(`{
		"$schema": "http://json-schema.org/draft-06/schema#",
		"type": "string"
	}`)
	_, err := Compile("https://gonext.test/draft06.json", raw)
	if !errors.Is(err, ErrUnsupportedDialect) {
		t.Errorf("expected ErrUnsupportedDialect, got %v", err)
	}
}

// TestCompile_RejectsEmptyInput surfaces the call-site programming
// error of passing an empty byte slice. We don't conflate this with
// dialect rejection — the message should make it obvious which one
// went wrong.
func TestCompile_RejectsEmptyInput(t *testing.T) {
	_, err := Compile("https://gonext.test/empty.json", nil)
	if err == nil {
		t.Fatal("Compile accepted empty input")
	}
	if errors.Is(err, ErrUnsupportedDialect) {
		t.Errorf("empty input should not be flagged as dialect mismatch: %v", err)
	}
}

// TestCompile_RejectsMalformedJSON guards against the regression
// where the dialect preflight accidentally swallowed a parse error.
// A malformed schema must come back as an error from Compile, with
// the message identifying parse trouble (not dialect trouble).
func TestCompile_RejectsMalformedJSON(t *testing.T) {
	_, err := Compile("https://gonext.test/bad.json", []byte(`{not json`))
	if err == nil {
		t.Fatal("Compile accepted malformed JSON")
	}
	if errors.Is(err, ErrUnsupportedDialect) {
		t.Errorf("malformed JSON should not be flagged as dialect mismatch: %v", err)
	}
}

// TestValidateDialect_TrimsAndAcceptsCanonicalURL exercises the
// "trailing whitespace is allowed" branch — authors sometimes paste
// the URL out of docs with a stray newline.
func TestValidateDialect_TrimsAndAcceptsCanonicalURL(t *testing.T) {
	raw := []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema  "}`)
	if err := ValidateDialect(raw); err != nil {
		t.Errorf("trailing whitespace rejected: %v", err)
	}
}

// TestValidateDialect_RejectsHTTPScheme covers a real-world typo: the
// 2020-12 URL uses https, but draft-07 used http. An author who
// hand-edits a draft-07 schema to bump to 2020-12 might forget to add
// the 's'.
func TestValidateDialect_RejectsHTTPScheme(t *testing.T) {
	raw := []byte(`{"$schema":"http://json-schema.org/draft/2020-12/schema"}`)
	if err := ValidateDialect(raw); !errors.Is(err, ErrUnsupportedDialect) {
		t.Errorf("http:// variant should be rejected: %v", err)
	}
}

// TestIsDraft2020_Matrix is a small table that locks down the
// canonical-string check. Future maintainers who want to relax the
// match (accept the URL fragment form, trailing slash, http) need to
// update this table — making the policy obvious.
func TestIsDraft2020_Matrix(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"https://json-schema.org/draft/2020-12/schema", true},
		// Whitespace trim is permitted.
		{"  https://json-schema.org/draft/2020-12/schema\n", true},
		// Older drafts: hard no.
		{"http://json-schema.org/draft-07/schema#", false},
		{"http://json-schema.org/draft-06/schema#", false},
		// Scheme/trailing variants we deliberately do NOT normalize:
		{"http://json-schema.org/draft/2020-12/schema", false},
		{"https://json-schema.org/draft/2020-12/schema/", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsDraft2020(c.s); got != c.want {
			t.Errorf("IsDraft2020(%q) = %v want %v", c.s, got, c.want)
		}
	}
}

// TestDraft2020URI_PinnedString locks the constant down. The value is
// referenced from documentation and from TS code; a quiet rename here
// would silently break consumers.
func TestDraft2020URI_PinnedString(t *testing.T) {
	const want = "https://json-schema.org/draft/2020-12/schema"
	if Draft2020URI != want {
		t.Errorf("Draft2020URI drift: got %q want %q", Draft2020URI, want)
	}
}

// TestCompile_AcceptsDraft2020Features documents the keywords issue
// #275 promised plugin authors: prefixItems, $dynamicRef,
// unevaluatedProperties. A 2020-12 schema using each must compile.
func TestCompile_AcceptsDraft2020Features(t *testing.T) {
	raw := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"unevaluatedProperties": false,
		"properties": {
			"tuple": {
				"type": "array",
				"prefixItems": [{"type":"string"},{"type":"integer"}]
			}
		}
	}`)
	if _, err := Compile("https://gonext.test/feat.json", raw); err != nil {
		t.Errorf("2020-12 keyword schema rejected: %v", err)
	}
}
