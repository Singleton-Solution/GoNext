package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store implementation used by tests and
// by the single-binary admin smoke runner. Production wires the
// Postgres-backed store (which lands alongside the rest of the data
// access layer in a follow-up issue); the wire surface here is
// identical so the swap is a one-line dependency change in main.go.
//
// Concurrency: protected by a single RWMutex. The access pattern is
// read-heavy (the grid lists thousands of times per upload); the lock
// is the right level for now and can be sharded later if a hot media
// library moves to a Postgres backend that doesn't need this struct
// in the first place.
type MemoryStore struct {
	mu     sync.RWMutex
	rows   map[string]*memRow
	byHash map[string]string // hex(sha256) → id
	byKey  map[string]string // storage_key → id
	clock  func() time.Time
	idGen  func() string
}

type memRow struct {
	Asset
	sha256    []byte
	deletedAt *time.Time
}

// NewMemoryStore returns an empty MemoryStore ready for use. clock is
// the time source (nil falls back to time.Now); idGen is the asset
// id generator (nil falls back to uuid.NewString) — tests pin both for
// deterministic output.
func NewMemoryStore(clock func() time.Time, idGen func() string) *MemoryStore {
	if clock == nil {
		clock = time.Now
	}
	if idGen == nil {
		idGen = uuid.NewString
	}
	return &MemoryStore{
		rows:   make(map[string]*memRow),
		byHash: make(map[string]string),
		byKey:  make(map[string]string),
		clock:  clock,
		idGen:  idGen,
	}
}

// hashKey turns a sha256 byte slice into a stable map key. We use a
// base64 string rather than the raw []byte because Go maps reject
// slice keys — and copying into [32]byte arrays would still require
// validating length, which we want to do exactly once at the boundary.
func hashKey(h []byte) string {
	return base64.RawStdEncoding.EncodeToString(h)
}

func (s *MemoryStore) Insert(_ context.Context, a AssetCreate) (Asset, error) {
	if len(a.SHA256) != 32 {
		return Asset{}, errors.New("admin/media: sha256 must be 32 bytes")
	}
	if a.StorageKey == "" {
		return Asset{}, errors.New("admin/media: storage_key is required")
	}
	if a.Filename == "" {
		return Asset{}, errors.New("admin/media: filename is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	hk := hashKey(a.SHA256)
	if existingID, ok := s.byHash[hk]; ok {
		row := s.rows[existingID]
		if row.deletedAt == nil {
			// Idempotent re-insert of an existing asset returns the
			// existing row. This is the dedupe contract the upload
			// handler relies on; without it a race between two upload
			// browsers would explode with a unique-violation.
			return row.Asset, nil
		}
		// Soft-deleted row with the same content — we still want
		// dedupe but we need to undelete first so the asset is
		// visible to the grid again. The alternative (insert a new
		// row pointing at the same bytes) would defeat dedupe; the
		// alternative (return ErrConflict) would surface a confusing
		// error to an operator who legitimately re-uploaded a deleted
		// asset.
		row.deletedAt = nil
		row.UpdatedAt = s.clock().UTC()
		return row.Asset, nil
	}
	if _, ok := s.byKey[a.StorageKey]; ok {
		return Asset{}, errors.New("admin/media: storage_key collision")
	}

	now := s.clock().UTC()
	id := s.idGen()
	row := &memRow{
		Asset: Asset{
			ID:         id,
			Filename:   a.Filename,
			MimeType:   a.MimeType,
			ByteSize:   a.ByteSize,
			Width:      a.Width,
			Height:     a.Height,
			AltText:    "",
			Caption:    "",
			StorageKey: a.StorageKey,
			UploaderID: a.UploaderID,
			CreatedAt:  now,
			UpdatedAt:  now,
			// Tags is non-nil so JSON renders `"tags": []` rather
			// than `"tags": null`. The grid expects an array.
			Tags: []string{},
		},
		sha256: append([]byte(nil), a.SHA256...),
	}
	s.rows[id] = row
	s.byHash[hk] = id
	s.byKey[a.StorageKey] = id
	return row.Asset, nil
}

func (s *MemoryStore) GetByHash(_ context.Context, h []byte) (Asset, error) {
	if len(h) != 32 {
		return Asset{}, errors.New("admin/media: sha256 must be 32 bytes")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHash[hashKey(h)]
	if !ok {
		return Asset{}, ErrNotFound
	}
	row := s.rows[id]
	if row.deletedAt != nil {
		return Asset{}, ErrNotFound
	}
	return row.Asset, nil
}

func (s *MemoryStore) GetByID(_ context.Context, id string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return Asset{}, ErrNotFound
	}
	return row.Asset, nil
}

func (s *MemoryStore) List(_ context.Context, f ListFilter) (Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filtered := make([]Asset, 0, len(s.rows))
	for _, row := range s.rows {
		if row.deletedAt != nil {
			continue
		}
		if f.MimeClass != "" && !matchesMimeClass(row.MimeType, f.MimeClass) {
			continue
		}
		if f.CollectionID != nil && !collectionMatches(row.CollectionID, *f.CollectionID) {
			continue
		}
		filtered = append(filtered, row.Asset)
	}

	// Sort newest-first. ID is the tiebreaker so a tie on
	// created_at (which happens in tests with a fixed clock) produces
	// deterministic output.
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		}
		return filtered[i].ID > filtered[j].ID
	})

	// Cursor decoding. The cursor is the index of the next row to
	// return, base64-encoded so the wire surface is opaque. A real
	// Postgres store will encode (created_at, id); the wire shape
	// stays identical so the UI doesn't have to know which backend
	// it is talking to.
	start := 0
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil {
			return Page{}, errors.New("admin/media: invalid cursor")
		}
		for i, a := range filtered {
			if a.ID == string(bytes.TrimSpace(raw)) {
				start = i
				break
			}
		}
	}

	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	out := Page{Data: append([]Asset(nil), filtered[start:end]...)}
	if end < len(filtered) {
		out.Pagination.NextCursor = base64.RawURLEncoding.EncodeToString(
			[]byte(filtered[end].ID),
		)
	}
	return out, nil
}

func (s *MemoryStore) UpdateMetadata(_ context.Context, id string, u AssetUpdate) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return Asset{}, ErrNotFound
	}
	if u.AltText != nil {
		row.AltText = *u.AltText
	}
	if u.Caption != nil {
		row.Caption = *u.Caption
	}
	row.UpdatedAt = s.clock().UTC()
	return row.Asset, nil
}

func (s *MemoryStore) SoftDelete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return ErrNotFound
	}
	now := s.clock().UTC()
	row.deletedAt = &now
	row.UpdatedAt = now
	return nil
}

// SetCollection re-files an asset into a collection. nil
// collectionID moves the asset to the implicit root. Mutates
// CollectionID + UpdatedAt; idempotent on a no-op move.
func (s *MemoryStore) SetCollection(_ context.Context, id string, collectionID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return ErrNotFound
	}
	if collectionID == nil {
		row.CollectionID = nil
	} else {
		v := *collectionID
		row.CollectionID = &v
	}
	row.UpdatedAt = s.clock().UTC()
	return nil
}

// SetTags replaces the asset's tag list. The handler is expected to
// normalise the list before calling (lowercase, deduplicated, no
// leading/trailing whitespace). Idempotent.
func (s *MemoryStore) SetTags(_ context.Context, id string, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return ErrNotFound
	}
	// Defensive copy so a caller mutating the input slice can't
	// poison the row.
	out := make([]string, len(tags))
	copy(out, tags)
	row.Tags = out
	row.UpdatedAt = s.clock().UTC()
	return nil
}

// collectionMatches reports whether a row's CollectionID lines up
// with a filter value. The filter sentinel "" means "match assets
// at the implicit root (CollectionID is nil)"; any other value is a
// concrete UUID compared by string equality.
func collectionMatches(rowID *string, filterID string) bool {
	if filterID == "" {
		return rowID == nil
	}
	if rowID == nil {
		return false
	}
	return *rowID == filterID
}

// SetVariants replaces the variant list on the asset's row. Used by
// the worker after the media.process task writes variants to storage.
// Idempotent: a re-run overwrites the previous list. The clock
// advances so the UpdatedAt column reflects the variant-write rather
// than the original insert.
func (s *MemoryStore) SetVariants(_ context.Context, id string, variants []Variant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.deletedAt != nil {
		return ErrNotFound
	}
	// Copy so callers can mutate the input slice without poisoning
	// the row. Variants are small and the volume per asset is fixed
	// (four entries per format), so the copy is cheap.
	out := make([]Variant, len(variants))
	copy(out, variants)
	row.Variants = out
	row.UpdatedAt = s.clock().UTC()
	return nil
}

// matchesMimeClass maps the UI's chip filter to a MIME-type predicate.
// Document covers PDF + the office formats because operators recognise
// "documents" as "things I would open in a viewer", not "things matching
// a specific MIME family". video/* and image/* are obvious. Anything
// else (audio/*, font/*, etc.) sits in the "no filter chip claims it"
// bucket which only the unfiltered grid shows.
func matchesMimeClass(mime, class string) bool {
	switch class {
	case "image":
		return len(mime) >= 6 && equalFoldASCII(mime[:6], "image/")
	case "video":
		return len(mime) >= 6 && equalFoldASCII(mime[:6], "video/")
	case "document":
		if equalFoldASCII(mime, "application/pdf") {
			return true
		}
		const prefix = "application/vnd."
		if len(mime) >= len(prefix) && equalFoldASCII(mime[:len(prefix)], prefix) {
			return true
		}
		return equalFoldASCII(mime, "text/plain")
	default:
		return true
	}
}
