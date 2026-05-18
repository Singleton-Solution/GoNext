package importer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// ---------------------------------------------------------------------------
// Unit cover: option parsing + dryrun walk. These run without Docker.
// ---------------------------------------------------------------------------

func TestParseConflictPolicy(t *testing.T) {
	cases := []struct {
		in   string
		want ConflictPolicy
		err  bool
	}{
		{"", ConflictSkip, false},
		{"skip", ConflictSkip, false},
		{"update", ConflictUpdate, false},
		{"fail", ConflictFail, false},
		{"bogus", ConflictSkip, true},
	}
	for _, tc := range cases {
		got, err := ParseConflictPolicy(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("ParseConflictPolicy(%q) err=%v want err=%v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Errorf("ParseConflictPolicy(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestOptions_Resolved_Defaults(t *testing.T) {
	o := Options{}.resolved()
	if o.BatchSize != 100 {
		t.Errorf("BatchSize default: got %d want 100", o.BatchSize)
	}
	if o.PlaceholderPasswordHash == "" {
		t.Error("PlaceholderPasswordHash should default to a non-empty stub")
	}
}

func TestOptions_Resolved_Overrides(t *testing.T) {
	o := Options{BatchSize: 5, PlaceholderPasswordHash: "x"}.resolved()
	if o.BatchSize != 5 {
		t.Errorf("BatchSize: got %d want 5", o.BatchSize)
	}
	if o.PlaceholderPasswordHash != "x" {
		t.Errorf("PlaceholderPasswordHash: got %q want %q", o.PlaceholderPasswordHash, "x")
	}
}

// TestRun_Dryrun_Counts confirms a dry run never touches the DB
// (pool is nil) but still produces an accurate Report.
func TestRun_Dryrun_Counts(t *testing.T) {
	data, err := os.ReadFile(testdataPath(t, "minimal.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	imp := New(nil, Options{Dryrun: true, BatchSize: 10})
	r, err := imp.Run(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Authors != 1 {
		t.Errorf("Authors: got %d want 1", r.Authors)
	}
	if r.Categories != 1 {
		t.Errorf("Categories: got %d want 1", r.Categories)
	}
	if r.Tags != 1 {
		t.Errorf("Tags: got %d want 1", r.Tags)
	}
	if r.Posts != 1 {
		t.Errorf("Posts: got %d want 1", r.Posts)
	}
	if r.Comments != 1 {
		t.Errorf("Comments: got %d want 1", r.Comments)
	}
	if r.HasErrors() {
		t.Errorf("Errors: got %+v want none", r.Errors)
	}
}

// TestRun_NilReader rejects a nil reader without panicking.
func TestRun_NilReader(t *testing.T) {
	imp := New(nil, Options{Dryrun: true})
	if _, err := imp.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

// TestRun_NilPool_NonDryrun rejects a nil pool when Dryrun is off.
func TestRun_NilPool_NonDryrun(t *testing.T) {
	imp := New(nil, Options{})
	if _, err := imp.Run(context.Background(), strings.NewReader("<rss></rss>")); err == nil {
		t.Fatal("expected error for nil pool on non-dryrun")
	}
}

func TestSlugFromTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  Trim Me  ", "trim-me"},
		{"Already-Kebab", "already-kebab"},
		{"Mixed   Spacing", "mixed-spacing"},
		{"", ""},
		{"!!!", ""},
	}
	for _, tc := range cases {
		if got := slugFromTitle(tc.in); got != tc.want {
			t.Errorf("slugFromTitle(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormaliseStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"publish", "published"},
		{"draft", "draft"},
		{"trash", "trash"},
		{"pending", "pending"},
		{"future", "scheduled"},
		{"private", "private"},
		{"inherit", "published"},
		{"weird", "draft"},
	}
	for _, tc := range cases {
		if got := normaliseStatus(tc.in); got != tc.want {
			t.Errorf("normaliseStatus(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommentStatusFromApproval(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "approved"},
		{"0", "pending"},
		{"spam", "spam"},
		{"trash", "trash"},
		{"", "pending"},
	}
	for _, tc := range cases {
		if got := commentStatusFromApproval(tc.in); got != tc.want {
			t.Errorf("commentStatusFromApproval(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestDryrunUUID_Deterministic(t *testing.T) {
	if dryrunUUID("42") != dryrunUUID("42") {
		t.Error("dryrunUUID should be deterministic for the same input")
	}
	if dryrunUUID("42") == dryrunUUID("43") {
		t.Error("dryrunUUID should differ across distinct inputs")
	}
	if dryrunUUID("") != (uuid.UUID{}) {
		t.Error("dryrunUUID(\"\") should return the zero UUID")
	}
}

// TestImportError_Error renders a representative shape so the
// format stays stable for log scrapers.
func TestImportError_Error(t *testing.T) {
	e := &ImportError{Stage: "post", WPID: "1", Slug: "hello", Reason: "broke"}
	got := e.Error()
	if !strings.Contains(got, "importer[post]") || !strings.Contains(got, "wp:1") ||
		!strings.Contains(got, "hello") || !strings.Contains(got, "broke") {
		t.Errorf("Error() = %q (missing fields)", got)
	}
}

func TestImportError_Unwrap(t *testing.T) {
	inner := errors.New("boom")
	e := newImportError("post", "1", "x", inner)
	if !errors.Is(e, inner) {
		t.Errorf("errors.Is should walk Unwrap chain")
	}
}

// ---------------------------------------------------------------------------
// Integration tests below — testcontainers Postgres.
// ---------------------------------------------------------------------------

// TestRun_Integration_MinimalWXR runs the full pipeline against
// the canonical 1-post WXR and asserts the resulting rows.
func TestRun_Integration_MinimalWXR(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	data, err := os.ReadFile(testdataPath(t, "minimal.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	imp := New(pool, Options{OnConflict: ConflictSkip, BatchSize: 10})
	report, err := imp.Run(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.HasErrors() {
		t.Errorf("unexpected per-record errors: %+v", report.Errors)
	}
	if got := report.Posts; got != 1 {
		t.Errorf("Posts: got %d want 1", got)
	}

	// Author row exists with must_reset_password=true.
	var mustReset bool
	if err := pool.QueryRow(context.Background(),
		`SELECT (meta->>'must_reset_password')::boolean FROM users WHERE handle = 'admin'::citext`,
	).Scan(&mustReset); err != nil {
		t.Fatalf("select user: %v", err)
	}
	if !mustReset {
		t.Errorf("user meta.must_reset_password: got false want true")
	}

	// user_passwords row exists.
	var pwCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM user_passwords up
		 JOIN users u ON u.id = up.user_id
		 WHERE u.handle = 'admin'::citext
	`).Scan(&pwCount); err != nil {
		t.Fatalf("count user_passwords: %v", err)
	}
	if pwCount != 1 {
		t.Errorf("user_passwords count: got %d want 1", pwCount)
	}

	// Post row exists, slug=hello-world, status=published.
	var (
		slug   string
		status string
		blocks string
	)
	if err := pool.QueryRow(context.Background(),
		`SELECT slug, status::text, content_blocks::text FROM posts WHERE post_type='post'`,
	).Scan(&slug, &status, &blocks); err != nil {
		t.Fatalf("select post: %v", err)
	}
	if slug != "hello-world" {
		t.Errorf("slug: got %q want hello-world", slug)
	}
	if status != "published" {
		t.Errorf("status: got %q want published", status)
	}
	// content_blocks should be a JSON array of blocks; confirm it's
	// at least valid JSON and contains "core/paragraph".
	if !json.Valid([]byte(blocks)) {
		t.Errorf("content_blocks not valid JSON: %s", blocks)
	}
	if !strings.Contains(blocks, "core/paragraph") {
		t.Errorf("content_blocks missing core/paragraph: %s", blocks)
	}

	// Term relationships: 1 category + 1 tag attached.
	var trCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM term_relationships`,
	).Scan(&trCount); err != nil {
		t.Fatalf("count term_relationships: %v", err)
	}
	if trCount != 2 {
		t.Errorf("term_relationships count: got %d want 2", trCount)
	}

	// Comment row exists, status=approved.
	var cCount int
	var cStatus string
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), max(status) FROM comments`,
	).Scan(&cCount, &cStatus); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if cCount != 1 {
		t.Errorf("comments count: got %d want 1", cCount)
	}
	if cStatus != "approved" {
		t.Errorf("comment status: got %q want approved", cStatus)
	}
}

// TestRun_Integration_100Posts validates the 100-post stream
// completes inside the budget and yields the right counts.
func TestRun_Integration_100Posts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	xml := generateWXR(100)

	start := time.Now()
	imp := New(pool, Options{OnConflict: ConflictSkip, BatchSize: 25})
	report, err := imp.Run(context.Background(), strings.NewReader(xml))
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.HasErrors() {
		t.Errorf("unexpected per-record errors: %d (first: %+v)",
			len(report.Errors), report.Errors[0])
	}
	if report.Posts != 100 {
		t.Errorf("Posts: got %d want 100", report.Posts)
	}
	if report.Authors != 1 {
		t.Errorf("Authors: got %d want 1", report.Authors)
	}
	// 5s budget per issue brief. Real runs are well under a
	// second; the budget exists to flag a regression.
	const budget = 5 * time.Second
	if took > budget {
		t.Errorf("import took %v, budget %v", took, budget)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM posts WHERE post_type='post'`,
	).Scan(&n); err != nil {
		t.Fatalf("count posts: %v", err)
	}
	if n != 100 {
		t.Errorf("posts row count: got %d want 100", n)
	}
}

// TestRun_Integration_ConflictPolicies exercises skip / update /
// fail by re-importing the minimal WXR twice on the same database.
func TestRun_Integration_ConflictPolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	data, err := os.ReadFile(testdataPath(t, "minimal.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	first := New(pool, Options{OnConflict: ConflictSkip})
	if _, err := first.Run(context.Background(), bytes.NewReader(data)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// skip: posts row count stays at 1.
	second := New(pool, Options{OnConflict: ConflictSkip})
	r2, err := second.Run(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("skip rerun: %v", err)
	}
	if r2.HasErrors() {
		t.Errorf("skip rerun errors: %+v", r2.Errors)
	}
	if got := postCount(t, pool); got != 1 {
		t.Errorf("after skip rerun: got %d want 1", got)
	}

	// update: rewrites title (we'll mutate the in-memory XML).
	mutated := strings.Replace(string(data),
		"<title>Hello World</title>",
		"<title>Hello Updated</title>", 1)
	third := New(pool, Options{OnConflict: ConflictUpdate})
	if _, err := third.Run(context.Background(), strings.NewReader(mutated)); err != nil {
		t.Fatalf("update rerun: %v", err)
	}
	var title string
	if err := pool.QueryRow(context.Background(),
		`SELECT title FROM posts WHERE slug='hello-world'::citext`,
	).Scan(&title); err != nil {
		t.Fatalf("select title: %v", err)
	}
	if title != "Hello Updated" {
		t.Errorf("title after update: got %q want %q", title, "Hello Updated")
	}

	// fail: re-importing the same WXR aborts with ErrAborted.
	fourth := New(pool, Options{OnConflict: ConflictFail})
	r4, err := fourth.Run(context.Background(), bytes.NewReader(data))
	if err == nil || !errors.Is(err, ErrAborted) {
		t.Errorf("fail rerun: got err=%v want errors.Is(err, ErrAborted)", err)
	}
	if r4 == nil {
		t.Fatal("Report should never be nil even on abort")
	}
}

// TestRun_Integration_MalformedMidStream confirms partial state
// is committed for records read before the truncation, and an
// error is recorded.
func TestRun_Integration_MalformedMidStream(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	data, err := os.ReadFile(testdataPath(t, "malformed.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	imp := New(pool, Options{OnConflict: ConflictSkip, BatchSize: 1})
	report, err := imp.Run(context.Background(), bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected stream error on malformed XML")
	}
	if report == nil {
		t.Fatal("report should never be nil")
	}
	// The first post was complete; it should be committed before
	// the malformed second post is encountered.
	if got := postCount(t, pool); got != 1 {
		t.Errorf("committed posts after malformed stream: got %d want 1", got)
	}
}

// TestRun_Integration_Dryrun_NoRowsWritten guards the dry-run
// invariant: even with a real pool wired up, --dry-run must not
// touch the DB.
func TestRun_Integration_Dryrun_NoRowsWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	data, err := os.ReadFile(testdataPath(t, "minimal.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	imp := New(pool, Options{Dryrun: true})
	r, err := imp.Run(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Posts != 1 || r.Authors != 1 {
		t.Errorf("dryrun counts: %+v", r)
	}
	if got := postCount(t, pool); got != 0 {
		t.Errorf("posts written under dryrun: got %d want 0", got)
	}
	var uc int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users`,
	).Scan(&uc); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if uc != 0 {
		t.Errorf("users written under dryrun: got %d want 0", uc)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

// postCount returns the row count of the posts table.
func postCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM posts`,
	).Scan(&n); err != nil {
		t.Fatalf("count posts: %v", err)
	}
	return n
}

// mustPostgresWithSchema spins up a Postgres container and
// applies the migrations the importer needs (users, post_types,
// posts, taxonomies+terms, comments). Returns a pool and a
// cleanup function the caller must defer.
func mustPostgresWithSchema(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	mustApplyMigrations(t, dsn)
	return pool, func() { pool.Close() }
}

// mustApplyMigrations applies the up-migrations from the repo's
// /migrations directory in order. We use database/sql here so we
// can hand the migrations one statement at a time without
// pulling in golang-migrate (which would be a circular import).
func mustApplyMigrations(t *testing.T, dsn string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "migrations")
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no migrations found in %s", dir)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(m), err)
		}
	}
}

// repoRoot walks up from this file until it finds the directory
// containing go.work — that's the repo root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root (go.work)")
	return ""
}

// generateWXR returns a WXR document with one author and `n` posts.
// Each post has a distinct slug and a small HTML body so the
// html2blocks converter has something to bite on.
func generateWXR(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"
  xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/"
  xmlns:content="http://purl.org/rss/1.0/modules/content/"
  xmlns:dc="http://purl.org/dc/elements/1.1/"
  xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
  <title>Bulk Blog</title>
  <link>https://bulk.example.com</link>
  <description>Bulk fixture</description>
  <wp:wxr_version>1.2</wp:wxr_version>
  <wp:base_site_url>https://bulk.example.com</wp:base_site_url>
  <wp:base_blog_url>https://bulk.example.com</wp:base_blog_url>
  <generator>test</generator>
  <wp:author>
    <wp:author_id>1</wp:author_id>
    <wp:author_login><![CDATA[bulk-admin]]></wp:author_login>
    <wp:author_email><![CDATA[admin@bulk.example.com]]></wp:author_email>
    <wp:author_display_name><![CDATA[Bulk Admin]]></wp:author_display_name>
  </wp:author>
`)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `
  <item>
    <title>Post %d</title>
    <link>https://bulk.example.com/post-%d/</link>
    <dc:creator><![CDATA[bulk-admin]]></dc:creator>
    <content:encoded><![CDATA[<p>Body of post %d.</p><p>Second paragraph.</p>]]></content:encoded>
    <excerpt:encoded><![CDATA[]]></excerpt:encoded>
    <wp:post_id>%d</wp:post_id>
    <wp:post_date><![CDATA[2024-03-14 13:00:00]]></wp:post_date>
    <wp:post_date_gmt><![CDATA[2024-03-14 13:00:00]]></wp:post_date_gmt>
    <wp:comment_status>closed</wp:comment_status>
    <wp:ping_status>closed</wp:ping_status>
    <wp:post_name><![CDATA[post-%d]]></wp:post_name>
    <wp:status>publish</wp:status>
    <wp:post_parent>0</wp:post_parent>
    <wp:menu_order>0</wp:menu_order>
    <wp:post_type>post</wp:post_type>
    <wp:post_password><![CDATA[]]></wp:post_password>
    <wp:is_sticky>0</wp:is_sticky>
  </item>`, i, i, i, i, i)
	}
	b.WriteString(`
</channel>
</rss>
`)
	return b.String()
}

// ensure io and friends stay referenced.
var (
	_ = io.EOF
	_ = json.Valid
)
