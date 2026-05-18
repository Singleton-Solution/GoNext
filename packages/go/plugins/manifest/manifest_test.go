package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readGolden loads a fixture by basename from the golden/ directory.
// We keep the fixtures on disk (rather than inline) so the file is the
// same artifact a CLI lint would feed to Validate — there is no risk of
// a test-only spelling that production never sees.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("golden", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v", name, err)
	}
	return b
}

func TestValidate_ValidMinimal(t *testing.T) {
	t.Parallel()
	data := readGolden(t, "minimal.json")
	m, err := Validate(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.APIVersion != APIVersion {
		t.Errorf("apiVersion: got %q want %q", m.APIVersion, APIVersion)
	}
	if m.Name != "gn-seo" {
		t.Errorf("name: got %q", m.Name)
	}
	if m.Version != "1.0.0" {
		t.Errorf("version: got %q", m.Version)
	}
	if m.Entry != "plugin.wasm" {
		t.Errorf("entry: got %q", m.Entry)
	}
	if len(m.Capabilities) != 0 {
		t.Errorf("capabilities: got %v want empty", m.Capabilities)
	}
	if m.Hooks != nil {
		t.Errorf("hooks: got %+v want nil", m.Hooks)
	}
	if !json.Valid(m.Raw) {
		t.Error("Raw is not valid JSON")
	}
}

func TestValidate_ValidFull(t *testing.T) {
	t.Parallel()
	data := readGolden(t, "full.json")
	m, err := Validate(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Version != "2.3.1-rc.1+build.42" {
		t.Errorf("version: got %q", m.Version)
	}
	if len(m.Capabilities) != 4 {
		t.Errorf("capabilities: got %d want 4", len(m.Capabilities))
	}
	if m.Hooks == nil || len(m.Hooks.Actions) != 2 || len(m.Hooks.Filters) != 2 {
		t.Errorf("hooks: got %+v", m.Hooks)
	}
	if len(m.Jobs) != 2 {
		t.Errorf("jobs: got %d want 2", len(m.Jobs))
	}
	if m.Requires == nil || m.Requires.Host != ">=1.0.0 <2.0.0" {
		t.Errorf("requires: got %+v", m.Requires)
	}
	if len(m.Signature) != 128 {
		t.Errorf("signature length: got %d want 128", len(m.Signature))
	}
	if len(m.Depends) != 2 {
		t.Fatalf("depends: got %d want 2 (%+v)", len(m.Depends), m.Depends)
	}
	if m.Depends[0].Name != "gn-core" || m.Depends[0].Version != "^1.0.0" {
		t.Errorf("depends[0]: got %+v", m.Depends[0])
	}
	if m.Depends[1].Name != "gn-i18n" || m.Depends[1].Version != ">=2.0.0 <3.0.0" {
		t.Errorf("depends[1]: got %+v", m.Depends[1])
	}
}

// TestValidate_DependsField pins the happy path for the depends[]
// array (issue #251). The schema's uniqueItems forbids exact
// duplicates; required name + version are both checked. A malformed
// dependency entry triggers a structured error.
func TestValidate_DependsField(t *testing.T) {
	t.Parallel()
	data := readGolden(t, "with-depends.json")
	m, err := Validate(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Depends) != 2 {
		t.Fatalf("depends: got %d want 2", len(m.Depends))
	}

	// Negative: malformed entry must surface at /depends/0.
	bad := readGolden(t, "invalid-depends-shape.json")
	if _, err := Validate(bad); err == nil {
		t.Fatal("Validate: want error for invalid depends shape")
	} else {
		var ve Errors
		if !errors.As(err, &ve) {
			t.Fatalf("want Errors, got %T", err)
		}
		if !errorHasPath(ve, "/depends/0") && !errorHasPath(ve, "/depends/1") {
			t.Errorf("expected /depends/N in errors: %v", ve)
		}
	}
}

// TestValidate_TableDriven exercises the negative paths. Each case
// asserts (a) Validate returned an error and (b) the error mentions the
// expected paths so we know the right rule fired (not just "something
// failed"). The expected paths are substrings — the underlying message
// shape is allowed to evolve with the library, but the JSON pointer is
// load-bearing for the admin UI.
func TestValidate_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		fixture       string
		wantPaths     []string
		wantMinErrors int
	}{
		{
			name:          "missing required entry and version",
			fixture:       "invalid-missing-required.json",
			wantPaths:     []string{""}, // missing-required is reported at the parent path
			wantMinErrors: 1,
		},
		{
			name:          "malformed semver",
			fixture:       "invalid-semver.json",
			wantPaths:     []string{"/version"},
			wantMinErrors: 1,
		},
		{
			name:          "wrong apiVersion literal",
			fixture:       "invalid-api-version.json",
			wantPaths:     []string{"/apiVersion"},
			wantMinErrors: 1,
		},
		{
			name:          "unknown capability shape",
			fixture:       "invalid-capability-shape.json",
			wantPaths:     []string{"/capabilities/0", "/capabilities/1"},
			wantMinErrors: 2,
		},
		{
			name:          "additionalProperties=false enforced",
			fixture:       "invalid-additional-property.json",
			wantPaths:     []string{""}, // additionalProperties is reported at the object path
			wantMinErrors: 1,
		},
		{
			name:          "multiple errors reported at once",
			fixture:       "invalid-multiple.json",
			wantPaths:     []string{"/apiVersion", "/name", "/version", "/entry"},
			wantMinErrors: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := readGolden(t, tc.fixture)
			_, err := Validate(data)
			if err == nil {
				t.Fatalf("Validate: want error, got nil")
			}
			var ve Errors
			if !errors.As(err, &ve) {
				t.Fatalf("Validate: want Errors, got %T: %v", err, err)
			}
			if len(ve) < tc.wantMinErrors {
				t.Errorf("error count: got %d want >= %d (errors: %v)", len(ve), tc.wantMinErrors, ve)
			}
			for _, want := range tc.wantPaths {
				if !errorHasPath(ve, want) {
					t.Errorf("missing expected path %q in errors: %v", want, ve)
				}
			}
		})
	}
}

// errorHasPath reports whether any error in es has Path equal to want or
// starts with want+"/". The trailing-segment match is needed for
// additionalProperties errors, which the library can report against the
// parent path with the offending key in the message.
func errorHasPath(es Errors, want string) bool {
	for _, e := range es {
		if e.Path == want {
			return true
		}
		if want != "" && strings.HasPrefix(e.Path, want+"/") {
			return true
		}
	}
	return false
}

func TestValidate_EmptyInput(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		nil,
		{},
		[]byte("   \n\t  "),
	}
	for i, data := range cases {
		_, err := Validate(data)
		if err == nil {
			t.Errorf("case %d: want error for empty input", i)
		}
	}
}

func TestValidate_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := Validate([]byte(`{"apiVersion": "gonext.io/v1"`)) // missing closing brace
	if err == nil {
		t.Fatal("Validate: want parse error")
	}
	if !strings.Contains(err.Error(), "manifest: parse") {
		t.Errorf("want parse prefix, got %v", err)
	}
}

// TestValidate_AggregatesAllErrors confirms the headline contract:
// every rule violation is surfaced in one call. A manifest that breaks
// four rules must yield >= 4 ValidationError entries.
func TestValidate_AggregatesAllErrors(t *testing.T) {
	t.Parallel()
	data := readGolden(t, "invalid-multiple.json")
	_, err := Validate(data)
	if err == nil {
		t.Fatal("want error")
	}
	var ve Errors
	if !errors.As(err, &ve) {
		t.Fatalf("want Errors, got %T", err)
	}
	if len(ve) < 4 {
		t.Errorf("aggregation: got %d errors, want >= 4: %v", len(ve), ve)
	}

	// Confirm Errors.Error() includes the "manifest:" prefix the install
	// flow looks for when surfacing the failure to operators.
	if !strings.HasPrefix(ve.Error(), "manifest:") {
		t.Errorf("Error() prefix: got %q", ve.Error())
	}
}

// TestValidate_SemverEdgeCases pins the SemVer regex against a handful
// of cases the npm-semver test suite calls out. We want the schema to
// accept prerelease + build metadata and reject leading-zero numerics.
func TestValidate_SemverEdgeCases(t *testing.T) {
	t.Parallel()
	type tc struct {
		version string
		ok      bool
	}
	cases := []tc{
		{"1.0.0", true},
		{"0.0.0", true},
		{"10.20.30", true},
		{"1.0.0-alpha", true},
		{"1.0.0-alpha.1", true},
		{"1.0.0-0.3.7", true},
		{"1.0.0+20130313144700", true},
		{"1.0.0-beta+exp.sha.5114f85", true},

		{"1.0", false},
		{"1", false},
		{"01.0.0", false},
		{"1.0.0-", false},
		{"v1.0.0", false},
		{"1.0.0.0", false},
	}
	for _, c := range cases {
		data := []byte(`{"apiVersion":"gonext.io/v1","name":"gn-x","version":"` + c.version + `","entry":"p.wasm"}`)
		_, err := Validate(data)
		if c.ok && err != nil {
			t.Errorf("version %q: want accept, got %v", c.version, err)
		}
		if !c.ok && err == nil {
			t.Errorf("version %q: want reject, got accept", c.version)
		}
	}
}

// TestSchemaDialectPinned asserts the embedded schema declares the
// 2020-12 dialect. A drift here is a release-blocker — the lifecycle
// install gate depends on the schema semantics matching the dialect the
// rest of the platform validates against.
func TestSchemaDialectPinned(t *testing.T) {
	t.Parallel()
	var meta struct {
		Schema string `json:"$schema"`
		ID     string `json:"$id"`
	}
	if err := json.Unmarshal(schemaBytes, &meta); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	if meta.Schema != SchemaDialect {
		t.Errorf("schema dialect: got %q want %q", meta.Schema, SchemaDialect)
	}
	if meta.ID == "" {
		t.Error("schema $id is empty; the compiler relies on it for resource resolution")
	}
}

// TestValidate_ParentTraversalInEntry checks the "no .. segments"
// guard. A malicious bundle that tries to point its entry at a host
// file should be rejected at validation time, before any unzip happens.
func TestValidate_ParentTraversalInEntry(t *testing.T) {
	t.Parallel()
	cases := []string{
		"../plugin.wasm",
		"a/../../etc.wasm",
		"..",
	}
	for _, entry := range cases {
		raw, err := json.Marshal(map[string]any{
			"apiVersion": APIVersion,
			"name":       "gn-seo",
			"version":    "1.0.0",
			"entry":      entry,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := Validate(raw); err == nil {
			t.Errorf("entry %q: want reject", entry)
		}
	}
}
