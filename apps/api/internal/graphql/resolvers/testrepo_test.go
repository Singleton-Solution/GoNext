package resolvers_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/resolvers"
)

// memPostRepo is the in-memory PostRepo used across the resolver
// tests. It is intentionally simple — index by id, list scans the
// whole map, filter is applied in Go — because the test scale is tiny.
type memPostRepo struct {
	mu    sync.RWMutex
	rows  []resolvers.PostRow
	next  int
	calls atomic.Int32 // count of ByID calls, used by the N+1 test
}

func newMemPostRepo(rows ...resolvers.PostRow) *memPostRepo {
	r := &memPostRepo{rows: append([]resolvers.PostRow{}, rows...)}
	r.next = len(rows) + 1
	return r
}

func (r *memPostRepo) ByID(_ context.Context, id string) (*resolvers.PostRow, error) {
	r.calls.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.rows {
		if r.rows[i].ID == id {
			row := r.rows[i]
			return &row, nil
		}
	}
	return nil, nil
}

func (r *memPostRepo) List(_ context.Context, f resolvers.PostFilter, first int, afterCursor string) (*resolvers.PostPage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]resolvers.PostRow, 0, len(r.rows))
	for _, row := range r.rows {
		if f.Status != nil && row.Status != *f.Status {
			continue
		}
		if f.AuthorID != nil && row.AuthorID != *f.AuthorID {
			continue
		}
		if f.TitlePrefix != nil && !strings.HasPrefix(row.Title, *f.TitlePrefix) {
			continue
		}
		out = append(out, row)
	}
	total := len(out)
	// Cursor scan: skip past afterCursor (raw id form, since
	// EncodeCursor turns raw -> b64 and the resolver decodes).
	if afterCursor != "" {
		i := 0
		for i < len(out) && out[i].ID != afterCursor {
			i++
		}
		if i < len(out) {
			out = out[i+1:]
		}
	}
	hasNext := false
	if first > 0 && len(out) > first {
		out = out[:first]
		hasNext = true
	}
	return &resolvers.PostPage{Rows: out, TotalCount: total, HasNext: hasNext}, nil
}

func (r *memPostRepo) Create(_ context.Context, row resolvers.PostRow) (*resolvers.PostRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row.ID == "" {
		row.ID = nextID(&r.next)
	}
	r.rows = append(r.rows, row)
	out := r.rows[len(r.rows)-1]
	return &out, nil
}

// memUserRepo is the in-memory UserRepo. It tracks calls separately
// from byID and byIDs so the N+1 test can assert that the dataloader
// actually batched: byIDs was called once for N rows, byID was not
// called at all.
type memUserRepo struct {
	mu        sync.RWMutex
	rows      []resolvers.UserRow
	byIDCalls atomic.Int32
	byIDsCalls atomic.Int32
}

func newMemUserRepo(rows ...resolvers.UserRow) *memUserRepo {
	return &memUserRepo{rows: append([]resolvers.UserRow{}, rows...)}
}

func (r *memUserRepo) ByID(_ context.Context, id string) (*resolvers.UserRow, error) {
	r.byIDCalls.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.rows {
		if r.rows[i].ID == id {
			row := r.rows[i]
			return &row, nil
		}
	}
	return nil, nil
}

func (r *memUserRepo) ByIDs(_ context.Context, ids []string) ([]*resolvers.UserRow, error) {
	r.byIDsCalls.Add(1)
	r.mu.RLock()
	defer r.mu.RUnlock()
	index := make(map[string]resolvers.UserRow, len(r.rows))
	for _, row := range r.rows {
		index[row.ID] = row
	}
	out := make([]*resolvers.UserRow, len(ids))
	for i, id := range ids {
		if row, ok := index[id]; ok {
			r := row
			out[i] = &r
		}
	}
	return out, nil
}

// erroringUserRepo is a UserRepo that returns errors on demand. Used
// to test the error path through the dataloader.
type erroringUserRepo struct {
	err error
}

func (e erroringUserRepo) ByID(context.Context, string) (*resolvers.UserRow, error) {
	if e.err == nil {
		return nil, errors.New("erroringUserRepo: missing err")
	}
	return nil, e.err
}

func (e erroringUserRepo) ByIDs(context.Context, []string) ([]*resolvers.UserRow, error) {
	if e.err == nil {
		return nil, errors.New("erroringUserRepo: missing err")
	}
	return nil, e.err
}

// nextID generates a tiny synthetic id for in-memory inserts.
// Sufficient for tests; production uses UUIDs from the DB.
func nextID(seq *int) string {
	id := *seq
	*seq++
	return "id-" + itoa(id)
}

// itoa is the smallest int -> string helper that doesn't drag in fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// fixedTime returns a stable time for fixtures.
func fixedTime() time.Time {
	return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
}
