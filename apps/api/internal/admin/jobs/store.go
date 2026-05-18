package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRedactionNotFound is returned by RedactionStore.Get when no
// redaction has been recorded for the given (queue, taskID). The list
// handler treats this as "no fields masked, render the payload preview
// verbatim", so it is part of the contract, not a true error.
var ErrRedactionNotFound = errors.New("admin/jobs: redaction not found")

// MemoryRedactionStore is an in-memory RedactionStore. Used by tests
// and as the fallback when the Postgres-backed store is not wired in
// (e.g. a single-binary smoke test of the admin UI). Thread-safe via a
// single RWMutex — the access pattern is read-heavy, with the list
// handler probing once per row.
type MemoryRedactionStore struct {
	mu   sync.RWMutex
	rows map[string]Redaction // keyed by task ID; one redaction per task
}

// NewMemoryRedactionStore returns an empty MemoryRedactionStore ready
// for use. The zero value is also safe to use after lazy initialisation
// inside the methods; the constructor is the idiomatic entry point.
func NewMemoryRedactionStore() *MemoryRedactionStore {
	return &MemoryRedactionStore{rows: make(map[string]Redaction)}
}

func (s *MemoryRedactionStore) Get(_ context.Context, _ string, taskID string) (Redaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rows[taskID]
	if !ok {
		return Redaction{}, ErrRedactionNotFound
	}
	return r, nil
}

func (s *MemoryRedactionStore) GetMany(_ context.Context, _ string, taskIDs []string) (map[string]Redaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Redaction, len(taskIDs))
	for _, id := range taskIDs {
		if r, ok := s.rows[id]; ok {
			out[id] = r
		}
	}
	return out, nil
}

func (s *MemoryRedactionStore) Upsert(_ context.Context, r Redaction) error {
	if r.TaskID == "" {
		return errors.New("admin/jobs: redaction task_id is required")
	}
	if len(r.Fields) == 0 {
		return errors.New("admin/jobs: redaction fields must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Defensive copy so a caller mutating the slice after Upsert
	// doesn't surprise a later Get. The list is small.
	fields := make([]string, len(r.Fields))
	copy(fields, r.Fields)
	r.Fields = fields
	if r.RedactedAt.IsZero() {
		r.RedactedAt = time.Now().UTC()
	}
	s.rows[r.TaskID] = r
	return nil
}

// Len returns the number of redactions currently stored. Test-only.
func (s *MemoryRedactionStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}
