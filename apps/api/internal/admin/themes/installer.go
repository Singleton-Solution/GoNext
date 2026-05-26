package themes

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// MaxThemeZipSize caps the upload at 10 MiB. The largest realistic
// theme (templates + parts + a hand-tuned screenshot.png) lands well
// under 1 MiB; the cap is "stop an operator from accidentally
// uploading a node_modules tarball" rather than a tight bound.
const MaxThemeZipSize = 10 * 1024 * 1024

// MaxThemeFileSize caps each entry inside the ZIP at 2 MiB. The same
// "stop a runaway upload" rationale as MaxThemeZipSize, applied per
// entry so a single file can't blow past the bound by hiding inside
// the archive.
const MaxThemeFileSize = 2 * 1024 * 1024

// MaxThemeFiles caps the number of entries the ZIP may carry. Themes
// in the wild ship a few dozen files; 500 is comfortable headroom
// without becoming a vector for zip-bomb-style inode exhaustion.
const MaxThemeFiles = 500

// slugPattern is the regex the installer enforces on the theme's
// chosen directory name. It is the same kebab-case alphabet
// theme.json's slug validation uses — keeping the two in lock-step
// means a slug that survives the manifest validator also survives
// the directory-write step.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}[a-z0-9]$`)

// Errors returned by Install. Callers errors.Is against these
// sentinels rather than string-matching the wrapped error text.
var (
	// ErrZipMissingManifest fires when the upload contains no
	// theme.json at the root of the archive (or at the root of its
	// single top-level directory). Translates to HTTP 400 at the
	// handler.
	ErrZipMissingManifest = errors.New("themes: archive has no theme.json")

	// ErrInvalidManifest fires when theme.json parses but fails
	// theme.Validate. The full validation list is attached as a
	// wrapped error on the InstallResult.
	ErrInvalidManifest = errors.New("themes: manifest validation failed")

	// ErrInvalidSlug fires when the resolved theme slug (directory
	// name inside the archive, falling back to the manifest title)
	// doesn't match slugPattern.
	ErrInvalidSlug = errors.New("themes: invalid slug")

	// ErrThemeExists fires when a theme with the resolved slug
	// already lives on disk under themeDir. The installer is
	// intentionally non-clobbering so an operator can't overwrite a
	// hand-edited theme by re-uploading.
	ErrThemeExists = errors.New("themes: theme already installed")

	// ErrUnsafePath fires when an entry in the archive resolves
	// outside the destination directory (path traversal attempt) or
	// uses an absolute path. The installer aborts before writing
	// anything when this triggers.
	ErrUnsafePath = errors.New("themes: archive contains unsafe path")

	// ErrEntryTooLarge fires when a single entry inside the archive
	// exceeds MaxThemeFileSize on decompression.
	ErrEntryTooLarge = errors.New("themes: archive entry exceeds size limit")

	// ErrTooManyFiles fires when the archive carries more than
	// MaxThemeFiles entries.
	ErrTooManyFiles = errors.New("themes: archive has too many entries")
)

// InstallResult is the success payload returned from Install. It
// carries the slug under which the theme landed plus the parsed
// manifest so the handler can render an immediate confirmation.
type InstallResult struct {
	Slug     string
	Manifest *theme.ThemeJSON
}

// Install extracts a .gntheme ZIP archive into themeDir, validating
// the embedded theme.json before writing anything to disk. The
// resolved slug is the basename of the single top-level directory
// inside the archive when one exists, falling back to the manifest's
// title slug.
//
// The function is fail-closed: any validation failure aborts the
// installation before files land on disk. Successful installs are
// committed via a rename from a temp directory into the final
// destination, so a concurrent reader of themeDir either sees the
// whole theme or none of it (no partial-write window).
//
// data is the raw ZIP bytes (the handler reads them off the
// multipart upload). themeDir is the absolute path of the themes
// directory.
func Install(themeDir string, data []byte) (*InstallResult, error) {
	if themeDir == "" {
		return nil, errors.New("themes: empty themeDir")
	}
	if int64(len(data)) > MaxThemeZipSize {
		return nil, fmt.Errorf("themes: upload exceeds %d bytes", MaxThemeZipSize)
	}
	r, err := zip.NewReader(bytesReaderAt(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("themes: open zip: %w", err)
	}
	if len(r.File) > MaxThemeFiles {
		return nil, ErrTooManyFiles
	}

	// Find theme.json. We accept either of two layouts:
	//  - flat: theme.json at the root of the archive
	//  - nested: <slug>/theme.json with every other file under that
	// prefix
	manifestEntry, prefix, findErr := findManifest(r.File)
	if findErr != nil {
		return nil, findErr
	}
	manifestBytes, err := readZipEntry(manifestEntry)
	if err != nil {
		return nil, fmt.Errorf("themes: read manifest: %w", err)
	}
	manifest, err := theme.Parse(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("themes: parse manifest: %w", err)
	}
	if errs := manifest.Validate(); len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidManifest, strings.Join(msgs, "; "))
	}

	slug := resolveSlug(prefix, manifest)
	if !slugPattern.MatchString(slug) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSlug, slug)
	}

	dest := filepath.Join(themeDir, slug)
	if _, statErr := os.Stat(dest); statErr == nil {
		return nil, fmt.Errorf("%w: %q", ErrThemeExists, slug)
	}

	// Extract to a sibling temp directory, then rename into place.
	// Filepath.TempDir in the parent gives us atomic rename
	// semantics on the same filesystem.
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		return nil, fmt.Errorf("themes: ensure dir: %w", err)
	}
	staging, err := os.MkdirTemp(themeDir, ".install-")
	if err != nil {
		return nil, fmt.Errorf("themes: temp dir: %w", err)
	}
	// On any failure past this point, sweep the staging dir.
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	for _, f := range r.File {
		if err := writeZipEntry(f, prefix, staging); err != nil {
			return nil, err
		}
	}

	if err := os.Rename(staging, dest); err != nil {
		return nil, fmt.Errorf("themes: rename to %s: %w", dest, err)
	}
	committed = true
	return &InstallResult{Slug: slug, Manifest: manifest}, nil
}

// findManifest scans the archive for theme.json. We tolerate both
// the flat layout and the single-top-level-dir layout; anything else
// (theme.json buried two levels deep, or no theme.json at all) is a
// packaging error.
//
// Returns the zip entry, the directory prefix to strip from every
// other entry, and an error.
func findManifest(files []*zip.File) (*zip.File, string, error) {
	var flat *zip.File
	var nested *zip.File
	var nestedPrefix string
	for _, f := range files {
		name := path.Clean(f.Name)
		if name == "theme.json" {
			flat = f
			continue
		}
		// Match "<one-segment>/theme.json".
		if strings.HasSuffix(name, "/theme.json") {
			parts := strings.SplitN(name, "/", 2)
			if len(parts) == 2 && parts[1] == "theme.json" && !strings.Contains(parts[0], "/") {
				// First match wins; a malformed archive with two
				// nested manifests is rejected by falling through
				// the loop without flagging an ambiguity error —
				// the second one (alphabetical zip-order) is
				// ignored.
				if nested == nil {
					nested = f
					nestedPrefix = parts[0] + "/"
				}
			}
		}
	}
	if flat != nil {
		return flat, "", nil
	}
	if nested != nil {
		return nested, nestedPrefix, nil
	}
	return nil, "", ErrZipMissingManifest
}

// resolveSlug picks a slug for the new theme directory. The
// archive's top-level directory is the primary signal (operators
// expect the directory they zipped to be the directory they get
// back); we fall back to the manifest's title — slugified — when the
// archive was flat.
func resolveSlug(prefix string, manifest *theme.ThemeJSON) string {
	if prefix != "" {
		return strings.TrimSuffix(prefix, "/")
	}
	if manifest.Title != "" {
		return slugifyTitle(manifest.Title)
	}
	return ""
}

// slugifyTitle lowercases + replaces runs of non-alphanumeric with a
// single hyphen. Mirrors the kebab-case validator in the theme
// package so the produced slug round-trips through slugPattern.
func slugifyTitle(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// readZipEntry reads up to MaxThemeFileSize bytes from a zip entry.
// Anything past that limit signals a potential zip bomb (or an
// operator who packaged a theme with a multi-megabyte hero image);
// we error rather than truncating silently.
func readZipEntry(f *zip.File) ([]byte, error) {
	if int64(f.UncompressedSize64) > MaxThemeFileSize {
		return nil, fmt.Errorf("%w: %s (%d bytes)", ErrEntryTooLarge, f.Name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	limited := io.LimitReader(rc, MaxThemeFileSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > MaxThemeFileSize {
		return nil, fmt.Errorf("%w: %s", ErrEntryTooLarge, f.Name)
	}
	return body, nil
}

// writeZipEntry extracts a single archive entry into staging. We
// strip the optional top-level prefix, enforce a path-traversal
// guard, refuse symlinks (mode&os.ModeSymlink), and create parent
// directories on demand.
func writeZipEntry(f *zip.File, prefix, staging string) error {
	name := f.Name
	if prefix != "" {
		if !strings.HasPrefix(name, prefix) {
			// An entry outside the top-level directory in a
			// nested-layout zip — refuse so we don't sprinkle
			// random files across the staging root.
			return fmt.Errorf("%w: %s outside prefix %q", ErrUnsafePath, name, prefix)
		}
		name = strings.TrimPrefix(name, prefix)
	}
	if name == "" {
		// The entry IS the top-level directory; nothing to write.
		return nil
	}
	// Refuse absolute or traversal paths defensively. filepath.Clean
	// + abs/leading-".." check is the standard "zip slip" defense.
	clean := path.Clean(name)
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return fmt.Errorf("%w: %s", ErrUnsafePath, f.Name)
	}
	target := filepath.Join(staging, filepath.FromSlash(clean))
	rel, err := filepath.Rel(staging, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%w: %s", ErrUnsafePath, f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	// Refuse symlinks — they're a path-traversal vector even with
	// the guard above (an attacker could ship "config -> /etc/shadow"
	// and read it via a subsequent renderer load).
	if f.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s (symlink)", ErrUnsafePath, f.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body, err := readZipEntry(f)
	if err != nil {
		return err
	}
	return os.WriteFile(target, body, 0o644)
}

// bytesReaderAt is the minimal io.ReaderAt over a byte slice; the
// stdlib's bytes.NewReader already provides ReadAt but we avoid the
// extra import by wrapping inline.
type bytesReaderAt []byte

// ReadAt implements io.ReaderAt.
func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
