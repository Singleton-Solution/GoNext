package theme

import (
	"strings"
	"testing"
)

func TestEmitCSSCustomProperties_Empty(t *testing.T) {
	tj := &ThemeJSON{Version: 1}
	got := tj.EmitCSSCustomProperties()
	if got != "" {
		t.Errorf("empty manifest should produce empty string, got %q", got)
	}
}

func TestEmitCSSCustomProperties_Nil(t *testing.T) {
	var tj *ThemeJSON
	if got := tj.EmitCSSCustomProperties(); got != "" {
		t.Errorf("nil receiver should produce empty string, got %q", got)
	}
}

func TestEmitCSSCustomProperties_FullExample(t *testing.T) {
	tj, err := Parse([]byte(fullExampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := tj.EmitCSSCustomProperties()

	// Spot-check every kind of preset emitted.
	want := []string{
		"--wp-preset--color--ink: #0f172a;",
		"--wp-preset--color--paper: #ffffff;",
		"--wp-preset--color--accent: #2563eb;",
		"--wp-preset--color--accent-fg: #ffffff;",
		"--wp-preset--gradient--sunset: linear-gradient(135deg, #f59e0b, #ef4444);",
		"--wp-preset--font-family--sans: Inter, ui-sans-serif, system-ui;",
		"--wp-preset--font-size--sm: 0.875rem;",
		"--wp-preset--font-size--md: 1rem;",
		"--wp-preset--font-size--xxl: clamp(2rem, 2.5rem, 3.5rem);",
		"--wp-preset--layout--content: 720px;",
		"--wp-preset--layout--wide: 1180px;",
		"--wp-preset--shadow--soft: 0 1px 2px rgba(0,0,0,.05);",
		"--wp-preset--shadow--lifted: 0 8px 24px rgba(0,0,0,.12);",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output missing expected line: %q\noutput:\n%s", w, got)
		}
	}

	// Structural sanity: ":root { ... }".
	if !strings.HasPrefix(strings.TrimSpace(got), ":root {") {
		t.Errorf("output should begin with :root { — got:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "}") {
		t.Errorf("output should end with } — got:\n%s", got)
	}
}

func TestEmitCSSCustomProperties_FluidFontSize(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Typography: TypographySet{
				FontSizes: []FontSize{
					{
						Slug: "xxl", Name: "Display", Size: "2.5rem",
						Fluid: &FluidFontSize{Min: "2rem", Max: "3.5rem"},
					},
				},
			},
		},
	}
	got := tj.EmitCSSCustomProperties()
	want := "--wp-preset--font-size--xxl: clamp(2rem, 2.5rem, 3.5rem);"
	if !strings.Contains(got, want) {
		t.Errorf("fluid font size: output missing %q\ngot:\n%s", want, got)
	}
}

func TestEmitCSSCustomProperties_Deterministic(t *testing.T) {
	tj, err := Parse([]byte(fullExampleJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	first := tj.EmitCSSCustomProperties()
	for i := 0; i < 5; i++ {
		next := tj.EmitCSSCustomProperties()
		if next != first {
			t.Fatalf("emit not deterministic on iteration %d", i+1)
		}
	}
}

func TestEmitCSSCustomProperties_OrderPreserved(t *testing.T) {
	tj := &ThemeJSON{
		Version: 1,
		Settings: Settings{
			Color: ColorSettings{
				Palette: []ColorEntry{
					{Slug: "zebra", Name: "Z", Color: "#000"},
					{Slug: "alpha", Name: "A", Color: "#fff"},
				},
			},
		},
	}
	got := tj.EmitCSSCustomProperties()
	zebraIdx := strings.Index(got, "--wp-preset--color--zebra")
	alphaIdx := strings.Index(got, "--wp-preset--color--alpha")
	if zebraIdx < 0 || alphaIdx < 0 {
		t.Fatalf("missing palette entries in output:\n%s", got)
	}
	if zebraIdx >= alphaIdx {
		t.Errorf("expected zebra (declared first) before alpha; got positions %d and %d", zebraIdx, alphaIdx)
	}
}

func TestEmitCSSCustomProperties_OnlyLayout(t *testing.T) {
	tj := &ThemeJSON{
		Version:  1,
		Settings: Settings{Layout: LayoutSettings{ContentSize: "720px"}},
	}
	got := tj.EmitCSSCustomProperties()
	want := "--wp-preset--layout--content: 720px;"
	if !strings.Contains(got, want) {
		t.Errorf("layout-only manifest: output missing %q\ngot:\n%s", want, got)
	}
	// Wide is absent — should not be emitted.
	if strings.Contains(got, "--wp-preset--layout--wide") {
		t.Errorf("wide should not be emitted when absent\ngot:\n%s", got)
	}
}
