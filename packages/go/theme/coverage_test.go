package theme

import (
	"testing"
)

// These tests target specific edges that the main tests don't exercise:
// nil-receiver paths in hasAnyToken, a couple of rgb()/hsl() malformed
// shapes, and the typography-validator branches that the full example
// happens to never hit.

func TestEmitCSSCustomProperties_OnlyGradients(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Gradients: []GradientEntry{
					{Slug: "g", Name: "G", Gradient: "linear-gradient(45deg, red, blue)"},
				},
			},
		},
	}
	if got := tj.EmitCSSCustomProperties(); got == "" {
		t.Errorf("expected non-empty output for gradient-only manifest")
	}
}

func TestEmitCSSCustomProperties_OnlyFontFamilies(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontFamilies: []FontFamily{
					{Slug: "sans", Name: "Sans", FontFamily: "Inter"},
				},
			},
		},
	}
	if got := tj.EmitCSSCustomProperties(); got == "" {
		t.Errorf("expected non-empty output for font-family-only manifest")
	}
}

func TestEmitCSSCustomProperties_OnlyShadow(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Shadow: ShadowSettings{
				Presets: []ShadowPreset{
					{Slug: "soft", Name: "Soft", Shadow: "0 1px 2px rgba(0,0,0,.05)"},
				},
			},
		},
	}
	if got := tj.EmitCSSCustomProperties(); got == "" {
		t.Errorf("expected non-empty output for shadow-only manifest")
	}
}

func TestRGBArgs_WrongCount(t *testing.T) {
	cases := []string{
		"rgb(0, 0)",          // too few
		"rgb(0, 0, 0, 0, 0)", // too many
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if isValidCSSColor(c) {
				t.Errorf("expected invalid: %s", c)
			}
		})
	}
}

func TestHSLArgs_WrongCount(t *testing.T) {
	cases := []string{
		"hsl(120, 50%)",                  // too few
		"hsl(120, 50%, 50%, 0.5, extra)", // too many
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if isValidCSSColor(c) {
				t.Errorf("expected invalid: %s", c)
			}
		})
	}
}

func TestHSLArgs_FourArgWithBadAlpha(t *testing.T) {
	if isValidCSSColor("hsl(120, 50%, 50%, abc)") {
		t.Errorf("expected invalid: bad alpha")
	}
	if !isValidCSSColor("hsl(120, 50%, 50%, 0.5)") {
		t.Errorf("expected valid: hsla with numeric alpha")
	}
}

func TestExtractUnit_AllAlpha(t *testing.T) {
	// All-letter input: extractUnit returns the whole string. This
	// branch is reached only via internal call sites that pre-screen
	// the input — we exercise it directly to lock the behaviour.
	got := extractUnit("foo")
	if got != "foo" {
		t.Errorf("extractUnit(\"foo\") = %q, want %q", got, "foo")
	}
}

func TestValidate_TypographyBadFontFamilySlug(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontFamilies: []FontFamily{
					{Slug: "Bad!", Name: "Sans", FontFamily: "Inter"},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontFamilies/0/slug"); !ok {
		t.Errorf("expected fontFamily slug error, got: %v", errs)
	}
}

func TestValidate_TypographyBadFontSizeSlug(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontSizes: []FontSize{
					{Slug: "Bad!", Name: "X", Size: "1rem"},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontSizes/0/slug"); !ok {
		t.Errorf("expected fontSizes slug error, got: %v", errs)
	}
}

func TestValidate_FluidFontSize_BadMax(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontSizes: []FontSize{
					{
						Slug: "xxl", Name: "Display", Size: "2.5rem",
						Fluid: &FluidFontSize{Min: "2rem", Max: "not-a-length"},
					},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontSizes/0/fluid/max"); !ok {
		t.Errorf("expected fluid.max error, got: %v", errs)
	}
}

func TestValidate_SpacingScale_BadMediumStep(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Spacing: SpacingSettings{
				SpacingScale: SpacingScale{
					Operator: "+", Increment: 1.5, Steps: 7, MediumStep: 0, Unit: "rem",
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/spacing/spacingScale/mediumStep"); !ok {
		t.Errorf("expected mediumStep error, got: %v", errs)
	}
}
