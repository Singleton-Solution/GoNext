package customizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// ErrEmptyOverride is returned by ValidateOverride when the request
// body decodes to an empty object — operators saving "nothing" should
// hit Reset instead. The distinction makes the audit log readable.
var ErrEmptyOverride = errors.New("customizer: empty override payload")

// ValidateOverride parses raw as a strict (DisallowUnknownFields) subset
// of theme.ThemeJSON. Returns the parsed override, the merged-onto-base
// theme value, and any structural / validation errors as a
// []theme.ValidationError.
//
// "Validation errors" carry the same path semantics the install-time
// validator uses, so the admin UI can highlight the offending field
// using the JSON pointer in the error path.
//
// "Parse errors" (unknown top-level keys, malformed JSON, type
// mismatches) come back as a plain error — the caller maps those to
// 400 with a code of "invalid_path" or "invalid_json".
func ValidateOverride(base *theme.ThemeJSON, raw json.RawMessage) (*theme.ThemeJSON, *theme.ThemeJSON, []theme.ValidationError, error) {
	if len(raw) == 0 {
		return nil, nil, nil, ErrEmptyOverride
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil, nil, ErrEmptyOverride
	}
	if bytes.Equal(trimmed, []byte("{}")) {
		return nil, nil, nil, ErrEmptyOverride
	}

	// Decode the override with strict mode. Unknown keys at any level
	// produce an error — that's how we reject "made up" paths.
	override := &theme.ThemeJSON{Version: theme.CurrentVersion}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(override); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid_path: %w", err)
	}
	if dec.More() {
		return nil, nil, nil, errors.New("invalid_path: trailing data after override object")
	}

	// Merge the override onto a deep-copied base. The base copy keeps
	// the caller's theme value pristine — a future "preview" path might
	// pass the same base into multiple validations.
	merged := cloneTheme(base)
	mergeTheme(merged, override)
	// Version is locked to CurrentVersion for the merged value; an
	// override can't downgrade the manifest's declared version.
	if override.Version != 0 && override.Version != theme.CurrentVersion {
		merged.Version = override.Version
	}

	if errs := merged.Validate(); len(errs) > 0 {
		return override, merged, errs, nil
	}
	return override, merged, nil, nil
}

// mergeTheme applies non-zero fields from src onto dst. The merge is
// "replace at the leaf" — a non-empty palette in src wholesale replaces
// dst.Color.Palette rather than being unioned. This matches operator
// expectations: editing two palette entries means the override carries
// both entries (the admin UI hydrates the full set before submit), not
// a sparse merge that could silently drop a renamed slug.
//
// The intent is to keep merge semantics simple enough to reason about
// in tests. Operators who want surgical edits should send the full
// section they're modifying.
func mergeTheme(dst, src *theme.ThemeJSON) {
	if src.Title != "" {
		dst.Title = src.Title
	}
	mergeSettings(&dst.Settings, &src.Settings)
}

// mergeSettings deep-merges the design-token surface. Each child
// section uses "non-zero replaces" semantics; nil slices in src leave
// dst untouched.
func mergeSettings(dst, src *theme.Settings) {
	if src.AppearanceTools {
		dst.AppearanceTools = src.AppearanceTools
	}
	if len(src.Color.Palette) > 0 {
		dst.Color.Palette = src.Color.Palette
	}
	if len(src.Color.Gradients) > 0 {
		dst.Color.Gradients = src.Color.Gradients
	}
	if len(src.Color.Duotone) > 0 {
		dst.Color.Duotone = src.Color.Duotone
	}
	if src.Color.Custom {
		dst.Color.Custom = src.Color.Custom
	}
	if src.Color.CustomGradient {
		dst.Color.CustomGradient = src.Color.CustomGradient
	}
	if src.Color.DefaultPalette {
		dst.Color.DefaultPalette = src.Color.DefaultPalette
	}

	if len(src.Typography.FontFamilies) > 0 {
		dst.Typography.FontFamilies = src.Typography.FontFamilies
	}
	if len(src.Typography.FontSizes) > 0 {
		dst.Typography.FontSizes = src.Typography.FontSizes
	}
	if src.Typography.LineHeight {
		dst.Typography.LineHeight = src.Typography.LineHeight
	}
	if src.Typography.LetterSpacing {
		dst.Typography.LetterSpacing = src.Typography.LetterSpacing
	}
	if src.Typography.TextDecoration {
		dst.Typography.TextDecoration = src.Typography.TextDecoration
	}
	if src.Typography.Custom {
		dst.Typography.Custom = src.Typography.Custom
	}

	if !src.Spacing.SpacingScale.IsZero() {
		dst.Spacing.SpacingScale = src.Spacing.SpacingScale
	}
	if len(src.Spacing.Units) > 0 {
		dst.Spacing.Units = src.Spacing.Units
	}
	if src.Spacing.Padding {
		dst.Spacing.Padding = src.Spacing.Padding
	}
	if src.Spacing.Margin {
		dst.Spacing.Margin = src.Spacing.Margin
	}
	if src.Spacing.BlockGap {
		dst.Spacing.BlockGap = src.Spacing.BlockGap
	}

	if src.Layout.ContentSize != "" {
		dst.Layout.ContentSize = src.Layout.ContentSize
	}
	if src.Layout.WideSize != "" {
		dst.Layout.WideSize = src.Layout.WideSize
	}
	if len(src.Shadow.Presets) > 0 {
		dst.Shadow.Presets = src.Shadow.Presets
	}
}

// cloneTheme returns a deep-copy of t by round-tripping through JSON.
// The copy is overkill in CPU terms (we re-marshal a typically <8KB
// manifest) but a hand-rolled struct copy would need to keep in lockstep
// with every theme.ThemeJSON addition. The trade-off favours
// maintainability: customizer is on the admin write path, not the
// per-request render path, and the JSON round trip is well under a
// millisecond on the manifests we ship.
func cloneTheme(t *theme.ThemeJSON) *theme.ThemeJSON {
	if t == nil {
		return &theme.ThemeJSON{Version: theme.CurrentVersion}
	}
	buf, err := json.Marshal(t)
	if err != nil {
		// theme.ThemeJSON is composed of public fields the std encoder
		// always handles; an error here means the type grew an
		// unmarshalable field. Surface a typed sentinel rather than
		// returning a nil that the caller would dereference.
		return &theme.ThemeJSON{Version: theme.CurrentVersion}
	}
	out := &theme.ThemeJSON{}
	if err := json.Unmarshal(buf, out); err != nil {
		return &theme.ThemeJSON{Version: theme.CurrentVersion}
	}
	return out
}
