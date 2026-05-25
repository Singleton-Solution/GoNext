package i18n

import (
	"strings"
	"sync"
)

// Translator looks up a localised string for a (key, locale) pair.
//
// Implementations MUST be safe for concurrent use. The plugin host
// calls Translate from the gn_i18n_translate host function, which fires
// on every plugin-issued translate() — concurrent calls from many
// goroutines are the norm, not the exception.
//
// The contract on a missing entry is "return the key unchanged": this
// matches gettext convention and gives the UI a visually obvious
// fallback ("post.author" rendered in English on a French locale)
// instead of a blank string or a panic.
type Translator interface {
	// Translate returns the localised string for key at the given
	// BCP-47 locale tag (e.g. "fr-FR", "en"). On a missing entry —
	// whether the key, the locale, or both are unknown — it returns
	// key verbatim. Empty key returns the empty string.
	Translate(key, locale string) string
}

// NoopTranslator is the zero-configuration Translator: every Translate
// call returns the key, no matter the locale. It is the package-default
// backend before any catalogue has been installed.
//
// Useful as a typed sentinel — wiring code can pass NoopTranslator{}
// when nothing better is available without nil-checking.
type NoopTranslator struct{}

// Translate implements Translator. Returns key unchanged.
func (NoopTranslator) Translate(key, _ string) string { return key }

// MapTranslator is a tiny in-memory catalogue used by tests and by the
// stub backend ahead of the real PO/MO loader. The catalogue is keyed
// by locale -> message key -> translated string; lookups fall back to
// returning the key when either dimension misses.
//
// Locale matching is exact-then-language-prefix: a lookup for "fr-FR"
// that misses the full tag retries against the bare language code
// "fr" before falling back to the key. This is the same fallback
// chain BCP-47 consumers expect from gettext.
//
// MapTranslator is safe for concurrent use. Mutations (Set) take a
// write lock; Translate takes only a read lock.
type MapTranslator struct {
	mu       sync.RWMutex
	catalogs map[string]map[string]string // locale -> key -> translation
}

// NewMapTranslator returns a Translator backed by the supplied
// catalogue. Pass nil for an empty catalogue you populate via Set.
//
// The supplied map is copied — callers can mutate their original
// without disturbing the Translator.
func NewMapTranslator(catalogs map[string]map[string]string) *MapTranslator {
	t := &MapTranslator{catalogs: make(map[string]map[string]string, len(catalogs))}
	for loc, entries := range catalogs {
		copyEntries := make(map[string]string, len(entries))
		for k, v := range entries {
			copyEntries[k] = v
		}
		t.catalogs[loc] = copyEntries
	}
	return t
}

// Set installs (or overwrites) a single (locale, key) entry. Useful in
// tests; the production loader populates the catalogue in bulk via a
// dedicated Load function in a follow-up.
func (t *MapTranslator) Set(locale, key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.catalogs == nil {
		t.catalogs = make(map[string]map[string]string)
	}
	entries, ok := t.catalogs[locale]
	if !ok {
		entries = make(map[string]string)
		t.catalogs[locale] = entries
	}
	entries[key] = value
}

// Translate implements Translator with locale fallback.
func (t *MapTranslator) Translate(key, locale string) string {
	if key == "" {
		return ""
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if entries, ok := t.catalogs[locale]; ok {
		if v, ok := entries[key]; ok {
			return v
		}
	}
	// Fall back to the bare language code (fr-FR -> fr).
	if idx := strings.IndexByte(locale, '-'); idx > 0 {
		lang := locale[:idx]
		if entries, ok := t.catalogs[lang]; ok {
			if v, ok := entries[key]; ok {
				return v
			}
		}
	}
	return key
}
