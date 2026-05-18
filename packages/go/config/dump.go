package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// DumpRedactedMaskFn is the function used to mask a redacted string value.
// Operators see "***REDACTED*** (len=N, sha256[:8]=xxxxxxxx)" so they can
// verify, from the deploy artifact's expected-secret hash, that the right
// secret loaded — without seeing the plaintext. The empty string still
// produces a stable "(len=0, sha256[:8]=e3b0c442)" line; that's intentional,
// so operators see "I deployed without a secret set" rather than a blank
// line they might mistake for "fine".
func redactedMask(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("***REDACTED*** (len=%d, sha256[:8]=%s)", len(value), hex.EncodeToString(sum[:])[:8])
}

// nameRedactPattern is the fallback rule: any field whose name matches this
// regex is redacted, even without a `redact:"true"` struct tag. The tag is
// the canonical signal — this exists so a freshly added secret field ships
// with redaction-by-default even if the contributor forgot the tag. New
// keywords go at the end so the bytecode for the existing alternation is
// unchanged (avoids cache thrash on hot init paths).
var nameRedactPattern = regexp.MustCompile(`(?i)(password|secret|token|key|pepper|dsn)`)

// dumpEntry is a single rendered line, kept separate from io so the
// produced lines can be sorted deterministically before flushing.
type dumpEntry struct {
	key   string
	value string
}

// Dump walks cfg via reflection and writes each leaf field to w as
// "KEY=value\n", sorted by KEY. Sub-structs are flattened with dotted
// names ("Database.URL"). Slices, maps, and times are stringified via
// fmt.Sprint; secrets are masked per the rules below.
//
// Redaction rules (canonical first, fallback second):
//
//  1. If the field's struct tag contains `redact:"true"`, its value is
//     masked via redactedMask regardless of name.
//  2. Otherwise, if the field name matches nameRedactPattern
//     (password|secret|token|key|pepper|dsn, case-insensitive), the value
//     is masked. This is a defense-in-depth fallback — the tag is what
//     gets reviewed in PRs; the regex catches anything that slipped past.
//
// Output is deterministic: lines are sorted by KEY so a diff between two
// dumps highlights only the keys that actually changed, not a reflection-
// walk ordering artifact.
//
// Dump is safe to call with secrets present; that's the whole point. It
// is not safe to call with arbitrary user input — only on Config or types
// shaped like it (no cycles, no unexported pointer fields).
func Dump(cfg Config, w io.Writer) error {
	entries := make([]dumpEntry, 0, 64)
	walk(reflect.ValueOf(cfg), reflect.TypeOf(cfg), "", &entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for _, e := range entries {
		if _, err := fmt.Fprintf(w, "%s=%s\n", e.key, e.value); err != nil {
			return err
		}
	}
	return nil
}

// walk recursively visits each exported field of v (a struct) and appends
// "leaf" entries to out. prefix is the dotted-name accumulator.
//
// We do NOT walk into types from the standard library (time.Time, time.Duration)
// — those are rendered with their String method. Anything else that's a
// struct is recursed into; that's how Database, Redis, Storage, Auth, etc.
// get expanded without per-field plumbing.
func walk(v reflect.Value, t reflect.Type, prefix string, out *[]dumpEntry) {
	// Defensive: only struct kinds enter walk. Anything else means a caller
	// passed something unusual; render as a single leaf so the operator at
	// least sees the value rather than a silent drop.
	if t.Kind() != reflect.Struct {
		appendLeaf(out, prefix, false, v)
		return
	}

	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		fv := v.Field(i)
		name := ft.Name
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}

		redactTag := ft.Tag.Get("redact") == "true"

		// Time.Duration is a typed int64 — reflect reports it as Int kind.
		// Stringify it via its method so "30s" beats "30000000000".
		if fv.Type().String() == "time.Duration" {
			appendLeaf(out, key, redactTag || matchesNameRule(name), fv)
			continue
		}

		switch fv.Kind() {
		case reflect.Struct:
			// Render time.Time as a leaf (its String method is human-readable);
			// recurse into every other struct.
			if fv.Type().String() == "time.Time" {
				appendLeaf(out, key, redactTag || matchesNameRule(name), fv)
				continue
			}
			walk(fv, fv.Type(), key, out)
		default:
			appendLeaf(out, key, redactTag || matchesNameRule(name), fv)
		}
	}
}

// matchesNameRule applies the case-insensitive substring fallback. We do
// NOT match against the dotted path — only the bare field name — so
// "Auth.Pepper" matches via "Pepper" but "DatabaseConfig" (a hypothetical
// non-secret field literally named that) wouldn't false-match on "base".
// (The current schema doesn't have that collision, but the rule documents
// the principle.)
func matchesNameRule(fieldName string) bool {
	return nameRedactPattern.MatchString(fieldName)
}

// appendLeaf renders v as a string and appends a (key, value) entry.
// If redact is true, the value is masked via redactedMask — we mask the
// string form, so a redacted []string still shows length information via
// the formatted representation's length, not the underlying slice's. This
// is intentional: operators verifying "did the right SMTP password load"
// care about the secret's bytes, not the slice-cap byte count.
func appendLeaf(out *[]dumpEntry, key string, redact bool, v reflect.Value) {
	s := renderValue(v)
	if redact {
		s = redactedMask(s)
	}
	*out = append(*out, dumpEntry{key: key, value: s})
}

// renderValue produces the string form of a reflected value, choosing
// representations that round-trip cleanly through diff tools. For example,
// a nil slice renders as "[]" rather than "<nil>", so a configured empty
// list and an unset list look the same in the dump — which they should,
// because they behave the same at runtime.
func renderValue(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return "[]"
		}
		parts := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			parts[i] = renderValue(v.Index(i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return ""
		}
		return renderValue(v.Elem())
	default:
		// Covers ints, bools, time.Duration (via its String method picked
		// up by fmt), and Env (string alias). fmt.Sprint reaches the right
		// representation for all the types currently in Config.
		return fmt.Sprint(v.Interface())
	}
}
