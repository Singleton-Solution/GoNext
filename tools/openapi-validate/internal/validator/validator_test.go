package validator

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// canonicalSpec returns the absolute path to the production spec relative
// to this file. The tool ships in tools/openapi-validate and the spec
// lives in apps/api/openapi — walk up twice and back down.
func canonicalSpec(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller unavailable")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..",
		"apps", "api", "openapi", "gonext.openapi.json")
}

// TestProductionSpec_Validates is the gate the lint-openapi CI job
// exercises: the canonical spec must validate without issues.
func TestProductionSpec_Validates(t *testing.T) {
	t.Parallel()

	doc, err := Load(canonicalSpec(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	issues := Validate(doc)
	if len(issues) != 0 {
		for _, iss := range issues {
			t.Errorf("issue: %s", iss)
		}
	}
}

// TestProductionSpec_OperationIDsUnique cross-checks the uniqueness rule
// from CollectOperationIDs's vantage. A duplicated id would silently land
// in the SDK as one of the two methods overriding the other.
func TestProductionSpec_OperationIDsUnique(t *testing.T) {
	t.Parallel()

	doc, err := Load(canonicalSpec(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops := CollectOperationIDs(doc)
	if len(ops) == 0 {
		t.Fatal("no operations collected — spec is empty?")
	}
	seen := make(map[string]string)
	for _, op := range ops {
		key := op.Method + " " + op.Path
		if prev, ok := seen[op.ID]; ok {
			t.Errorf("operationId %q reused: %s vs %s", op.ID, prev, key)
		}
		seen[op.ID] = key
	}
}

// TestProductionSpec_HasExpectedTags is the smoke test for "every shipped
// surface has at least one path tagged with its name". A tag here without
// any operation referencing it is harmless; the inverse — an operation
// using a tag the index doesn't know about — is the regression worth
// catching.
func TestProductionSpec_HasExpectedTags(t *testing.T) {
	t.Parallel()

	doc, err := Load(canonicalSpec(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	declared := make(map[string]bool, len(doc.Tags))
	for _, tag := range doc.Tags {
		if name, ok := tag["name"].(string); ok {
			declared[name] = true
		}
	}
	for _, want := range []string{
		"meta", "auth", "posts", "pages", "users", "comments",
		"terms", "media", "plugins", "jobs", "settings", "search",
		"webhooks", "rum", "openapi",
	} {
		if !declared[want] {
			t.Errorf("tag %q missing from declared tags index", want)
		}
	}

	ops := CollectOperationIDs(doc)
	used := make(map[string]bool)
	for _, op := range ops {
		for _, t := range op.Tags {
			used[t] = true
		}
	}
	// Every tag used on an operation must be declared in the index.
	var orphaned []string
	for tag := range used {
		if !declared[tag] {
			orphaned = append(orphaned, tag)
		}
	}
	sort.Strings(orphaned)
	if len(orphaned) > 0 {
		t.Errorf("operations reference tags absent from the index: %v", orphaned)
	}
}

// TestValidate_DetectsMissingOperationId is a regression guard for the
// shape of the error message — generated SDKs hard-code on the absence of
// operationId being a hard failure.
func TestValidate_DetectsMissingOperationId(t *testing.T) {
	t.Parallel()

	doc := minimalDoc()
	// Replace the single operation's body with one that has no operationId.
	doc.Paths["/x"] = []byte(`{"get":{"responses":{"200":{"description":"ok"}}}}`)

	issues := Validate(doc)
	if !containsString(issues, "missing operationId") {
		t.Fatalf("expected an issue mentioning missing operationId; got %v", issues)
	}
}

// TestValidate_DetectsUnresolvedRef proves an orphan $ref is flagged. A
// generated SDK that hits an unresolved ref will crash at codegen rather
// than at runtime, so we want the validator to surface it first.
func TestValidate_DetectsUnresolvedRef(t *testing.T) {
	t.Parallel()

	doc := minimalDoc()
	doc.Paths["/x"] = []byte(`{"get":{"operationId":"x","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/MissingSchema"}}}}}}}`)

	issues := Validate(doc)
	if !containsString(issues, "unresolved $ref") {
		t.Fatalf("expected unresolved-ref issue; got %v", issues)
	}
}

// TestValidate_DetectsUnknownSecurityScheme guards the security-aware
// half of the validator: a per-operation `security:` block naming a
// scheme that isn't in components.securitySchemes is the kind of typo
// that would still serve OK but generate a broken SDK.
func TestValidate_DetectsUnknownSecurityScheme(t *testing.T) {
	t.Parallel()

	doc := minimalDoc()
	doc.Paths["/x"] = []byte(`{"get":{"operationId":"x","security":[{"NoSuchScheme":[]}],"responses":{"200":{"description":"ok"}}}}`)

	issues := Validate(doc)
	if !containsString(issues, `"NoSuchScheme"`) {
		t.Fatalf("expected unknown-scheme issue; got %v", issues)
	}
}

// TestValidate_DuplicateOperationID is the SDK-codegen worst case — two
// methods would silently share a name.
func TestValidate_DuplicateOperationID(t *testing.T) {
	t.Parallel()

	doc := minimalDoc()
	doc.Paths["/x"] = []byte(`{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}}`)
	doc.Paths["/y"] = []byte(`{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}}`)

	issues := Validate(doc)
	if !containsString(issues, `duplicated`) {
		t.Fatalf("expected duplicate-id issue; got %v", issues)
	}
}

// minimalDoc returns the smallest Document that passes Validate, which
// lets each test mutate one field to exercise the specific check.
func minimalDoc() *Document {
	return &Document{
		OpenAPI: "3.1.0",
		Info: map[string]json.RawMessage{
			"title":   []byte(`"GoNext API"`),
			"version": []byte(`"0.0.0"`),
		},
		Servers: []map[string]any{{"url": "http://localhost:8080"}},
		Paths: map[string]json.RawMessage{
			"/x": []byte(`{"get":{"operationId":"x","responses":{"200":{"description":"ok"}}}}`),
		},
		Components: Components{
			Schemas: map[string]json.RawMessage{
				"Stub": []byte(`{"type":"object"}`),
			},
			SecuritySchemes: map[string]json.RawMessage{
				"CookieSession": []byte(`{"type":"apiKey","in":"cookie","name":"sid"}`),
			},
		},
	}
}

func containsString(issues []string, substr string) bool {
	for _, s := range issues {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
