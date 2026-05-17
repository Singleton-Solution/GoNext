package posts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-process [Store] backed by a map. It is the
// substrate every handler test runs against — production code uses
// [PgStore] instead. The store is goroutine-safe; tests share one
// instance across parallel requests via httptest.
//
// The struct is intentionally a faithful imitation of the Postgres
// semantics this package depends on:
//
//   - id is UUIDv7 so the sort order is the natural cursor key.
//   - version starts at 1 and increments on every Update/Trash, just
//     like the bump_version trigger.
//   - content_blocks_hash is recomputed (sha256) on every write that
//     touches content_blocks.
//
// The few semantics we don't reproduce — published_at preservation
// across re-publish, hierarchical slug uniqueness — are out of scope
// for this issue's tests; the production store implements them.
type MemoryStore struct {
	mu    sync.RWMutex
	rows  map[string]*memoryRow
	now   func() time.Time
	newID func() string
}

// memoryRow is the internal representation. Separate from [Post] so we
// can keep the sensitive password as a typed pointer without leaking
// it across the package boundary.
type memoryRow struct {
	id            string
	postType      string
	parentID      *string
	authorID      string
	status        string
	title         string
	slug          string
	excerpt       *string
	contentBlocks json.RawMessage
	contentHash   []byte
	password      *string
	commentStatus string
	pingStatus    string
	menuOrder     int
	meta          json.RawMessage
	publishedAt   *time.Time
	scheduledFor  *time.Time
	createdAt     time.Time
	updatedAt     time.Time
	version       int
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rows:  map[string]*memoryRow{},
		now:   time.Now,
		newID: func() string { return uuid.Must(uuid.NewV7()).String() },
	}
}

// SetNow lets tests pin the clock without sleeping. Goroutine-safe to
// call before any Emit; do not race it against in-flight requests.
func (s *MemoryStore) SetNow(f func() time.Time) { s.now = f }

// SetIDFunc lets tests inject a deterministic id sequence so cursor
// assertions don't depend on UUIDv7's embedded timestamp ordering.
func (s *MemoryStore) SetIDFunc(f func() string) { s.newID = f }

func (s *MemoryStore) List(_ context.Context, postType string, filter ListFilter) ([]Post, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	// Materialize the rows that match the filter. The match cost is
	// O(N) — acceptable for the test substrate (which never inserts
	// more than a few hundred rows). The production PgStore pushes
	// these predicates into SQL.
	matched := make([]*memoryRow, 0, len(s.rows))
	for _, row := range s.rows {
		if row.postType != postType {
			continue
		}
		if filter.Status != "" && row.status != filter.Status {
			continue
		}
		if filter.AuthorID != "" && row.authorID != filter.AuthorID {
			continue
		}
		if filter.Search != "" && !strings.Contains(strings.ToLower(row.title), strings.ToLower(filter.Search)) {
			continue
		}
		if filter.After != "" && row.id <= filter.After {
			continue
		}
		matched = append(matched, row)
	}

	// Stable id ASC so cursor pagination is deterministic.
	sort.Slice(matched, func(i, j int) bool { return matched[i].id < matched[j].id })

	// Fetch one extra so the handler can detect "more pages" without
	// asking the database for a count.
	cap := limit + 1
	if len(matched) > cap {
		matched = matched[:cap]
	}

	out := make([]Post, len(matched))
	for i, row := range matched {
		out[i] = row.toPost()
	}
	return out, nil
}

func (s *MemoryStore) Get(_ context.Context, postType, id string) (Post, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, ok := s.rows[id]
	if !ok || row.postType != postType {
		return Post{}, ErrNotFound
	}
	return row.toPost(), nil
}

func (s *MemoryStore) Create(_ context.Context, postType, authorID string, in CreateInput) (Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	row := &memoryRow{
		id:            s.newID(),
		postType:      postType,
		authorID:      authorID,
		status:        "draft",
		title:         "",
		slug:          "",
		commentStatus: "open",
		pingStatus:    "closed",
		meta:          json.RawMessage("{}"),
		contentBlocks: json.RawMessage("[]"),
		menuOrder:     0,
		createdAt:     now,
		updatedAt:     now,
		version:       1,
	}
	applyCreate(row, in)
	row.recomputeHash()

	if err := s.checkSlugUniqueness(row, ""); err != nil {
		return Post{}, err
	}

	s.rows[row.id] = row
	return row.toPost(), nil
}

func (s *MemoryStore) Update(_ context.Context, postType, id string, expectedVersion int, in UpdateInput) (Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[id]
	if !ok || row.postType != postType {
		return Post{}, ErrNotFound
	}
	if row.version != expectedVersion {
		return Post{}, ErrVersionConflict
	}
	applyUpdate(row, in)
	row.recomputeHash()
	row.updatedAt = s.now().UTC()
	row.version++

	if err := s.checkSlugUniqueness(row, id); err != nil {
		return Post{}, err
	}
	return row.toPost(), nil
}

func (s *MemoryStore) Trash(_ context.Context, postType, id string, expectedVersion int) (Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[id]
	if !ok || row.postType != postType {
		return Post{}, ErrNotFound
	}
	if row.version != expectedVersion {
		return Post{}, ErrVersionConflict
	}
	row.status = "trash"
	row.updatedAt = s.now().UTC()
	row.version++
	return row.toPost(), nil
}

// checkSlugUniqueness mirrors the partial-unique-index rule in
// 000004_posts.up.sql: a slug must be unique within (post_type, slug)
// when parent_id is NULL and status != 'trash', and within
// (post_type, parent_id, slug) when parent_id is non-NULL.
//
// `selfID` is empty on insert and the row's id on update — it lets
// the row exclude itself from the collision check.
func (s *MemoryStore) checkSlugUniqueness(target *memoryRow, selfID string) error {
	if target.slug == "" {
		return nil
	}
	if target.status == "trash" {
		return nil
	}
	for _, other := range s.rows {
		if other.id == selfID {
			continue
		}
		if other.postType != target.postType {
			continue
		}
		if other.status == "trash" {
			continue
		}
		if !strings.EqualFold(other.slug, target.slug) {
			continue
		}
		switch {
		case target.parentID == nil && other.parentID == nil:
			return ErrDuplicateSlug
		case target.parentID != nil && other.parentID != nil && *target.parentID == *other.parentID:
			return ErrDuplicateSlug
		}
	}
	return nil
}

func (r *memoryRow) toPost() Post {
	p := Post{
		ID:            r.id,
		PostType:      r.postType,
		ParentID:      r.parentID,
		AuthorID:      r.authorID,
		Status:        r.status,
		Title:         r.title,
		Slug:          r.slug,
		Excerpt:       r.excerpt,
		ContentBlocks: r.contentBlocks,
		CommentStatus: r.commentStatus,
		PingStatus:    r.pingStatus,
		MenuOrder:     r.menuOrder,
		Meta:          r.meta,
		PublishedAt:   r.publishedAt,
		ScheduledFor:  r.scheduledFor,
		CreatedAt:     r.createdAt,
		UpdatedAt:     r.updatedAt,
		Version:       r.version,
		Protected:     r.password != nil && *r.password != "",
		hash:          r.contentHash,
		password:      r.password,
	}
	if len(p.ContentBlocks) == 0 {
		p.ContentBlocks = json.RawMessage("[]")
	}
	if len(p.Meta) == 0 {
		p.Meta = json.RawMessage("{}")
	}
	return p
}

// recomputeHash sets contentHash to sha256(content_blocks). Mirrors the
// behavior we expect the production store / renderer to provide: the
// hash is the ETag input for conditional GET.
func (r *memoryRow) recomputeHash() {
	h := sha256.Sum256(r.contentBlocks)
	r.contentHash = append([]byte(nil), h[:]...)
}

// applyCreate copies non-nil fields from in onto row. CreateInput fields
// are nil-as-omitted; non-nil writes the underlying value.
func applyCreate(row *memoryRow, in CreateInput) {
	if in.ParentID != nil {
		row.parentID = strPtrCopy(in.ParentID)
	}
	if in.Status != nil {
		row.status = *in.Status
	}
	if in.Title != nil {
		row.title = *in.Title
	}
	if in.Slug != nil {
		row.slug = *in.Slug
	}
	if in.Excerpt != nil {
		row.excerpt = strPtrCopy(in.Excerpt)
	}
	if in.ContentBlocks != nil {
		row.contentBlocks = append(json.RawMessage(nil), in.ContentBlocks...)
	}
	if in.Password != nil {
		row.password = strPtrCopy(in.Password)
	}
	if in.CommentStatus != nil {
		row.commentStatus = *in.CommentStatus
	}
	if in.PingStatus != nil {
		row.pingStatus = *in.PingStatus
	}
	if in.MenuOrder != nil {
		row.menuOrder = *in.MenuOrder
	}
	if in.Meta != nil {
		row.meta = append(json.RawMessage(nil), in.Meta...)
	}
	if in.PublishedAt != nil {
		t := in.PublishedAt.UTC()
		row.publishedAt = &t
	}
	if in.ScheduledFor != nil {
		t := in.ScheduledFor.UTC()
		row.scheduledFor = &t
	}
}

func applyUpdate(row *memoryRow, in UpdateInput) {
	if in.ParentID != nil {
		row.parentID = strPtrCopy(in.ParentID)
	}
	if in.Status != nil {
		row.status = *in.Status
	}
	if in.Title != nil {
		row.title = *in.Title
	}
	if in.Slug != nil {
		row.slug = *in.Slug
	}
	if in.Excerpt != nil {
		row.excerpt = strPtrCopy(in.Excerpt)
	}
	if in.ContentBlocks != nil {
		row.contentBlocks = append(json.RawMessage(nil), in.ContentBlocks...)
	}
	if in.Password != nil {
		row.password = strPtrCopy(in.Password)
	}
	if in.CommentStatus != nil {
		row.commentStatus = *in.CommentStatus
	}
	if in.PingStatus != nil {
		row.pingStatus = *in.PingStatus
	}
	if in.MenuOrder != nil {
		row.menuOrder = *in.MenuOrder
	}
	if in.Meta != nil {
		row.meta = append(json.RawMessage(nil), in.Meta...)
	}
	if in.PublishedAt != nil {
		t := in.PublishedAt.UTC()
		row.publishedAt = &t
	}
	if in.ScheduledFor != nil {
		t := in.ScheduledFor.UTC()
		row.scheduledFor = &t
	}
}

func strPtrCopy(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
