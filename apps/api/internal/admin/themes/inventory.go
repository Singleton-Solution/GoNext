package themes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// ThemeInfo is the per-theme record returned by ListInstalled. It is
// the minimum the switcher UI needs: machine slug + display title +
// one-line description + whether a screenshot.png exists alongside
// the manifest. The full manifest is intentionally omitted here so
// the list endpoint stays cheap; the customizer's /active endpoint
// is the place to fetch the full ThemeJSON for the current theme.
type ThemeInfo struct {
	// Slug is the on-disk directory name. It is also the stable
	// identifier the active-theme option stores and the activate
	// endpoint accepts.
	Slug string `json:"slug"`

	// Title is the human-readable theme name read from theme.json.
	// Falls back to the slug when the manifest doesn't declare one.
	Title string `json:"title"`

	// Description is a short summary surfaced in the switcher card.
	// Currently always empty (theme.json doesn't carry a description
	// field in v1) — kept on the wire shape so a future manifest key
	// lands without a UI break.
	Description string `json:"description,omitempty"`

	// Version is the manifest schema version (currently always 1).
	// Surfaced so the switcher can warn when a theme is on an
	// unsupported schema.
	Version int `json:"version"`

	// HasScreenshot reports whether a screenshot.png file sits next
	// to the manifest. The UI uses this to decide between rendering
	// a real preview image and a CSS scene placeholder.
	HasScreenshot bool `json:"has_screenshot"`
}

// ListInstalled walks themeDir and returns every subdirectory whose
// theme.json parses cleanly. Themes whose manifest is missing or
// malformed are skipped silently (we don't want one bad theme to
// blank the entire switcher); the caller can run the validator
// independently if it wants to surface those.
//
// The result is sorted by slug so the order is stable across calls
// — operators rely on the gallery not shuffling between page loads.
func ListInstalled(_ context.Context, themeDir string) ([]ThemeInfo, error) {
	if themeDir == "" {
		return nil, errors.New("themes: empty themeDir")
	}
	entries, err := os.ReadDir(themeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A fresh deploy with no themes installed isn't an error
			// — it's an empty switcher. The seeder normally creates
			// the directory + the gn-hello theme on first boot.
			return []ThemeInfo{}, nil
		}
		return nil, fmt.Errorf("themes: read %s: %w", themeDir, err)
	}

	out := make([]ThemeInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Directories starting with a dot are operator-private (e.g.
		// editor scratch dirs) — skip them so they don't pollute the
		// gallery.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		slug := e.Name()
		manifestPath := filepath.Join(themeDir, slug, "theme.json")
		data, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			// Missing manifest = not a theme directory; skip without
			// noise. Permission errors fall here too; the operator
			// can fix the chmod and the theme will appear on next
			// list.
			continue
		}
		manifest, parseErr := theme.Parse(data)
		if parseErr != nil {
			// Malformed manifest = skip, see comment above.
			continue
		}
		_, hasScreenshot := os.Stat(filepath.Join(themeDir, slug, "screenshot.png"))
		title := manifest.Title
		if title == "" {
			title = slug
		}
		out = append(out, ThemeInfo{
			Slug:          slug,
			Title:         title,
			Version:       manifest.Version,
			HasScreenshot: hasScreenshot == nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// ThemeInstalled reports whether a theme directory with the given
// slug ships a parseable theme.json. Used by the activate handler to
// validate "you can switch to this slug" before writing the option.
func ThemeInstalled(themeDir, slug string) bool {
	if themeDir == "" || slug == "" {
		return false
	}
	manifestPath := filepath.Join(themeDir, slug, "theme.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	if _, err := theme.Parse(data); err != nil {
		return false
	}
	return true
}
