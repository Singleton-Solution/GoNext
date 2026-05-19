package media

import (
	"context"
	"fmt"
	"sync"
)

// MemoryPutter is the in-memory ObjectPutter used by tests. It records
// every PutObject call so tests can assert "the upload pipeline did/did
// not push bytes to storage" without standing up a real MinIO. The
// dedupe path's whole reason to exist is to NOT call PutObject on a
// repeat upload, so tests need a way to observe that.
//
// PublicURL returns a synthetic URL of the form "memory:///{key}";
// tests assert on the string but production callers never see this
// because the real S3 putter implements the same interface.
type MemoryPutter struct {
	mu      sync.Mutex
	stored  map[string][]byte
	putErr  error
}

// NewMemoryPutter returns an empty in-memory putter.
func NewMemoryPutter() *MemoryPutter {
	return &MemoryPutter{stored: make(map[string][]byte)}
}

// SetPutError makes the next (and subsequent) PutObject calls return
// err. Used by tests that exercise the "storage rejected the upload"
// branch.
func (p *MemoryPutter) SetPutError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putErr = err
}

// Stored returns a copy of the bytes stored at key, or nil if no put
// has landed at that key. Test-only — the upload handler never reads
// back what it wrote.
func (p *MemoryPutter) Stored(key string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.stored[key]
	if !ok {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// PutCount returns the number of successful PutObject calls. Used by
// dedupe tests to assert that "second upload of the same bytes does
// not call PutObject a second time".
func (p *MemoryPutter) PutCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.stored)
}

// PutObject implements ObjectPutter.
func (p *MemoryPutter) PutObject(_ context.Context, key string, body []byte, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.putErr != nil {
		return p.putErr
	}
	b := make([]byte, len(body))
	copy(b, body)
	p.stored[key] = b
	return nil
}

// PublicURL implements ObjectPutter.
func (p *MemoryPutter) PublicURL(key string) string {
	return fmt.Sprintf("memory:///%s", key)
}
