package lifecycle

import (
	"errors"
	"strings"
	"testing"
)

// TestLintMigration_HappyPath exercises every shape we explicitly
// support: CREATE TABLE (with various decorators), ALTER TABLE,
// CREATE INDEX with quoted/schema-qualified identifiers, and a
// multi-statement migration. None should trip the linter when every
// referenced object carries the `plugin_<slug>_` prefix.
func TestLintMigration_HappyPath(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{
			"create-table-simple",
			`CREATE TABLE plugin_seo_keywords (id uuid PRIMARY KEY);`,
		},
		{
			"create-table-if-not-exists",
			`CREATE TABLE IF NOT EXISTS plugin_seo_keywords (id uuid);`,
		},
		{
			"create-table-schema-qualified",
			`CREATE TABLE public.plugin_seo_keywords (id uuid);`,
		},
		{
			"create-table-quoted-identifier",
			`CREATE TABLE "plugin_seo_keywords" (id uuid);`,
		},
		{
			"alter-table",
			`ALTER TABLE plugin_seo_keywords ADD COLUMN score int;`,
		},
		{
			"create-index",
			`CREATE INDEX plugin_seo_idx_score ON plugin_seo_keywords (score);`,
		},
		{
			"create-unique-index-concurrently",
			`CREATE UNIQUE INDEX CONCURRENTLY plugin_seo_idx_slug ON plugin_seo_keywords (slug);`,
		},
		{
			"multi-statement",
			`CREATE TABLE plugin_seo_keywords (id uuid);
ALTER TABLE plugin_seo_keywords ADD COLUMN score int;
CREATE INDEX plugin_seo_idx_score ON plugin_seo_keywords (score);`,
		},
		{
			"with-comments",
			`-- DROP TABLE users; this comment should be stripped.
/* Block comment with CREATE TABLE users (); should also be stripped. */
CREATE TABLE plugin_seo_keywords (id uuid);`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := LintMigration("seo", tc.sql); err != nil {
				t.Fatalf("expected no error; got %v", err)
			}
		})
	}
}

// TestLintMigration_RejectsNonPluginPrefix is the negative path: any
// statement referencing an object outside `plugin_<slug>_` must
// reject with ErrLintViolation and an error message that names the
// offending identifier (so plugin authors can fix their migration).
func TestLintMigration_RejectsNonPluginPrefix(t *testing.T) {
	cases := []struct {
		name        string
		sql         string
		wantIDFrag  string
	}{
		{
			"create-table-core-name",
			`CREATE TABLE users (id uuid);`,
			"users",
		},
		{
			"create-table-other-plugin-prefix",
			`CREATE TABLE plugin_other_table (id uuid);`,
			"plugin_other_table",
		},
		{
			"alter-table-core-name",
			`ALTER TABLE posts ADD COLUMN x int;`,
			"posts",
		},
		{
			"create-index-on-core-table",
			`CREATE INDEX plugin_seo_idx ON users (id);`,
			"users",
		},
		{
			"create-index-with-non-plugin-name",
			`CREATE INDEX idx_score ON plugin_seo_keywords (score);`,
			"idx_score",
		},
		{
			"drop-core-table",
			`DROP TABLE users;`,
			"users",
		},
		{
			"create-table-quoted-core",
			`CREATE TABLE "users" (id uuid);`,
			"users",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := LintMigration("seo", tc.sql)
			if err == nil {
				t.Fatalf("expected ErrLintViolation; got nil")
			}
			if !errors.Is(err, ErrLintViolation) {
				t.Fatalf("expected ErrLintViolation; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIDFrag) {
				t.Fatalf("error message should mention %q; got %v", tc.wantIDFrag, err)
			}
		})
	}
}

// TestLintMigration_RejectsForbiddenVerbs verifies the forbidden-verb
// kill-list. These statements have no plugin-scoped target by
// construction (CREATE EXTENSION targets the database; GRANT targets
// a role) so the linter refuses them outright. The kill-list is the
// conservative posture: a future ADR can carve out specific verbs
// behind capability flags if a real plugin needs them.
func TestLintMigration_RejectsForbiddenVerbs(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"create-function", `CREATE FUNCTION evil() RETURNS void AS $$ BEGIN END; $$ LANGUAGE plpgsql;`},
		{"create-extension", `CREATE EXTENSION pg_trgm;`},
		{"create-view", `CREATE VIEW vuln AS SELECT * FROM users;`},
		{"create-materialized-view", `CREATE MATERIALIZED VIEW mv AS SELECT 1;`},
		{"create-trigger", `CREATE TRIGGER t BEFORE INSERT ON plugin_seo_x EXECUTE FUNCTION f();`},
		{"grant", `GRANT SELECT ON users TO public;`},
		{"revoke", `REVOKE SELECT ON users FROM public;`},
		{"truncate", `TRUNCATE plugin_seo_keywords;`},
		{"alter-schema", `ALTER SCHEMA public OWNER TO postgres;`},
		{"drop-schema", `DROP SCHEMA public CASCADE;`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := LintMigration("seo", tc.sql)
			if err == nil {
				t.Fatalf("expected ErrLintViolation; got nil")
			}
			if !errors.Is(err, ErrLintViolation) {
				t.Fatalf("expected ErrLintViolation; got %v", err)
			}
		})
	}
}

// TestLintMigration_RejectsEmptySlug guards against the only
// pathological seed-input the linter can't validate: an empty slug.
// With prefix == "plugin__", any identifier starting with "plugin_"
// would pass — including a hostile `plugin_other_users`. We reject
// outright instead of fail-open.
func TestLintMigration_RejectsEmptySlug(t *testing.T) {
	if err := LintMigration("", "CREATE TABLE plugin_x_y (id uuid);"); err == nil {
		t.Fatal("expected error for empty slug; got nil")
	}
}

// TestLintMigration_PrefixIsSlugScoped confirms that a migration
// targeting *another* plugin's prefix is rejected — `plugin_other_x`
// is not in `plugin_seo_*`, so the seo plugin's migration can't
// touch it even though both names start with `plugin_`.
func TestLintMigration_PrefixIsSlugScoped(t *testing.T) {
	err := LintMigration("seo", `CREATE TABLE plugin_other_keywords (id uuid);`)
	if !errors.Is(err, ErrLintViolation) {
		t.Fatalf("expected ErrLintViolation for cross-plugin write; got %v", err)
	}
}

// TestLintMigration_EmptySQL is a sanity check: an empty migration is
// vacuously fine. Plugins ship empty migrations during scaffolding;
// failing those would be a worse UX than allowing the no-op through.
func TestLintMigration_EmptySQL(t *testing.T) {
	if err := LintMigration("seo", ""); err != nil {
		t.Fatalf("empty SQL should be accepted; got %v", err)
	}
	if err := LintMigration("seo", "-- only a comment\n"); err != nil {
		t.Fatalf("comment-only SQL should be accepted; got %v", err)
	}
}
