package secrets

import (
	"os"
	"path/filepath"
)

// writeTestFile is a small helper shared across test files. It writes a
// file under dir with mode 0600 (so accidental world-readable test fixtures
// don't fool a reader into thinking we're sloppy about real secret files).
func writeTestFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
}
