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
// mirrors the comments table plus a tiny posts surface (just enough
// for PostExists).
//
// Concurrency: a single sync.RWMutex guards every read and write.
// The public submit surface is rate-limited at the handler so the
// store doesn't have to be lock-free.
type MemoryStore struct {
	mu sync.RWMutex

	// rows is keyed by comment ID.
	rows map[string]storedComment

	// posts is the set of known post IDs. Seeded by tests and by the
	// dev fall-through; the Postgres backend queries posts directly.
	posts map[string]struct{}

	// now returns the wall clock. Tests inject a deterministic clock;
	// production wiring passes time.Now.
	now func() time.Time
}

// storedComment is the persisted form: the public Comment plus the
// fields the public API never returns (email, ip, user_agent, status).
// Splitting the shape keeps the public Comment lean and prevents an
// accidental encoder leak.
type storedComment struct {
	Comment
	Status          Status
	AuthorEmail     string
	AuthorIP        string
	AuthorUserAgent string
	AuthorUserID    string
}

// NewMemoryStore returns an empty MemoryStore using time.Now as its
// clock.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(time.Now)
}

// NewMemoryStoreWithClock returns an empty MemoryStore using the
// supplied clock. nil falls back to time.Now.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		rows:  make(map[string]storedComment),
		posts: make(map[string]struct{}),
		now:   now,
	}
}

// SeedPost registers postID as a known post. Used by tests; the dev
// fall-through registers posts as they're encountered on the public
// site. The Postgres backend doesn't need this — it queries posts
// directly.
func (s *MemoryStore) SeedPost(postID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts[postID] = struct{}{}
}

// Seed inserts c verbatim. Path/Depth/CreatedAt are preserved as-is;
// tests are expected to supply a well-formed row. Status defaults to
// approved when zero so a Seed call shows up in the public list
// without a follow-up status flip.
func (s *MemoryStore) Seed(c Comment, status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == "" {
		status = StatusApproved
	}
	if c.AuthorDisplayName == "" {
		c.AuthorDisplayName = "Anonymous"
	}
	if c.Path == "" {
		c.Path = labelFromID(c.ID)
	}
	if c.Depth == 0 {
		c.Depth = strings.Count(c.Path, ".") + 1
	}
	s.posts[c.PostID] = struct{}{}
	s.rows[c.ID] = storedComment{Comment: c, Status: status}
}

// List returns approved comments on f.PostID, sorted by path ASC.
func (s *MemoryStore) List(_ context.Context, f ListFilter) (ListPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matches := make([]Comment, 0, len(s.rows))
	for _, row := range s.rows {
		if row.Status != StatusApproved {
			continue
		}
		if f.PostID != "" && row.PostID != f.PostID {
			continue
		}
		if f.AfterPath != "" && row.Path <= f.AfterPath {
			continue
		}
		matches = append(matches, row.Comment)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path == matches[j].Path {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].Path < matches[j].Path
	})

	limit := f.Limit
	if limit < 1 {
		limit = 50
	}

	hasNext := false
	if len(matches) > limit {
		matches = matches[:limit]
		hasNext = true
	}

	return ListPage{Comments: matches, HasNext: hasNext}, nil
}

// Submit creates a new comment row.
func (s *MemoryStore) Submit(_ context.Context, in SubmitInput, initialStatus Status) (Comment, error) {
	if strings.TrimSpace(in.Content) == "" {
		return Comment{}, ErrEmptyContent
	}
	if in.PostID == "" {
		return Comment{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.posts[in.PostID]; !ok {
		return Comment{}, ErrNotFound
	}

	var parentPath string
	if in.ParentID != "" {
		parent, ok := s.rows[in.ParentID]
		if !ok {
			return Comment{}, ErrNotFound
		}
		if parent.PostID != in.PostID {
			return Comment{}, ErrParentMismatch
		}
		parentPath = parent.Path
	}

	id := uuid.New().String()
	now := s.now()
	displayName := in.AuthorName
	if displayName == "" {
		displayName = "Anonymous"
	}

	path := labelFromID(id)
	if parentPath != "" {
		path = parentPath + "." + labelFromID(id)
	}

	c := Comment{
		ID:                id,
		PostID:            in.PostID,
		ParentID:          in.ParentID,
		Path:              path,
		Depth:             strings.Count(path, ".") + 1,
		AuthorDisplayName: displayName,
		Content:           in.Content,
		CreatedAt:         now,
	}
	s.rows[id] = storedComment{
		Comment:         c,
		Status:          initialStatus,
		AuthorEmail:     in.AuthorEmail,
		AuthorIP:        in.AuthorIP,
		AuthorUserAgent: in.AuthorUserAgent,
		AuthorUserID:    in.AuthorUserID,
	}
	return c, nil
}

// PostExists reports whether the given post is known.
func (s *MemoryStore) PostExists(_ context.Context, postID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.posts[postID]
	return ok, nil
}

// CommentsByIP returns the number of comments the given IP has
// submitted since since.
func (s *MemoryStore) CommentsByIP(_ context.Context, ip string, since time.Time) (int, error) {
	if ip == "" {
		return 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, row := range s.rows {
		if row.AuthorIP != ip {
			continue
		}
		if row.CreatedAt.Before(since) {
			continue
		}
		n++
	}
	return n, nil
}

// ErrEmptyContent is returned when the submit content is empty after
// whitespace trimming. The handler validates this before reaching the
// store; this is belt-and-braces for the dev fall-through.
var ErrEmptyContent = errors.New("rest/comments: content is empty")

// labelFromID returns the ltree label form of a UUID: hyphens
// replaced by underscores. Mirrors the comments_set_path trigger in
// migration 000006.
func labelFromID(id string) string {
	return strings.ReplaceAll(id, "-", "_")
}
