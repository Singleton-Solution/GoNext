package plugintest

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// manifestFilename is the conventional manifest name inside a `.gnplugin`
// bundle (see docs/02-plugin-system.md §2.1).
const manifestFilename = "manifest.json"

// defaultWASMPath is the manifest-declared default for the server module.
// Used only as a fallback when the manifest itself doesn't specify
// `server.wasm` — the bundle layout in §2.1 places the module at
// `server/plugin.wasm`.
const defaultWASMPath = "server/plugin.wasm"

// Bundle hard caps. These guard the .gnplugin loader against pathological
// archives — zip bombs, traversal attacks, accidental dumps of a whole
// `node_modules`. See issue #27 (bundle hardening). The numbers mirror
// docs/02-plugin-system.md §2.1: 50 MiB total, 10 MiB per entry, 10k
// entries. Each is a hard ceiling — exceeding any of the three rejects
// the bundle before any further work happens (Read, Stat, manifest
// parse).
const (
	// MaxBundleSize caps the total compressed-on-disk size of a
	// `.gnplugin` archive at 50 MiB. The cap is checked against
	// os.Stat(Path).Size() before the zip reader ever touches the
	// file; a hostile archive cannot trick the loader into reading
	// more than this off disk.
	MaxBundleSize int64 = 50 * 1024 * 1024

	// MaxEntrySize caps the uncompressed size of any single zip entry
	// at 10 MiB. The cap defends against zip-bomb decompression —
	// a 1 KB compressed entry that expands to 4 GiB is a textbook
	// DoS vector. Callers reading entries via Bundle.FS() are
	// expected to also LimitReader to MaxEntrySize; the entry-size
	// guard here ensures the declared uncompressed size never
	// exceeds the cap even if the caller forgets.
	MaxEntrySize int64 = 10 * 1024 * 1024

	// MaxEntryCount caps the number of files inside a bundle at
	// 10,000. The cap is the third leg of the zip-bomb defence:
	// a 0-byte archive with a million entries would otherwise
	// exhaust the file-descriptor pool inside archive/zip.
	MaxEntryCount = 10_000
)

// ErrBundleTooLarge is returned when the archive on disk exceeds
// [MaxBundleSize]. Sentinel rather than an inline fmt.Errorf so callers
// can branch on the failure mode (e.g. to surface a friendlier "your
// plugin is too big" message in the admin UI vs. a generic parser
// failure).
var ErrBundleTooLarge = errors.New("plugintest: bundle exceeds size cap")

// ErrBundleEntryTooLarge is returned when any single entry's declared
// uncompressed size exceeds [MaxEntrySize].
var ErrBundleEntryTooLarge = errors.New("plugintest: bundle entry exceeds size cap")

// ErrBundleTooManyEntries is returned when the archive carries more
// than [MaxEntryCount] entries.
var ErrBundleTooManyEntries = errors.New("plugintest: bundle exceeds entry count cap")

// ErrBundleUnsafePath is returned when any entry's name contains a
// path-traversal segment (".." anywhere in the cleaned path) or is
// absolute (leading "/"). The zip format permits these names but a
// well-behaved bundle never uses them; rejecting at parse time
// prevents a zip-slip extract step downstream from clobbering files
// outside the bundle root.
var ErrBundleUnsafePath = errors.New("plugintest: bundle entry has unsafe path")

// Bundle is a read-only view over a plugin bundle backed by either a
// directory on disk or an opened zip archive.
//
// The zero value is not usable — use [OpenBundle] to construct one.
type Bundle struct {
	// Path is the original argument passed to [OpenBundle]. Used for
	// diagnostic messages only.
	Path string

	// fsys is the filesystem the bundle contents live under. For a
	// directory bundle, this is rooted at Path. For a `.gnplugin` zip,
	// this is the zip's [fs.FS] view.
	fsys fs.FS

	// closer is the underlying [io.Closer] for zip-backed bundles, or nil
	// for directory bundles.
	closer io.Closer
}

// OpenBundle opens a plugin bundle from a filesystem path. The argument may
// be either:
//
//   - A directory containing the unpacked bundle layout (manifest.json at
//     the top level).
//   - A regular file with extension `.gnplugin` or `.zip`, which is opened
//     as a zip archive.
//
// The caller is responsible for calling [Bundle.Close].
func OpenBundle(p string) (*Bundle, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("open bundle %q: %w", p, err)
	}
	if st.IsDir() {
		// Use os.DirFS rooted at the bundle directory. fs.FS paths inside
		// the bundle are then relative ("manifest.json", "server/plugin.wasm").
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve bundle dir: %w", err)
		}
		return &Bundle{Path: p, fsys: os.DirFS(abs)}, nil
	}
	// Treat as a zip-backed archive.
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".gnplugin" && ext != ".zip" {
		return nil, fmt.Errorf("open bundle %q: unsupported extension %q (want directory, .gnplugin, or .zip)", p, ext)
	}
	// Reject anything larger than MaxBundleSize before we even open
	// the zip. archive/zip itself doesn't enforce a cap; without this
	// a multi-gigabyte file would consume FDs and CPU on the central
	// directory scan before we got to validate anything else.
	if st.Size() > MaxBundleSize {
		return nil, fmt.Errorf("open bundle %q: %w (size=%d, cap=%d)",
			p, ErrBundleTooLarge, st.Size(), MaxBundleSize)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("open zip %q: %w", p, err)
	}
	// Walk the central directory once to enforce the entry-count,
	// per-entry-size and unsafe-path caps. Doing this upfront means
	// every later call (ReadManifest, ReadWASM, CheckLayout) is
	// already operating on a known-bounded archive — there's no
	// "did we forget to check this path?" hole. On any violation we
	// close the reader (it's owned by us at this point) and return.
	if err := validateZipEntries(zr); err != nil {
		_ = zr.Close()
		return nil, fmt.Errorf("open bundle %q: %w", p, err)
	}
	return &Bundle{Path: p, fsys: &zr.Reader, closer: zr}, nil
}

// validateZipEntries enforces the three structural caps on a freshly
// opened zip reader: entry count, per-entry size, and unsafe paths.
// All three are independent — a bundle with 11k tiny files fails
// ErrBundleTooManyEntries; a bundle with one 11 MiB file fails
// ErrBundleEntryTooLarge; a bundle with `../etc/passwd` fails
// ErrBundleUnsafePath. We return on the first violation so error
// messages stay specific.
//
// Extracted as a free function so the unit tests can drive it against
// a *zip.ReadCloser without going through OpenBundle.
func validateZipEntries(zr *zip.ReadCloser) error {
	if zr == nil {
		return errors.New("plugintest: nil zip reader")
	}
	if len(zr.File) > MaxEntryCount {
		return fmt.Errorf("%w (entries=%d, cap=%d)",
			ErrBundleTooManyEntries, len(zr.File), MaxEntryCount)
	}
	for _, f := range zr.File {
		// Reject absolute paths and path-traversal segments. We
		// inspect the *raw* name (not path.Clean'd) because cleaning
		// an absolute "/x" to "x" would silently allow it; we want
		// to reject any name a hostile author chose, not the
		// post-cleaning view.
		raw := f.Name
		if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) {
			return fmt.Errorf("%w: %q (absolute path)",
				ErrBundleUnsafePath, raw)
		}
		// Reject ".." anywhere in the path components — including
		// "foo/../bar" (which path.Clean would collapse to "bar")
		// because the COMPRESSED form on disk still encodes the
		// traversal segment and a downstream extractor that doesn't
		// call path.Clean would honor it. This is the zip-slip
		// vector documented at https://snyk.io/research/zip-slip.
		cleaned := path.Clean(raw)
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
			strings.Contains(raw, "..\\") || strings.Contains(raw, "../") {
			return fmt.Errorf("%w: %q (traversal)",
				ErrBundleUnsafePath, raw)
		}
		// Per-entry size check uses the declared uncompressed size.
		// A hostile entry that *lies* about its size will be caught
		// by the reader's CRC check at decompression time, but
		// before that the LimitReader-equivalent cap on ReadAll
		// keeps us from buffering more than MaxEntrySize bytes (see
		// readZipEntryCapped below).
		if int64(f.UncompressedSize64) > MaxEntrySize {
			return fmt.Errorf("%w: %q (size=%d, cap=%d)",
				ErrBundleEntryTooLarge, raw, f.UncompressedSize64, MaxEntrySize)
		}
	}
	return nil
}

// Close releases the underlying archive handle, if any. Directory-backed
// bundles have nothing to release and Close is a no-op.
func (b *Bundle) Close() error {
	if b == nil || b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// FS returns the read-only filesystem view of the bundle contents.
func (b *Bundle) FS() fs.FS { return b.fsys }

// ReadManifest reads the manifest bytes from the bundle. The returned slice
// is the raw JSON — callers parse and validate it via [ValidateManifest].
//
// Capped at [MaxEntrySize] via an io.LimitReader so a hostile manifest
// blob can't exhaust process memory even when the central directory
// validation has been bypassed (e.g. directory-backed bundles, where
// no central directory exists).
func (b *Bundle) ReadManifest() ([]byte, error) {
	f, err := b.fsys.Open(manifestFilename)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestFilename, err)
	}
	defer f.Close()
	return readCapped(f, manifestFilename)
}

// ReadWASM reads the WASM module bytes at the given bundle-relative path. If
// p is empty, [defaultWASMPath] is used.
func (b *Bundle) ReadWASM(p string) ([]byte, error) {
	if p == "" {
		p = defaultWASMPath
	}
	p = path.Clean(p)
	f, err := b.fsys.Open(p)
	if err != nil {
		return nil, fmt.Errorf("read wasm %q: %w", p, err)
	}
	defer f.Close()
	return readCapped(f, p)
}

// readCapped reads up to [MaxEntrySize] bytes from r and returns
// [ErrBundleEntryTooLarge] if the file is larger. We use the
// classic "+1 trick": LimitReader to cap+1, then assert len <= cap.
// A truncated read (n < cap) confirms we hit EOF cleanly; a read
// that returns cap+1 bytes tells us the source exceeded the cap.
//
// label is only used for the error message — the file path inside
// the bundle.
func readCapped(r io.Reader, label string) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, MaxEntrySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > MaxEntrySize {
		return nil, fmt.Errorf("%w: %q (cap=%d)",
			ErrBundleEntryTooLarge, label, MaxEntrySize)
	}
	return buf, nil
}

// CheckLayout verifies the bundle has the structural entries the loader will
// require: a manifest at the top level, and the WASM module at the path the
// manifest declares (or the default if unspecified). It does not parse the
// manifest beyond enough to find the WASM path.
//
// wasmPath, if non-empty, is the path declared in `server.wasm` in the
// manifest. Pass "" to fall back to [defaultWASMPath].
func (b *Bundle) CheckLayout(wasmPath string) error {
	if _, err := fs.Stat(b.fsys, manifestFilename); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("missing %s at bundle root", manifestFilename)
		}
		return fmt.Errorf("stat %s: %w", manifestFilename, err)
	}
	target := wasmPath
	if target == "" {
		target = defaultWASMPath
	}
	target = path.Clean(target)
	if _, err := fs.Stat(b.fsys, target); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("missing WASM module at %s", target)
		}
		return fmt.Errorf("stat %s: %w", target, err)
	}
	return nil
}
