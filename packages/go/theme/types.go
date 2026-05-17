package theme

// CurrentVersion is the only theme.json schema version this package
// accepts. The §3.3 design note explicitly drops WordPress's
// version=2 legacy; GoNext starts fresh at 1.
const CurrentVersion = 1

// ThemeJSON is the typed in-memory representation of a v1 theme.json
// manifest. Every key in docs/03-theme-system.md §3.1 maps to a field
// here; absent JSON keys leave the corresponding Go field at its zero
// value (Validate distinguishes "not present" from "present but
// invalid" via the JSON tags + path emission).
type ThemeJSON struct {
	// Schema is the JSON $schema URL, optional but recommended so
	// authoring tools can offer autocomplete.
	Schema string `json:"$schema,omitempty"`

	// Version is the manifest schema version. Must equal CurrentVersion.
	Version int `json:"version"`

	// Title is the human-readable theme name (also surfaced in the
	// admin switcher beside the screenshot).
	Title string `json:"title,omitempty"`

	// Settings is the design-token surface: palette, type scale,
	// spacing, layout, border, shadow presets, block opt-ins.
	Settings Settings `json:"settings"`

	// Styles is the *concrete* style application — typically var()
	// references back into the tokens declared under Settings. See
	// docs/03-theme-system.md §3.1 for the canonical example.
	Styles Styles `json:"styles,omitempty"`

	// Supports declares which optional renderer features the theme
	// opts into. Unknown keys here are ignored (forward compatible);
	// new features land here without a schema bump.
	Supports Supports `json:"supports,omitempty"`

	// Patterns lists block-pattern slugs the theme contributes to
	// the editor.
	Patterns []string `json:"patterns,omitempty"`

	// CustomTemplates declares page-template aliases that show up in
	// the admin "Template" dropdown for the matching post types.
	CustomTemplates []TemplateDef `json:"customTemplates,omitempty"`

	// TemplateParts declares the named regions (header, footer,
	// sidebar, uncategorized) this theme ships parts for.
	TemplateParts []TemplatePartDef `json:"templateParts,omitempty"`
}

// Settings is the design-token surface declared by a theme.
type Settings struct {
	AppearanceTools bool             `json:"appearanceTools,omitempty"`
	Color           ColorSettings    `json:"color,omitempty"`
	Typography      TypographySet    `json:"typography,omitempty"`
	Spacing         SpacingSettings  `json:"spacing,omitempty"`
	Layout          LayoutSettings   `json:"layout,omitempty"`
	Border          BorderSettings   `json:"border,omitempty"`
	Shadow          ShadowSettings   `json:"shadow,omitempty"`
	Blocks          map[string]Block `json:"blocks,omitempty"`
}

// ColorSettings groups palette/gradient declarations and per-feature
// opt-ins (custom picker, default palette inclusion).
type ColorSettings struct {
	Palette        []ColorEntry    `json:"palette,omitempty"`
	Gradients      []GradientEntry `json:"gradients,omitempty"`
	Custom         bool            `json:"custom,omitempty"`
	CustomGradient bool            `json:"customGradient,omitempty"`
	DefaultPalette bool            `json:"defaultPalette,omitempty"`
	Duotone        []DuotoneEntry  `json:"duotone,omitempty"`
}

// ColorEntry is one named color in the palette. Slug is the stable
// machine identifier (used as the CSS custom-property suffix); Name is
// the human label surfaced in the editor; Color is the CSS color value.
type ColorEntry struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// GradientEntry mirrors ColorEntry for CSS gradients.
type GradientEntry struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Gradient string `json:"gradient"`
}

// DuotoneEntry mirrors ColorEntry for the editor's duotone filter.
type DuotoneEntry struct {
	Slug   string   `json:"slug"`
	Name   string   `json:"name"`
	Colors []string `json:"colors"`
}

// TypographySet groups the type scale, font face declarations, and
// per-feature opt-ins.
type TypographySet struct {
	FontFamilies   []FontFamily `json:"fontFamilies,omitempty"`
	FontSizes      []FontSize   `json:"fontSizes,omitempty"`
	LineHeight     bool         `json:"lineHeight,omitempty"`
	LetterSpacing  bool         `json:"letterSpacing,omitempty"`
	TextDecoration bool         `json:"textDecoration,omitempty"`
	Custom         bool         `json:"custom,omitempty"`
}

// FontFamily is one entry in the font scale. FontFace, when present,
// declares one or more @font-face descriptors the renderer emits into
// the document head.
type FontFamily struct {
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	FontFamily string     `json:"fontFamily"`
	FontFace   []FontFace `json:"fontFace,omitempty"`
}

// FontFace is a single @font-face descriptor.
type FontFace struct {
	Src         string `json:"src"`
	FontWeight  string `json:"fontWeight,omitempty"`
	FontStyle   string `json:"fontStyle,omitempty"`
	FontDisplay string `json:"fontDisplay,omitempty"`
}

// FontSize is one step in the type scale. Size is the default; Fluid,
// when set, declares clamp() min/max for fluid scaling.
type FontSize struct {
	Slug  string         `json:"slug"`
	Name  string         `json:"name"`
	Size  string         `json:"size"`
	Fluid *FluidFontSize `json:"fluid,omitempty"`
}

// FluidFontSize is the min/max envelope for fluid font scaling.
type FluidFontSize struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// SpacingSettings groups the spacing scale + per-feature opt-ins.
type SpacingSettings struct {
	Units        []string     `json:"units,omitempty"`
	SpacingScale SpacingScale `json:"spacingScale,omitempty"`
	Padding      bool         `json:"padding,omitempty"`
	Margin       bool         `json:"margin,omitempty"`
	BlockGap     bool         `json:"blockGap,omitempty"`
}

// SpacingScale defines a generated spacing scale. The §3.3 design note
// names this as the only blessed way to declare spacing — WordPress's
// "presets vs scale" duality is gone.
//
// The presence of an explicit Operator+Steps distinguishes "scale
// declared" from "scale absent"; a zero-value struct is treated as
// absent and skipped during validation.
type SpacingScale struct {
	Operator   string  `json:"operator,omitempty"`   // "+" or "*"
	Increment  float64 `json:"increment,omitempty"`  // step size
	Steps      int     `json:"steps,omitempty"`      // how many steps
	MediumStep float64 `json:"mediumStep,omitempty"` // anchor value
	Unit       string  `json:"unit,omitempty"`       // px, rem, em, %, vw
}

// IsZero reports whether the scale is the zero value (i.e. not
// declared in the manifest).
func (s SpacingScale) IsZero() bool {
	return s.Operator == "" && s.Increment == 0 && s.Steps == 0 && s.MediumStep == 0 && s.Unit == ""
}

// LayoutSettings declares the two content-width tokens the renderer
// emits as --gn-layout-content and --gn-layout-wide.
type LayoutSettings struct {
	ContentSize string `json:"contentSize,omitempty"`
	WideSize    string `json:"wideSize,omitempty"`
}

// BorderSettings groups per-feature border opt-ins.
type BorderSettings struct {
	Color  bool `json:"color,omitempty"`
	Radius bool `json:"radius,omitempty"`
	Style  bool `json:"style,omitempty"`
	Width  bool `json:"width,omitempty"`
}

// ShadowSettings groups the shadow preset list.
type ShadowSettings struct {
	Presets []ShadowPreset `json:"presets,omitempty"`
}

// ShadowPreset is one named CSS shadow value.
type ShadowPreset struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Shadow string `json:"shadow"`
}

// Block is the per-block opt-in surface declared under
// settings.blocks[name]. The shape mirrors the top-level Settings so
// themes can scope features to individual block types.
type Block struct {
	Border BorderSettings `json:"border,omitempty"`
	Color  ColorBlock     `json:"color,omitempty"`
}

// ColorBlock is the per-block subset of ColorSettings — currently
// just the background/text opt-ins.
type ColorBlock struct {
	Background bool `json:"background,omitempty"`
	Text       bool `json:"text,omitempty"`
}

// Styles is the *concrete* style application surface. Values are
// typically var() references back into the tokens declared under
// Settings; the renderer applies them at the matching CSS selectors.
type Styles struct {
	Color      *StyleColor          `json:"color,omitempty"`
	Typography *StyleTypography     `json:"typography,omitempty"`
	Elements   map[string]Element   `json:"elements,omitempty"`
	Blocks     map[string]BlockStyle `json:"blocks,omitempty"`
}

// StyleColor is the color application in styles.
type StyleColor struct {
	Background string `json:"background,omitempty"`
	Text       string `json:"text,omitempty"`
}

// StyleTypography is the typography application in styles.
type StyleTypography struct {
	FontFamily string `json:"fontFamily,omitempty"`
	FontSize   string `json:"fontSize,omitempty"`
	LineHeight string `json:"lineHeight,omitempty"`
}

// Element is one entry in styles.elements (e.g. h1, h2, link).
type Element struct {
	Color      *StyleColor      `json:"color,omitempty"`
	Typography *StyleTypography `json:"typography,omitempty"`
}

// BlockStyle is one entry in styles.blocks[name] — a concrete styling
// applied to that block type.
type BlockStyle struct {
	Color  *StyleColor  `json:"color,omitempty"`
	Border *StyleBorder `json:"border,omitempty"`
}

// StyleBorder is the border application in styles.
type StyleBorder struct {
	Radius string `json:"radius,omitempty"`
	Color  string `json:"color,omitempty"`
	Style  string `json:"style,omitempty"`
	Width  string `json:"width,omitempty"`
}

// Supports declares which optional renderer features the theme opts
// into. The §3.3 design note collapses WordPress's add_theme_support()
// surface into this single struct.
type Supports struct {
	BlockTemplates bool     `json:"blockTemplates,omitempty"`
	SiteEditor     bool     `json:"siteEditor,omitempty"`
	DarkModeAuto   bool     `json:"darkModeAuto,omitempty"`
	Customizer     bool     `json:"customizer,omitempty"`
	Menus          []string `json:"menus,omitempty"`
	WidgetAreas    []string `json:"widgetAreas,omitempty"`
}

// TemplateDef declares a custom page template (the §3.1 example shows
// page-landing for the "page" post type).
type TemplateDef struct {
	Name      string   `json:"name"`
	Title     string   `json:"title,omitempty"`
	Area      string   `json:"area,omitempty"`
	PostTypes []string `json:"postTypes,omitempty"`
}

// TemplatePartDef declares a named template part (header, footer,
// sidebar, uncategorized).
type TemplatePartDef struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Area  string `json:"area"`
}
