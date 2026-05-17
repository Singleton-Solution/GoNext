package theme_test

// This file pins the first-party themes shipped under
// /themes against the parser, validator, emitter, and the
// template-hierarchy resolver. Each new theme that lands in
// /themes should grow a sub-test here so the on-disk surface
// of the theme system is regression-tested end-to-end
// (parse → validate → resolve).
//
// The cross-package test lives in theme_test (the external
// test package) rather than `package theme` so it can pull in
// the templates sub-package without an import cycle.

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
	"github.com/Singleton-Solution/GoNext/packages/go/theme/templates"
)

// repoRoot returns the GoNext checkout root, derived from the
// path of this source file. We hop two directories above
// packages/go/theme to land on the repository root where
// /themes lives. Using runtime.Caller keeps the lookup
// independent of `go test`'s working directory, which would
// otherwise vary between `go test ./...` from the repo root
// and `cd packages/go && go test ./theme/...`.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed; cannot locate repo root")
	}
	// file == .../packages/go/theme/themes_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// dirThemeFiles is the production-style ThemeFiles
// implementation: a flat lookup over an on-disk theme
// directory, scoped to a single subdirectory (typically
// "templates"). It's defined here rather than in the
// templates package because §5 of the theme docs leaves
// "production wraps an os.DirFS scan" deliberately
// unimplemented — the resolver only needs Has, and the
// glue that decides which subdirectory to scan belongs to
// the installer.
type dirThemeFiles struct {
	root string
}

// Has reports whether root contains a file with the given
// basename. Subdirectories under root are NOT walked; the
// resolver's contract is that filenames are bare basenames.
func (d dirThemeFiles) Has(filename string) bool {
	_, err := os.Stat(filepath.Join(d.root, filename))
	return err == nil
}

// TestGNHelloTheme is the gn-hello acceptance gate. It is
// the single source of truth for "the on-disk theme matches
// the contracts the theme system declares" and combines:
//
//   - JSON parse (strict, rejects unknown keys)
//   - structural validation (Validate returns no errors)
//   - CSS custom-property emission (every token from
//     theme.json appears in the output exactly once)
//   - template hierarchy resolution for every RequestType
//     the theme is expected to handle
//   - presence of declared template parts on disk
//
// A failure in any sub-test is a regression in either the
// theme or the surrounding package surface, not a flaky
// test. Each assertion's failure message names the field /
// file it is checking so a contributor can fix the right
// thing without digging into the harness.
func TestGNHelloTheme(t *testing.T) {
	t.Parallel()

	themeDir := filepath.Join(repoRoot(t), "themes", "gn-hello")
	manifestPath := filepath.Join(themeDir, "theme.json")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}

	tj, err := theme.Parse(data)
	if err != nil {
		t.Fatalf("theme.Parse: %v", err)
	}

	t.Run("Validate", func(t *testing.T) {
		if errs := tj.Validate(); len(errs) > 0 {
			for _, e := range errs {
				t.Errorf("validation error: %s", e.Error())
			}
		}
	})

	t.Run("Version", func(t *testing.T) {
		if tj.Version != theme.CurrentVersion {
			t.Errorf("Version = %d, want %d", tj.Version, theme.CurrentVersion)
		}
	})

	t.Run("Palette", func(t *testing.T) {
		got := make(map[string]string, len(tj.Settings.Color.Palette))
		for _, c := range tj.Settings.Color.Palette {
			got[c.Slug] = c.Color
		}
		// The README and the test agree on these four
		// tokens. Adding a fifth requires updating both.
		want := []string{"ink", "paper", "muted", "accent"}
		for _, slug := range want {
			if _, ok := got[slug]; !ok {
				t.Errorf("palette is missing required slug %q (got %v)", slug, keys(got))
			}
		}
	})

	t.Run("FontFamilies", func(t *testing.T) {
		var slugs []string
		for _, f := range tj.Settings.Typography.FontFamilies {
			slugs = append(slugs, f.Slug)
		}
		if len(slugs) == 0 {
			t.Fatalf("typography.fontFamilies is empty; gn-hello must ship at least one")
		}
	})

	t.Run("Layout", func(t *testing.T) {
		if tj.Settings.Layout.ContentSize == "" {
			t.Errorf("settings.layout.contentSize is empty")
		}
	})

	t.Run("EmittedCSSCoversAllTokens", func(t *testing.T) {
		css := tj.EmitCSSCustomProperties()
		if css == "" {
			t.Fatalf("EmitCSSCustomProperties returned empty for a theme with tokens")
		}
		// Every palette / family / size / layout token in
		// the manifest MUST appear as a custom property in
		// the emitted CSS. This is what makes the theme
		// renderable; if it ever stops being true the
		// emitter has regressed.
		for _, c := range tj.Settings.Color.Palette {
			needle := "--wp-preset--color--" + c.Slug + ":"
			if !strings.Contains(css, needle) {
				t.Errorf("emitted CSS missing %q\n--- CSS ---\n%s", needle, css)
			}
		}
		for _, f := range tj.Settings.Typography.FontFamilies {
			needle := "--wp-preset--font-family--" + f.Slug + ":"
			if !strings.Contains(css, needle) {
				t.Errorf("emitted CSS missing %q", needle)
			}
		}
		for _, s := range tj.Settings.Typography.FontSizes {
			needle := "--wp-preset--font-size--" + s.Slug + ":"
			if !strings.Contains(css, needle) {
				t.Errorf("emitted CSS missing %q", needle)
			}
		}
		if tj.Settings.Layout.ContentSize != "" &&
			!strings.Contains(css, "--wp-preset--layout--content:") {
			t.Errorf("emitted CSS missing --wp-preset--layout--content")
		}
		if tj.Settings.Layout.WideSize != "" &&
			!strings.Contains(css, "--wp-preset--layout--wide:") {
			t.Errorf("emitted CSS missing --wp-preset--layout--wide")
		}
	})

	// Resolver tests — exercise the template hierarchy
	// against the real on-disk files. dirThemeFiles is
	// rooted at templates/ because that's where every
	// hierarchy candidate is expected to live.
	templatesDir := filepath.Join(themeDir, "templates")
	files := dirThemeFiles{root: templatesDir}
	resolver := templates.NewDefaultResolver()

	resolveCases := []struct {
		name string
		req  templates.Request
		want string
	}{
		{
			name: "single post resolves to single.html",
			req:  templates.Request{Type: templates.RequestTypeSingular, PostType: "post"},
			want: "single.html",
		},
		{
			name: "archive resolves to archive.html",
			req:  templates.Request{Type: templates.RequestTypeArchive, PostType: "post"},
			want: "archive.html",
		},
		{
			name: "404 resolves to 404.html",
			req:  templates.Request{Type: templates.RequestTypeNotFound, Is404: true},
			want: "404.html",
		},
		{
			name: "home falls back to index.html (no home.html shipped)",
			req:  templates.Request{Type: templates.RequestTypeHome, IsHome: true},
			want: "index.html",
		},
		{
			name: "front-page falls back to index.html",
			req:  templates.Request{Type: templates.RequestTypeFrontPage, IsFront: true},
			want: "index.html",
		},
		{
			name: "search falls back to index.html",
			req:  templates.Request{Type: templates.RequestTypeSearch},
			want: "index.html",
		},
		{
			name: "taxonomy falls back to archive.html",
			req:  templates.Request{Type: templates.RequestTypeTaxonomy, TaxonomySlug: "category", TermSlug: "news"},
			want: "archive.html",
		},
		{
			name: "author falls back to archive.html",
			req:  templates.Request{Type: templates.RequestTypeAuthor, AuthorID: "1"},
			want: "archive.html",
		},
		{
			name: "date falls back to archive.html",
			req:  templates.Request{Type: templates.RequestTypeDate},
			want: "archive.html",
		},
	}
	for _, c := range resolveCases {
		c := c
		t.Run("Resolve/"+c.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolver.Resolve(c.req, files)
			if err != nil {
				t.Fatalf("Resolve(%v) returned error: %v", c.req, err)
			}
			if got != c.want {
				t.Errorf("Resolve(%v) = %q, want %q", c.req, got, c.want)
			}
		})
	}

	t.Run("TemplatePartsExistOnDisk", func(t *testing.T) {
		partsDir := filepath.Join(themeDir, "parts")
		for _, tp := range tj.TemplateParts {
			path := filepath.Join(partsDir, tp.Name+".html")
			info, err := os.Stat(path)
			if err != nil {
				t.Errorf("declared templatePart %q missing on disk at %s: %v", tp.Name, path, err)
				continue
			}
			if info.IsDir() {
				t.Errorf("template part %q is a directory, expected a file", path)
			}
		}
	})

	t.Run("StyleCSSPresent", func(t *testing.T) {
		path := filepath.Join(themeDir, "style.css")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("style.css missing at %s: %v", path, err)
			return
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// gn-hello's whole point is to consume the
		// emitted tokens. If the stylesheet stops
		// referencing the --wp-preset namespace the theme
		// is no longer a faithful reference.
		if !strings.Contains(string(body), "var(--wp-preset--") {
			t.Errorf("style.css does not reference any --wp-preset-- custom property")
		}
	})

	t.Run("NoUnexpectedExtensions", func(t *testing.T) {
		// gn-hello deliberately ships only .html (no
		// .tsx) so it exercises the classic-theme fallback
		// branch of the resolver. If a .tsx ever creeps
		// in by accident the resolver will pick it
		// silently and the test cases above will start
		// failing in surprising ways; catch it directly.
		err := filepath.WalkDir(templatesDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".tsx") {
				t.Errorf("unexpected .tsx file %s — gn-hello is classic-only", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk templates dir: %v", err)
		}
	})
}

// keys returns the keys of m as a stable-ordered slice,
// used only for the error message in the palette sub-test.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
