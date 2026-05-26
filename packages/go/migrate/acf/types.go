package acf

import (
	"encoding/json"
	"fmt"
)

// FieldGroupExport is the top-level shape of an ACF JSON export file.
// ACF stores one field group per file under acf-json/, but the
// importer must also accept arrays (some plugins concatenate every
// field group into a single bundle). [UnmarshalJSON] handles both.
type FieldGroupExport struct {
	// Groups is the slice of field groups present in the file,
	// preserving the on-disk order so titles compare stably.
	Groups []FieldGroup
}

// UnmarshalJSON accepts either a single FieldGroup object or a JSON
// array of them. ACF Pro's export-as-PHP feature converts to JSON
// shaped like an array; the per-file format is a single object.
func (e *FieldGroupExport) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("acf: empty input")
	}
	// Disambiguate by the first non-whitespace byte.
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			var arr []FieldGroup
			if err := json.Unmarshal(data, &arr); err != nil {
				return fmt.Errorf("acf: decode array: %w", err)
			}
			e.Groups = arr
			return nil
		case '{':
			var single FieldGroup
			if err := json.Unmarshal(data, &single); err != nil {
				return fmt.Errorf("acf: decode object: %w", err)
			}
			e.Groups = []FieldGroup{single}
			return nil
		}
	}
	return fmt.Errorf("acf: not a JSON object or array")
}

// FieldGroup is one ACF field group as it appears in the export file.
// Only the fields the migrator needs are decoded; everything else
// (location rules with edge cases, modified timestamps, position
// hints) is preserved as raw JSON in Extras for round-tripping but
// not consumed.
type FieldGroup struct {
	// Key is the ACF-internal opaque identifier, e.g. "group_5f3a".
	// Treat it as a stable handle: re-importing a group with the
	// same Key updates rather than duplicates.
	Key string `json:"key"`

	// Title is the human-visible label shown in the admin.
	Title string `json:"title"`

	// Fields is the ordered list of direct child fields.
	Fields []Field `json:"fields"`

	// Location is ACF's targeting rule set, e.g. "show this group on
	// posts of type page". We surface it as raw rules so the consumer
	// can decide whether to honour any of them in GoNext — ACF's
	// location grammar is complex and we don't aim for full parity.
	Location [][]LocationRule `json:"location,omitempty"`

	// MenuOrder controls display ordering in the WP admin.
	MenuOrder int `json:"menu_order,omitempty"`

	// Active is whether the field group is currently enabled.
	Active bool `json:"active,omitempty"`
}

// LocationRule is one row of ACF's "show this group when …" matrix.
type LocationRule struct {
	Param    string `json:"param"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

// Field is one ACF field definition. ACF emits ~30 type strings; the
// migrator handles ~12 directly (see doc.go). Unknown types are
// preserved here so the diagnostic report can name them.
type Field struct {
	// Key is the opaque ACF identifier, e.g. "field_5f3a_title".
	// It is the join key for per-post values: a postmeta row with
	// meta_key matching `_<field_name>` carries a serialised value
	// whose ACF reference is this Key.
	Key string `json:"key"`

	// Name is the slug used as the meta_key (without leading
	// underscore). For a field named "subtitle", the postmeta key
	// is "subtitle" and the companion underscore key "_subtitle"
	// stores the field Key.
	Name string `json:"name"`

	// Label is the human-visible label.
	Label string `json:"label"`

	// Type is the ACF field type. Common values: text, textarea,
	// number, email, url, image, file, select, checkbox, radio,
	// true_false, post_object, page_link, relationship, taxonomy,
	// user, date_picker, date_time_picker, time_picker, repeater,
	// flexible_content, group, message, tab, accordion.
	Type string `json:"type"`

	// Required is 1 (true) or 0 (false) in ACF's JSON; we decode
	// it as int and convert to bool in [Field.IsRequired].
	Required int `json:"required,omitempty"`

	// Instructions is the help text shown under the field input.
	Instructions string `json:"instructions,omitempty"`

	// DefaultValue is the default string value (used for text-like
	// fields; numeric and select types may set it to a number or
	// option key respectively).
	DefaultValue any `json:"default_value,omitempty"`

	// Choices is the option map for select/radio/checkbox. Keys are
	// the stored values, values are the labels shown to the author.
	Choices map[string]string `json:"choices,omitempty"`

	// SubFields is populated for repeater and group types. The
	// repeater's serialised value carries one set of child rows per
	// repeat; group is a fixed bag of named children.
	SubFields []Field `json:"sub_fields,omitempty"`

	// Layouts is populated for flexible_content. Each layout is a
	// named bag of sub-fields keyed by its own slug.
	Layouts []FlexibleLayout `json:"layouts,omitempty"`

	// ReturnFormat varies by field type. For image: "array", "url",
	// or "id". The migrator targets the "id" representation so
	// references survive media-table re-keying.
	ReturnFormat string `json:"return_format,omitempty"`

	// PostType narrows post_object / relationship to a specific
	// WordPress post type. Single string or array of strings in
	// the JSON; we accept both via [Field.PostTypes].
	PostType json.RawMessage `json:"post_type,omitempty"`

	// Taxonomy is the taxonomy slug for taxonomy fields.
	Taxonomy string `json:"taxonomy,omitempty"`

	// Min, Max are numeric/length constraints. ACF emits "" for
	// "no bound"; we accept any to absorb that.
	Min any `json:"min,omitempty"`
	Max any `json:"max,omitempty"`
}

// IsRequired returns the required flag as a Go bool.
func (f Field) IsRequired() bool { return f.Required != 0 }

// PostTypes parses Field.PostType, which ACF emits as either a JSON
// string ("post") or array (["post","page"]). Returns nil when the
// field isn't a post-object family field or no constraint is set.
func (f Field) PostTypes() []string {
	if len(f.PostType) == 0 {
		return nil
	}
	// Array first — string second.
	var arr []string
	if err := json.Unmarshal(f.PostType, &arr); err == nil {
		return arr
	}
	var s string
	if err := json.Unmarshal(f.PostType, &s); err == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	return nil
}

// FlexibleLayout is one entry in a flexible_content field. The
// stored value is an ordered list of layout names plus their
// sub-field bags.
type FlexibleLayout struct {
	Key       string  `json:"key"`
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Display   string  `json:"display,omitempty"`
	SubFields []Field `json:"sub_fields"`
}
