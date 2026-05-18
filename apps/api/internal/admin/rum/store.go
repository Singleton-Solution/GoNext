package rum

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// EventStore is the contract every RUM persistence backend
// implements. Two concrete backends are envisaged:
//
//   - PostgresStore (apps/api wiring): writes rum_events rows
//     in a single batched INSERT with COPY semantics, reads via
//     percentile_disc aggregation.
//   - MemoryStore (this file): tests + a fall-through that the
//     binary uses on dev when no DB is wired. The handler
//     contract is identical so the swap is a Deps wiring change.
//
// The interface is deliberately minimal:
//
//	Insert is called by the beacon handler. It receives the
//	server-side ingest time so a test can pin the clock; production
//	wiring passes time.Now.
//
//	Percentiles is called by the admin read handler. The "path"
//	filter is optional — an empty string aggregates across all
//	routes.
//
//	SlowestRoutes feeds the admin "top slowest routes" table. The
//	limit is enforced at the store boundary so the SQL backend
//	can use LIMIT and the memory backend can skip a sort+slice.
type EventStore interface {
	Insert(ctx context.Context, ingestAt time.Time, events []Event) error
	Percentiles(ctx context.Context, metric, path string, from, to time.Time) (PercentileResult, error)
	SlowestRoutes(ctx context.Context, metric string, from, to time.Time, limit int) ([]RouteSlowRow, error)
}

// memoryRow mirrors the rum_events row layout but in plain Go so
// the in-memory store doesn't depend on a pgx type.
type memoryRow struct {
	ts       time.Time
	metric   string
	value    float64
	rating   string
	pagePath string
}

// MemoryStore is an in-process EventStore. Used by tests and the
// "no DB wired" fall-through in dev. Thread-safe via RWMutex.
//
// The implementation is deliberately naive — every Percentiles
// call scans the full slice, sorts the matching values, and reads
// the chosen rank. That is O(n log n) per call. For tests with
// at most a few thousand rows this is well below 1 ms; production
// uses the Postgres store where percentile_disc is index-served.
type MemoryStore struct {
	mu   sync.RWMutex
	rows []memoryRow
}

// NewMemoryStore returns an empty MemoryStore ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Insert appends the events to the in-memory slice. The clock is
// passed in so the tests can pin it; production callers pass
// time.Now (or a logical clock that wraps it).
func (s *MemoryStore) Insert(_ context.Context, ingestAt time.Time, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range events {
		s.rows = append(s.rows, memoryRow{
			ts:       ingestAt,
			metric:   e.Metric,
			value:    e.Value,
			rating:   e.Rating,
			pagePath: e.PagePath,
		})
	}
	return nil
}

// Percentiles returns p50/p75/p95 over the rows matching metric +
// optional path within [from, to]. An empty path aggregates over
// every route.
//
// Returns an empty PercentileResult (with Sample=0) if no rows
// match; the admin handler renders this as "no data yet" rather
// than as an error. This is deliberate — a brand-new
// deployment's first /performance page render should not 500.
func (s *MemoryStore) Percentiles(_ context.Context, metric, path string, from, to time.Time) (PercentileResult, error) {
	if metric == "" {
		return PercentileResult{}, errors.New("rum: metric is required")
	}
	if !from.Before(to) {
		return PercentileResult{}, errors.New("rum: from must precede to")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	values := make([]float64, 0, 64)
	for _, r := range s.rows {
		if r.metric != metric {
			continue
		}
		if path != "" && r.pagePath != path {
			continue
		}
		if r.ts.Before(from) || r.ts.After(to) {
			continue
		}
		values = append(values, r.value)
	}

	out := PercentileResult{
		Metric: metric,
		Path:   path,
		From:   from,
		To:     to,
		Sample: len(values),
	}
	if len(values) == 0 {
		return out, nil
	}
	sort.Float64s(values)
	out.P50 = percentile(values, 0.50)
	out.P75 = percentile(values, 0.75)
	out.P95 = percentile(values, 0.95)
	return out, nil
}

// SlowestRoutes returns the top-N routes sorted by p75 (desc) for
// the given metric within the window. Routes with fewer than
// minSampleForSlowest events are skipped — a single 9 s LCP on a
// page that nobody else has visited is a flake, not a real signal.
func (s *MemoryStore) SlowestRoutes(_ context.Context, metric string, from, to time.Time, limit int) ([]RouteSlowRow, error) {
	if metric == "" {
		return nil, errors.New("rum: metric is required")
	}
	if !from.Before(to) {
		return nil, errors.New("rum: from must precede to")
	}
	if limit < 1 {
		limit = 1
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	byPath := make(map[string][]float64, 32)
	for _, r := range s.rows {
		if r.metric != metric {
			continue
		}
		if r.ts.Before(from) || r.ts.After(to) {
			continue
		}
		byPath[r.pagePath] = append(byPath[r.pagePath], r.value)
	}

	rows := make([]RouteSlowRow, 0, len(byPath))
	for path, vs := range byPath {
		if len(vs) < minSampleForSlowest {
			continue
		}
		sort.Float64s(vs)
		rows = append(rows, RouteSlowRow{
			Path:   path,
			Metric: metric,
			P75:    percentile(vs, 0.75),
			Sample: len(vs),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].P75 > rows[j].P75 })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// minSampleForSlowest is the cut-off used by SlowestRoutes to
// hide flakes from the operator's view. 3 is the smallest count
// where "the median is a real median" — at n=2 the p75 is just
// the larger value, which doesn't carry a useful signal.
const minSampleForSlowest = 3

// percentile reads the p-th percentile from a SORTED slice using
// the linear-interpolation method (the same one numpy and Excel
// default to; matches what an operator who copies the data into
// a notebook expects). The slice must be sorted ascending; we
// don't sort here so the caller can amortise sorting across
// multiple percentile reads.
//
// p is in [0, 1]. Out-of-range p is clamped to the endpoints.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	// Index in (0, len-1). Linear interpolation between floor
	// and ceil. The +0.0 keeps the expression float — without
	// it the integer math would produce a step function.
	pos := p * float64(len(sorted)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}
