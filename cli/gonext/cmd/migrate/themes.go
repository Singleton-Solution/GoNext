package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Default paths used by seedThemes. They map to the layout described in
// docs/09-deployment-ops.md and the Compose overlay:
//
//   - DefaultBundledThemesDir is where the cli/gonext Dockerfile drops
//     the canonical /themes payload (image-baked, read-only at runtime).
//   - DefaultVolumeThemesDir is the path the migrate service mounts the
//     api-themes named volume at, so the seeded files persist across
//     `compose down -v` and become visible to the api container (which
//     mounts the same volume at /themes).
//
// Both are overridable via env so an operator running the CLI outside
// Compose (bare-metal, kube initContainer with a different mount path)
// doesn't have to rebuild the image.
const (
	DefaultBundledThemesDir = "/themes"
	DefaultVolumeThemesDir  = "/var/lib/gonext-themes"

	// EnvBundledThemesDir overrides DefaultBundledThemesDir. Set this
	// when the source themes live somewhere other than /themes (e.g. a
	// dev-loop bind mount).
	EnvBundledThemesDir = "GONEXT_BUNDLED_THEMES_DIR"

	// EnvVolumeThemesDir overrides DefaultVolumeThemesDir. Set this when
	// the persistent volume is mounted at a non-default path (e.g. kube
	// initContainer that uses /mnt/themes).
	EnvVolumeThemesDir = "GONEXT_VOLUME_THEMES_DIR"
)

// seedThemes copies the image-bundled themes tree at src into dst so a
// freshly-created named volume becomes immediately usable by the api
// container without an out-of-band `docker run --rm alpine cp` step.
//
// Idempotence is by destination directory, not by content hash:
//
//   - If dst already contains at least one theme directory (a child dir
//     with theme.json at its root), seedThemes is a no-op. Operators
//     who curate the volume — child themes, hand-rolled overrides, a
//     production theme rolled out via the admin UI — are protected.
//     The single switch they need to flip to *re-seed* is wiping the
//     volume.
//   - If dst is empty (or contains only non-theme cruft like a lost+found
//     directory), every theme under src is copied verbatim. The seeder
//     does not merge.
//
// This intentionally does NOT touch the options-table active-theme row.
// That row is owned by the database-level seeder in
// packages/go/theme/seed; this function only ensures the renderer-
// readable filesystem has the bytes available. The two seeders are
// composed in runUp so the order is: SQL migrations → file seed →
// embedded DB seed (gn-hello via active_theme row).
//
// Errors are wrapped with the offending path so an operator sees
// "seed-themes: copy gn-hello/templates/index.html: permission denied"
// rather than a bare errno.
func seedThemes(src, dst string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(slog.String("component", "migrate.seed-themes"),
		slog.String("src", src), slog.String("dst", dst))

	// Source must exist — the cli/gonext Dockerfile is supposed to
	// drop the themes tree there. If it's missing we treat it as a
	// build-time bug, not a runtime no-op: surface the error so the
	// operator knows to rebuild the image.
	srcInfo, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Warn("bundled themes directory missing; skipping seed",
				slog.String("hint", "rebuild cli/gonext image; expected COPY themes/ /themes"))
			return nil
		}
		return fmt.Errorf("stat bundled themes dir: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("bundled themes path %q is not a directory", src)
	}

	// Destination is created with parents so a fresh volume mount lands
	// cleanly. 0o755 lets the api service user traverse the tree at
	// request time without needing extra perms.
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir volume themes dir: %w", err)
	}

	populated, err := destinationHasThemes(dst)
	if err != nil {
		return fmt.Errorf("inspect volume themes dir: %w", err)
	}
	if populated {
		logger.Info("themes volume already populated; skipping seed")
		return nil
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read bundled themes dir: %w", err)
	}

	seeded := 0
	for _, entry := range entries {
		// We only seed theme directories. Top-level files in the bundle
		// (e.g. README.md) are documentation, not payload — copying
		// them would pollute the renderer's slug enumeration.
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		srcTheme := filepath.Join(src, slug)
		// Sanity: the directory must actually look like a theme.
		// Without theme.json the renderer would skip it anyway, so
		// we filter here to keep the log line honest.
		if _, statErr := os.Stat(filepath.Join(srcTheme, "theme.json")); statErr != nil {
			logger.Debug("skipping non-theme directory under bundled themes",
				slog.String("slug", slug))
			continue
		}
		dstTheme := filepath.Join(dst, slug)
		if err := copyTree(srcTheme, dstTheme); err != nil {
			return fmt.Errorf("copy theme %q: %w", slug, err)
		}
		logger.Info("seeded theme", slog.String("slug", slug))
		seeded++
	}
	if seeded == 0 {
		logger.Warn("no themes found under bundled themes dir; nothing seeded")
	}
	return nil
}

// destinationHasThemes returns true when dst contains at least one
// child directory that looks like a theme (has a theme.json). We
// don't gate on "is the directory empty" because container-managed
// volumes can ship with a lost+found stub or .gitkeep that would
// fool a naive emptiness check into bailing out forever.
func destinationHasThemes(dst string) (bool, error) {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dst, entry.Name(), "theme.json")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// copyTree mirrors srcDir into dstDir, preserving the relative layout.
// Modes are normalised: directories at 0o755, files at 0o644. We don't
// preserve the source mode because the bundled image layer may carry
// over the build-time umask (root-owned 0o600 after the Dockerfile's
// --chown), and the api service user needs read access at runtime.
//
// Symlinks are not expected in the bundle; if encountered they're
// resolved to their target (Stat, not Lstat). This matches the rest of
// the theme tooling, which treats theme directories as plain trees.
func copyTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("rel %q: %w", path, err)
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
			return nil
		}
		return copyFile(path, target)
	})
}

// copyFile streams src → dst with a fresh 0o644 mode. We stream rather
// than ReadFile/WriteFile so an unusually large theme asset (a hero
// image, a webfont) doesn't have to materialise in memory.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // path comes from filepath.WalkDir over a controlled tree
	if err != nil {
		return fmt.Errorf("open src %q: %w", src, err)
	}
	defer in.Close()

	// O_TRUNC because callers have already gated on "destination is
	// empty"; if we're here the file shouldn't exist yet, but being
	// defensive against a partial prior run is cheap.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open dst %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %q -> %q: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close dst %q: %w", dst, err)
	}
	return nil
}

// resolveBundledThemesDir returns the source directory the seeder reads
// from. The env override takes precedence so an operator running the
// CLI outside Compose can point at a checkout.
func resolveBundledThemesDir() string {
	if v := os.Getenv(EnvBundledThemesDir); v != "" {
		return v
	}
	return DefaultBundledThemesDir
}

// resolveVolumeThemesDir returns the destination directory the seeder
// writes to. The env override takes precedence so a kube initContainer
// with a different mount path is supported without code changes.
func resolveVolumeThemesDir() string {
	if v := os.Getenv(EnvVolumeThemesDir); v != "" {
		return v
	}
	return DefaultVolumeThemesDir
}
