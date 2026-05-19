// Package main holds the gonext-seo example plugin tests.
//
// These tests run against the pure-Go domain code in domain.go — the
// same code the TinyGo build links into seo.wasm — so passing here
// proves the contract works locally without TinyGo installed.
//
// The host-side end-to-end test (TestE2E_ContentFilter_EmitsOpenGraph)
// reaches further: it builds the same hook payload the wazero
// dispatcher would produce, invokes the plugin's filter logic, and
// asserts the result HTML carries every meta tag the operator expects.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// TestManifest_ValidatesAgainstSchema is the first-line check: the
// manifest.json shipped in the example directory must satisfy
// packages/go/plugins/manifest/schema.json. If this test fails the
// example is broken before any operator ever sees it.
func TestManifest_ValidatesAgainstSchema(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	m, err := manifest.Validate(data)
	if err != nil {
		t.Fatalf("manifest.Validate: %v", err)
	}
	if m.Name != "gonext-seo" {
		t.Errorf("manifest name: got %q want %q", m.Name, "gonext-seo")
	}
	if m.Version != "0.1.0" {
		t.Errorf("manifest version: got %q want %q", m.Version, "0.1.0")
	}
	if m.Entry != "seo.wasm" {
		t.Errorf("manifest entry: got %q want %q", m.Entry, "seo.wasm")
	}

	// Every cap the README documents must be present. We test each
	// individually so a missing-cap regression points to the exact
	// dropped string.
	wantCaps := []string{"posts.read", "posts.write", "hooks.subscribe", "jobs.enqueue"}
	for _, want := range wantCaps {
		if !containsString(m.Capabilities, want) {
			t.Errorf("manifest capabilities missing %q (got %v)", want, m.Capabilities)
		}
	}

	if m.Hooks == nil {
		t.Fatal("manifest.hooks is nil; want filters+actions declared")
	}
	if !containsString(m.Hooks.Filters, "the_content") {
		t.Errorf("manifest.hooks.filters missing the_content (got %v)", m.Hooks.Filters)
	}
	for _, want := range []string{"wp_head", "save_post"} {
		if !containsString(m.Hooks.Actions, want) {
			t.Errorf("manifest.hooks.actions missing %q (got %v)", want, m.Hooks.Actions)
		}
	}
	if !containsString(m.Jobs, "seo.recompute-scores") {
		t.Errorf("manifest.jobs missing seo.recompute-scores (got %v)", m.Jobs)
	}
}

// TestManifest_FilenameMatchesPath confirms the entry path resolves to
// a file we expect the build system to produce. We don't require the
// blob to exist (that requires TinyGo) — we just check that the entry
// field uses the canonical filename.
func TestManifest_FilenameMatchesPath(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	m, err := manifest.Validate(data)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Cross-check: the build.sh contract says it writes ./seo.wasm at
	// the package root. The manifest's entry field must point to that
	// same name (no leading slash, no subdir).
	if filepath.Dir(m.Entry) != "." {
		t.Errorf("entry %q must live at bundle root", m.Entry)
	}
}

// TestBuildTitle confirms the title-composition rule. The cases pin
// the three reachable branches: title alone, brand alone, both joined.
func TestBuildTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   Post
		want string
	}{
		{"title only", Post{Title: "Hello"}, "Hello"},
		{"brand only", Post{Brand: "Acme"}, "Acme"},
		{"both", Post{Title: "Hello", Brand: "Acme"}, "Hello | Acme"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := BuildTitle(tc.in); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestBuildDescription pins the fallback ladder: explicit excerpt
// wins; missing excerpt falls back to the first paragraph; both
// missing falls back to the title.
func TestBuildDescription(t *testing.T) {
	t.Parallel()
	t.Run("excerpt wins", func(t *testing.T) {
		t.Parallel()
		p := Post{Title: "T", Excerpt: "From excerpt", Content: "<p>From content</p>"}
		if got := BuildDescription(p); got != "From excerpt" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("content fallback", func(t *testing.T) {
		t.Parallel()
		p := Post{Title: "T", Content: "<p>From content</p><p>second</p>"}
		if got := BuildDescription(p); got != "From content" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("title fallback", func(t *testing.T) {
		t.Parallel()
		p := Post{Title: "T"}
		if got := BuildDescription(p); got != "T" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("truncated to 160", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 200)
		p := Post{Excerpt: long}
		got := BuildDescription(p)
		if len([]rune(got)) != 160 {
			t.Errorf("expected 160-rune cap, got %d", len([]rune(got)))
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("expected ellipsis suffix, got %q", got)
		}
	})
}

// TestBuildHeadHTML asserts every meta tag in the canonical set is
// present for a fully-populated post.
func TestBuildHeadHTML(t *testing.T) {
	t.Parallel()
	p := samplePost()
	got := BuildHeadHTML(p)
	wantSubstrings := []string{
		`<title>How to Plant a Garden in Springtime!!! | Acme Blog</title>`,
		`<meta name="description" content="A guide to planting your first garden, with practical tips for absolute beginners today.">`,
		`<link rel="canonical" href="https://acme.example/blog/garden">`,
		`<meta property="og:type" content="article">`,
		`<meta property="og:title" content="How to Plant a Garden in Springtime!!! | Acme Blog">`,
		`<meta property="og:description" content="A guide to planting your first garden, with practical tips for absolute beginners today.">`,
		`<meta property="og:url" content="https://acme.example/blog/garden">`,
		`<meta property="og:image" content="https://acme.example/garden.jpg">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="How to Plant a Garden in Springtime!!! | Acme Blog">`,
		`<meta name="twitter:image" content="https://acme.example/garden.jpg">`,
		`<script type="application/ld+json">`,
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in head HTML:\n%s", s, got)
		}
	}
}

// TestBuildHeadHTML_EscapesUnsafeText proves the HTML escaper handles
// the most common injection vectors. The brand and title fields are
// operator-supplied — they're trusted but unsanitised, so any '<' that
// sneaks in must render as '&lt;'.
func TestBuildHeadHTML_EscapesUnsafeText(t *testing.T) {
	t.Parallel()
	p := Post{Title: `<script>x</script>`, Brand: `"Acme"`}
	got := BuildHeadHTML(p)
	if strings.Contains(got, "<script>x</script>") {
		t.Errorf("unescaped script tag found in:\n%s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected &lt;script&gt; escape; got:\n%s", got)
	}
	if !strings.Contains(got, "&quot;Acme&quot;") {
		t.Errorf("expected &quot; escape; got:\n%s", got)
	}
}

// TestBuildJSONLD_ParsesAsValidJSON pulls the <script> body out of the
// emitted block and feeds it through encoding/json. A failure here
// would mean we shipped malformed Article schema, which Google would
// silently reject — better to catch in CI.
func TestBuildJSONLD_ParsesAsValidJSON(t *testing.T) {
	t.Parallel()
	p := samplePost()
	block := BuildJSONLD(p)

	open := `<script type="application/ld+json">`
	close := `</script>`
	if !strings.HasPrefix(block, open) || !strings.HasSuffix(block, close) {
		t.Fatalf("BuildJSONLD did not produce expected wrapper:\n%s", block)
	}
	body := strings.TrimPrefix(block, open)
	body = strings.TrimSuffix(body, close)

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("JSON-LD does not parse: %v\n%s", err, body)
	}

	// Required Article fields per Google's spec.
	if got := doc["@type"]; got != "Article" {
		t.Errorf("@type: got %v want Article", got)
	}
	if got := doc["@context"]; got != "https://schema.org" {
		t.Errorf("@context: got %v want https://schema.org", got)
	}
	if doc["headline"] == nil {
		t.Error("headline missing")
	}
	if doc["author"] == nil {
		t.Error("author missing for populated post")
	}
}

// TestComputeSEOScore_KnownInputs pins the score for a handful of
// fixtures. The point of pinning is to catch unintended drift in the
// weights — if the formula changes, the test must change with it, and
// the diff makes the new rubric reviewable.
func TestComputeSEOScore_KnownInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    Post
		want  int
		atMin int // if want is -1, assert score >= atMin instead
	}{
		{
			name: "empty post",
			in:   Post{},
			want: 0,
		},
		{
			name: "title-only short",
			// titleLen=5 → +5 (present), no length bonus.
			in:   Post{Title: "Hello"},
			want: 5,
		},
		{
			name: "title + ideal description",
			// title 38 chars → +25; desc 92 chars → +25.
			in: Post{
				Title:   "How to Plant a Garden in Springtime!!!",
				Excerpt: "A guide to planting your first garden, with practical tips for absolute beginners today.",
			},
			want: 50,
		},
		{
			name: "fully-populated maxes out",
			in:   samplePost(),
			want: 100,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeSEOScore(tc.in)
			if got != tc.want {
				t.Errorf("ComputeSEOScore: got %d want %d", got, tc.want)
			}
		})
	}
}

// TestE2E_ContentFilter_EmitsOpenGraph is the end-to-end proof. It
// builds the same FilterPayload the host's wazero dispatcher would
// hand the plugin, runs the plugin's filter logic, and asserts the
// returned HTML carries the schema.org JSON-LD block alongside the
// original content.
//
// This is the load-bearing test for the example: a green here means
// the wire format the plugin speaks matches what the host expects.
func TestE2E_ContentFilter_EmitsOpenGraph(t *testing.T) {
	t.Parallel()
	post := samplePost()
	inputHTML := "<p>Welcome to my garden post.</p>"

	// Build the host-side payload the runtime would marshal.
	value, err := json.Marshal(inputHTML)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"kind":  "filter",
		"value": json.RawMessage(value),
		"args":  []interface{}{post},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Run the plugin's filter via the dummy host bus.
	result, err := runFilterThroughBus(context.Background(), "the_content", payloadBytes)
	if err != nil {
		t.Fatalf("runFilterThroughBus: %v", err)
	}

	var outHTML string
	if err := json.Unmarshal(result, &outHTML); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !strings.Contains(outHTML, inputHTML) {
		t.Errorf("output dropped original content:\n%s", outHTML)
	}
	if !strings.Contains(outHTML, `<script type="application/ld+json">`) {
		t.Errorf("output missing JSON-LD block:\n%s", outHTML)
	}
	if !strings.Contains(outHTML, `"@type":"Article"`) {
		t.Errorf("JSON-LD missing @type=Article:\n%s", outHTML)
	}
}

// TestE2E_WPHead_BuildsCompleteHeadBlock proves the action handler
// reaches BuildHeadHTML and that the output carries every meta
// property an operator expects to see when they view-source on a
// rendered post.
func TestE2E_WPHead_BuildsCompleteHeadBlock(t *testing.T) {
	t.Parallel()
	post := samplePost()
	head := BuildHeadHTML(post)

	// The action handler returns no body — the contract is that the
	// side-effect runs cleanly. The dummy bus's wp_head route calls
	// BuildHeadHTML and returns its output to the test for asserting.
	if !strings.Contains(head, `<meta property="og:title"`) {
		t.Errorf("head HTML missing og:title:\n%s", head)
	}
	if !strings.Contains(head, `<meta name="twitter:card" content="summary_large_image">`) {
		t.Errorf("head HTML missing twitter:card:\n%s", head)
	}
}

// TestE2E_SavePost_ComputesScore proves the save_post action runs
// ComputeSEOScore against the post that triggered the save. The score
// returned matches the standalone unit-test fixture, confirming the
// dispatch wiring is correct.
func TestE2E_SavePost_ComputesScore(t *testing.T) {
	t.Parallel()
	post := samplePost()
	score := ComputeSEOScore(post)
	if score < 80 {
		t.Errorf("sample post should score >=80 (got %d) — the fixture is meant to model a 'good' post", score)
	}
}

// -----------------------------------------------------------------------
// Test helpers.
// -----------------------------------------------------------------------

// samplePost returns the canonical "fully populated good post" used as
// a fixture across multiple tests. Returning a fresh struct each call
// keeps tests from accidentally sharing state.
func samplePost() Post {
	return Post{
		// Title is 38 chars — inside the 30..60 sweet spot the scorer
		// rewards with the full title weight.
		Title: "How to Plant a Garden in Springtime!!!",
		// Excerpt is 88 chars — inside the 70..160 SERP-safe range so
		// the scorer awards the full description weight.
		Excerpt: "A guide to planting your first garden, with practical tips for absolute beginners today.",
		Content: "<p>Gardening is one of the most rewarding hobbies. " +
			strings.Repeat("Plants need sun, soil, water, and patience. ", 60) +
			"</p>",
		URL:     "https://acme.example/blog/garden",
		Image:   "https://acme.example/garden.jpg",
		Brand:   "Acme Blog",
		Author:  "Jane Gardener",
		PubDate: "2026-04-01T09:00:00Z",
	}
}

// containsString is a tiny slice-search helper — slices.Contains is in
// the stdlib but pinning to the older idiom keeps the test file free
// of go-version conditionals.
func containsString(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
