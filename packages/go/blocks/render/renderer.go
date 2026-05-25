package render

import (
	"fmt"
	"html/template"
	"strings"
)

// attrString reads an attribute as a string, returning fallback when
// the key is missing or holds a non-string value.
//
// The renderer never panics on malformed attribute shapes — the
// validator package is what enforces correctness at save time; a
// renderer reaching for the value at render time uses this helper to
// degrade gracefully when validation hasn't run (e.g. a plugin
// installed a block whose schema doesn't agree with its renderer).
func attrString(attrs map[string]any, key string, fallback string) string {
	if v, ok := attrs[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

// attrInt reads an attribute as an int, returning fallback when the
// key is missing or the value can't be coerced. JSON-decoded numbers
// arrive as float64; we accept that too so a renderer doesn't need
// to know whether the caller used json.Number.
func attrInt(attrs map[string]any, key string, fallback int) int {
	v, ok := attrs[key]
	if !ok {
		return fallback
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	}
	return fallback
}

// attrBool reads an attribute as a bool, returning fallback when
// missing or non-bool.
func attrBool(attrs map[string]any, key string, fallback bool) bool {
	if v, ok := attrs[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}

// classAttr builds the ` class="..."` attribute fragment for a list
// of class names, returning the empty string when the list is empty
// or every entry is blank. Mirrors the TS classAttr helper in
// packages/ts/blocks-core/src/internal/escape.ts so the editor preview
// (`save()` output) and the Go-server output line up byte-for-byte
// for the static cases.
func classAttr(classes []string) string {
	out := classes[:0:0]
	for _, c := range classes {
		if strings.TrimSpace(c) == "" {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return ""
	}
	return fmt.Sprintf(` class=%q`, strings.Join(out, " "))
}

// idAttr returns ` id="..."` when id is non-empty, escaped via
// template.HTMLEscapeString so a user-controlled anchor can't break
// out of the attribute. Empty id returns "".
func idAttr(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return fmt.Sprintf(` id=%q`, template.HTMLEscapeString(id))
}

// hrefAttr returns ` href="..."` when href is non-empty, applying
// html/template's URL filter so a javascript: scheme can't be
// smuggled in. Empty href returns "" so callers can compose without
// branching.
func hrefAttr(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	// template.URLQueryEscaper is too aggressive; we use the
	// HTMLEscapeString + scheme allowlist approach used elsewhere
	// in the codebase (see packages/go/redirects/middleware.go).
	return fmt.Sprintf(` href=%q`, template.HTMLEscapeString(href))
}

// escapeText is the inline-text escape rule. Mirrors the TS
// escapeHtml helper in packages/ts/blocks-core/src/internal/escape.ts
// so static-string blocks line up byte-for-byte across the two
// renderers. The replacement order matters — `&` MUST be first to
// avoid double-encoding the entities introduced by subsequent steps.
func escapeText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}
