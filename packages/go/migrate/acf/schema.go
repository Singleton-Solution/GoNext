package acf

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// Schema is GoNext's field-group representation. It is the target of
// MapFieldGroup and pairs with packages/go/fields at runtime.
//
// The shape deliberately mirrors a JSON-Schema-ish object: each
// SchemaField is a property descriptor with a kind, optional label,
// optional choices, and optional sub-field children. Consumers wire
// this into a posts_field_groups row whose definition column is the
// marshalled JSON.
type Schema struct {
	// Key is the ACF group key carried over verbatim. GoNext uses
	// it as the natural id for upsert; re-importing the same group
	// updates rather than duplicates.
	Key string `json:"key"`

	// Title is the human label.
	Title string `json:"title"`

	// PostTypes is the resolved list of WordPress post types this
	// group targets, harvested from ACF's location rules. Empty
	// means "no post-type restriction" — the consumer decides
	// whether that means "all" or "show in admin only".
	PostTypes []string `json:"post_types,omitempty"`

	// Fields is the ordered list of field definitions. Order
	// matters: the admin renders fields in this sequence.
	Fields []SchemaField `json:"fields"`
}

// SchemaField is one property of a Schema. The shape is intentionally
// flat — sub-fields nest under Children — because deeply-nested JSON
// schema fragments make admin form rendering brittle.
type SchemaField struct {
	// Name is the slug used as the meta_key. Identical to ACF's
	// field name so a postmeta row with meta_key=Name resolves
	// against the parent post.
	Name string `json:"name"`

	// Label is the form label.
	Label string `json:"label"`

	// Kind is the GoNext field-kind discriminator. One of:
	// "text", "textarea", "number", "email", "url", "boolean",
	// "select", "image", "file", "post_ref", "user_ref",
	// "taxonomy_ref", "date", "time", "datetime", "repeater",
	// "flexible", "group". Unknown ACF types map to "" (zero
	// value) and are emitted with a Warning on the mapping report.
	Kind string `json:"kind"`

	// Required tracks the source flag.
	Required bool `json:"required,omitempty"`

	// Help mirrors the field's instruction text.
	Help string `json:"help,omitempty"`

	// Choices is the option list for select-like fields, in the
	// order ACF supplied. Each entry preserves the value/label
	// pair so the renderer can present labels without needing the
	// original ACF JSON.
	Choices []SchemaChoice `json:"choices,omitempty"`

	// Multiple is set when the source allows multiple selection
	// (relationship, multi-select, checkbox).
	Multiple bool `json:"multiple,omitempty"`

	// PostTypes / Taxonomy refine reference fields.
	PostTypes []string `json:"post_types,omitempty"`
	Taxonomy  string   `json:"taxonomy,omitempty"`

	// Default is the field's default value as-is. The renderer is
	// free to coerce to the field's kind at form construction.
	Default any `json:"default,omitempty"`

	// Children populates for repeater / group / flexible.
	Children []SchemaField `json:"children,omitempty"`

	// SourceType is the original ACF type string, retained so the
	// admin can render a hint banner ("imported from ACF
	// flexible_content").
	SourceType string `json:"source_type,omitempty"`
}

// SchemaChoice is one option in a select-like field.
type SchemaChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// MapReport collects mapping diagnostics for a single MapFieldGroup
// call. The migrator presents it to the operator as a "fields
// imported / fields skipped" summary.
type MapReport struct {
	// Imported is the count of top-level fields successfully mapped.
	Imported int

	// Warnings is the list of non-fatal issues encountered, e.g.
	// "field kind 'message' is presentational and was skipped" or
	// "field 'foo' has no name; using key as fallback".
	Warnings []string
}

// MapFieldGroup converts one ACF FieldGroup into a GoNext Schema.
//
// Unknown field types do NOT fail; they accumulate as Warnings on
// the returned MapReport and the field is skipped. This is the
// "best effort" posture the issue spec requires.
//
// logger may be nil — diagnostics are still attached to the report.
func MapFieldGroup(g *FieldGroup, logger *slog.Logger) (*Schema, *MapReport, error) {
	if g == nil {
		return nil, nil, fmt.Errorf("acf: nil field group")
	}
	if g.Key == "" {
		return nil, nil, fmt.Errorf("acf: field group missing key")
	}
	rep := &MapReport{}
	s := &Schema{
		Key:       g.Key,
		Title:     g.Title,
		PostTypes: extractPostTypes(g.Location),
		Fields:    make([]SchemaField, 0, len(g.Fields)),
	}
	for _, f := range g.Fields {
		sf, ok, note := mapField(f)
		if note != "" {
			rep.Warnings = append(rep.Warnings, note)
			if logger != nil {
				logger.Warn("acf: field mapping note",
					slog.String("group", g.Key),
					slog.String("field", f.Name),
					slog.String("type", f.Type),
					slog.String("note", note),
				)
			}
		}
		if !ok {
			continue
		}
		s.Fields = append(s.Fields, sf)
		rep.Imported++
	}
	return s, rep, nil
}

// mapField maps one ACF Field to a SchemaField. ok=false means the
// field was skipped (presentational or unknown). note carries a
// human-readable explanation in either case.
func mapField(f Field) (SchemaField, bool, string) {
	sf := SchemaField{
		Name:       fallbackName(f),
		Label:      f.Label,
		Required:   f.IsRequired(),
		Help:       f.Instructions,
		Default:    f.DefaultValue,
		SourceType: f.Type,
	}
	switch f.Type {
	case "text", "email", "url", "password":
		sf.Kind = "text"
	case "textarea", "wysiwyg":
		sf.Kind = "textarea"
	case "number", "range":
		sf.Kind = "number"
	case "true_false":
		sf.Kind = "boolean"
	case "select", "radio":
		sf.Kind = "select"
		sf.Choices = choicesFrom(f.Choices)
	case "checkbox":
		sf.Kind = "select"
		sf.Multiple = true
		sf.Choices = choicesFrom(f.Choices)
	case "image", "gallery":
		sf.Kind = "image"
		sf.Multiple = f.Type == "gallery"
	case "file":
		sf.Kind = "file"
	case "post_object", "page_link":
		sf.Kind = "post_ref"
		sf.PostTypes = f.PostTypes()
	case "relationship":
		sf.Kind = "post_ref"
		sf.Multiple = true
		sf.PostTypes = f.PostTypes()
	case "taxonomy":
		sf.Kind = "taxonomy_ref"
		sf.Taxonomy = f.Taxonomy
	case "user":
		sf.Kind = "user_ref"
	case "date_picker":
		sf.Kind = "date"
	case "time_picker":
		sf.Kind = "time"
	case "date_time_picker":
		sf.Kind = "datetime"
	case "repeater":
		sf.Kind = "repeater"
		sf.Children = mapChildren(f.SubFields)
	case "group":
		sf.Kind = "group"
		sf.Children = mapChildren(f.SubFields)
	case "flexible_content":
		sf.Kind = "flexible"
		// Flatten each layout's sub-fields into Children, prefixed
		// with the layout name so the same SchemaField slice can
		// host every layout's children without collision.
		for _, l := range f.Layouts {
			for _, sub := range l.SubFields {
				child, ok, _ := mapField(sub)
				if !ok {
					continue
				}
				child.Name = l.Name + "." + child.Name
				sf.Children = append(sf.Children, child)
			}
		}
	case "tab", "accordion", "message", "clone":
		// Presentational types; ACF uses them to lay out the admin
		// form, not to store values. Drop with a warning.
		return SchemaField{}, false, fmt.Sprintf("skipped presentational field %q (%s)", f.Name, f.Type)
	case "":
		return SchemaField{}, false, fmt.Sprintf("field %q has empty type", f.Name)
	default:
		return SchemaField{}, false, fmt.Sprintf("unknown ACF type %q on field %q", f.Type, f.Name)
	}
	return sf, true, ""
}

// mapChildren maps a slice of sub-fields, silently dropping any that
// fail. The parent's warnings are not collected here — repeater/group
// children failures are visible through the source_type fingerprint.
func mapChildren(in []Field) []SchemaField {
	out := make([]SchemaField, 0, len(in))
	for _, f := range in {
		sf, ok, _ := mapField(f)
		if !ok {
			continue
		}
		out = append(out, sf)
	}
	return out
}

// choicesFrom converts ACF's map[string]string choices into the
// SchemaChoice slice in deterministic order (sorted by value) so two
// runs of the importer against identical input produce byte-identical
// JSON.
func choicesFrom(m map[string]string) []SchemaChoice {
	if len(m) == 0 {
		return nil
	}
	out := make([]SchemaChoice, 0, len(m))
	// Stable iteration: collect keys, sort, emit.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		out = append(out, SchemaChoice{Value: k, Label: m[k]})
	}
	return out
}

// extractPostTypes pulls the post-type targets out of ACF's location
// rule matrix. ACF's location is OR-of-ANDs; we treat every
// post_type==X clause anywhere in the matrix as a target.
func extractPostTypes(rules [][]LocationRule) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, group := range rules {
		for _, r := range group {
			if r.Param == "post_type" && r.Operator == "==" && r.Value != "" {
				if _, ok := seen[r.Value]; !ok {
					seen[r.Value] = struct{}{}
					out = append(out, r.Value)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	sortStrings(out)
	return out
}

// fallbackName returns f.Name if set; else the ACF Key. Empty Name on
// a field is a sign the export is malformed but we don't want to lose
// the field entirely — using the Key as a slug is salvageable.
func fallbackName(f Field) string {
	if f.Name != "" {
		return f.Name
	}
	return f.Key
}

// AsJSON renders the schema as canonical (indented) JSON. Useful for
// snapshot tests and for writing the migrated schema to disk before
// the importer applies it.
func (s *Schema) AsJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
