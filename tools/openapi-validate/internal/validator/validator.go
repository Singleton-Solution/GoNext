// Package validator structurally validates the GoNext OpenAPI 3.1
// document. It is intentionally stdlib-only — the tool runs in CI under
// GOWORK=off and we don't want to drag in kin-openapi for one round of
// JSON pointer walks.
//
// The validator is the structural enforcer for the contract documented in
// apps/api/openapi/README.md. It guarantees the following invariants:
//
//  1. The document is a valid JSON object with `openapi` starting "3.1".
//  2. Every top-level section the rest of the codebase depends on is
//     present (info, servers, paths, components).
//  3. operationId values are present on every operation and unique across
//     the whole document.
//  4. Every $ref pointer resolves to a defined JSON Pointer target.
//  5. Every security: requirement names a defined components.securitySchemes
//     entry.
//  6. Every named response and parameter referenced from a path resolves.
//
// What we DON'T do here: full JSON Schema 2020-12 validation. That's the
// runtime concern; we focus on the static-document health a generated SDK
// depends on.
package validator

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Document is the projection of the OpenAPI 3.1 document that the
// validator cares about. We use json.RawMessage for the operation bodies
// because we walk them generically (looking for $ref / operationId) rather
// than typing every field.
type Document struct {
	OpenAPI    string                     `json:"openapi"`
	Info       map[string]json.RawMessage `json:"info"`
	Servers    []map[string]any           `json:"servers"`
	Tags       []map[string]any           `json:"tags"`
	Paths      map[string]json.RawMessage `json:"paths"`
	Components Components                 `json:"components"`
	Security   []map[string][]string      `json:"security"`
}

// Components is the structural subset of the spec we walk. Anything not
// listed here is left as json.RawMessage so the validator stays small.
type Components struct {
	Schemas         map[string]json.RawMessage `json:"schemas"`
	Responses       map[string]json.RawMessage `json:"responses"`
	Parameters      map[string]json.RawMessage `json:"parameters"`
	SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
}

// Load reads and parses the OpenAPI document at path.
func Load(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &doc, nil
}

// Validate returns a sorted slice of human-readable issue strings; an
// empty slice means the document passes. Issues are deterministic
// (sorted) so the validator's output is diff-friendly.
func Validate(doc *Document) []string {
	var issues []string

	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		issues = append(issues, fmt.Sprintf("openapi version = %q, want 3.1.x", doc.OpenAPI))
	}
	if len(doc.Info) == 0 {
		issues = append(issues, "info: section is empty")
	} else {
		if _, ok := doc.Info["title"]; !ok {
			issues = append(issues, "info.title: missing")
		}
		if _, ok := doc.Info["version"]; !ok {
			issues = append(issues, "info.version: missing")
		}
	}
	if len(doc.Servers) == 0 {
		issues = append(issues, "servers: at least one entry required")
	}
	if len(doc.Paths) == 0 {
		issues = append(issues, "paths: at least one path required")
	}
	if len(doc.Components.Schemas) == 0 {
		issues = append(issues, "components.schemas: at least one schema required")
	}
	if len(doc.Components.SecuritySchemes) == 0 {
		issues = append(issues, "components.securitySchemes: at least one scheme required")
	}

	// Collect operationIds and per-operation refs in one pass.
	ops, refs := collectOpsAndRefs(doc)

	// Uniqueness of operationId.
	seen := make(map[string]string, len(ops))
	for _, op := range ops {
		if op.id == "" {
			issues = append(issues, fmt.Sprintf("%s %s: missing operationId", op.method, op.path))
			continue
		}
		if prev, ok := seen[op.id]; ok {
			issues = append(issues, fmt.Sprintf("operationId %q duplicated: %s vs %s", op.id, prev, op.path+":"+op.method))
			continue
		}
		seen[op.id] = op.path + ":" + op.method
	}

	// Every $ref must resolve.
	for _, ref := range refs {
		if err := resolveRef(doc, ref.ref); err != nil {
			issues = append(issues, fmt.Sprintf("%s: unresolved $ref %q: %v", ref.where, ref.ref, err))
		}
	}

	// Every security requirement at root + per-operation must name a
	// defined scheme.
	for _, req := range doc.Security {
		for name := range req {
			if _, ok := doc.Components.SecuritySchemes[name]; !ok {
				issues = append(issues, fmt.Sprintf("root security: scheme %q not defined", name))
			}
		}
	}
	for _, op := range ops {
		for _, req := range op.security {
			for name := range req {
				if _, ok := doc.Components.SecuritySchemes[name]; !ok {
					issues = append(issues, fmt.Sprintf("%s %s: security scheme %q not defined", op.method, op.path, name))
				}
			}
		}
	}

	sort.Strings(issues)
	return issues
}

// CollectOperationIDs returns every operationId in the document along
// with its method and path. Exposed for the endpoint-sample test.
func CollectOperationIDs(doc *Document) []OperationRef {
	ops, _ := collectOpsAndRefs(doc)
	out := make([]OperationRef, 0, len(ops))
	for _, op := range ops {
		out = append(out, OperationRef{
			ID:     op.id,
			Method: op.method,
			Path:   op.path,
			Tags:   op.tags,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OperationRef is the public shape of a documented operation, used by
// callers that want to iterate the spec.
type OperationRef struct {
	ID     string
	Method string
	Path   string
	Tags   []string
}

// operation is the internal shape of one operation parse.
type operation struct {
	path     string
	method   string
	id       string
	tags     []string
	security []map[string][]string
}

// refSite is a single $ref usage; we track where it came from so the
// error message points at it.
type refSite struct {
	ref   string
	where string
}

// httpMethods is the set of method keys ServeMux recognises in patterns
// — also the set OpenAPI considers operation verbs.
var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// collectOpsAndRefs walks doc.Paths once and returns every (operationId,
// method, path) triple along with every $ref site for downstream
// validation.
func collectOpsAndRefs(doc *Document) ([]operation, []refSite) {
	var ops []operation
	var refs []refSite

	for path, raw := range doc.Paths {
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(raw, &pathItem); err != nil {
			continue
		}
		for _, m := range httpMethods {
			body, ok := pathItem[m]
			if !ok {
				continue
			}
			var opBody map[string]json.RawMessage
			if err := json.Unmarshal(body, &opBody); err != nil {
				continue
			}
			op := operation{path: path, method: strings.ToUpper(m)}
			if id, ok := opBody["operationId"]; ok {
				_ = json.Unmarshal(id, &op.id)
			}
			if t, ok := opBody["tags"]; ok {
				_ = json.Unmarshal(t, &op.tags)
			}
			if sec, ok := opBody["security"]; ok {
				_ = json.Unmarshal(sec, &op.security)
			}
			ops = append(ops, op)
		}
		refs = append(refs, collectRefs(raw, fmt.Sprintf("paths.%s", path))...)
	}

	// Walk components for refs too — schemas reference each other.
	for name, raw := range doc.Components.Schemas {
		refs = append(refs, collectRefs(raw, fmt.Sprintf("components.schemas.%s", name))...)
	}
	for name, raw := range doc.Components.Responses {
		refs = append(refs, collectRefs(raw, fmt.Sprintf("components.responses.%s", name))...)
	}
	for name, raw := range doc.Components.Parameters {
		refs = append(refs, collectRefs(raw, fmt.Sprintf("components.parameters.%s", name))...)
	}
	return ops, refs
}

// collectRefs walks a JSON value and returns every $ref it finds. The
// `where` prefix is used to build the diagnostic context for each ref.
func collectRefs(raw json.RawMessage, where string) []refSite {
	var any any
	if err := json.Unmarshal(raw, &any); err != nil {
		return nil
	}
	var out []refSite
	walk(any, where, func(ref, w string) {
		out = append(out, refSite{ref: ref, where: w})
	})
	return out
}

func walk(node any, where string, emit func(ref, where string)) {
	switch v := node.(type) {
	case map[string]any:
		if r, ok := v["$ref"].(string); ok {
			emit(r, where)
		}
		for k, child := range v {
			walk(child, where+"."+k, emit)
		}
	case []any:
		for i, child := range v {
			walk(child, fmt.Sprintf("%s[%d]", where, i), emit)
		}
	}
}

// resolveRef confirms ref points at a defined JSON Pointer target in the
// document. We support only local references (the `#/...` form); external
// refs would need an http fetch which is out of scope for the structural
// validator.
func resolveRef(doc *Document, ref string) error {
	if !strings.HasPrefix(ref, "#/") {
		return fmt.Errorf("non-local refs not supported")
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	if len(parts) < 2 {
		return fmt.Errorf("malformed ref")
	}
	switch parts[0] {
	case "components":
		switch parts[1] {
		case "schemas":
			if len(parts) < 3 {
				return fmt.Errorf("malformed schema ref")
			}
			if _, ok := doc.Components.Schemas[parts[2]]; !ok {
				return fmt.Errorf("schema not defined")
			}
			return nil
		case "responses":
			if len(parts) < 3 {
				return fmt.Errorf("malformed response ref")
			}
			if _, ok := doc.Components.Responses[parts[2]]; !ok {
				return fmt.Errorf("response not defined")
			}
			return nil
		case "parameters":
			if len(parts) < 3 {
				return fmt.Errorf("malformed parameter ref")
			}
			if _, ok := doc.Components.Parameters[parts[2]]; !ok {
				return fmt.Errorf("parameter not defined")
			}
			return nil
		case "securitySchemes":
			if len(parts) < 3 {
				return fmt.Errorf("malformed securityScheme ref")
			}
			if _, ok := doc.Components.SecuritySchemes[parts[2]]; !ok {
				return fmt.Errorf("securityScheme not defined")
			}
			return nil
		}
	}
	return fmt.Errorf("unrecognised ref namespace %q", parts[0])
}
