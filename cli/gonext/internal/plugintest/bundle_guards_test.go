// Tests for the structural guards added in issue #27 — bundle size cap,
// per-entry size cap, entry-count cap, and zip-slip path rejection.
//
// Each test constructs a hostile fixture (oversize archive, zip bomb,
// traversal entry, etc.) and asserts OpenBundle refuses it with the
// matching sentinel error. The fixtures are inline rather than
// committed binaries so reviewers can see exactly what's being
// attempted without diffing through a hex dump.
package plugintest

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRawZip is the low-level helper for the guard tests — it lets us
// produce archives that would not normally survive writeBundleZip's
// well-formed assumptions (e.g. no manifest, traversal names,
// oversize declared entries).
func writeRawZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.gnplugin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}

// TestOpenBundle_RejectsOversizeFile asserts the bundle-level cap kicks
// in BEFORE any zip parsing. The fixture is a 50-MiB+1 file of zeros
// with a .gnplugin extension — it's not a valid zip at all, but the
// size guard should reject it before zip.OpenReader is called.
func TestOpenBundle_RejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.gnplugin")
	// Use SeekFile semantics — write a single byte at offset
	// MaxBundleSize+1 so the file claims that size without us
	// burning 50 MiB of test memory.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Seek(MaxBundleSize, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = OpenBundle(path)
	if err == nil {
		t.Fatal("expected ErrBundleTooLarge; got nil")
	}
	if !errors.Is(err, ErrBundleTooLarge) {
		t.Fatalf("expected ErrBundleTooLarge; got %v", err)
	}
}

// TestOpenBundle_RejectsTooManyEntries verifies the entry-count cap. We
// build a zip with MaxEntryCount+1 tiny files; each is well within the
// per-entry cap but the cardinality alone should reject the bundle.
func TestOpenBundle_RejectsTooManyEntries(t *testing.T) {
	files := make(map[string][]byte, MaxEntryCount+1)
	// First entry is a valid manifest so the test fixture would pass
	// every OTHER check — only the entry count is wrong.
	files["manifest.json"] = []byte(`{"slug":"x","version":"0.0.1","abi_version":1}`)
	for i := 0; i < MaxEntryCount; i++ {
		files[name("f", i)] = []byte("x")
	}
	path := writeRawZip(t, files)
	_, err := OpenBundle(path)
	if err == nil {
		t.Fatal("expected ErrBundleTooManyEntries; got nil")
	}
	if !errors.Is(err, ErrBundleTooManyEntries) {
		t.Fatalf("expected ErrBundleTooManyEntries; got %v", err)
	}
}

// TestOpenBundle_RejectsOversizeEntry verifies the per-entry cap. The
// fixture declares an entry whose uncompressed size exceeds
// MaxEntrySize — this is the classic zip-bomb shape (small compressed
// payload, huge declared size).
func TestOpenBundle_RejectsOversizeEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bomb.gnplugin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(f)
	// Write a single highly-compressible entry that exceeds the cap.
	// We don't use raw mode (which would lie about the size); we just
	// write MaxEntrySize+1 zeros and let deflate compress them down
	// to a few bytes. The resulting central directory faithfully
	// reports the uncompressed size, which is what our guard checks.
	w, err := zw.Create("bomb.bin")
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	chunk := bytes.Repeat([]byte{0}, 1<<16)
	written := int64(0)
	target := MaxEntrySize + 1
	for written < target {
		n := int64(len(chunk))
		if written+n > target {
			n = target - written
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatalf("write: %v", err)
		}
		written += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = OpenBundle(path)
	if err == nil {
		t.Fatal("expected ErrBundleEntryTooLarge; got nil")
	}
	if !errors.Is(err, ErrBundleEntryTooLarge) {
		t.Fatalf("expected ErrBundleEntryTooLarge; got %v", err)
	}
}

// TestOpenBundle_RejectsTraversalPath verifies the zip-slip guard. Each
// sub-test exercises a different shape of traversal — leading "..",
// embedded "..", absolute path, Windows-style backslash absolute, and
// Windows-style backslash traversal. All should reject with
// ErrBundleUnsafePath.
func TestOpenBundle_RejectsTraversalPath(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"leading-dotdot", "../etc/passwd"},
		{"embedded-dotdot", "ok/../../etc/passwd"},
		{"absolute-unix", "/etc/passwd"},
		{"absolute-windows", `\Windows\System32\config\SAM`},
		{"backslash-traversal", `..\etc\passwd`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{
				"manifest.json": []byte(`{"slug":"x","version":"0.0.1","abi_version":1}`),
				tc.entry:        []byte("payload"),
			}
			path := writeRawZip(t, files)
			_, err := OpenBundle(path)
			if err == nil {
				t.Fatalf("expected ErrBundleUnsafePath for %q; got nil", tc.entry)
			}
			if !errors.Is(err, ErrBundleUnsafePath) {
				t.Fatalf("expected ErrBundleUnsafePath for %q; got %v", tc.entry, err)
			}
		})
	}
}

// TestOpenBundle_HappyPath confirms the new guards don't reject a
// well-formed bundle. This is the regression check: the guards should
// be invisible to honest plugins.
func TestOpenBundle_HappyPath(t *testing.T) {
	path := writeBundleZip(t,
		[]byte(`{"slug":"ok","version":"0.0.1","abi_version":1}`),
		[]byte("\x00asm\x01\x00\x00\x00"),
	)
	b, err := OpenBundle(path)
	if err != nil {
		t.Fatalf("expected happy-path bundle to open; got %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if got, err := b.ReadManifest(); err != nil {
		t.Fatalf("ReadManifest: %v", err)
	} else if !strings.Contains(string(got), `"slug":"ok"`) {
		t.Fatalf("manifest contents lost: %q", got)
	}
}

// TestReadCapped_RejectsOversize verifies the LimitReader-backed
// ReadManifest/ReadWASM guard rejects a directory-bundle file whose
// contents exceed MaxEntrySize. The zip-central-directory check
// can't see directory bundles, so this is the only line of defence
// for the directory-form bundle path.
func TestReadCapped_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	// MaxEntrySize is 10 MiB; write 10 MiB + 1 bytes so the cap
	// trips. We seek-and-write to avoid burning 10 MiB of test
	// memory on a sentinel buffer.
	mf := filepath.Join(dir, "manifest.json")
	f, err := os.Create(mf)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Seek(MaxEntrySize, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	b, err := OpenBundle(dir)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	_, err = b.ReadManifest()
	if err == nil {
		t.Fatal("expected ErrBundleEntryTooLarge; got nil")
	}
	if !errors.Is(err, ErrBundleEntryTooLarge) {
		t.Fatalf("expected ErrBundleEntryTooLarge; got %v", err)
	}
}

// name is a 1-line helper that produces a unique zip entry name —
// keeping the inline maps in TestOpenBundle_RejectsTooManyEntries
// readable.
func name(prefix string, n int) string {
	// We use a 6-digit pad so the entry names are well-distributed
	// in the central directory (zip puts them in insertion order;
	// stable names just make a failing diff easier to read).
	const pad = "000000"
	s := stringInt(n)
	if len(s) < len(pad) {
		s = pad[len(s):] + s
	}
	return prefix + "_" + s
}

// stringInt is a tiny strconv.Itoa stand-in — we avoid pulling in
// strconv at the top of this test file just for one call site.
func stringInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
