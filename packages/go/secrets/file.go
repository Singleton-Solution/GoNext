package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileStore reads secrets from a directory of files named "<key>.txt".
// It targets two real-world deployments:
//
//   - Docker Secrets: the daemon mounts each secret at
//     /run/secrets/<name>. With ".txt" suffix conventions or symlinks
//     this maps cleanly.
//   - Kubernetes: a projected volume (or a regular Secret volume mount)
//     produces one file per key under a directory like /etc/secrets.
//
// Values are read fresh on every Get — no in-memory cache. This is
// deliberate: in Kubernetes, a Secret update propagates by replacing the
// file at the projected path, and a stale in-process cache would defeat
// rotation. Re-reading a small file on each call is well below the cost
// of any operation that needs a secret.
//
// The trailing newline on the value, if any, is trimmed. This matches the
// shape `echo "secret" > key.txt` and the way K8s writes secret files.
//
// FileStore is safe for concurrent use.
type FileStore struct {
	dir    string
	suffix string // ".txt" by default
}

// NewFileStore returns a FileStore rooted at dir. Keys map to files of the
// form "<dir>/<key>.txt". The directory is not validated at construction
// time so that a process can be configured against a path that doesn't
// exist yet (the orchestrator may mount the volume on start). Errors
// surface at Get time.
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir, suffix: ".txt"}
}

// Get reads <dir>/<key>.txt and returns its trimmed contents. A missing
// file or an empty file both return ErrNotFound. Other I/O errors (a
// directory permission problem, say) surface as wrapped errors that
// never include the file's contents.
func (s *FileStore) Get(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", fmt.Errorf("file secret %q: %w", key, err)
	}
	path := filepath.Join(s.dir, key+s.suffix)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("file %q: %w", key, ErrNotFound)
		}
		// Strip the value (we don't have one here, but be defensive about
		// any future wrappers): the path is fine to include since it's
		// already operator-known config.
		return "", fmt.Errorf("file secret %q: read: %w", key, err)
	}
	v := strings.TrimRight(string(b), "\r\n")
	if v == "" {
		return "", fmt.Errorf("file %q: %w", key, ErrNotFound)
	}
	return v, nil
}

// MustGet returns the value or panics with a redacted message.
func (s *FileStore) MustGet(key string) string { return mustGet(s, key) }

// validateKey rejects path-traversal attempts before they touch the file
// system. Keys are operator-controlled, so this is defence-in-depth rather
// than a primary security boundary, but it costs nothing.
func validateKey(key string) error {
	if key == "" {
		return errors.New("empty key")
	}
	if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
		return errors.New("invalid key: must not contain path separators or '..'")
	}
	return nil
}
