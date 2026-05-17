package theme

import (
	"strings"
	"testing"
)

func TestParse_Empty(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tc.in)
			}
		})
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte("{not json"))
	if err == nil {
		t.Fatal("Parse: expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse: %v", err)
	}
}

func TestParse_TrailingData(t *testing.T) {
	_, err := Parse([]byte(`{"version":1} {"version":1}`))
	if err == nil {
		t.Fatal("Parse: expected error for trailing data, got nil")
	}
}

func TestParse_UnknownTopLevelKey(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"unknownTopLevelKey": "bad"
	}`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("Parse: expected error for unknown top-level key, got nil")
	}
}

func TestParse_TypeMismatch(t *testing.T) {
	// version should be int, not string
	data := []byte(`{"version": "1"}`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("Parse: expected error for type mismatch, got nil")
	}
}

func TestParse_ValidMinimal(t *testing.T) {
	data := []byte(`{"version": 1}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version: got %d, want 1", got.Version)
	}
}

func TestParse_ValidFullExample(t *testing.T) {
	got, err := Parse([]byte(fullExampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version: got %d, want 1", got.Version)
	}
	if got.Title != "Hello GoNext" {
		t.Errorf("Title: got %q, want %q", got.Title, "Hello GoNext")
	}
	if len(got.Settings.Color.Palette) != 5 {
		t.Errorf("palette: got %d entries, want 5", len(got.Settings.Color.Palette))
	}
	if !got.Settings.Color.Custom {
		t.Errorf("Color.Custom: want true")
	}
	if got.Settings.Color.Palette[0].Slug != "ink" {
		t.Errorf("palette[0].slug: got %q, want %q", got.Settings.Color.Palette[0].Slug, "ink")
	}
	if got.Settings.Color.Palette[0].Color != "#0f172a" {
		t.Errorf("palette[0].color: got %q, want %q", got.Settings.Color.Palette[0].Color, "#0f172a")
	}
	if len(got.Settings.Typography.FontFamilies) != 2 {
		t.Errorf("fontFamilies: got %d entries, want 2", len(got.Settings.Typography.FontFamilies))
	}
	if len(got.Settings.Typography.FontSizes) != 5 {
		t.Errorf("fontSizes: got %d entries, want 5", len(got.Settings.Typography.FontSizes))
	}
	if got.Settings.Typography.FontSizes[4].Fluid == nil {
		t.Errorf("fontSizes[4].fluid: want set")
	}
	if got.Settings.Spacing.SpacingScale.Operator != "*" {
		t.Errorf("spacingScale.operator: got %q, want %q", got.Settings.Spacing.SpacingScale.Operator, "*")
	}
	if got.Settings.Layout.ContentSize != "720px" {
		t.Errorf("layout.contentSize: got %q", got.Settings.Layout.ContentSize)
	}
	if !got.Supports.SiteEditor {
		t.Errorf("supports.siteEditor: want true")
	}
	if len(got.CustomTemplates) != 1 {
		t.Errorf("customTemplates: got %d, want 1", len(got.CustomTemplates))
	}
	if len(got.TemplateParts) != 2 {
		t.Errorf("templateParts: got %d, want 2", len(got.TemplateParts))
	}
}

// fullExampleJSON is the §3.1 example from docs/03-theme-system.md.
// We carry it verbatim so any drift between docs and validator
// surfaces in CI as a test failure.
const fullExampleJSON = `{
  "$schema": "https://gonext.dev/schemas/theme.json/v1",
  "version": 1,
  "title": "Hello GoNext",
  "settings": {
    "appearanceTools": true,
    "color": {
      "palette": [
        { "slug": "ink",       "name": "Ink",       "color": "#0f172a" },
        { "slug": "paper",     "name": "Paper",     "color": "#ffffff" },
        { "slug": "muted",     "name": "Muted",     "color": "#64748b" },
        { "slug": "accent",    "name": "Accent",    "color": "#2563eb" },
        { "slug": "accent-fg", "name": "On Accent", "color": "#ffffff" }
      ],
      "gradients": [
        { "slug": "sunset",
          "name": "Sunset",
          "gradient": "linear-gradient(135deg, #f59e0b, #ef4444)" }
      ],
      "custom": true,
      "customGradient": true,
      "duotone": []
    },
    "typography": {
      "fontFamilies": [
        { "slug": "sans",
          "name": "Sans",
          "fontFamily": "Inter, ui-sans-serif, system-ui",
          "fontFace": [
            { "src": "/assets/fonts/Inter-Variable.woff2",
              "fontWeight": "100 900",
              "fontStyle": "normal",
              "fontDisplay": "swap" }
          ]},
        { "slug": "serif",
          "name": "Serif",
          "fontFamily": "Iowan Old Style, Apple Garamond, Baskerville, serif" }
      ],
      "fontSizes": [
        { "slug": "sm",  "name": "Small",  "size": "0.875rem" },
        { "slug": "md",  "name": "Medium", "size": "1rem"     },
        { "slug": "lg",  "name": "Large",  "size": "1.25rem"  },
        { "slug": "xl",  "name": "X-Large","size": "1.75rem"  },
        { "slug": "xxl", "name": "Display","size": "2.5rem", "fluid": { "min": "2rem", "max": "3.5rem" } }
      ],
      "lineHeight": true,
      "letterSpacing": true,
      "textDecoration": true
    },
    "spacing": {
      "units": ["px", "rem", "em", "%", "vw"],
      "spacingScale": {
        "operator": "*",
        "increment": 1.5,
        "steps": 7,
        "mediumStep": 1.5,
        "unit": "rem"
      },
      "padding": true,
      "margin": true,
      "blockGap": true
    },
    "layout": {
      "contentSize": "720px",
      "wideSize":    "1180px"
    },
    "border": {
      "color": true,
      "radius": true,
      "style": true,
      "width": true
    },
    "shadow": {
      "presets": [
        { "slug": "soft",  "name": "Soft",  "shadow": "0 1px 2px rgba(0,0,0,.05)" },
        { "slug": "lifted","name": "Lifted","shadow": "0 8px 24px rgba(0,0,0,.12)" }
      ]
    },
    "blocks": {
      "core/button": {
        "border": { "radius": true },
        "color":  { "background": true, "text": true }
      }
    }
  },
  "styles": {
    "color": { "background": "var(--wp-preset--color--paper)", "text": "var(--wp-preset--color--ink)" },
    "typography": {
      "fontFamily": "var(--wp-preset--font-family--sans)",
      "fontSize":   "var(--wp-preset--font-size--md)",
      "lineHeight": "1.6"
    },
    "elements": {
      "h1": { "typography": { "fontSize": "var(--wp-preset--font-size--xxl)", "lineHeight": "1.1" } },
      "h2": { "typography": { "fontSize": "var(--wp-preset--font-size--xl)" } },
      "link": { "color": { "text": "var(--wp-preset--color--accent)" } }
    },
    "blocks": {
      "core/button": {
        "color": { "background": "var(--wp-preset--color--accent)", "text": "var(--wp-preset--color--accent-fg)" },
        "border": { "radius": "0.5rem" }
      }
    }
  },
  "supports": {
    "blockTemplates": true,
    "siteEditor":     true,
    "darkModeAuto":   true,
    "customizer":     true,
    "menus":          ["primary", "footer"],
    "widgetAreas":    ["sidebar-main", "footer-1", "footer-2"]
  },
  "patterns": ["hero-cta", "three-column-features"],
  "customTemplates": [
    { "name": "page-landing", "title": "Landing Page", "postTypes": ["page"] }
  ],
  "templateParts": [
    { "name": "header", "title": "Header", "area": "header" },
    { "name": "footer", "title": "Footer", "area": "footer" }
  ]
}`
