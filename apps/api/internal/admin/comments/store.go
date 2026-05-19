package comments

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store implementation. It backs unit
// tests and the no-DB development fall-through. The data shape
// mirrors the comments table plus the joined post + author columns
// the list view needs.
//
// Concurrency: a single sync.RWMutex guards every read and write.
// The admin moderation surface is a low-frequency operator path, so
// the simplest correct strategy wins.
type MemoryStore struct {
	mu sync.RWMutex

	// rows is keyed by comment ID. Values are the full row including
	// the denormalised post + author fields the list endpoint needs.
	rows map[string]Comment

	// now returns the wall clock. Tests inject a deterministic clock;
	// production wiring passes time.Now.
	now func() time.Time
}

// NewMemoryStore returns an empty MemoryStore using time.Now as its
// clock. Tests use NewMemoryStoreWithClock to pin the time source.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(time.Now)
}

// NewMemoryStoreWithClock returns an empty MemoryStore using the
// supplied clock. The clock is only consulted on writes (UpdatedAt
// stamps and reply CreatedAt). nil falls back to time.Now.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		rows: make(map[string]Comment),
		now:  now,
	}
}

// Seed inserts c verbatim. Used by tests and the dev fall-through to
// pre-load a corpus. Existing entries with the same ID are
// overwritten. The path is preserved as-is; tests are expected to
// supply a well-formed ltree value.
func (s *MemoryStore) Seed(c Comment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.AuthorDisplayName == "" {
		c.AuthorDisplayName = "Anonymous"
	}
	if c.ContentFormat == "" {
		c.ContentFormat = "html"
	}
	if c.Path == "" {
		c.Path = labelFromID(c.ID)
	}
	s.rows[c.ID] = c
}

// List returns matching comments sorted by created_at DESC. The
// HasNext flag is true when at least one row exists beyond the
// returned page — the handler uses it to decide whether to issue a
// next_cursor.
func (s *MemoryStore) List(_ context.Context, f ListFilter) (ListPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matches := make([]Comment, 0, len(s.rows))
	for _, c := range s.rows {
		if f.Status != "" && c.Status != f.Status {
			continue
		}
		if f.PostID != "" && c.PostID != f.PostID {
			continue
		}
		if f.UserID != "" && c.AuthorUserID != f.UserID {
			continue
		}
		matches = append(matches, c)
	}

	// Newest first. Tie-break on ID so the order is stable across
	// runs — important for the pagination tests that walk pages.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})

	page := f.Page
	if page < 1 {
		page = 1
	}
	limit := f.Limit
	if limit < 1 {
		limit = 30
	}

	start := (page - 1) * limit
	if start >= len(matches) {
		return ListPage{Comments: nil, HasNext: false}, nil
	}
	end := start + limit
	hasNext := false
	if end < len(matches) {
		hasNext = true
	} else {
		end = len(matches)
	}

	page1 := make([]Comment, end-start)
	copy(page1, matches[start:end])
	return ListPage{Comments: page1, HasNext: hasNext}, nil
}

// Get returns a single comment by ID, or ErrNotFound.
func (s *MemoryStore) Get(_ context.Context, id string) (Comment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.rows[id]
	if !ok {
		return Comment{}, ErrNotFound
	}
	return c, nil
}

// UpdateStatus transitions the comment's status and stamps UpdatedAt.
// Returns ErrNotFound if the row is missing.
func (s *MemoryStore) UpdateStatus(_ context.Context, id string, status Status) (Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.rows[id]
	if !ok {
		return Comment{}, ErrNotFound
	}
	c.Status = status
	c.UpdatedAt = s.now()
	s.rows[id] = c
	return c, nil
}

// Bulk applies status to every comment in ids in a single
// transaction. If any ID is unknown, the operation is rejected and
// the store is unchanged. Returns the updated comments in the same
// order as ids.
//
// The all-or-nothing semantics match the Postgres backend: a CTE
// with a WHERE id = ANY($1) returns the affected count, and the
// handler rolls back when the count doesn't equal len(ids). Keeping
// the in-memory store atomic too means the tests catch a bulk
// regression even before the DB lands.
func (s *MemoryStore) Bulk(_ context.Context, ids []string, status Status) ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Two-phase: validate every ID exists, then apply. The check
	// avoids partial updates if an ID 3-of-5 is missing.
	for _, id := range ids {
		if _, ok := s.rows[id]; !ok {
			return nil, ErrBulkPartial
		}
	}

	now := s.now()
	out := make([]Comment, 0, len(ids))
	for _, id := range ids {
		c := s.rows[id]
		c.Status = status
		c.UpdatedAt = now
		s.rows[id] = c
		out = append(out, c)
	}
	return out, nil
}

// Reply creates a child comment under parentID. The new comment
// inherits the parent's post_id and post_title; its path is the
// parent's path with a fresh self-label appended (mirroring the
// trigger in migration 000006).
//
// content must be non-empty; the handler validates this before
// reaching the store, but the store double-checks so the dev
// fall-through doesn't accept an empty body.
func (s *MemoryStore) Reply(_ context.Context, parentID, authorUserID, authorName, content string) (Comment, error) {
	if strings.TrimSpace(content) == "" {
		return Comment{}, ErrEmptyContent
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	parent, ok := s.rows[parentID]
	if !ok {
		return Comment{}, ErrNotFound
	}

	id := uuid.New().String()
	displayName := authorName
	if displayName == "" {
		displayName = "Moderator"
	}
	now := s.now()
	child := Comment{
		ID:                id,
		PostID:            parent.PostID,
		PostTitle:         parent.PostTitle,
		ParentID:          parent.ID,
		Path:              parent.Path + "." + labelFromID(id),
		AuthorUserID:      authorUserID,
		AuthorDisplayName: displayName,
		Content:           content,
		ContentFormat:     "html",
		// Replies from a moderator land approved by default — the
		// whole point of operator-side reply is to surface a
		// response immediately. The Postgres backend will set the
		// same default; non-moderator replies use the public surface
		// which has its own pending/approved logic.
		Status:    StatusApproved,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.rows[id] = child
	return child, nil
}

// ErrEmptyContent is returned by Reply when the content is empty
// after whitespace trimming. The handler maps this to a 400.
var ErrEmptyContent = errors.New("admin/comments: content is empty")

// labelFromID returns the ltree label form of a UUID: hyphens
// replaced by underscores. Matches the comments_set_path trigger in
// migration 000006.
func labelFromID(id string) string {
	return strings.ReplaceAll(id, "-", "_")
}
