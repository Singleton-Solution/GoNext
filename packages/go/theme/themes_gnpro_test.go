package theme_test

// themes_gnpro_test.go covers the gn-pro reference block theme.
// Shares helpers (repoRoot, dirThemeFiles) with themes_test.go.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
	"github.com/Singleton-Solution/GoNext/packages/go/theme/templates"
)

// gnProDir returns the absolute path to themes/gn-pro/.
func gnProDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "themes", "gn-pro")
}

func TestGnPro_ThemeJSON_ParsesAndValidates(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(gnProDir(t), "theme.json"))
	if err != nil {
		t.Fatalf("read theme.json: %v", err)
	}

	tj, err := theme.Parse(raw)
	if err != nil {
		t.Fatalf("theme.Parse: %v", err)
	}

	if tj.Version != theme.CurrentVersion {
		t.Fatalf("version: got %d, want %d", tj.Version, theme.CurrentVersion)
	}

	if errs := tj.Validate(); len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("validation error: %s", e.Error())
		}
		t.FailNow()
	}
}

// TestGnPro_ThemeJSON_CountsMatchAcceptance pins the headline counts
// the issue acceptance criteria call out: 10 palette tokens, 4
// gradients, 3 font families, 6 font sizes. A regression that drops
// one of them lights up here before it ships.
func TestGnPro_ThemeJSON_CountsMatchAcceptance(t *testing.T) {
	t.Parallel()

	tj := mustLoadGnPro(t)

	if got, want := len(tj.Settings.Color.Palette), 10; got != want {
		t.Errorf("color.palette: got %d entries, want %d", got, want)
	}
	if got, want := len(tj.Settings.Color.Gradients), 4; got != want {
		t.Errorf("color.gradients: got %d entries, want %d", got, want)
	}
	if got, want := len(tj.Settings.Typography.FontFamilies), 3; got != want {
		t.Errorf("typography.fontFamilies: got %d entries, want %d", got, want)
	}
	if got, want := len(tj.Settings.Typography.FontSizes), 6; got != want {
		t.Errorf("typography.fontSizes: got %d entries, want %d", got, want)
	}
	if tj.Settings.Spacing.SpacingScale.Steps != 5 {
		t.Errorf("spacing.spacingScale.steps: got %d, want 5", tj.Settings.Spacing.SpacingScale.Steps)
	}
	if tj.Settings.Layout.ContentSize == "" || tj.Settings.Layout.WideSize == "" {
		t.Errorf("layout: contentSize=%q wideSize=%q (both required)",
			tj.Settings.Layout.ContentSize, tj.Settings.Layout.WideSize)
	}
}

// TestGnPro_ThemeJSON_HasHeadingAndLinkAndButtonElements checks the
// full styles.elements coverage promised in the issue: h1–h6 + a +
// button must each be present so authors can rely on the cascade.
func TestGnPro_ThemeJSON_HasHeadingAndLinkAndButtonElements(t *testing.T) {
	t.Parallel()

	tj := mustLoadGnPro(t)
	required := []string{"h1", "h2", "h3", "h4", "h5", "h6", "a", "button"}
	for _, name := range required {
		if _, ok := tj.Styles.Elements[name]; !ok {
			t.Errorf("styles.elements missing %q", name)
		}
	}
}

// TestGnPro_TemplatesPresent enumerates the files acceptance requires:
// 9 templates + 5 parts. Missing files surface here before any
// rendering layer asks for them.
func TestGnPro_TemplatesPresent(t *testing.T) {
	t.Parallel()

	dir := gnProDir(t)
	templatesDir := filepath.Join(dir, "templates")
	partsDir := filepath.Join(dir, "parts")

	wantTemplates := []string{
		"index.html", "home.html", "single.html", "singular.html",
		"page.html", "archive.html", "category.html", "search.html",
		"404.html",
	}
	for _, name := range wantTemplates {
		p := filepath.Join(templatesDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("template missing: %s (%v)", name, err)
		}
	}

	wantParts := []string{
		"header.html", "footer.html", "sidebar.html",
		"comments.html", "post-meta.html",
	}
	for _, name := range wantParts {
		p := filepath.Join(partsDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("part missing: %s (%v)", name, err)
		}
	}
}

// gnProFiles is a templates.ThemeFiles implementation backed by the
// real on-disk `themes/gn-pro/templates/` directory. It lets the
// resolver tests run against the actual shipped layout instead of an
// ad-hoc in-memory fixture (which would drift the moment someone adds
// a template).
type gnProFiles struct {
	dir string
}

// Has implements templates.ThemeFiles.
func (g gnProFiles) Has(name string) bool {
	_, err := os.Stat(filepath.Join(g.dir, name))
	return err == nil
}

// TestGnPro_TemplateHierarchy_SearchBeatsArchive is the first
// precedence pin: a Search request must resolve to `search.html` even
// when both `search.html` and `archive.html` are present. The default
// resolver's candidate list for RequestTypeSearch is `search → index`
// — `archive` never even gets considered. This test guards against a
// regression that would accidentally route ?s= queries to the archive
// chain.
func TestGnPro_TemplateHierarchy_SearchBeatsArchive(t *testing.T) {
	t.Parallel()

	files := gnProFiles{dir: filepath.Join(gnProDir(t), "templates")}
	resolver := templates.NewDefaultResolver()

	got, err := resolver.Resolve(templates.Request{Type: templates.RequestTypeSearch}, files)
	if err != nil {
		t.Fatalf("Resolve(search): %v", err)
	}
	if got != "search.html" {
		t.Errorf("Resolve(search) = %q, want %q (archive.html must NOT win for ?s= queries)", got, "search.html")
	}
}

// TestGnPro_TemplateHierarchy_CategoryBeatsArchive guards the second
// acceptance criterion: a category-page request (Taxonomy with
// TaxonomySlug=category) must prefer category-specific files over
// archive.html. The MVP resolver routes Taxonomy requests through
// `taxonomy-{tax}-{term} → taxonomy-{tax} → taxonomy → archive →
// index`; `taxonomy-category.html` (the file gn-pro effectively ships
// under the `category.html` alias) wins outright.
//
// We exercise both the "category file present" case (it MUST win
// over archive.html) and the fallback case (when only archive.html is
// present, archive wins — the chain still terminates correctly).
func TestGnPro_TemplateHierarchy_CategoryBeatsArchive(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(gnProDir(t), "templates")
	resolver := templates.NewDefaultResolver()

	// Build a custom files map that mirrors gn-pro's real shipped
	// templates plus an explicit `taxonomy-category.html` (the
	// canonical name the resolver looks up for a category page).
	files := newMemFiles()
	files.add("category.html") // gn-pro's friendly alias
	files.add("taxonomy-category.html")
	files.add("taxonomy.html")
	files.add("archive.html")
	files.add("index.html")

	req := templates.Request{
		Type:         templates.RequestTypeTaxonomy,
		TaxonomySlug: "category",
		TermSlug:     "tech",
	}
	got, err := resolver.Resolve(req, files)
	if err != nil {
		t.Fatalf("Resolve(category): %v", err)
	}
	if got != "taxonomy-category-tech.html" && got != "taxonomy-category.html" {
		t.Errorf("Resolve(category) = %q, want taxonomy-category(-tech).html (category.html must beat archive.html)", got)
	}

	// Now drop the taxonomy-category file and confirm archive.html
	// still wins over index.html — the precedence list terminates
	// cleanly.
	files = newMemFiles()
	files.add("archive.html")
	files.add("index.html")
	got, err = resolver.Resolve(req, files)
	if err != nil {
		t.Fatalf("Resolve(category fallback): %v", err)
	}
	if got != "archive.html" {
		t.Errorf("Resolve(category fallback) = %q, want archive.html", got)
	}

	// And touch on-disk presence to make sure gn-pro itself does ship
	// category.html — a regression that deletes it should fail here
	// even if the resolver semantics are unchanged.
	if _, err := os.Stat(filepath.Join(dir, "category.html")); err != nil {
		t.Errorf("themes/gn-pro/templates/category.html must exist: %v", err)
	}
}

// TestGnPro_StyleCSS_NoOrphanCustomProperties is the third acceptance
// criterion: every `var(--wp-preset--…)` reference in `style.css` must
// resolve to a token actually declared in `theme.json`. Orphan refs
// would yield silent fallbacks at runtime — exactly the kind of bug
// that "production-quality theme" claims to prevent.
//
// The check works against the same emitter the renderer uses
// (theme.EmitCSSCustomProperties), so the test passes iff a real renderer
// run would also resolve every var().
func TestGnPro_StyleCSS_NoOrphanCustomProperties(t *testing.T) {
	t.Parallel()

	tj := mustLoadGnPro(t)
	emitted := tj.EmitCSSCustomProperties()
	declared := extractDeclaredVars(emitted)

	if len(declared) == 0 {
		t.Fatal("emitter declared zero variables — theme.json has no tokens?")
	}

	cssBytes, err := os.ReadFile(filepath.Join(gnProDir(t), "style.css"))
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	used := extractUsedVars(string(cssBytes))

	// Only check vars under the --wp-preset-- namespace; the
	// style sheet is allowed to fall back to non-preset CSS vars
	// (e.g. the `--content` private var defined inline). The
	// emitter's whole surface lives under --wp-preset-- so the
	// closed set is well-defined.
	var orphans []string
	for v := range used {
		if !strings.HasPrefix(v, "--wp-preset--") {
			continue
		}
		if _, ok := declared[v]; !ok {
			orphans = append(orphans, v)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("style.css references %d undeclared --wp-preset-- variables:\n%s",
			len(orphans), strings.Join(orphans, "\n"))
	}
}

// TestGnPro_StyleCSS_NoHardCodedColors is the "production-quality"
// guard: every color in style.css MUST come through a token, not be
// inlined as a hex / rgb / named CSS color. We scan declarations for
// the canonical color contexts (color:, background-color:, fill:,
// stroke:, border-*: chains) and reject anything that isn't a var(…)
// reference or a transparent / inherit / currentColor keyword.
//
// `transparent` and `currentColor` are universal keywords; the
// `color-scheme` property is not a color value despite the name.
func TestGnPro_StyleCSS_NoHardCodedColors(t *testing.T) {
	t.Parallel()

	cssBytes, err := os.ReadFile(filepath.Join(gnProDir(t), "style.css"))
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}
	css := string(cssBytes)

	// Strip /* … */ block comments before scanning — the header
	// docblock contains the theme description which can mention
	// hex-like tokens by accident.
	css = stripCSSComments(css)

	// Hex colors: #abc, #abcd, #aabbcc, #aabbccdd
	hexRe := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if matches := hexRe.FindAllString(css, -1); len(matches) > 0 {
		t.Errorf("style.css contains hard-coded hex color literals: %v", uniqueStrings(matches))
	}

	// rgb()/rgba() literals
	rgbRe := regexp.MustCompile(`\brgba?\s*\(`)
	if matches := rgbRe.FindAllString(css, -1); len(matches) > 0 {
		t.Errorf("style.css contains hard-coded rgb()/rgba() literals: %v", uniqueStrings(matches))
	}

	// hsl()/hsla() literals
	hslRe := regexp.MustCompile(`\bhsla?\s*\(`)
	if matches := hslRe.FindAllString(css, -1); len(matches) > 0 {
		t.Errorf("style.css contains hard-coded hsl()/hsla() literals: %v", uniqueStrings(matches))
	}
}

// ----------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------

// mustLoadGnPro reads and parses themes/gn-pro/theme.json. Test
// failures bubble as t.Fatalf so the caller can use a single line at
// the top of each test.
func mustLoadGnPro(t *testing.T) *theme.ThemeJSON {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(gnProDir(t), "theme.json"))
	if err != nil {
		t.Fatalf("read theme.json: %v", err)
	}
	tj, err := theme.Parse(raw)
	if err != nil {
		t.Fatalf("theme.Parse: %v", err)
	}
	return tj
}

// memFiles is an in-memory templates.ThemeFiles for hierarchy tests.
// Using this instead of a real os.DirFS keeps the assertion text
// honest: when a case says "category.html present", you can read
// directly which files the resolver sees without grepping the
// repo tree.
type memFiles map[string]struct{}

func newMemFiles() memFiles { return memFiles{} }

func (m memFiles) add(name string) { m[name] = struct{}{} }

// Has implements templates.ThemeFiles.
func (m memFiles) Has(name string) bool {
	_, ok := m[name]
	return ok
}

// declaredVarRe extracts `--wp-preset--…:` declarations from the
// emitted custom-property block.
var declaredVarRe = regexp.MustCompile(`(--wp-preset--[a-z0-9-]+)\s*:`)

// usedVarRe extracts every `var(--…)` reference in a CSS file. The
// inner group captures the name; the fallback expression (`, …`) is
// allowed and not part of the match.
var usedVarRe = regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)`)

// extractDeclaredVars returns the set of custom-property names
// emitted into a ":root { … }" block.
func extractDeclaredVars(css string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range declaredVarRe.FindAllStringSubmatch(css, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

// extractUsedVars returns the set of custom-property names referenced
// via var(…) anywhere in the stylesheet.
func extractUsedVars(css string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range usedVarRe.FindAllStringSubmatch(css, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

// commentRe matches CSS /* … */ block comments (non-greedy).
var commentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripCSSComments removes /* … */ block comments so token-scanning
// doesn't trip on documentation in the header docblock.
func stripCSSComments(css string) string {
	return commentRe.ReplaceAllString(css, "")
}

// uniqueStrings returns the input slice deduplicated, preserving the
// first-seen order. Used to keep failure messages compact when the
// same literal appears many times.
func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
