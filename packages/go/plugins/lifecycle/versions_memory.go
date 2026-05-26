package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryVersionLog is the in-process VersionLog used by tests and
// dev setups. It is paired with MemoryStorage; together they exercise
// the same paths the production postgres backend does without a live
// database.
//
// Concurrency: every mutating method takes a single Mutex. Reads also
// take it because the slice mutations involve multiple steps that
// must appear atomic to a concurrent List call. Contention is fine at
// test scale.
type MemoryVersionLog struct {
	mu   sync.Mutex
	rows map[string][]VersionRow // slug -> ordered (by InstalledAt) rows
}

// NewMemoryVersionLog returns an empty in-memory version log.
func NewMemoryVersionLog() *MemoryVersionLog {
	return &MemoryVersionLog{rows: make(map[string][]VersionRow)}
}

// AppendActive inserts row as Active. Any existing Active row for the
// slug is moved to Retiring atomically with the insert so a concurrent
// reader sees exactly one Active per slug.
func (m *MemoryVersionLog) AppendActive(_ context.Context, row VersionRow) (*VersionRow, error) {
	if row.Slug == "" || row.Version == "" {
		return nil, fmt.Errorf("lifecycle/memory-versions: AppendActive: slug and version are required")
	}
	row.State = VersionActive
	m.mu.Lock()
	defer m.mu.Unlock()

	rows := m.rows[row.Slug]
	var previous *VersionRow
	for i := range rows {
		if rows[i].State == VersionActive {
			rows[i].State = VersionRetiring
			rows[i].RetiredAt = row.InstalledAt
			cp := rows[i]
			previous = &cp
		}
		if rows[i].Version == row.Version {
			return nil, fmt.Errorf("lifecycle/memory-versions: version %q already exists for %q", row.Version, row.Slug)
		}
	}
	rows = append(rows, row)
	m.rows[row.Slug] = rows
	return previous, nil
}

// MarkRetained transitions slug+version from Retiring (or Active,
// for symmetry with rollback flows) to Retained.
func (m *MemoryVersionLog) MarkRetained(_ context.Context, slug, version string, retentionEnd time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[slug]
	for i := range rows {
		if rows[i].Version == version {
			rows[i].State = VersionRetained
			rows[i].RetentionEnd = retentionEnd
			return nil
		}
	}
	return fmt.Errorf("lifecycle/memory-versions: MarkRetained %q/%q: not found", slug, version)
}

// PromoteToActive swaps the existing active row to Retiring and
// flips the named retained row to Active.
func (m *MemoryVersionLog) PromoteToActive(_ context.Context, slug, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[slug]
	if len(rows) == 0 {
		return fmt.Errorf("%w: slug %q", ErrNoRollback, slug)
	}
	foundTarget := false
	for i := range rows {
		if rows[i].Version == version && rows[i].State == VersionRetained {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		return fmt.Errorf("%w: version %q", ErrNoRollback, version)
	}
	now := time.Now().UTC()
	for i := range rows {
		switch {
		case rows[i].Version == version:
			rows[i].State = VersionActive
			rows[i].ActivatedAt = now
		case rows[i].State == VersionActive:
			rows[i].State = VersionRetiring
			rows[i].RetiredAt = now
		}
	}
	m.rows[slug] = rows
	return nil
}

// MarkRetired moves a row to Retired (fully unloaded). Used by the
// cleanup cron after the runtime drops the WASM module.
func (m *MemoryVersionLog) MarkRetired(_ context.Context, slug, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.rows[slug]
	for i := range rows {
		if rows[i].Version == version {
			rows[i].State = VersionRetired
			return nil
		}
	}
	return fmt.Errorf("lifecycle/memory-versions: MarkRetired %q/%q: not found", slug, version)
}

// ListRetained returns Retained rows sorted newest first.
func (m *MemoryVersionLog) ListRetained(_ context.Context, slug string) ([]VersionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []VersionRow
	for _, r := range m.rows[slug] {
		if r.State == VersionRetained {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].InstalledAt.After(out[j].InstalledAt)
	})
	return out, nil
}

// PurgeExpired drops Retained rows whose RetentionEnd has passed,
// then any Retired rows. Returns the count purged.
func (m *MemoryVersionLog) PurgeExpired(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	purged := 0
	for slug, rows := range m.rows {
		kept := rows[:0]
		for _, r := range rows {
			if r.State == VersionRetained && !r.RetentionEnd.IsZero() && now.After(r.RetentionEnd) {
				purged++
				continue
			}
			if r.State == VersionRetired {
				purged++
				continue
			}
			kept = append(kept, r)
		}
		if len(kept) == 0 {
			delete(m.rows, slug)
		} else {
			m.rows[slug] = kept
		}
	}
	return purged, nil
}

// List exposes the full slice for tests / debug. Not part of the
// VersionLog interface so callers can't depend on it.
func (m *MemoryVersionLog) List(slug string) []VersionRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]VersionRow, len(m.rows[slug]))
	copy(out, m.rows[slug])
	return out
}

// UpdateActiveVersion is the VersionedStorage extension on
// MemoryStorage. It rewrites the version/manifest/ABI on an Active
// row in place — used by Update when the storage backend supports
// active-to-active writes.
func (s *MemoryStorage) UpdateActiveVersion(_ context.Context, slug, version string, manifestBytes []byte, abiVersion int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.rows[slug]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	p.Version = version
	if abiVersion > 0 {
		p.ABIVersion = abiVersion
	}
	if len(manifestBytes) > 0 {
		cp := make([]byte, len(manifestBytes))
		copy(cp, manifestBytes)
		p.Manifest = cp
	}
	p.UpdatedAt = s.now().UTC()
	p.RowVersion++
	s.rows[slug] = p
	return nil
}

// Compile-time check.
var _ VersionLog = (*MemoryVersionLog)(nil)
var _ VersionedStorage = (*MemoryStorage)(nil)
