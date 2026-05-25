package imgproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache stores rendered variants on local disk under a configurable
// root. Keys are derived from (assetID, canonical-spec) so a cache
// hit is a single os.Open call; misses fall through to the
// transformer.
//
// The disk cache layout is:
//
//	<root>/<assetID-prefix>/<assetID>.<canonical-spec>
//
// Where the prefix is the first two hex characters of the assetID to
// keep the directory size manageable on bucketed filesystems (a flat
// directory with millions of entries is fine on modern ext4 but
// makes `ls` and `rsync` unhappy).
//
// Cache is safe for concurrent use. Writes go through an atomic
// rename so a partial write never appears as a successful cache
// entry; readers see either the previous bytes or the new ones.
type Cache struct {
	root string
}

// CacheEntry is the metadata returned by Cache.Get on a hit. The
// Reader is owned by the caller and must be closed; Size and
// MIMEType travel separately so the HTTP handler can set
// Content-Length and Content-Type without re-decoding the body.
type CacheEntry struct {
	Reader   io.ReadCloser
	Size     int64
	MIMEType string
	ModTime  time.Time
}

// ErrCacheMiss is returned by Cache.Get when no entry exists for the
// given key. The HTTP handler treats this as a signal to invoke the
// transformer.
var ErrCacheMiss = errors.New("imgproxy: cache miss")

// NewCache returns a Cache rooted at the given directory. The
// directory is created on first write (Get on a fresh root just
// returns ErrCacheMiss). The caller is responsible for picking a
// stable root — the cache survives across process restarts.
//
// A typical wiring uses <media-root>/cache so the cache lives
// alongside the original uploads under one operator-managed disk.
func NewCache(root string) *Cache {
	return &Cache{root: root}
}

// Root returns the configured cache root. Useful for the HTTP
// handler's health endpoint or for an admin "clear cache" tool.
func (c *Cache) Root() string {
	return c.root
}

// Key returns the on-disk path for (assetID, spec). Exported so
// tests can assert the layout without re-implementing the
// derivation.
//
// The canonical spec form is used so semantically equivalent
// requests collapse to one cache entry — e.g. "h-600.w-800" and
// "w-800.h-600" both resolve to the same path.
func (c *Cache) Key(assetID string, spec Spec) string {
	// Hash the assetID to keep the bucket prefix uniform across IDs
	// that share a common prefix (e.g. UUIDs minted by the same
	// generator). Two hex characters give us 256 buckets, which keeps
	// each bucket under ~4k files for a million-asset library.
	h := sha256.Sum256([]byte(assetID))
	bucket := hex.EncodeToString(h[:1]) // 2 chars
	canonical := spec.Canonical()
	// Filename: <assetID>.<canonical>.<ext>. The extension is
	// included redundantly so an operator who pokes around with
	// `file *` gets readable output without re-parsing the canonical
	// string.
	name := assetID + "." + canonical
	return filepath.Join(c.root, bucket, name)
}

// Get looks up a cached entry. Returns ErrCacheMiss when no file
// exists at the derived key. The returned Reader is an *os.File the
// caller must Close.
//
// The handler typically calls Get, falls through to the transformer
// on miss, then calls Put with the rendered bytes. Concurrent Get
// calls for the same key may all miss the first one — the request
// coalescer (one level up) is what collapses them to a single
// transform.
func (c *Cache) Get(assetID string, spec Spec) (*CacheEntry, error) {
	if assetID == "" {
		return nil, errors.New("imgproxy.Cache.Get: assetID is empty")
	}
	path := c.Key(assetID, spec)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCacheMiss
		}
		return nil, fmt.Errorf("imgproxy.Cache.Get: open %s: %w", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("imgproxy.Cache.Get: stat %s: %w", path, err)
	}
	return &CacheEntry{
		Reader:   f,
		Size:     fi.Size(),
		MIMEType: spec.Format.MIMEType(),
		ModTime:  fi.ModTime(),
	}, nil
}

// Put writes body at the (assetID, spec) key. The write is atomic:
// bytes land at a temporary path first, then rename into place. A
// concurrent Get during a Put will see either the previous entry or
// the new one, never a half-written file.
//
// Put creates the bucket directory on demand. An EEXIST race
// (two writers creating the same bucket) is benign.
func (c *Cache) Put(assetID string, spec Spec, body []byte) error {
	if assetID == "" {
		return errors.New("imgproxy.Cache.Put: assetID is empty")
	}
	if len(body) == 0 {
		return errors.New("imgproxy.Cache.Put: body is empty")
	}
	path := c.Key(assetID, spec)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("imgproxy.Cache.Put: mkdir %s: %w", dir, err)
	}

	// Write to a per-PID/per-call temp file in the same directory so
	// os.Rename is atomic (rename across filesystems isn't). The
	// goroutine ID would tighten the unique suffix further but the
	// time + pid pair has been enough in practice.
	tmpName := fmt.Sprintf(".%s.%d.%d.tmp",
		filepath.Base(path), os.Getpid(), nextTmpCounter())
	tmp := filepath.Join(dir, tmpName)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("imgproxy.Cache.Put: write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("imgproxy.Cache.Put: rename %s: %w", path, err)
	}
	return nil
}

// tmpCounter is a process-local monotonic counter used to mint
// unique temp filenames inside Put. The sync.Mutex wins over an
// atomic.Int64 because the cost is dominated by the I/O around it
// and the lock makes the call site easier to reason about.
var (
	tmpCounterMu sync.Mutex
	tmpCounterN  uint64
)

func nextTmpCounter() uint64 {
	tmpCounterMu.Lock()
	defer tmpCounterMu.Unlock()
	tmpCounterN++
	return tmpCounterN
}
