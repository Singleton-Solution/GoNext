package theme

import (
	"strings"
	"testing"
)

// findError reports whether errs contains an entry whose Path equals
// (or has prefix) p. We use it instead of direct slice indexing so
// tests survive reordering of unrelated validation rules.
func findError(errs []ValidationError, p string) (ValidationError, bool) {
	for _, e := range errs {
		if e.Path == p {
			return e, true
		}
	}
	return ValidationError{}, false
}

// hasErrorAt reports whether any entry's Path begins with prefix.
// Useful when validation may attach an error at a child path under a
// known root.
func hasErrorAt(errs []ValidationError, prefix string) bool {
	for _, e := range errs {
		if strings.HasPrefix(e.Path, prefix) {
			return true
		}
	}
	return false
}

func TestValidate_FullExample_NoErrors(t *testing.T) {
	tj, err := Parse([]byte(fullExampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	errs := tj.Validate()
	if len(errs) != 0 {
		t.Errorf("Validate: expected 0 errors, got %d:", len(errs))
		for _, e := range errs {
			t.Errorf("  %s", e.Error())
		}
	}
}

func TestValidate_Version(t *testing.T) {
	cases := []struct {
		name    string
		version int
		want    bool // expect error
	}{
		{"v0", 0, true},
		{"v1", 1, false},
		{"v2", 2, true},
		{"v99", 99, true},
		{"negative", -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tj := &ThemeJSON{Version: tc.version}
			errs := tj.Validate()
			e, found := findError(errs, "/version")
			if tc.want && !found {
				t.Errorf("expected /version error for version=%d", tc.version)
			}
			if !tc.want && found {
				t.Errorf("unexpected /version error: %s", e.Error())
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	e := ValidationError{Path: "/version", Message: "must be 1"}
	if got := e.Error(); got != "/version: must be 1" {
		t.Errorf("Error: got %q", got)
	}
	e2 := ValidationError{Message: "must be 1"}
	if got := e2.Error(); got != "must be 1" {
		t.Errorf("Error (no path): got %q", got)
	}
}

func TestValidate_BadSlug(t *testing.T) {
	cases := []struct {
		name string
		slug string
		ok   bool
	}{
		{"valid", "accent", true},
		{"valid-kebab", "accent-fg", true},
		{"valid-with-digits", "h1-strong", true},
		{"valid-x2", "x2", true},
		{"capitals", "Foo", false},
		{"special", "Foo!", false},
		{"underscore", "foo_bar", false},
		{"trailing-hyphen", "foo-", false},
		{"leading-hyphen", "-foo", false},
		{"double-hyphen", "foo--bar", false},
		{"empty", "", false},
		{"digit-leading", "2xl", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidSlug(tc.slug); got != tc.ok {
				t.Errorf("isValidSlug(%q) = %v, want %v", tc.slug, got, tc.ok)
			}
		})
	}
}

func TestValidate_BadSlugInPalette(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Palette: []ColorEntry{
					{Slug: "Foo!", Name: "Foo", Color: "#ffffff"},
				},
			},
		},
	}
	errs := tj.Validate()
	e, ok := findError(errs, "/settings/color/palette/0/slug")
	if !ok {
		t.Fatalf("expected slug error, got: %v", errs)
	}
	if !strings.Contains(e.Message, "kebab-case") {
		t.Errorf("error message should mention kebab-case: %q", e.Message)
	}
}

func TestValidate_MissingPaletteName(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Palette: []ColorEntry{
					{Slug: "ink", Name: "", Color: "#000000"},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/color/palette/0/name"); !ok {
		t.Errorf("expected /settings/color/palette/0/name error, got: %v", errs)
	}
}

func TestValidate_BadColor(t *testing.T) {
	cases := []struct {
		name  string
		color string
		ok    bool
	}{
		{"hex6", "#ffffff", true},
		{"hex8-alpha", "#ffffff80", true},
		{"hex3", "#fff", true},
		{"hex-invalid", "#zzz", false},
		{"hex-wrong-length", "#fffff", false},
		{"rgb", "rgb(0, 0, 0)", true},
		{"rgba", "rgba(0, 0, 0, 0.5)", true},
		{"rgb-space-separated", "rgb(0 0 0)", true},
		{"rgb-percentage", "rgb(50%, 50%, 50%)", true},
		{"rgb-slash-alpha", "rgb(0 0 0 / 50%)", true},
		{"rgb-malformed", "rgb(abc)", false},
		{"rgb-empty", "rgb()", false},
		{"hsl", "hsl(120, 50%, 50%)", true},
		{"hsla", "hsla(120, 50%, 50%, 0.5)", true},
		{"hsl-deg", "hsl(120deg, 50%, 50%)", true},
		{"hsl-bad-hue", "hsl(abc, 50%, 50%)", false},
		{"hsl-bad-saturation", "hsl(120, abc, 50%)", false},
		{"named-red", "red", true},
		{"named-rebeccapurple", "rebeccapurple", true},
		{"named-typo", "redd", false},
		{"named-cap-RED", "RED", true},
		{"transparent", "transparent", true},
		{"currentcolor", "currentcolor", true},
		{"var-token", "var(--wp-preset--color--accent)", true},
		{"var-bare", "var(--)", false},
		{"empty", "", false},
		{"whitespace-padded", " #fff ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidCSSColor(tc.color); got != tc.ok {
				t.Errorf("isValidCSSColor(%q) = %v, want %v", tc.color, got, tc.ok)
			}
		})
	}
}

func TestValidate_BadColorInPalette(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Palette: []ColorEntry{
					{Slug: "good", Name: "Good", Color: "#0f172a"},
					{Slug: "bad", Name: "Bad", Color: "not-a-color"},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/color/palette/1/color"); !ok {
		t.Errorf("expected /settings/color/palette/1/color error, got: %v", errs)
	}
	// Position 0 should be clean.
	if hasErrorAt(errs, "/settings/color/palette/0") {
		t.Errorf("unexpected error on entry 0: %v", errs)
	}
}

func TestValidate_CSSLength(t *testing.T) {
	cases := []struct {
		name string
		v    string
		ok   bool
	}{
		{"zero", "0", true},
		{"px", "12px", true},
		{"rem", "1.5rem", true},
		{"em", "0.875em", true},
		{"pct", "100%", true},
		{"vw", "50vw", true},
		{"vh", "50vh", true},
		{"negative", "-2rem", true},
		{"var", "var(--wp-preset--font-size--md)", true},
		{"calc", "calc(100% - 20px)", true},
		{"clamp", "clamp(1rem, 2vw, 2rem)", true},
		{"unknown-unit", "12foo", false},
		{"no-unit-nonzero", "12", false},
		{"bare-string", "small", false},
		{"empty", "", false},
		{"whitespace", " ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidCSSLength(tc.v); got != tc.ok {
				t.Errorf("isValidCSSLength(%q) = %v, want %v", tc.v, got, tc.ok)
			}
		})
	}
}

func TestValidate_BadFontSize(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontSizes: []FontSize{
					{Slug: "good", Name: "Good", Size: "1rem"},
					{Slug: "bad", Name: "Bad", Size: "not-a-length"},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontSizes/1/size"); !ok {
		t.Errorf("expected size error, got: %v", errs)
	}
}

func TestValidate_FluidFontSizeRequiresValidLengths(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontSizes: []FontSize{
					{
						Slug: "xxl", Name: "Display", Size: "2.5rem",
						Fluid: &FluidFontSize{Min: "not-a-length", Max: "3.5rem"},
					},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontSizes/0/fluid/min"); !ok {
		t.Errorf("expected fluid.min error, got: %v", errs)
	}
}

func TestValidate_MissingFontFamilyFields(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontFamilies: []FontFamily{
					{Slug: "sans", Name: "", FontFamily: ""},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontFamilies/0/name"); !ok {
		t.Errorf("expected name error, got: %v", errs)
	}
	if _, ok := findError(errs, "/settings/typography/fontFamilies/0/fontFamily"); !ok {
		t.Errorf("expected fontFamily error, got: %v", errs)
	}
}

func TestValidate_FontFaceMissingSrc(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontFamilies: []FontFamily{
					{
						Slug:       "sans",
						Name:       "Sans",
						FontFamily: "Inter",
						FontFace:   []FontFace{{Src: ""}},
					},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/typography/fontFamilies/0/fontFace/0/src"); !ok {
		t.Errorf("expected src error, got: %v", errs)
	}
}

func TestValidate_SpacingScale(t *testing.T) {
	t.Run("absent-scale-valid", func(t *testing.T) {
		tj := &ThemeJSON{Version: 1}
		errs := tj.Validate()
		if len(errs) != 0 {
			t.Errorf("expected no errors for empty manifest, got: %v", errs)
		}
	})

	t.Run("bad-operator", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			Settings: Settings{
				Spacing: SpacingSettings{
					SpacingScale: SpacingScale{
						Operator: "-", Increment: 1.5, Steps: 7, MediumStep: 1.5, Unit: "rem",
					},
				},
			},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/settings/spacing/spacingScale/operator"); !ok {
			t.Errorf("expected operator error, got: %v", errs)
		}
	})

	t.Run("bad-unit", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			Settings: Settings{
				Spacing: SpacingSettings{
					SpacingScale: SpacingScale{
						Operator: "*", Increment: 1.5, Steps: 7, MediumStep: 1.5, Unit: "furlong",
					},
				},
			},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/settings/spacing/spacingScale/unit"); !ok {
			t.Errorf("expected unit error, got: %v", errs)
		}
	})

	t.Run("zero-increment", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			Settings: Settings{
				Spacing: SpacingSettings{
					SpacingScale: SpacingScale{
						Operator: "+", Increment: 0, Steps: 7, MediumStep: 1.5, Unit: "rem",
					},
				},
			},
		}
		errs := tj.Validate()
		// Operator "+" with increment 0 — increment must be > 0.
		// Steps and MediumStep present so no error there.
		if _, ok := findError(errs, "/settings/spacing/spacingScale/increment"); !ok {
			t.Errorf("expected increment error, got: %v", errs)
		}
	})

	t.Run("zero-steps", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			Settings: Settings{
				Spacing: SpacingSettings{
					SpacingScale: SpacingScale{
						Operator: "+", Increment: 1, Steps: 0, MediumStep: 1.5, Unit: "rem",
					},
				},
			},
		}
		errs := tj.Validate()
		// IsZero returns true when all are zero, so we need a non-zero
		// field to enter validation — Operator suffices. Steps==0 is
		// the invalid case we want flagged. But IsZero conditions on
		// all-zero, and we have Operator set, so we should reach the
		// Steps check.
		if _, ok := findError(errs, "/settings/spacing/spacingScale/steps"); !ok {
			t.Errorf("expected steps error, got: %v", errs)
		}
	})
}

func TestValidate_SpacingUnitsList(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Spacing: SpacingSettings{
				Units: []string{"px", "furlong"},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/spacing/units/1"); !ok {
		t.Errorf("expected units[1] error, got: %v", errs)
	}
}

func TestValidate_Layout(t *testing.T) {
	t.Run("good", func(t *testing.T) {
		tj := &ThemeJSON{
			Version:  1,
			Settings: Settings{Layout: LayoutSettings{ContentSize: "720px", WideSize: "1180px"}},
		}
		errs := tj.Validate()
		if len(errs) != 0 {
			t.Errorf("expected no errors, got: %v", errs)
		}
	})

	t.Run("bad-content-size", func(t *testing.T) {
		tj := &ThemeJSON{
			Version:  1,
			Settings: Settings{Layout: LayoutSettings{ContentSize: "wide", WideSize: "1180px"}},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/settings/layout/contentSize"); !ok {
			t.Errorf("expected contentSize error, got: %v", errs)
		}
	})

	t.Run("bad-wide-size", func(t *testing.T) {
		tj := &ThemeJSON{
			Version:  1,
			Settings: Settings{Layout: LayoutSettings{ContentSize: "720px", WideSize: "extra-wide"}},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/settings/layout/wideSize"); !ok {
			t.Errorf("expected wideSize error, got: %v", errs)
		}
	})
}

func TestValidate_ShadowPreset(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Shadow: ShadowSettings{
				Presets: []ShadowPreset{
					{Slug: "Bad!", Name: "X", Shadow: "0 1px 2px rgba(0,0,0,.05)"},
					{Slug: "soft", Name: "Soft", Shadow: ""},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/shadow/presets/0/slug"); !ok {
		t.Errorf("expected shadow slug error, got: %v", errs)
	}
	if _, ok := findError(errs, "/settings/shadow/presets/1/shadow"); !ok {
		t.Errorf("expected shadow body required error, got: %v", errs)
	}
}

func TestValidate_CustomTemplate_TitleRequiredWhenAreaSet(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		CustomTemplates: []TemplateDef{
			{Name: "page-landing", Title: "", Area: "header"},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/customTemplates/0/title"); !ok {
		t.Errorf("expected title-required error, got: %v", errs)
	}
}

func TestValidate_CustomTemplate_AreaWhenSetMustBeValid(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		CustomTemplates: []TemplateDef{
			{Name: "page-landing", Title: "Landing", Area: "atrium"},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/customTemplates/0/area"); !ok {
		t.Errorf("expected area error, got: %v", errs)
	}
}

func TestValidate_CustomTemplate_NoAreaIsValid(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		CustomTemplates: []TemplateDef{
			{Name: "page-landing", Title: "Landing Page", PostTypes: []string{"page"}},
		},
	}
	errs := tj.Validate()
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_CustomTemplate_BadName(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		CustomTemplates: []TemplateDef{
			{Name: "Bad Name!", Title: "X"},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/customTemplates/0/name"); !ok {
		t.Errorf("expected name error, got: %v", errs)
	}
}

func TestValidate_TemplatePart(t *testing.T) {
	t.Run("good", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			TemplateParts: []TemplatePartDef{
				{Name: "header", Title: "Header", Area: "header"},
				{Name: "footer", Title: "Footer", Area: "footer"},
				{Name: "main", Title: "Main", Area: "uncategorized"},
				{Name: "side", Title: "Side", Area: "sidebar"},
			},
		}
		errs := tj.Validate()
		if len(errs) != 0 {
			t.Errorf("expected no errors, got: %v", errs)
		}
	})

	t.Run("bad-area", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			TemplateParts: []TemplatePartDef{
				{Name: "weird", Title: "Weird", Area: "atrium"},
			},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/templateParts/0/area"); !ok {
			t.Errorf("expected area error, got: %v", errs)
		}
	})

	t.Run("missing-area", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			TemplateParts: []TemplatePartDef{
				{Name: "weird", Title: "Weird"},
			},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/templateParts/0/area"); !ok {
			t.Errorf("expected area required error, got: %v", errs)
		}
	})

	t.Run("bad-name", func(t *testing.T) {
		tj := &ThemeJSON{
			Version: 1,
			TemplateParts: []TemplatePartDef{
				{Name: "Bad Header!", Title: "Header", Area: "header"},
			},
		}
		errs := tj.Validate()
		if _, ok := findError(errs, "/templateParts/0/name"); !ok {
			t.Errorf("expected name error, got: %v", errs)
		}
	})
}

func TestValidate_GradientSlugAndContent(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Gradients: []GradientEntry{
					{Slug: "Bad!", Name: "Bad", Gradient: "linear-gradient(45deg, red, blue)"},
					{Slug: "ok", Name: "OK", Gradient: ""},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/color/gradients/0/slug"); !ok {
		t.Errorf("expected gradient slug error, got: %v", errs)
	}
	if _, ok := findError(errs, "/settings/color/gradients/1/gradient"); !ok {
		t.Errorf("expected gradient required error, got: %v", errs)
	}
}

func TestValidate_Duotone(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Duotone: []DuotoneEntry{
					{Slug: "good", Name: "Good", Colors: []string{"#000", "#fff"}},
					{Slug: "bad-len", Name: "Bad", Colors: []string{"#000"}},
					{Slug: "Bad!", Name: "Bad", Colors: []string{"not-a-color", "#fff"}},
				},
			},
		},
	}
	errs := tj.Validate()
	if _, ok := findError(errs, "/settings/color/duotone/1/colors"); !ok {
		t.Errorf("expected duotone length error, got: %v", errs)
	}
	if _, ok := findError(errs, "/settings/color/duotone/2/slug"); !ok {
		t.Errorf("expected duotone slug error, got: %v", errs)
	}
	if _, ok := findError(errs, "/settings/color/duotone/2/colors/0"); !ok {
		t.Errorf("expected duotone color value error, got: %v", errs)
	}
}

func TestValidate_AccumulatesMultipleErrors(t *testing.T) {
	tj := &ThemeJSON{
		Version: 0, // bad
		Settings: Settings{
			Color: ColorSettings{
				Palette: []ColorEntry{
					{Slug: "BAD", Name: "", Color: "not-a-color"},
				},
			},
		},
	}
	errs := tj.Validate()
	// We expect at least version + 3 palette errors (slug, name,
	// color) = 4.
	if len(errs) < 4 {
		t.Errorf("expected ≥4 errors, got %d: %v", len(errs), errs)
	}
}
