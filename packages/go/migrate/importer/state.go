package importer

import (
	"crypto/sha1" //nolint:gosec // not used as a cryptographic primitive — see comment

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/wxr"
)

// runState is the per-Run scratch space that maps WordPress-side
// identifiers to GoNext-side UUIDs. The maps stay in memory for
// the lifetime of a single Run; downstream callers that need a
// durable mapping table (issue #147) should write through the
// migration_map storage layer once that lands. The local maps
// here are the in-memory stub the issue brief permits.
type runState struct {
	// authorsByLogin maps wp:author_login to the GoNext users.id
	// the importer wrote (or found, under ConflictSkip). The
	// post.Creator field references the login, not the wp ID, so
	// this is what the post upsert keys off.
	authorsByLogin map[string]uuid.UUID

	// authorsByWPID maps wp:author_id → users.id for completeness
	// (some custom WXR variants reference the numeric id from
	// post.Meta).
	authorsByWPID map[string]uuid.UUID

	// terms maps (taxonomy, nicename) → terms.id. WP doesn't
	// give us a stable separator between categories and tags
	// inside a post's <category> children, so we key by the
	// (domain, nicename) pair to avoid collisions.
	terms map[termKey]uuid.UUID

	// posts maps wp:post_id → posts.id. Lets a future hierarchical
	// post lookup wp:post_parent without re-querying the DB.
	posts map[string]uuid.UUID
}

// termKey is the composite key used by runState.terms. We can't
// use a struct literal as a map key directly without it; defining
// it here keeps the map declaration readable.
type termKey struct {
	domain string // "category", "post_tag", or a custom taxonomy slug
	slug   string
}

// newRunState builds an empty runState. Maps are allocated
// up-front so the importer never branches on nil.
func newRunState() *runState {
	return &runState{
		authorsByLogin: map[string]uuid.UUID{},
		authorsByWPID:  map[string]uuid.UUID{},
		terms:          map[termKey]uuid.UUID{},
		posts:          map[string]uuid.UUID{},
	}
}

// recordAuthor caches the mapping for an author row.
func (s *runState) recordAuthor(a *wxr.Author, id uuid.UUID) {
	if s == nil || a == nil {
		return
	}
	if a.Login != "" {
		s.authorsByLogin[a.Login] = id
	}
	if a.ID != "" {
		s.authorsByWPID[a.ID] = id
	}
}

// recordTerm caches the mapping for a term row.
func (s *runState) recordTerm(domain, slug string, id uuid.UUID) {
	if s == nil || slug == "" {
		return
	}
	s.terms[termKey{domain: domain, slug: slug}] = id
}

// recordPost caches the mapping for a post row.
func (s *runState) recordPost(wpID string, id uuid.UUID) {
	if s == nil || wpID == "" {
		return
	}
	s.posts[wpID] = id
}

// lookupAuthorByLogin returns the GoNext author id and a found flag.
func (s *runState) lookupAuthorByLogin(login string) (uuid.UUID, bool) {
	if s == nil || login == "" {
		return uuid.UUID{}, false
	}
	v, ok := s.authorsByLogin[login]
	return v, ok
}

// lookupTerm returns the GoNext term id for (domain, slug).
func (s *runState) lookupTerm(domain, slug string) (uuid.UUID, bool) {
	if s == nil || slug == "" {
		return uuid.UUID{}, false
	}
	v, ok := s.terms[termKey{domain: domain, slug: slug}]
	return v, ok
}

// lookupPost returns the GoNext post id for a wp post id.
func (s *runState) lookupPost(wpID string) (uuid.UUID, bool) {
	if s == nil || wpID == "" {
		return uuid.UUID{}, false
	}
	v, ok := s.posts[wpID]
	return v, ok
}

// dryrunUUID returns a deterministic-but-fake UUID v8 derived from
// the WP id. Used by Dryrun so the runState behaves the same on a
// dry run as on a real import (e.g. post→author and post→parent
// lookups still hit the in-memory map without writing rows).
//
// The bytes are sha1(wpID) truncated to 16, with the version and
// variant nibbles forced to v8 / RFC 4122. v8 is the
// experimental/custom-namespace version, which is appropriate here
// — these UUIDs never leave runState and never reach the DB.
func dryrunUUID(wpID string) uuid.UUID {
	if wpID == "" {
		return uuid.UUID{}
	}
	h := sha1.Sum([]byte("gonext-dryrun:" + wpID)) //nolint:gosec // see file comment
	var u uuid.UUID
	copy(u[:], h[:16])
	// Version 8 (custom).
	u[6] = (u[6] & 0x0f) | 0x80
	// Variant: RFC 4122.
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}
