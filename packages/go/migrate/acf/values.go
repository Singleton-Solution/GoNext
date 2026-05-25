package acf

import (
	"fmt"
	"strconv"
	"strings"
)

// Value is one mapped post-meta entry. Multiple Values may emit per
// source ACF field — a repeater of N rows of M sub-fields explodes
// to N*M entries with bracketed keys ("photos.0.caption", etc.).
//
// The importer takes a []Value and writes one post_meta row per
// entry, all parented to the same post_id.
type Value struct {
	// Key is the meta_key. For top-level fields it equals the
	// field's slug; for repeater/group sub-fields it carries a
	// dotted path (e.g. "team.0.name").
	Key string

	// Value is the meta_value. Stored as a string because that's
	// the WordPress on-disk format; consumers parse to native
	// types using the schema's Kind hint.
	Value string

	// Kind is the field's mapped kind, copied from Schema for
	// downstream typing. Useful when the meta_key alone can't tell
	// the importer "this is an image reference, dereference it".
	Kind string

	// Notes is a list of human-readable conversion notes attached
	// to this value, e.g. "rewrote image attachment_id 123 → media
	// uuid 7a8b...". Empty in the steady state.
	Notes []string
}

// MapPostValues converts the raw ACF postmeta map (meta_key →
// meta_value, as stored in wp_postmeta) into a list of GoNext
// post-meta values, using the schema as the type lookup.
//
// ACF on disk:
//
//	subtitle = "About the team"
//	_subtitle = "field_5f3a_subtitle"     // companion key naming the ACF field
//
// We use the schema (rather than scanning the companion keys) so
// fields that have moved between groups still map cleanly.
func MapPostValues(schema *Schema, postMeta map[string]string) ([]Value, error) {
	if schema == nil {
		return nil, fmt.Errorf("acf: nil schema")
	}
	out := []Value{}
	for _, sf := range schema.Fields {
		mapped, err := mapValueForField(sf, "", postMeta)
		if err != nil {
			return nil, fmt.Errorf("acf: field %q: %w", sf.Name, err)
		}
		out = append(out, mapped...)
	}
	return out, nil
}

// mapValueForField recursively maps one schema field's value(s).
// prefix is the dotted path for nested fields ("team.0").
func mapValueForField(sf SchemaField, prefix string, postMeta map[string]string) ([]Value, error) {
	key := sf.Name
	if prefix != "" {
		key = prefix + "." + sf.Name
	}
	raw, present := postMeta[key]
	switch sf.Kind {
	case "repeater":
		return mapRepeater(sf, key, postMeta)
	case "flexible":
		return mapFlexible(sf, key, postMeta)
	case "group":
		out := []Value{}
		for _, child := range sf.Children {
			vs, err := mapValueForField(child, key, postMeta)
			if err != nil {
				return nil, err
			}
			out = append(out, vs...)
		}
		return out, nil
	}
	if !present {
		// Nothing stored — silently skip rather than emit empty
		// meta rows. The schema's defaults can supply the value at
		// read time.
		return nil, nil
	}
	v := Value{Key: key, Value: raw, Kind: sf.Kind}
	switch sf.Kind {
	case "boolean":
		// ACF stores "1" / "0"; normalise to "true" / "false".
		if raw == "1" || strings.EqualFold(raw, "true") {
			v.Value = "true"
		} else {
			v.Value = "false"
		}
	case "image", "file":
		// ACF stores the attachment_id string. The importer's media
		// path rewrites WP ID → GoNext media UUID; we just attach a
		// note so operators see what happened.
		v.Notes = append(v.Notes, "attachment_id retained for media rewrite")
	case "post_ref":
		if sf.Multiple {
			// ACF's relationship value is a comma-separated or
			// PHP-serialised list of post IDs. We accept the
			// comma form; PHP-serialised inputs require the
			// unserialise step in the importer's wp_meta package
			// and round-trip through here as a comma-joined string.
			v.Value = normaliseIDList(raw)
		}
	case "user_ref":
		// User IDs survive as-is; the user importer maps WP id →
		// GoNext user uuid using the same table the post author
		// rewrite uses.
	case "select":
		if sf.Multiple {
			v.Value = normaliseIDList(raw)
		}
	}
	return []Value{v}, nil
}

// mapRepeater explodes a repeater's row count into per-row child
// values. ACF stores the row count under the repeater's own key, e.g.
//
//	team = "3"
//	team_0_name = "Alex"
//	team_0_role = "Lead"
//	team_1_name = "..."
//
// Note the underscore separator on disk; we normalise to dot
// separators in the output so the dotted-path convention is uniform.
func mapRepeater(sf SchemaField, key string, postMeta map[string]string) ([]Value, error) {
	raw, present := postMeta[key]
	if !present || raw == "" {
		return nil, nil
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count < 0 {
		return nil, fmt.Errorf("repeater %q: row count %q is not a non-negative integer", key, raw)
	}
	out := []Value{}
	for i := 0; i < count; i++ {
		rowPrefix := fmt.Sprintf("%s_%d", key, i)
		// Re-key the postMeta for this row from underscore form to
		// dotted form, then recurse using the child schema fields.
		rowMeta := map[string]string{}
		for k, v := range postMeta {
			if strings.HasPrefix(k, rowPrefix+"_") {
				suffix := strings.TrimPrefix(k, rowPrefix+"_")
				rowMeta[suffix] = v
			}
		}
		for _, child := range sf.Children {
			// Use the original child.Name (which equals the slug on
			// disk after the underscore split) directly against
			// rowMeta, then attach the row's dotted prefix to the
			// emitted Key.
			vs, err := mapValueForFieldDirect(child, rowMeta)
			if err != nil {
				return nil, fmt.Errorf("repeater %q row %d: %w", key, i, err)
			}
			for _, v := range vs {
				v.Key = fmt.Sprintf("%s.%d.%s", key, i, v.Key)
				out = append(out, v)
			}
		}
	}
	return out, nil
}

// mapValueForFieldDirect maps a child field using the supplied meta
// map without any prefix re-keying. Used by mapRepeater after it has
// already extracted the row's flat slice.
func mapValueForFieldDirect(sf SchemaField, postMeta map[string]string) ([]Value, error) {
	return mapValueForField(sf, "", postMeta)
}

// mapFlexible explodes a flexible_content field's layout sequence.
// ACF stores the layout list as a serialised array under the field
// key, with each layout's sub-fields under "<key>_<idx>_<sub>".
// We accept the simpler comma-separated form alongside the PHP form.
func mapFlexible(sf SchemaField, key string, postMeta map[string]string) ([]Value, error) {
	raw, present := postMeta[key]
	if !present || raw == "" {
		return nil, nil
	}
	layouts := splitListValue(raw)
	out := []Value{}
	for i, layoutName := range layouts {
		// Find children whose Name has this layout's prefix.
		for _, child := range sf.Children {
			parts := strings.SplitN(child.Name, ".", 2)
			if len(parts) != 2 || parts[0] != layoutName {
				continue
			}
			subName := parts[1]
			subKey := fmt.Sprintf("%s_%d_%s", key, i, subName)
			if val, ok := postMeta[subKey]; ok {
				out = append(out, Value{
					Key:   fmt.Sprintf("%s.%d.%s.%s", key, i, layoutName, subName),
					Value: val,
					Kind:  child.Kind,
				})
			}
		}
	}
	return out, nil
}

// splitListValue accepts either a comma-joined list or a simple
// single-token string. Empty input yields nil. Used for flexible
// layout sequences and multi-select values.
func splitListValue(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// PHP serialised arrays start with `a:`; we don't unserialise
	// here. The importer's wp_meta package handles that and emits
	// the comma form into the map passed to us.
	if strings.HasPrefix(s, "a:") {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normaliseIDList trims whitespace from a comma-joined ID list and
// drops empty entries. Output preserves the input order.
func normaliseIDList(s string) string {
	parts := splitListValue(s)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ",")
}
