// Package i18n is the minimal host-side translation surface that backs
// the plugin runtime's gn_i18n_translate ABI export.
//
// The package starts as a stub — there is no message catalogue, no PO
// loader, no plural rules. What it does provide is the Translator
// interface and a NoopTranslator default so the plugin host can wire
// gn_i18n_translate against a non-nil value from day one. The full
// catalogue layer (PO/MO loading, plural rules, locale fallbacks,
// admin-uploaded language packs) lands in a follow-up — every consumer
// that wires through Translator picks up the upgrade for free.
//
// # Why ship the stub now
//
// The plugin ABI freezes early. If gn_i18n_translate doesn't exist in
// v1, plugin SDKs cannot offer a translate() helper, and adding one
// later is a breaking guest-side change. Shipping the export with a
// noop backend gives plugin authors the function signature they need;
// when the real catalogue arrives, the same function does something
// useful without any plugin recompilation.
//
// # Lookup semantics
//
// Translator.Translate(key, locale) takes a dotted message key and a
// BCP-47 locale tag and returns either the localised string or, when
// no catalogue entry exists, the key itself. Returning the key as a
// last resort matches gettext convention: a missing translation is
// visually obvious in the UI without crashing the page.
//
// Implementations MUST be safe for concurrent use — translate is called
// from every plugin invocation that touches user-facing copy.
package i18n
