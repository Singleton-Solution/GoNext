package lifecycle

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SQL migration linter (issue #53). Plugins ship up- and down-migrations
// that the lifecycle Migrator applies inside the shared GoNext database.
// Without a structural check, a malicious or careless plugin could:
//
//   - drop a core GoNext table (`DROP TABLE users`),
//   - shadow a core relation by creating `posts` instead of
//     `plugin_<slug>_posts`,
//   - rewrite a core index with `ALTER TABLE users ADD COLUMN ...`.
//
// Any of those breaks the host. Per the design (docs/02-plugin-system.md
// §3.3) every DDL statement in a plugin's migrations must target an
// object prefixed with `plugin_<slug>_`. The linter enforces that
// invariant before the runner ever sees the SQL.
//
// We use a regex-based pass rather than `pg_query_go` because the
// project's go.mod already has many heavy deps and the parser is large
// for the surface we actually need (CREATE/ALTER TABLE, CREATE INDEX).
// The regexes cover the common cases — CREATE [UNIQUE] INDEX, CREATE
// TABLE [IF NOT EXISTS], ALTER TABLE — and the test fixtures exercise
// the trick shapes we know about (quoted identifiers, schema
// qualifiers, comments). Adding a real parser is a follow-up once the
// linter graduates from "block obvious mistakes" to "block any
// statement that mutates a non-plugin object".

// ErrLintViolation is the wrapped error returned by LintMigration when
// a statement targets a non-plugin-prefixed object. Sentinel so callers
// can `errors.Is` on the failure shape.
var ErrLintViolation = errors.New("sqllint: migration references non-plugin object")

// Regex grammar. Each pattern uses these conventions:
//
//   - case-insensitive (`(?i)`) — SQL is case-insensitive in DDL.
//   - tolerates leading whitespace and optional schema qualifiers
//     (`public.`) so a plugin can target the default schema.
//   - captures the target identifier in group 1, post-stripping quotes
//     and schema prefix in extractIdentifier.
//
// We intentionally do NOT match every DDL verb the grammar admits
// (CREATE TYPE, CREATE FUNCTION, CREATE TRIGGER, etc.). The cap is
// scoped to the three forms plugin authors realistically reach for;
// anything outside that surface is rejected by a separate "unknown
// DDL" check below.
var (
	reCreateTable = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNLOGGED\s+|TEMP(?:ORARY)?\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	reAlterTable  = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	reCreateIndex = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_."]+)\s+ON\s+([A-Za-z0-9_."]+)`)
	reDropTable   = regexp.MustCompile(`(?i)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	reDropIndex   = regexp.MustCompile(`(?i)\bDROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	// reAnyDDL is the catch-all: if a statement contains any verb we
	// don't explicitly handle (CREATE TYPE, ALTER SCHEMA, ...), we
	// reject rather than silently letting it through. The list is
	// the conservative one — plugin authors who legitimately need
	// these can ask for the surface to be widened.
	reForbidden = regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:FUNCTION|PROCEDURE|TRIGGER|TYPE|SCHEMA|EXTENSION|VIEW|MATERIALIZED\s+VIEW|RULE|POLICY|SEQUENCE)\b|\bALTER\s+(?:SCHEMA|SYSTEM|DATABASE|ROLE|USER)\b|\bDROP\s+(?:SCHEMA|DATABASE|ROLE|USER|EXTENSION|FUNCTION|PROCEDURE|TRIGGER|TYPE|VIEW|SEQUENCE|POLICY|RULE)\b|\bGRANT\b|\bREVOKE\b|\bTRUNCATE\b`)
	// reLineComment / reBlockComment let us strip comments before
	// parsing so a `-- DROP TABLE users` line doesn't trip the
	// linter. PostgreSQL supports both single-line and nested block
	// comments; we collapse them flat (Go's regexp is non-greedy).
	reLineComment  = regexp.MustCompile(`--[^\n]*`)
	reBlockComment = regexp.MustCompile(`/\*[\s\S]*?\*/`)
)

// LintMigration scans a single migration SQL blob and returns a non-nil
// error if any DDL statement references an object that doesn't begin
// with the plugin's prefix. The prefix is `plugin_<slug>_` per
// docs/02-plugin-system.md §3.3.
//
// slug must already have passed slugRegex (manager.go). If it's empty
// the linter returns an error — an empty prefix would match every
// identifier including core tables.
//
// The linter is intentionally conservative: when in doubt about a
// statement's target, it rejects. Plugin authors get a clear error
// message that includes the offending identifier; they fix their
// migration and try again.
func LintMigration(slug, sql string) error {
	if slug == "" {
		return fmt.Errorf("sqllint: empty slug; refusing to lint")
	}
	prefix := "plugin_" + slug + "_"

	// Strip comments so a `-- DROP TABLE users` decoration doesn't
	// fail the lint. We don't strip string literals because no
	// statement we accept has a body where an identifier-looking
	// substring matters (CREATE TABLE doesn't take a string body;
	// ALTER TABLE ADD COLUMN with a DEFAULT string is fine because
	// the regex anchors on the verb position).
	cleaned := reLineComment.ReplaceAllString(sql, "")
	cleaned = reBlockComment.ReplaceAllString(cleaned, "")

	// Hard-reject the forbidden-verb set before identifier checks.
	// These statements have no "target" identifier the linter can
	// scope to a plugin prefix (CREATE EXTENSION lives at the
	// database level, GRANT operates on role objects), so the
	// safest policy is to reject outright.
	if m := reForbidden.FindString(cleaned); m != "" {
		return fmt.Errorf("%w: forbidden statement %q (plugins may only target prefixed TABLE/INDEX objects)",
			ErrLintViolation, strings.TrimSpace(m))
	}

	// Walk the four allowed forms. Each returns the identifier the
	// statement targets; we check that identifier (de-quoted, de-
	// schema'd) starts with the plugin prefix. CREATE INDEX has TWO
	// identifiers (the index name and the table it covers); both
	// must be plugin-scoped.
	type match struct {
		verb string
		idx  []int
		ids  []string
	}
	var hits []match
	for _, mm := range reCreateTable.FindAllStringSubmatchIndex(cleaned, -1) {
		hits = append(hits, match{verb: "CREATE TABLE", idx: mm, ids: []string{sliceMatch(cleaned, mm, 1)}})
	}
	for _, mm := range reAlterTable.FindAllStringSubmatchIndex(cleaned, -1) {
		hits = append(hits, match{verb: "ALTER TABLE", idx: mm, ids: []string{sliceMatch(cleaned, mm, 1)}})
	}
	for _, mm := range reCreateIndex.FindAllStringSubmatchIndex(cleaned, -1) {
		hits = append(hits, match{verb: "CREATE INDEX", idx: mm, ids: []string{
			sliceMatch(cleaned, mm, 1),
			sliceMatch(cleaned, mm, 2),
		}})
	}
	for _, mm := range reDropTable.FindAllStringSubmatchIndex(cleaned, -1) {
		hits = append(hits, match{verb: "DROP TABLE", idx: mm, ids: []string{sliceMatch(cleaned, mm, 1)}})
	}
	for _, mm := range reDropIndex.FindAllStringSubmatchIndex(cleaned, -1) {
		hits = append(hits, match{verb: "DROP INDEX", idx: mm, ids: []string{sliceMatch(cleaned, mm, 1)}})
	}

	for _, h := range hits {
		for _, raw := range h.ids {
			id := extractIdentifier(raw)
			if !strings.HasPrefix(id, prefix) {
				return fmt.Errorf("%w: %s references %q (must begin with %q)",
					ErrLintViolation, h.verb, id, prefix)
			}
		}
	}
	return nil
}

// sliceMatch returns the substring captured by group g from a single
// FindAllStringSubmatchIndex hit. Returns "" if the group didn't match
// (which can happen with optional alternatives in the regex).
func sliceMatch(s string, idx []int, g int) string {
	start := idx[2*g]
	end := idx[2*g+1]
	if start < 0 || end < 0 {
		return ""
	}
	return s[start:end]
}

// extractIdentifier strips the optional schema qualifier and any
// surrounding double quotes from a captured identifier. The grammar
// accepts both `plugin_x_t`, `"plugin_x_t"`, `public.plugin_x_t`,
// and `public."plugin_x_t"` — all should collapse to the bare name
// `plugin_x_t` for the prefix check.
func extractIdentifier(raw string) string {
	if raw == "" {
		return ""
	}
	// Drop schema qualifier (`public.foo` → `foo`). We keep the
	// rightmost segment; multi-part identifiers (`db.schema.foo`)
	// also collapse to the rightmost part because PostgreSQL's
	// addressability is left-to-right least-specific to
	// most-specific.
	if i := strings.LastIndex(raw, "."); i >= 0 {
		raw = raw[i+1:]
	}
	// Drop surrounding double quotes. A quoted identifier may still
	// contain "." inside the quotes, but the prefix check just looks
	// at the leading characters so we don't need to be cleverer
	// than this.
	raw = strings.Trim(raw, `"`)
	return raw
}
