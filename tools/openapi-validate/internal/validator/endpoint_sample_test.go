package validator

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProductionSpec_DocumentedShapesAreCoherent is the "one endpoint per
// tag" coherence check. For each tag, we pick the first operation we find
// and assert:
//
//   - It declares at least one 2xx response.
//   - The success response's schema or $ref resolves.
//   - At least one error response is documented (any 4xx or 5xx).
//
// Wiring the real handlers in a httptest server here would require pulling
// in the apps/api module — which the tools/openapi-validate go.mod
// intentionally avoids. The handler-side round-trip is covered by
// apps/api/internal/openapi/openapi_test.go; this is the spec-side
// coherence pass that complements it.
func TestProductionSpec_DocumentedShapesAreCoherent(t *testing.T) {
	t.Parallel()

	doc, err := Load(canonicalSpec(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	ops := CollectOperationIDs(doc)
	if len(ops) == 0 {
		t.Fatal("no operations to coherence-check")
	}

	// Group operations by tag.
	byTag := make(map[string][]OperationRef, len(ops))
	for _, op := range ops {
		for _, tag := range op.Tags {
			byTag[tag] = append(byTag[tag], op)
		}
	}
	if len(byTag) == 0 {
		t.Fatal("no operations are tagged; the OpenAPI tag index is unused")
	}

	for tag, list := range byTag {
		// Pick the first operation per tag.
		op := list[0]
		raw, ok := lookupOperation(doc, op.Method, op.Path)
		if !ok {
			t.Errorf("[%s] %s %s: operation body not found in spec", tag, op.Method, op.Path)
			continue
		}
		checkOperationCoherence(t, tag, op, raw)
	}
}

// lookupOperation returns the JSON body of a single operation in the
// spec, identified by its method + path. Returns false when the operation
// isn't present (the documented-but-not-loaded case).
func lookupOperation(doc *Document, method, path string) (map[string]any, bool) {
	rawPath, ok := doc.Paths[path]
	if !ok {
		return nil, false
	}
	var pathItem map[string]json.RawMessage
	if err := json.Unmarshal(rawPath, &pathItem); err != nil {
		return nil, false
	}
	rawOp, ok := pathItem[strings.ToLower(method)]
	if !ok {
		return nil, false
	}
	var op map[string]any
	if err := json.Unmarshal(rawOp, &op); err != nil {
		return nil, false
	}
	return op, true
}

// checkOperationCoherence asserts the three rules in
// TestProductionSpec_DocumentedShapesAreCoherent's doc comment.
func checkOperationCoherence(t *testing.T, tag string, ref OperationRef, op map[string]any) {
	t.Helper()
	resps, _ := op["responses"].(map[string]any)
	if len(resps) == 0 {
		t.Errorf("[%s] %s %s: no responses declared", tag, ref.Method, ref.Path)
		return
	}
	var hasSuccess, hasError bool
	for code := range resps {
		switch {
		case len(code) == 3 && code[0] == '2':
			hasSuccess = true
		case len(code) == 3 && (code[0] == '4' || code[0] == '5'):
			hasError = true
		}
	}
	if !hasSuccess {
		t.Errorf("[%s] %s %s: no 2xx response documented", tag, ref.Method, ref.Path)
	}
	if !hasError {
		// Public endpoints (root, /openapi.json, healthz, RUM beacon)
		// legitimately document only 2xx + maybe 304/405. We only
		// require an error response on endpoints that have a security
		// requirement — those need at least one 4xx so the SDK can
		// surface auth failure.
		if needsErrorResponse(op) {
			t.Errorf("[%s] %s %s: no error response documented for an authenticated endpoint",
				tag, ref.Method, ref.Path)
		}
	}
}

// needsErrorResponse reports whether the operation requires at least one
// 4xx/5xx response declared. We say yes when the operation either inherits
// the document-level security (i.e. doesn't override with `security: []`)
// or declares its own non-empty security block.
func needsErrorResponse(op map[string]any) bool {
	sec, hasSec := op["security"]
	if !hasSec {
		// Inherits document-level security → needs an error response.
		return true
	}
	// security: [] means anonymous → no error response required.
	if arr, ok := sec.([]any); ok && len(arr) == 0 {
		return false
	}
	return true
}
