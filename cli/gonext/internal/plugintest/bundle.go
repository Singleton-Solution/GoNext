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
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("open zip %q: %w", p, err)
	}
	return &Bundle{Path: p, fsys: &zr.Reader, closer: zr}, nil
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
func (b *Bundle) ReadManifest() ([]byte, error) {
	f, err := b.fsys.Open(manifestFilename)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestFilename, err)
	}
	defer f.Close()
	return io.ReadAll(f)
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
	return io.ReadAll(f)
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
