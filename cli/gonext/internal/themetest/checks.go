package themetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Run runs the full contract suite against the theme rooted at dir and
// returns the assembled Report. The function never returns an error for
// "the theme failed checks" — failures are encoded as StatusFail rows in
// the report. An error is returned only when the theme directory itself
// is unreadable.
func Run(dir string) (*Report, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	r := &Report{ThemePath: abs}

	pkg := checkPackageJSON(r, abs)
	themeJSON := checkThemeJSON(r, abs)
	checkTemplateHierarchy(r, abs, pkg, themeJSON)
	checkParts(r, abs)
	checkBlockVsClassic(r, abs, pkg)
	checkReserved(r)

	return r, nil
}

// packageMeta is the slice of package.json we care about for checks.
type packageMeta struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	GoNext  gonextMeta     `json:"gonext"`
	Extra   map[string]any `json:"-"`
}

type gonextMeta struct {
	Kind          string `json:"kind"`
	Type          string `json:"type"`   // "block" | "classic"
	Parent        any    `json:"parent"` // string | null
	EngineVersion string `json:"engineVersion"`
	TextDomain    string `json:"textDomain"`
}

// themeJSONMeta is the slice of theme.json we care about for checks.
type themeJSONMeta struct {
	Version         int                  `json:"version"`
	Title           string               `json:"title"`
	Settings        map[string]any       `json:"settings"`
	Styles          map[string]any       `json:"styles"`
	Supports        map[string]any       `json:"supports"`
	Patterns        []string             `json:"patterns"`
	CustomTemplates []customTemplateDecl `json:"customTemplates"`
	TemplateParts   []templatePartDecl   `json:"templateParts"`
}

type customTemplateDecl struct {
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	PostTypes []string `json:"postTypes"`
}

type templatePartDecl struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Area  string `json:"area"`
}

// themeNameRE accepts both the recommended "@scope/theme-*" convention
// and the broader "@scope/<slug>" form that docs/03-theme-system.md §2
// uses in its example (`@acme/hello-gonext`). We treat the theme- prefix
// as recommended but not required; absent → NOTE, not FAIL.
var (
	scopedNameRE      = regexp.MustCompile(`^@[a-z0-9][a-z0-9_.-]*\/[a-z0-9][a-z0-9_.-]*$`)
	themePrefixedRE   = regexp.MustCompile(`^@[a-z0-9][a-z0-9_.-]*\/theme-[a-z0-9][a-z0-9_.-]*$`)
	unscopedNameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	supportedSemverRE = regexp.MustCompile(`^[~^]?\d+\.\d+(\.\d+)?$`)
)

// checkPackageJSON validates the presence + minimum shape of package.json.
// Returns the parsed metadata (possibly empty) so downstream checks can use it.
func checkPackageJSON(r *Report, dir string) packageMeta {
	path := filepath.Join(dir, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.Add(Check{
				ID:      "package-json.present",
				Title:   "package.json present",
				Status:  StatusFail,
				Message: "package.json not found at theme root",
			})
		} else {
			r.Add(Check{
				ID:      "package-json.present",
				Title:   "package.json present",
				Status:  StatusFail,
				Message: fmt.Sprintf("read package.json: %v", err),
			})
		}
		return packageMeta{}
	}
	r.Add(Check{ID: "package-json.present", Title: "package.json present", Status: StatusPass})

	var meta packageMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		r.Add(Check{
			ID:      "package-json.json-valid",
			Title:   "package.json is valid JSON",
			Status:  StatusFail,
			Message: err.Error(),
		})
		return packageMeta{}
	}
	r.Add(Check{ID: "package-json.json-valid", Title: "package.json is valid JSON", Status: StatusPass})
	r.ThemeName = meta.Name
	r.ThemeType = meta.GoNext.Type
	if r.ThemeType == "" {
		r.ThemeType = "unknown"
	}

	// gonext key with kind=theme
	if strings.ToLower(meta.GoNext.Kind) != "theme" {
		r.Add(Check{
			ID:      "package-json.gonext-kind",
			Title:   `package.json "gonext.kind" is "theme"`,
			Status:  StatusFail,
			Message: fmt.Sprintf(`expected "gonext.kind":"theme", got %q`, meta.GoNext.Kind),
		})
	} else {
		r.Add(Check{ID: "package-json.gonext-kind", Title: `package.json "gonext.kind" is "theme"`, Status: StatusPass})
	}

	// type is block or classic
	switch strings.ToLower(meta.GoNext.Type) {
	case "block", "classic":
		r.Add(Check{ID: "package-json.gonext-type", Title: `"gonext.type" is "block" or "classic"`, Status: StatusPass})
	case "":
		r.Add(Check{
			ID:      "package-json.gonext-type",
			Title:   `"gonext.type" is "block" or "classic"`,
			Status:  StatusFail,
			Message: `"gonext.type" is required; must be "block" or "classic"`,
		})
	default:
		r.Add(Check{
			ID:      "package-json.gonext-type",
			Title:   `"gonext.type" is "block" or "classic"`,
			Status:  StatusFail,
			Message: fmt.Sprintf(`unknown "gonext.type" %q (expected "block" or "classic")`, meta.GoNext.Type),
		})
	}

	// name convention
	switch {
	case meta.Name == "":
		r.Add(Check{
			ID:      "package-json.name",
			Title:   "package.json name follows @scope/theme-* convention",
			Status:  StatusFail,
			Message: `"name" is required`,
		})
	case themePrefixedRE.MatchString(meta.Name):
		r.Add(Check{ID: "package-json.name", Title: "package.json name follows @scope/theme-* convention", Status: StatusPass})
	case scopedNameRE.MatchString(meta.Name):
		r.Add(Check{
			ID:      "package-json.name",
			Title:   "package.json name follows @scope/theme-* convention",
			Status:  StatusNote,
			Message: fmt.Sprintf(`%q is scoped but missing the recommended "theme-" prefix`, meta.Name),
		})
	case unscopedNameRE.MatchString(meta.Name):
		r.Add(Check{
			ID:      "package-json.name",
			Title:   "package.json name follows @scope/theme-* convention",
			Status:  StatusNote,
			Message: fmt.Sprintf(`%q is unscoped; "@scope/theme-<slug>" is recommended`, meta.Name),
		})
	default:
		r.Add(Check{
			ID:      "package-json.name",
			Title:   "package.json name follows @scope/theme-* convention",
			Status:  StatusFail,
			Message: fmt.Sprintf("%q is not a valid npm package name", meta.Name),
		})
	}

	// engineVersion advisory
	if meta.GoNext.EngineVersion != "" {
		// We don't pin a semver-range parser here; sniff for a plausible shape.
		if !strings.ContainsAny(meta.GoNext.EngineVersion, "0123456789") {
			r.Add(Check{
				ID:      "package-json.engine-version",
				Title:   `"gonext.engineVersion" looks like a semver range`,
				Status:  StatusFail,
				Message: fmt.Sprintf(`engineVersion %q does not contain a version number`, meta.GoNext.EngineVersion),
			})
		} else {
			r.Add(Check{ID: "package-json.engine-version", Title: `"gonext.engineVersion" looks like a semver range`, Status: StatusPass})
		}
	} else {
		r.Add(Check{
			ID:      "package-json.engine-version",
			Title:   `"gonext.engineVersion" looks like a semver range`,
			Status:  StatusNote,
			Message: `"gonext.engineVersion" not declared; install will not version-gate`,
		})
	}

	return meta
}

// checkThemeJSON validates the presence + schema-ish shape of theme.json.
// Returns parsed metadata (possibly empty) for downstream cross-checks.
func checkThemeJSON(r *Report, dir string) themeJSONMeta {
	path := filepath.Join(dir, "theme.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.Add(Check{
				ID:      "theme-json.present",
				Title:   "theme.json present",
				Status:  StatusFail,
				Message: "theme.json not found at theme root (required per docs/03-theme-system.md §2)",
			})
		} else {
			r.Add(Check{
				ID:      "theme-json.present",
				Title:   "theme.json present",
				Status:  StatusFail,
				Message: fmt.Sprintf("read theme.json: %v", err),
			})
		}
		return themeJSONMeta{}
	}
	r.Add(Check{ID: "theme-json.present", Title: "theme.json present", Status: StatusPass})

	var meta themeJSONMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		r.Add(Check{
			ID:      "theme-json.json-valid",
			Title:   "theme.json is valid JSON",
			Status:  StatusFail,
			Message: err.Error(),
		})
		return themeJSONMeta{}
	}
	r.Add(Check{ID: "theme-json.json-valid", Title: "theme.json is valid JSON", Status: StatusPass})

	// version: must be present and currently must be 1 (per §3.3 "Start at version: 1").
	if meta.Version == 0 {
		r.Add(Check{
			ID:      "theme-json.version",
			Title:   `theme.json has "version": 1`,
			Status:  StatusFail,
			Message: `"version" is required (must be 1)`,
		})
	} else if meta.Version != 1 {
		r.Add(Check{
			ID:      "theme-json.version",
			Title:   `theme.json has "version": 1`,
			Status:  StatusFail,
			Message: fmt.Sprintf(`unsupported theme.json version %d (expected 1)`, meta.Version),
		})
	} else {
		r.Add(Check{ID: "theme-json.version", Title: `theme.json has "version": 1`, Status: StatusPass})
	}

	// settings sub-sections — each is advisory (NOTE) if missing, PASS if present.
	checkThemeJSONSection(r, meta.Settings, "color")
	checkThemeJSONSection(r, meta.Settings, "typography")
	checkThemeJSONSection(r, meta.Settings, "spacing")

	// templateParts and customTemplates declared in theme.json must reference
	// either a file on disk (classic) or a JSON template (block). We treat
	// a missing referenced file as a FAIL — the renderer would 404 at runtime.
	for _, tp := range meta.TemplateParts {
		if tp.Name == "" {
			continue
		}
		if !templateOrPartFileExists(dir, "parts", tp.Name) {
			r.Add(Check{
				ID:      "theme-json.template-part:" + tp.Name,
				Title:   fmt.Sprintf("templateParts entry %q has a backing file", tp.Name),
				Status:  StatusFail,
				Message: fmt.Sprintf("no parts/%s.{tsx,jsx,ts,js,json} on disk", tp.Name),
			})
		} else {
			r.Add(Check{
				ID:     "theme-json.template-part:" + tp.Name,
				Title:  fmt.Sprintf("templateParts entry %q has a backing file", tp.Name),
				Status: StatusPass,
			})
		}
	}
	for _, ct := range meta.CustomTemplates {
		if ct.Name == "" {
			continue
		}
		if !templateOrPartFileExists(dir, "templates", ct.Name) {
			r.Add(Check{
				ID:      "theme-json.custom-template:" + ct.Name,
				Title:   fmt.Sprintf("customTemplates entry %q has a backing file", ct.Name),
				Status:  StatusFail,
				Message: fmt.Sprintf("no templates/%s.{tsx,jsx,ts,js,json} on disk", ct.Name),
			})
		} else {
			r.Add(Check{
				ID:     "theme-json.custom-template:" + ct.Name,
				Title:  fmt.Sprintf("customTemplates entry %q has a backing file", ct.Name),
				Status: StatusPass,
			})
		}
	}

	return meta
}

// checkThemeJSONSection adds a row for one of the canonical settings groups.
func checkThemeJSONSection(r *Report, settings map[string]any, key string) {
	if settings == nil {
		r.Add(Check{
			ID:      "theme-json.settings." + key,
			Title:   fmt.Sprintf(`theme.json settings.%s declared`, key),
			Status:  StatusNote,
			Message: `"settings" block is empty; tokens will come from host defaults`,
		})
		return
	}
	if _, ok := settings[key]; ok {
		r.Add(Check{
			ID:     "theme-json.settings." + key,
			Title:  fmt.Sprintf(`theme.json settings.%s declared`, key),
			Status: StatusPass,
		})
		return
	}
	r.Add(Check{
		ID:      "theme-json.settings." + key,
		Title:   fmt.Sprintf(`theme.json settings.%s declared`, key),
		Status:  StatusNote,
		Message: fmt.Sprintf(`settings.%s not declared; theme will inherit host defaults`, key),
	})
}

// templateExtensions is the set of extensions we accept for templates and parts.
// `.tsx` is canonical for classic themes; `.json` for block themes. The
// `.jsx`/`.ts`/`.js` cases support JS-only authors; `.html` is supported as a
// minimal entrypoint so authors can ship a static placeholder while wiring
// the rest of a theme.
var templateExtensions = []string{".tsx", ".jsx", ".ts", ".js", ".json", ".html"}

func templateOrPartFileExists(dir, sub, name string) bool {
	for _, ext := range templateExtensions {
		if _, err := os.Stat(filepath.Join(dir, sub, name+ext)); err == nil {
			return true
		}
	}
	return false
}

// checkTemplateHierarchy verifies the mandatory entry-point (index) exists
// and reports presence/absence of canonical fallback templates. Only the
// mandatory entry is a FAIL; the rest are NOTEs so authors see which
// hierarchy branches will resolve.
func checkTemplateHierarchy(r *Report, dir string, _ packageMeta, _ themeJSONMeta) {
	templatesDir := filepath.Join(dir, "templates")
	if _, err := os.Stat(templatesDir); err != nil {
		r.Add(Check{
			ID:      "templates.dir",
			Title:   "templates/ directory present",
			Status:  StatusFail,
			Message: `templates/ directory not found (required per docs/03-theme-system.md §2)`,
		})
		return
	}
	r.Add(Check{ID: "templates.dir", Title: "templates/ directory present", Status: StatusPass})

	// index is the ultimate fallback and is mandatory.
	if templateOrPartFileExists(dir, "templates", "index") {
		r.Add(Check{ID: "templates.index", Title: "templates/index.{tsx,json,html} present", Status: StatusPass})
	} else {
		r.Add(Check{
			ID:      "templates.index",
			Title:   "templates/index.{tsx,json,html} present",
			Status:  StatusFail,
			Message: "every theme MUST ship templates/index — docs/03-theme-system.md §2",
		})
	}

	// Canonical fallback templates listed in docs/03-theme-system.md §4.2.
	// Reported advisory; absence is not a failure.
	canonical := []string{
		"front-page", "home",
		"single", "singular", "page",
		"archive", "category", "tag",
		"author", "date", "search", "404",
	}
	for _, name := range canonical {
		if templateOrPartFileExists(dir, "templates", name) {
			r.Add(Check{
				ID:     "templates.canonical:" + name,
				Title:  fmt.Sprintf("canonical template %q present", name),
				Status: StatusPass,
			})
		} else {
			r.Add(Check{
				ID:      "templates.canonical:" + name,
				Title:   fmt.Sprintf("canonical template %q present", name),
				Status:  StatusNote,
				Message: fmt.Sprintf("templates/%s.* not found; the hierarchy will fall back to index", name),
			})
		}
	}
}

// checkParts inspects parts/ — fully optional but conventionally contains
// header.tsx and footer.tsx (docs/03-theme-system.md §5).
func checkParts(r *Report, dir string) {
	partsDir := filepath.Join(dir, "parts")
	if _, err := os.Stat(partsDir); err != nil {
		r.Add(Check{
			ID:      "parts.dir",
			Title:   "parts/ directory present",
			Status:  StatusNote,
			Message: "parts/ is optional; theme exposes no shared composition units",
		})
		return
	}
	r.Add(Check{ID: "parts.dir", Title: "parts/ directory present", Status: StatusPass})
	for _, name := range []string{"header", "footer"} {
		if templateOrPartFileExists(dir, "parts", name) {
			r.Add(Check{
				ID:     "parts.canonical:" + name,
				Title:  fmt.Sprintf("conventional part %q present", name),
				Status: StatusPass,
			})
		} else {
			r.Add(Check{
				ID:      "parts.canonical:" + name,
				Title:   fmt.Sprintf("conventional part %q present", name),
				Status:  StatusNote,
				Message: fmt.Sprintf("parts/%s.* not found; templates will inline the equivalent markup", name),
			})
		}
	}
}

// checkBlockVsClassic compares declared package.json `gonext.type` against
// on-disk template extensions. Templates ending in `.json` indicate block
// themes; `.tsx`/`.jsx`/`.ts`/`.js` indicate classic. Mixed → NOTE.
// Disagreement between declaration and disk → FAIL.
func checkBlockVsClassic(r *Report, dir string, pkg packageMeta) {
	templatesDir := filepath.Join(dir, "templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		// Already reported by checkTemplateHierarchy; skip silently.
		return
	}
	var hasBlock, hasClassic bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".json":
			hasBlock = true
		case ".tsx", ".jsx", ".ts", ".js":
			hasClassic = true
		}
	}

	detected := "unknown"
	switch {
	case hasBlock && hasClassic:
		detected = "mixed"
	case hasBlock:
		detected = "block"
	case hasClassic:
		detected = "classic"
	}

	declared := strings.ToLower(pkg.GoNext.Type)
	switch {
	case declared == "":
		// package.json check already reported the missing type as FAIL.
		r.Add(Check{
			ID:      "theme.kind-detected",
			Title:   "block-vs-classic kind matches templates/",
			Status:  StatusSkip,
			Message: fmt.Sprintf("on-disk detection: %s; package.json did not declare a type", detected),
		})
	case detected == "mixed":
		r.Add(Check{
			ID:      "theme.kind-detected",
			Title:   "block-vs-classic kind matches templates/",
			Status:  StatusNote,
			Message: fmt.Sprintf("templates/ contains both .json and .tsx/.jsx/.ts/.js entries; declared %q", declared),
		})
	case detected == "unknown":
		r.Add(Check{
			ID:      "theme.kind-detected",
			Title:   "block-vs-classic kind matches templates/",
			Status:  StatusNote,
			Message: "templates/ contains no recognised template files",
		})
	case detected != declared:
		r.Add(Check{
			ID:      "theme.kind-detected",
			Title:   "block-vs-classic kind matches templates/",
			Status:  StatusFail,
			Message: fmt.Sprintf("package.json declares type %q but templates/ contains %s templates", declared, detected),
		})
	default:
		r.Add(Check{
			ID:     "theme.kind-detected",
			Title:  "block-vs-classic kind matches templates/",
			Status: StatusPass,
		})
	}
}

// checkReserved emits the deterministic SKIP rows for the §6.1 checks that
// require a runtime we don't have yet. Keeping these in the report shape
// means CI gates and marketplace ingest get a stable set of row IDs to
// pin against; flipping each one to PASS/FAIL is just a question of
// landing the runtime, not changing the runner shape.
func checkReserved(r *Report) {
	reserved := []struct {
		id, title, why string
	}{
		{
			id:    "runtime.render",
			title: "Template hierarchy fallback resolves for every route class",
			why:   "theme runtime (React renderer) not yet available; tracked in docs/03-theme-system.md §13",
		},
		{
			id:    "runtime.block-variations",
			title: "Block style variations render without React warnings",
			why:   "block renderer not yet wired",
		},
		{
			id:    "runtime.a11y",
			title: "Accessibility scan: zero serious/critical violations on canonical templates",
			why:   "axe-core runner depends on the theme renderer",
		},
		{
			id:    "runtime.bundle-budget",
			title: "Per-template JS bundle within budget (docs/07 §21)",
			why:   "bundle compiler not yet wired",
		},
		{
			id:    "runtime.ssr-parity",
			title: "Two SSR renders with identical inputs produce byte-identical output",
			why:   "depends on runtime.render",
		},
	}
	for _, c := range reserved {
		r.Add(Check{ID: c.id, Title: c.title, Status: StatusSkip, Message: c.why})
	}
}
