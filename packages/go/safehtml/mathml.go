package safehtml

// mathmlAllowedElements is the closed list of MathML element local
// names. MathML's surface is much smaller than SVG's — most of the
// tags are presentation primitives.
var mathmlAllowedElements = map[string]bool{
	"math":          true,
	"mi":            true,
	"mn":            true,
	"mo":            true,
	"ms":            true,
	"mtext":         true,
	"mspace":        true,
	"mrow":          true,
	"mfrac":         true,
	"msqrt":         true,
	"mroot":         true,
	"mstyle":        true,
	"merror":        true,
	"mpadded":       true,
	"mphantom":      true,
	"mfenced":       true,
	"menclose":      true,
	"msub":          true,
	"msup":          true,
	"msubsup":       true,
	"munder":        true,
	"mover":         true,
	"munderover":    true,
	"mmultiscripts": true,
	"mtable":        true,
	"mtr":           true,
	"mtd":           true,
	"mlabeledtr":    true,
	"maligngroup":   true,
	"malignmark":    true,
	"mglyph":        true,
	"semantics":     true,
	"annotation":    true,
}

// mathmlAllowedAttributes is the closed list of MathML attribute
// names. We're permissive on presentation attributes (lspace, rspace,
// mathcolor, etc.) but the URL attributes go through the same
// sanitizeURLAttribute filter as SVG.
var mathmlAllowedAttributes = map[string]bool{
	"id":             true,
	"class":          true,
	"style":          true,
	"display":        true,
	"mathvariant":    true,
	"mathsize":       true,
	"mathcolor":      true,
	"mathbackground": true,
	"href":           true,
	"linethickness":  true,
	"lspace":         true,
	"rspace":         true,
	"align":          true,
	"columnalign":    true,
	"rowalign":       true,
	"frame":          true,
	"width":          true,
	"height":         true,
	"depth":          true,
	"open":           true,
	"close":          true,
	"separators":     true,
	"notation":       true,
	"encoding":       true,
	"xmlns":          true,
	"accent":         true,
	"accentunder":    true,
	"largeop":        true,
	"movablelimits":  true,
	"stretchy":       true,
	"symmetric":      true,
	"voffset":        true,
	"dir":            true,
	"selection":      true,
	"actiontype":     true,
}

// SanitizeMathML returns a clean MathML fragment derived from raw.
// Drops every element/attribute outside the allowlist, plus any
// javascript:/data: URL on the href attribute.
//
// Behavior mirrors SanitizeSVG; see that function's godoc.
func SanitizeMathML(raw string) (string, error) {
	return sanitizeWithAllowlists(raw, mathmlAllowedElements, mathmlAllowedAttributes, sanitizeURLAttribute)
}
