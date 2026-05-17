package theme

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ValidationError is one issue surfaced by Validate. Path is a JSON
// pointer (RFC 6901-ish) into the original document so the admin or
// CLI can highlight the exact field; Message is human-readable.
type ValidationError struct {
	Path    string
	Message string
}

// Error implements the error interface so a single ValidationError can
// be returned where a generic error is expected.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// slugPattern enforces lowercase kebab-case: lowercase ASCII letters,
// digits, and inner hyphens. The first character must be a letter so
// numeric-prefixed slugs (which collide with CSS identifier rules) are
// rejected up front.
//
// Examples that pass: "ink", "accent-fg", "h1-strong", "x2".
// Examples that fail: "Ink", "accent_fg", "--accent", "2xl" (starts
// with digit), "" (empty).
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// hexShortPattern matches #rgb (3-digit).
var hexShortPattern = regexp.MustCompile(`^#[0-9a-fA-F]{3}$`)

// hexLongPattern matches #rrggbb and #rrggbbaa.
var hexLongPattern = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// rgbFuncPattern matches the rgb()/rgba() function form. The argument
// list is parsed by hand below — this regex is just a structural
// sieve. We deliberately don't enforce the inner number ranges in the
// regex (so the error message can be more specific).
var rgbFuncPattern = regexp.MustCompile(`^rgba?\(\s*[^)]+\s*\)$`)

// hslFuncPattern is the same idea for hsl()/hsla().
var hslFuncPattern = regexp.MustCompile(`^hsla?\(\s*[^)]+\s*\)$`)

// cssLengthPattern matches a CSS length: optional minus, digits with
// optional fractional part, followed by a unit token. Unitless zero
// ("0") is also accepted. The set of allowed units is closed —
// validation against actual unit strings happens in isValidCSSLength.
var cssLengthPattern = regexp.MustCompile(`^-?\d+(\.\d+)?[a-zA-Z%]+$`)

// validUnits is the closed set of length units the renderer is happy
// to emit unchanged. New units land here intentionally.
var validUnits = map[string]struct{}{
	"px": {}, "rem": {}, "em": {}, "%": {},
	"vw": {}, "vh": {}, "vmin": {}, "vmax": {},
	"ch": {}, "ex": {}, "pt": {}, "pc": {},
	"cm": {}, "mm": {}, "in": {},
}

// validAreas is the closed set of template-part areas. Per
// docs/03-theme-system.md §5.3 the Site Editor surfaces areas as
// logical regions; "uncategorized" is the catch-all for parts that
// don't fit header/footer/sidebar.
var validAreas = map[string]struct{}{
	"header":        {},
	"footer":        {},
	"sidebar":       {},
	"uncategorized": {},
}

// validSpacingOperators is the closed set of arithmetic operators
// SpacingScale accepts.
var validSpacingOperators = map[string]struct{}{
	"+": {},
	"*": {},
}

// validSpacingUnits is the spacing-scale-specific unit set. Spacing
// scales must resolve to a single absolute or relative unit; mixed
// (calc()) values are not legal here.
var validSpacingUnits = map[string]struct{}{
	"px": {}, "rem": {}, "em": {}, "%": {}, "vw": {}, "vh": {},
}

// namedCSSColors is the CSS Color Module Level 4 named color set,
// lowercased. We carry the table inline so the package has zero
// transitive dependencies. The renderer never normalizes the user's
// casing — case-insensitive comparison is fine here.
//
// Source: https://www.w3.org/TR/css-color-4/#named-colors
var namedCSSColors = map[string]struct{}{
	"aliceblue": {}, "antiquewhite": {}, "aqua": {}, "aquamarine": {},
	"azure": {}, "beige": {}, "bisque": {}, "black": {},
	"blanchedalmond": {}, "blue": {}, "blueviolet": {}, "brown": {},
	"burlywood": {}, "cadetblue": {}, "chartreuse": {}, "chocolate": {},
	"coral": {}, "cornflowerblue": {}, "cornsilk": {}, "crimson": {},
	"cyan": {}, "darkblue": {}, "darkcyan": {}, "darkgoldenrod": {},
	"darkgray": {}, "darkgreen": {}, "darkgrey": {}, "darkkhaki": {},
	"darkmagenta": {}, "darkolivegreen": {}, "darkorange": {}, "darkorchid": {},
	"darkred": {}, "darksalmon": {}, "darkseagreen": {}, "darkslateblue": {},
	"darkslategray": {}, "darkslategrey": {}, "darkturquoise": {}, "darkviolet": {},
	"deeppink": {}, "deepskyblue": {}, "dimgray": {}, "dimgrey": {},
	"dodgerblue": {}, "firebrick": {}, "floralwhite": {}, "forestgreen": {},
	"fuchsia": {}, "gainsboro": {}, "ghostwhite": {}, "gold": {},
	"goldenrod": {}, "gray": {}, "green": {}, "greenyellow": {},
	"grey": {}, "honeydew": {}, "hotpink": {}, "indianred": {},
	"indigo": {}, "ivory": {}, "khaki": {}, "lavender": {},
	"lavenderblush": {}, "lawngreen": {}, "lemonchiffon": {}, "lightblue": {},
	"lightcoral": {}, "lightcyan": {}, "lightgoldenrodyellow": {}, "lightgray": {},
	"lightgreen": {}, "lightgrey": {}, "lightpink": {}, "lightsalmon": {},
	"lightseagreen": {}, "lightskyblue": {}, "lightslategray": {}, "lightslategrey": {},
	"lightsteelblue": {}, "lightyellow": {}, "lime": {}, "limegreen": {},
	"linen": {}, "magenta": {}, "maroon": {}, "mediumaquamarine": {},
	"mediumblue": {}, "mediumorchid": {}, "mediumpurple": {}, "mediumseagreen": {},
	"mediumslateblue": {}, "mediumspringgreen": {}, "mediumturquoise": {}, "mediumvioletred": {},
	"midnightblue": {}, "mintcream": {}, "mistyrose": {}, "moccasin": {},
	"navajowhite": {}, "navy": {}, "oldlace": {}, "olive": {},
	"olivedrab": {}, "orange": {}, "orangered": {}, "orchid": {},
	"palegoldenrod": {}, "palegreen": {}, "paleturquoise": {}, "palevioletred": {},
	"papayawhip": {}, "peachpuff": {}, "peru": {}, "pink": {},
	"plum": {}, "powderblue": {}, "purple": {}, "rebeccapurple": {},
	"red": {}, "rosybrown": {}, "royalblue": {}, "saddlebrown": {},
	"salmon": {}, "sandybrown": {}, "seagreen": {}, "seashell": {},
	"sienna": {}, "silver": {}, "skyblue": {}, "slateblue": {},
	"slategray": {}, "slategrey": {}, "snow": {}, "springgreen": {},
	"steelblue": {}, "tan": {}, "teal": {}, "thistle": {},
	"tomato": {}, "turquoise": {}, "violet": {}, "wheat": {},
	"white": {}, "whitesmoke": {}, "yellow": {}, "yellowgreen": {},
	"transparent": {}, "currentcolor": {},
}

// isValidSlug reports whether s matches the kebab-case-lowercase rule.
func isValidSlug(s string) bool {
	return slugPattern.MatchString(s)
}

// isValidCSSColor accepts hex (3, 6, 8 digit), rgb()/rgba(), hsl()/hsla(),
// var(--…) (token reference), and CSS named colors. We err on the side
// of "permissive but bounded": the goal is to catch user typos
// ("#zzzz", "redd", "rgb(abc)"), not to fully implement the CSS color
// grammar.
func isValidCSSColor(s string) bool {
	if s == "" {
		return false
	}
	trimmed := strings.TrimSpace(s)
	if trimmed != s {
		return false
	}

	// var(--token) references — accepted as-is. The renderer resolves
	// these later against the emitted custom properties.
	if strings.HasPrefix(s, "var(") && strings.HasSuffix(s, ")") {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "var("), ")")
		inner = strings.TrimSpace(inner)
		return strings.HasPrefix(inner, "--") && len(inner) > 2
	}

	// Hex notations.
	if hexShortPattern.MatchString(s) || hexLongPattern.MatchString(s) {
		return true
	}

	// Functional notations. We validate the argument lists are
	// non-empty and contain only digits, commas, whitespace, dots,
	// percent signs, and minus signs — the lexical surface common to
	// rgb/rgba/hsl/hsla.
	if rgbFuncPattern.MatchString(s) {
		return isValidRGBArgs(s)
	}
	if hslFuncPattern.MatchString(s) {
		return isValidHSLArgs(s)
	}

	// Named colors. Case-insensitive per CSS spec.
	if _, ok := namedCSSColors[strings.ToLower(s)]; ok {
		return true
	}

	return false
}

// isValidRGBArgs lightly validates the comma- or space-separated
// argument list of an rgb()/rgba() function. We check argument count
// and that each token parses as either a number or a percentage.
func isValidRGBArgs(s string) bool {
	open := strings.Index(s, "(")
	close := strings.LastIndex(s, ")")
	if open < 0 || close < 0 || close <= open+1 {
		return false
	}
	inner := strings.TrimSpace(s[open+1 : close])
	// Replace commas with spaces so we can split uniformly. CSS Color
	// Level 4 permits both forms.
	inner = strings.ReplaceAll(inner, ",", " ")
	// The "rgb(r g b / a)" form uses a slash before alpha; normalize
	// it to whitespace too. The downstream isFraction check accepts
	// either side.
	inner = strings.ReplaceAll(inner, "/", " ")
	fields := strings.Fields(inner)
	if len(fields) != 3 && len(fields) != 4 {
		return false
	}
	for _, f := range fields {
		if !isNumericOrPercent(f) {
			return false
		}
	}
	return true
}

// isValidHSLArgs mirrors isValidRGBArgs for hsl()/hsla(). The first
// argument is an angle (number, optionally suffixed deg/grad/rad/turn);
// the next two are percentages; the optional alpha is number or
// percent.
func isValidHSLArgs(s string) bool {
	open := strings.Index(s, "(")
	close := strings.LastIndex(s, ")")
	if open < 0 || close < 0 || close <= open+1 {
		return false
	}
	inner := strings.TrimSpace(s[open+1 : close])
	inner = strings.ReplaceAll(inner, ",", " ")
	inner = strings.ReplaceAll(inner, "/", " ")
	fields := strings.Fields(inner)
	if len(fields) != 3 && len(fields) != 4 {
		return false
	}
	if !isHSLAngle(fields[0]) {
		return false
	}
	if !isPercent(fields[1]) || !isPercent(fields[2]) {
		return false
	}
	if len(fields) == 4 && !isNumericOrPercent(fields[3]) {
		return false
	}
	return true
}

// isNumericOrPercent reports whether s parses as a plain number or as
// a percentage (a number suffixed by "%").
func isNumericOrPercent(s string) bool {
	if strings.HasSuffix(s, "%") {
		_, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		return err == nil
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// isPercent reports whether s parses as a CSS percentage value.
func isPercent(s string) bool {
	if !strings.HasSuffix(s, "%") {
		return false
	}
	_, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	return err == nil
}

// isHSLAngle reports whether s parses as a CSS angle (unitless or one
// of deg/grad/rad/turn).
func isHSLAngle(s string) bool {
	for _, suffix := range []string{"deg", "grad", "rad", "turn"} {
		if strings.HasSuffix(s, suffix) {
			_, err := strconv.ParseFloat(strings.TrimSuffix(s, suffix), 64)
			return err == nil
		}
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// isValidCSSLength reports whether s is a CSS length we are willing to
// emit verbatim. Accepted forms:
//
//   - "0" (the only unitless length)
//   - "<number><unit>" where unit ∈ validUnits
//   - "var(--…)" token references
//   - "calc(…)" expressions (passed through unchecked — the renderer
//     defers to the browser's calc parser)
//   - "clamp(…)" expressions (same rationale; used for fluid sizes)
func isValidCSSLength(s string) bool {
	if s == "" {
		return false
	}
	if s == "0" {
		return true
	}
	if strings.HasPrefix(s, "var(") && strings.HasSuffix(s, ")") {
		return true
	}
	if strings.HasPrefix(s, "calc(") && strings.HasSuffix(s, ")") {
		return true
	}
	if strings.HasPrefix(s, "clamp(") && strings.HasSuffix(s, ")") {
		return true
	}
	if !cssLengthPattern.MatchString(s) {
		return false
	}
	unit := extractUnit(s)
	_, ok := validUnits[unit]
	return ok
}

// extractUnit returns the trailing unit token of a CSS length, e.g.
// "12px" -> "px", "1.5rem" -> "rem". Returns "" if no alphabetic
// suffix is found.
func extractUnit(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' {
			return s[i+1:]
		}
	}
	return s
}

// pathf builds a JSON-pointer-style path with format-string ergonomics.
// We use it everywhere paths are constructed so the emitted paths stay
// consistent ("/settings/color/palette/0/color").
func pathf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// Validate returns every structural error in the manifest. Errors are
// emitted in document order (top-level first, then nested), and never
// short-circuit — a manifest with three bad slugs reports three errors
// in one call.
//
// Empty result == valid.
func (t *ThemeJSON) Validate() []ValidationError {
	var errs []ValidationError

	// Version is the gate. v1 is the only currently accepted version;
	// see docs/03-theme-system.md §3.3.
	if t.Version != CurrentVersion {
		errs = append(errs, ValidationError{
			Path: "/version",
			Message: fmt.Sprintf("must be %d (got %d)",
				CurrentVersion, t.Version),
		})
	}

	errs = append(errs, validateSettings(&t.Settings, "/settings")...)
	errs = append(errs, validateCustomTemplates(t.CustomTemplates, "/customTemplates")...)
	errs = append(errs, validateTemplateParts(t.TemplateParts, "/templateParts")...)

	return errs
}

// validateSettings walks the settings sub-tree and emits one error per
// violation. The path prefix lets the same function be reused for
// child themes that scope settings.
func validateSettings(s *Settings, prefix string) []ValidationError {
	var errs []ValidationError
	errs = append(errs, validateColorSettings(&s.Color, prefix+"/color")...)
	errs = append(errs, validateTypography(&s.Typography, prefix+"/typography")...)
	errs = append(errs, validateSpacing(&s.Spacing, prefix+"/spacing")...)
	errs = append(errs, validateLayout(&s.Layout, prefix+"/layout")...)
	errs = append(errs, validateShadow(&s.Shadow, prefix+"/shadow")...)
	return errs
}

func validateColorSettings(c *ColorSettings, prefix string) []ValidationError {
	var errs []ValidationError
	for i, entry := range c.Palette {
		p := pathf("%s/palette/%d", prefix, i)
		if !isValidSlug(entry.Slug) {
			errs = append(errs, ValidationError{
				Path:    p + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(entry.Slug) + ")",
			})
		}
		if entry.Name == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/name",
				Message: "required",
			})
		}
		if !isValidCSSColor(entry.Color) {
			errs = append(errs, ValidationError{
				Path:    p + "/color",
				Message: "invalid CSS color " + strconv.Quote(entry.Color),
			})
		}
	}
	for i, entry := range c.Gradients {
		p := pathf("%s/gradients/%d", prefix, i)
		if !isValidSlug(entry.Slug) {
			errs = append(errs, ValidationError{
				Path:    p + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(entry.Slug) + ")",
			})
		}
		if entry.Gradient == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/gradient",
				Message: "required",
			})
		}
	}
	for i, entry := range c.Duotone {
		p := pathf("%s/duotone/%d", prefix, i)
		if !isValidSlug(entry.Slug) {
			errs = append(errs, ValidationError{
				Path:    p + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(entry.Slug) + ")",
			})
		}
		if len(entry.Colors) < 2 {
			errs = append(errs, ValidationError{
				Path:    p + "/colors",
				Message: "duotone requires at least two colors",
			})
		}
		for ci, color := range entry.Colors {
			if !isValidCSSColor(color) {
				errs = append(errs, ValidationError{
					Path:    pathf("%s/colors/%d", p, ci),
					Message: "invalid CSS color " + strconv.Quote(color),
				})
			}
		}
	}
	return errs
}

func validateTypography(t *TypographySet, prefix string) []ValidationError {
	var errs []ValidationError
	for i, fam := range t.FontFamilies {
		p := pathf("%s/fontFamilies/%d", prefix, i)
		if !isValidSlug(fam.Slug) {
			errs = append(errs, ValidationError{
				Path:    p + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(fam.Slug) + ")",
			})
		}
		if fam.Name == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/name",
				Message: "required",
			})
		}
		if fam.FontFamily == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/fontFamily",
				Message: "required",
			})
		}
		for fi, face := range fam.FontFace {
			fp := pathf("%s/fontFace/%d", p, fi)
			if face.Src == "" {
				errs = append(errs, ValidationError{
					Path:    fp + "/src",
					Message: "required",
				})
			}
		}
	}
	for i, sz := range t.FontSizes {
		p := pathf("%s/fontSizes/%d", prefix, i)
		if !isValidSlug(sz.Slug) {
			errs = append(errs, ValidationError{
				Path:    p + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(sz.Slug) + ")",
			})
		}
		if sz.Name == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/name",
				Message: "required",
			})
		}
		if !isValidCSSLength(sz.Size) {
			errs = append(errs, ValidationError{
				Path:    p + "/size",
				Message: "invalid CSS length " + strconv.Quote(sz.Size),
			})
		}
		if sz.Fluid != nil {
			if !isValidCSSLength(sz.Fluid.Min) {
				errs = append(errs, ValidationError{
					Path:    p + "/fluid/min",
					Message: "invalid CSS length " + strconv.Quote(sz.Fluid.Min),
				})
			}
			if !isValidCSSLength(sz.Fluid.Max) {
				errs = append(errs, ValidationError{
					Path:    p + "/fluid/max",
					Message: "invalid CSS length " + strconv.Quote(sz.Fluid.Max),
				})
			}
		}
	}
	return errs
}

func validateSpacing(s *SpacingSettings, prefix string) []ValidationError {
	var errs []ValidationError
	if !s.SpacingScale.IsZero() {
		sp := prefix + "/spacingScale"
		if _, ok := validSpacingOperators[s.SpacingScale.Operator]; !ok {
			errs = append(errs, ValidationError{
				Path:    sp + "/operator",
				Message: `must be "+" or "*" (got ` + strconv.Quote(s.SpacingScale.Operator) + ")",
			})
		}
		if s.SpacingScale.Increment <= 0 {
			errs = append(errs, ValidationError{
				Path:    sp + "/increment",
				Message: "must be > 0",
			})
		}
		if s.SpacingScale.Steps <= 0 {
			errs = append(errs, ValidationError{
				Path:    sp + "/steps",
				Message: "must be > 0",
			})
		}
		if s.SpacingScale.MediumStep <= 0 {
			errs = append(errs, ValidationError{
				Path:    sp + "/mediumStep",
				Message: "must be > 0",
			})
		}
		if _, ok := validSpacingUnits[s.SpacingScale.Unit]; !ok {
			errs = append(errs, ValidationError{
				Path:    sp + "/unit",
				Message: "must be one of px, rem, em, %, vw, vh (got " + strconv.Quote(s.SpacingScale.Unit) + ")",
			})
		}
	}
	for i, u := range s.Units {
		if _, ok := validUnits[u]; !ok {
			errs = append(errs, ValidationError{
				Path:    pathf("%s/units/%d", prefix, i),
				Message: "unrecognised CSS unit " + strconv.Quote(u),
			})
		}
	}
	return errs
}

func validateLayout(l *LayoutSettings, prefix string) []ValidationError {
	var errs []ValidationError
	if l.ContentSize != "" && !isValidCSSLength(l.ContentSize) {
		errs = append(errs, ValidationError{
			Path:    prefix + "/contentSize",
			Message: "invalid CSS length " + strconv.Quote(l.ContentSize),
		})
	}
	if l.WideSize != "" && !isValidCSSLength(l.WideSize) {
		errs = append(errs, ValidationError{
			Path:    prefix + "/wideSize",
			Message: "invalid CSS length " + strconv.Quote(l.WideSize),
		})
	}
	return errs
}

func validateShadow(s *ShadowSettings, prefix string) []ValidationError {
	var errs []ValidationError
	for i, p := range s.Presets {
		pp := pathf("%s/presets/%d", prefix, i)
		if !isValidSlug(p.Slug) {
			errs = append(errs, ValidationError{
				Path:    pp + "/slug",
				Message: "slug must be lowercase kebab-case (got " + strconv.Quote(p.Slug) + ")",
			})
		}
		if p.Shadow == "" {
			errs = append(errs, ValidationError{
				Path:    pp + "/shadow",
				Message: "required",
			})
		}
	}
	return errs
}

func validateCustomTemplates(ts []TemplateDef, prefix string) []ValidationError {
	var errs []ValidationError
	for i, t := range ts {
		p := pathf("%s/%d", prefix, i)
		if !isValidSlug(t.Name) {
			errs = append(errs, ValidationError{
				Path:    p + "/name",
				Message: "name must be lowercase kebab-case (got " + strconv.Quote(t.Name) + ")",
			})
		}
		// title is required when area is set — the editor surfaces
		// the title beside the area when assigning a template.
		if t.Area != "" && t.Title == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/title",
				Message: "required when area is set",
			})
		}
		if t.Area != "" {
			if _, ok := validAreas[t.Area]; !ok {
				errs = append(errs, ValidationError{
					Path:    p + "/area",
					Message: "must be one of header, footer, sidebar, uncategorized (got " + strconv.Quote(t.Area) + ")",
				})
			}
		}
	}
	return errs
}

func validateTemplateParts(parts []TemplatePartDef, prefix string) []ValidationError {
	var errs []ValidationError
	for i, tp := range parts {
		p := pathf("%s/%d", prefix, i)
		if !isValidSlug(tp.Name) {
			errs = append(errs, ValidationError{
				Path:    p + "/name",
				Message: "name must be lowercase kebab-case (got " + strconv.Quote(tp.Name) + ")",
			})
		}
		if tp.Area == "" {
			errs = append(errs, ValidationError{
				Path:    p + "/area",
				Message: "required",
			})
		} else if _, ok := validAreas[tp.Area]; !ok {
			errs = append(errs, ValidationError{
				Path:    p + "/area",
				Message: "must be one of header, footer, sidebar, uncategorized (got " + strconv.Quote(tp.Area) + ")",
			})
		}
	}
	return errs
}
