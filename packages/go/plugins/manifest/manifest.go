// Package manifest parses and validates the manifest.json shipped inside
// every .gnplugin bundle against the gonext.io/v1 JSON Schema (draft
// 2020-12).
//
// The package is a pure function over bytes: it has no dependency on
// net/http, database/sql, or any other GoNext subsystem, so the CLI, the
// admin installer, the registry CI lint, and the lifecycle Manager can
// all share the same gate without dragging in the server. See
// docs/02-plugin-system.md §2 for the manifest reference and
// docs/02-plugin-system.md §2.1 for the install contract.
//
// Versioning: only gonext.io/v1 is recognised. Future revisions will
// land as a sibling package (plugins/manifest/v2, ...).
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	_ "embed"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// APIVersion is the only manifest API version this package validates.
// New schema revisions will increment this and live in a sibling package
// so the v1 surface keeps its frozen guarantees.
const APIVersion = "gonext.io/v1"

// SchemaDialect is the JSON Schema dialect the manifest schema is
// authored against. We pin it explicitly so a compiler upgrade can't
// silently change the validation surface.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// schemaURL is the stable identifier for the embedded schema. It is used
// only as a $ref base — the compiler never dereferences it as a URL.
const schemaURL = "https://gonext.io/schemas/plugins/manifest.json"

//go:embed schema.json
var schemaBytes []byte

// Manifest is the typed mirror of the JSON Schema. Field documentation
// is kept synchronised with the schema's `description` keywords; the
// schema is the source of truth, this struct exists so Go callers can
// poke at decoded fields without unmarshalling twice.
//
// Unknown fields are accepted by the json decoder and stripped — the
// schema's `additionalProperties: false` is what rejects them. Doing the
// strict check at the schema layer means we get the error in the same
// list as everything else, instead of a one-off json.Unmarshal failure.
type Manifest struct {
	// APIVersion is the literal "gonext.io/v1". Other values are
	// rejected by the schema (const constraint).
	APIVersion string `json:"apiVersion"`

	// Name is the platform-unique slug. Same regex as lifecycle's
	// slugRegex so storage and validator agree on what's installable.
	Name string `json:"name"`

	// Version is a strict SemVer 2.0.0 string.
	Version string `json:"version"`

	// Entry is the POSIX path inside the bundle to the WASM module. The
	// schema rejects parent-traversal segments (..); the runtime
	// re-checks at instantiation time.
	Entry string `json:"entry"`

	// Capabilities is the set of platform capabilities the plugin
	// requests. Operators review this list before activation.
	Capabilities []string `json:"capabilities,omitempty"`

	// Hooks declares the action and filter subscriptions the plugin
	// will install when active.
	Hooks *Hooks `json:"hooks,omitempty"`

	// Jobs lists the TaskSpec ids the plugin owns. The scheduler
	// resolves them against its registry at activation.
	Jobs []string `json:"jobs,omitempty"`

	// Requires captures compatibility ranges. Today only Host is read;
	// the field is a struct so additions (abi, runtime) don't break
	// callers.
	Requires *Requires `json:"requires,omitempty"`

	// Depends is the inter-plugin dependency list. Each entry pins
	// another plugin slug to a semver range; the lifecycle Activate
	// gate refuses to flip this plugin to Active unless every entry is
	// installed, in the Active state, and reports a version inside the
	// range. Install is not gated — Depends only blocks activation.
	// See plugins/depends for the resolver implementation.
	Depends []Dependency `json:"depends,omitempty"`

	// Signature is the detached ed25519 signature over the canonical
	// bundle bytes, lowercase hex (64 bytes => 128 hex chars). Optional
	// in v1.
	Signature string `json:"signature,omitempty"`

	// Raw is the original bytes the manifest was decoded from. Callers
	// that need to persist the manifest verbatim (e.g. the lifecycle
	// Plugin row) read this instead of re-marshalling — re-marshal
	// would lose key order and any future round-trip-relevant
	// formatting.
	Raw json.RawMessage `json:"-"`
}

// Hooks is the actions/filters split. Both arrays are optional; an
// empty Hooks object is legal (and pointless).
type Hooks struct {
	Actions []string `json:"actions,omitempty"`
	Filters []string `json:"filters,omitempty"`
}

// Requires is the compatibility-range bag. host is required if the
// object is present; the schema enforces that.
type Requires struct {
	Host string `json:"host"`
}

// Dependency is one entry of the manifest's depends[] array. Name is
// the dependency's plugin slug; Version is a semver range (npm-style:
// ^1.2.0, ~1.0.0, >=1.0.0 <2.0.0) the dependency's installed version
// must satisfy. The schema enforces both shapes.
//
// The typed struct lives in the manifest package because the manifest
// is the canonical source of truth for what the schema accepts. The
// resolver in plugins/depends consumes this exact shape — it does not
// duplicate the definition, to keep the two halves of the contract
// from drifting.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ValidationError is one issue surfaced by Validate. Path is a
// JSON-pointer-style location into the original document so the admin
// UI or CLI can highlight the exact offending field. Message is a
// human-readable explanation pulled from the underlying jsonschema
// error.
type ValidationError struct {
	Path    string
	Message string
}

// Error implements the error interface. The path is rendered first so
// `errors.Join` output groups related errors visually.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// Errors is a sortable slice of ValidationError. We expose it as a
// distinct type so callers can range over it and so the joined-error
// output reads as a single "validation failed: ..." surface.
type Errors []ValidationError

// Error implements the error interface. All issues are emitted, joined
// by "; ". Empty input returns "".
func (es Errors) Error() string {
	if len(es) == 0 {
		return ""
	}
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return "manifest: " + strings.Join(parts, "; ")
}

// compiledSchema is the singleton schema instance shared by every
// Validate call. Compiling the schema is moderately expensive
// (allocates a tree of validators); doing it once at package init keeps
// Validate cheap on the hot path.
var compiledSchema = func() *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		panic(fmt.Sprintf("plugins/manifest: embedded schema is not valid JSON: %v", err))
	}
	if err := c.AddResource(schemaURL, doc); err != nil {
		panic(fmt.Sprintf("plugins/manifest: add embedded schema: %v", err))
	}
	sch, err := c.Compile(schemaURL)
	if err != nil {
		panic(fmt.Sprintf("plugins/manifest: compile embedded schema: %v", err))
	}
	return sch
}()

// Validate parses and schema-checks a manifest.json byte slice and
// returns the typed Manifest. On schema violation it returns a non-nil
// Errors aggregating EVERY problem found, not just the first — the
// caller renders the whole list so a plugin author fixes their bundle
// in one round trip.
//
// On a hard parse failure (invalid JSON, empty input) it returns a
// plain error wrapped with the "manifest:" prefix; the schema check is
// not even attempted in that case because the document isn't a JSON
// object.
//
// On success the returned *Manifest has its Raw field populated with
// the input bytes verbatim so the lifecycle row can persist the exact
// manifest.json shipped in the bundle.
func Validate(data []byte) (*Manifest, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("manifest: empty input")
	}

	// Schema validation operates on the generic "any" decode shape the
	// jsonschema library expects. We unmarshal once into that, run the
	// schema, then unmarshal a second time into the typed struct. The
	// double-decode is intentional: schema errors are reported with
	// JSON-pointer paths into the original document, which is far more
	// useful than Go reflect tags ("Manifest.Hooks.Actions[3]").
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("manifest: parse: %w", err)
	}

	if err := compiledSchema.Validate(doc); err != nil {
		var verr *jsonschema.ValidationError
		if errors.As(err, &verr) {
			return nil, flattenErrors(verr)
		}
		return nil, fmt.Errorf("manifest: validate: %w", err)
	}

	// Strict decode into the typed struct. We allow unknown fields here
	// because the schema layer already ran with additionalProperties:
	// false — if we got this far, every key is one we recognise.
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: decode: %w", err)
	}
	m.Raw = append(json.RawMessage(nil), data...)
	return &m, nil
}

// flattenErrors converts a jsonschema.ValidationError tree into a flat
// list of ValidationError leaves. The library models the tree as one
// node per applied keyword; the leaves (Causes == nil) are the actually
// useful messages.
//
// Paths are rendered as RFC 6901 JSON pointers ("/hooks/actions/3").
// The list is sorted by path so the output is stable across runs.
func flattenErrors(root *jsonschema.ValidationError) Errors {
	var out Errors
	var walk func(*jsonschema.ValidationError)
	walk = func(n *jsonschema.ValidationError) {
		if n == nil {
			return
		}
		if len(n.Causes) == 0 {
			out = append(out, ValidationError{
				Path:    instancePointer(n.InstanceLocation),
				Message: leafMessage(n),
			})
			return
		}
		for _, c := range n.Causes {
			walk(c)
		}
	}
	walk(root)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// instancePointer joins the InstanceLocation segments into an RFC 6901
// JSON pointer. Empty segments (the document root) render as "".
func instancePointer(segments []string) string {
	if len(segments) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range segments {
		b.WriteByte('/')
		// RFC 6901 escapes: "~" -> "~0", "/" -> "~1". The order
		// matters: escape "~" first or the second pass would mangle
		// the "~1" emitted by the slash escape.
		s = strings.ReplaceAll(s, "~", "~0")
		s = strings.ReplaceAll(s, "/", "~1")
		b.WriteString(s)
	}
	return b.String()
}

// leafMessage strips the library's "jsonschema validation failed with..."
// prefix from a leaf error so the path-prefixed message reads cleanly.
// The library's Error() includes the SchemaURL and a multi-line trace
// that's helpful for debugging the schema itself but noise for a plugin
// author looking at their manifest.
func leafMessage(n *jsonschema.ValidationError) string {
	msg := fmt.Sprintf("%v", n.ErrorKind)
	msg = strings.TrimSpace(msg)
	if msg == "" {
		// Fall back to the full error if ErrorKind didn't render
		// anything useful (shouldn't happen, but defense in depth).
		msg = n.Error()
	}
	return msg
}
