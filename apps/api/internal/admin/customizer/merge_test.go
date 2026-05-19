package customizer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// TestValidateOverride_MergesPalette confirms that a palette override
// fully replaces the base palette — the merge is "replace at the leaf"
// (operators send full sections).
func TestValidateOverride_MergesPalette(t *testing.T) {
	base := baseTheme()
	raw := json.RawMessage(`{"settings":{"color":{"palette":[
		{"slug":"accent","name":"Accent","color":"#ff0066"}
	]}}}`)

	_, merged, errs, err := ValidateOverride(base, raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %+v", errs)
	}
	if len(merged.Settings.Color.Palette) != 1 || merged.Settings.Color.Palette[0].Color != "#ff0066" {
		t.Fatalf("palette not merged; got %+v", merged.Settings.Color.Palette)
	}
}

// TestValidateOverride_RejectsInvalidColor confirms the merged-then-
// validated path catches a bad CSS color at the correct path.
func TestValidateOverride_RejectsInvalidColor(t *testing.T) {
	base := baseTheme()
	raw := json.RawMessage(`{"settings":{"color":{"palette":[
		{"slug":"accent","name":"Accent","color":"#zzz"}
	]}}}`)

	_, _, errs, err := ValidateOverride(base, raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected validation errors; got none")
	}
	foundPath := false
	for _, e := range errs {
		if e.Path == "/settings/color/palette/0/color" {
			foundPath = true
			break
		}
	}
	if !foundPath {
		t.Fatalf("validator did not flag the bad color; errs = %+v", errs)
	}
}

// TestValidateOverride_RejectsUnknownTopLevelKey confirms the strict
// decoder bails out when the override mentions a field that doesn't
// exist on theme.ThemeJSON.
func TestValidateOverride_RejectsUnknownTopLevelKey(t *testing.T) {
	base := baseTheme()
	raw := json.RawMessage(`{"unknownThing":true}`)

	_, _, _, err := ValidateOverride(base, raw)
	if err == nil {
		t.Fatalf("expected parse error; got nil")
	}
	if !strings.Contains(err.Error(), "invalid_path") {
		t.Fatalf("error code missing; got %v", err)
	}
}

// TestValidateOverride_EmptyObject returns ErrEmptyOverride so the
// handler can map it to the right 400 message ("use DELETE").
func TestValidateOverride_EmptyObject(t *testing.T) {
	_, _, _, err := ValidateOverride(baseTheme(), json.RawMessage(`{}`))
	if !errors.Is(err, ErrEmptyOverride) {
		t.Fatalf("err = %v; want ErrEmptyOverride", err)
	}
}

// TestValidateOverride_MergesLayout confirms a partial override
// (just layout) preserves the rest of the base manifest unchanged.
func TestValidateOverride_MergesLayout(t *testing.T) {
	base := baseTheme()
	raw := json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`)

	_, merged, errs, err := ValidateOverride(base, raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("validation errors: %+v", errs)
	}
	if merged.Settings.Layout.ContentSize != "800px" {
		t.Fatalf("contentSize = %q; want 800px", merged.Settings.Layout.ContentSize)
	}
	// WideSize must come from base.
	if merged.Settings.Layout.WideSize != "1180px" {
		t.Fatalf("wideSize = %q; want 1180px (preserved from base)", merged.Settings.Layout.WideSize)
	}
	// Palette must come from base.
	if len(merged.Settings.Color.Palette) != 3 {
		t.Fatalf("palette = %d entries; want 3 (preserved from base)", len(merged.Settings.Color.Palette))
	}
}

// TestValidateOverride_RejectsInvalidLayoutLength catches a non-CSS
// length in the layout section.
func TestValidateOverride_RejectsInvalidLayoutLength(t *testing.T) {
	raw := json.RawMessage(`{"settings":{"layout":{"contentSize":"nope"}}}`)
	_, _, errs, err := ValidateOverride(baseTheme(), raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) == 0 {
		t.Fatalf("expected validation errors")
	}
}

// TestValidateOverride_MalformedJSON returns a parse error rather than
// a validation error — the handler maps this to 400 invalid_path.
func TestValidateOverride_MalformedJSON(t *testing.T) {
	_, _, _, err := ValidateOverride(baseTheme(), json.RawMessage(`{"settings":`))
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

// TestValidateOverride_TypographyMergeReplacesEntries verifies the
// font-sizes section follows the same replace-at-the-leaf semantics
// as the palette.
func TestValidateOverride_TypographyMergeReplacesEntries(t *testing.T) {
	raw := json.RawMessage(`{"settings":{"typography":{"fontSizes":[
		{"slug":"sm","name":"Small","size":"0.875rem"},
		{"slug":"md","name":"Medium","size":"1.1rem"},
		{"slug":"lg","name":"Large","size":"1.5rem"}
	]}}}`)
	_, merged, errs, err := ValidateOverride(baseTheme(), raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %+v", errs)
	}
	if len(merged.Settings.Typography.FontSizes) != 3 {
		t.Fatalf("fontSizes = %d; want 3 (full replacement)", len(merged.Settings.Typography.FontSizes))
	}
}

// TestValidateOverride_PreservesVersion confirms the merged manifest
// keeps the base's CurrentVersion when the override omits the field.
func TestValidateOverride_PreservesVersion(t *testing.T) {
	raw := json.RawMessage(`{"settings":{"layout":{"contentSize":"800px"}}}`)
	_, merged, errs, err := ValidateOverride(baseTheme(), raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("validation errors: %+v", errs)
	}
	if merged.Version != theme.CurrentVersion {
		t.Fatalf("version = %d; want %d", merged.Version, theme.CurrentVersion)
	}
}
