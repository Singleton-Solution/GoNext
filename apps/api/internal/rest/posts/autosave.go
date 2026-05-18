package posts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// ErrLocked is returned by AutosaveStore.Put when another user holds
// the post lock for post_id. Handlers translate this to 423 Locked.
//
// The lock-holder check happens inside the autosave write path rather
// than in the handler so the store can do the lookup atomically with
// the upsert (the production PgStore will run both inside a single
// transaction; the memory store mimics by holding s.mu).
var ErrLocked = errors.New("posts: locked by another user")

// Autosave is the on-the-wire shape returned by GET /autosave. It
// carries enough metadata for the client to compare against the
// canonical post and decide whether to offer "restore unsaved draft":
//
//   - PostID + UserID identify the (post, author) pair.
//   - Blocks is the autosaved block tree, raw JSON.
//   - UpdatedAt is the autosave's write timestamp; the client
//     compares against the canonical Post.UpdatedAt to detect
//     "the autosave is newer than the saved version".
type Autosave struct {
	PostID    string          `json:"post_id"`
	UserID    string          `json:"user_id"`
	Blocks    json.RawMessage `json:"blocks"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// AutosaveInput is the JSON body for POST /autosave. Only `blocks` is
// surfaced — autosave deliberately does NOT accept title, slug, status,
// or any other column. The contract is "stash the in-flight block
// tree"; everything else flows through the regular PATCH /posts/{id}.
type AutosaveInput struct {
	Blocks json.RawMessage `json:"blocks"`
}

// AutosaveStore is the persistence abstraction for the post_autosaves
// table. The production implementation talks to Postgres via pgxpool;
// tests use MemoryAutosaveStore.
//
// The contract is intentionally narrow:
//
//   - Get returns the latest autosave for (post_id, user_id) or
//     ErrNotFound when the user has no in-flight draft for this post.
//   - Put upserts the autosave row. If another user holds an
//     unexpired post_lock on post_id, Put returns ErrLocked instead
//     of writing — this is the same gate the regular PATCH handler
//     uses, but on the autosave write path it surfaces as 423 Locked
//     rather than the implicit "you don't have edit access" 403.
type AutosaveStore interface {
	Get(ctx context.Context, postID, userID string) (Autosave, error)
	Put(ctx context.Context, postID, userID string, blocks json.RawMessage) (Autosave, error)
}

// MemoryAutosaveStore is the in-process AutosaveStore used by tests.
// It is goroutine-safe; tests share one instance across parallel
// requests via httptest. The shape mirrors the production PgStore
// closely enough that handler tests catch interface-level bugs
// without needing a real Postgres.
//
// The locks field is the in-test approximation of post_locks. We
// don't model lock expiry here — the tests that exercise the lock
// path explicitly seed the lock via SetLockHolder, which is enough
// to validate the 423 path. The production store calls
// acquire_post_lock() in the same transaction.
type MemoryAutosaveStore struct {
	mu    sync.Mutex
	rows  map[string]map[string]*memoryAutosave // post_id -> user_id -> row
	locks map[string]string                     // post_id -> user_id currently holding the lock
	now   func() time.Time
}

type memoryAutosave struct {
	blocks    json.RawMessage
	updatedAt time.Time
}

// NewMemoryAutosaveStore returns an empty in-memory autosave store.
func NewMemoryAutosaveStore() *MemoryAutosaveStore {
	return &MemoryAutosaveStore{
		rows:  map[string]map[string]*memoryAutosave{},
		locks: map[string]string{},
		now:   time.Now,
	}
}

// SetNow lets tests pin the clock so updated_at comparisons are
// deterministic. Goroutine-safe to call before any Put; do not race
// against in-flight requests.
func (s *MemoryAutosaveStore) SetNow(f func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = f
}

// SetLockHolder seeds a fake post_lock for testing the 423 path.
// Passing an empty userID clears the lock (i.e. "the lock expired,
// nobody holds it now"). Goroutine-safe.
func (s *MemoryAutosaveStore) SetLockHolder(postID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" {
		delete(s.locks, postID)
		return
	}
	s.locks[postID] = userID
}

func (s *MemoryAutosaveStore) Get(_ context.Context, postID, userID string) (Autosave, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, ok := s.rows[postID]
	if !ok {
		return Autosave{}, ErrNotFound
	}
	row, ok := users[userID]
	if !ok {
		return Autosave{}, ErrNotFound
	}
	return Autosave{
		PostID:    postID,
		UserID:    userID,
		Blocks:    append(json.RawMessage(nil), row.blocks...),
		UpdatedAt: row.updatedAt,
	}, nil
}

func (s *MemoryAutosaveStore) Put(_ context.Context, postID, userID string, blocks json.RawMessage) (Autosave, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lock-holder gate. If somebody else holds the lock, refuse the
	// write — the editor will surface this as "another user is editing
	// this post" rather than letting both authors stomp on each other.
	// Same-user re-acquires (the common case: the editor heartbeats
	// every 60s, autosaves every 30s) fall through to the upsert.
	if holder, locked := s.locks[postID]; locked && holder != userID {
		return Autosave{}, ErrLocked
	}

	users, ok := s.rows[postID]
	if !ok {
		users = map[string]*memoryAutosave{}
		s.rows[postID] = users
	}
	row, ok := users[userID]
	if !ok {
		row = &memoryAutosave{}
		users[userID] = row
	}
	row.blocks = append(json.RawMessage(nil), blocks...)
	row.updatedAt = s.now().UTC()
	return Autosave{
		PostID:    postID,
		UserID:    userID,
		Blocks:    append(json.RawMessage(nil), row.blocks...),
		UpdatedAt: row.updatedAt,
	}, nil
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

// MountAutosave wires the autosave routes onto mux. The base path
// should be the same one passed to Mount — autosave lives under the
// per-post resource:
//
//	posts.MountAutosave(mux, "/api/v1/posts", AutosaveDeps{...})
//	posts.MountAutosave(mux, "/api/v1/pages", AutosaveDeps{...})
//
// The autosave routes share the same requireAuth gate as the main
// posts routes; an unauthenticated caller sees 401 before anything
// else runs.
func MountAutosave(mux *http.ServeMux, base string, deps AutosaveDeps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	h := &autosaveHandlers{
		posts:     deps.PostStore,
		autosaves: deps.AutosaveStore,
		policy:    deps.Policy,
		postType:  deps.PostType,
		caps:      capsFor(deps.PostType),
	}
	mux.Handle("POST "+base+"/{id}/autosave", h.requireAuth(h.put))
	mux.Handle("GET "+base+"/{id}/autosave", h.requireAuth(h.get))
	return nil
}

// AutosaveDeps is the dependency bundle for MountAutosave.
type AutosaveDeps struct {
	PostStore     Store
	AutosaveStore AutosaveStore
	Policy        policy.Policy
	PostType      string
}

func (d AutosaveDeps) validate() error {
	if d.PostStore == nil {
		return errors.New("posts.MountAutosave: Deps.PostStore is required")
	}
	if d.AutosaveStore == nil {
		return errors.New("posts.MountAutosave: Deps.AutosaveStore is required")
	}
	if d.Policy == nil {
		return errors.New("posts.MountAutosave: Deps.Policy is required")
	}
	if d.PostType != PostTypePost && d.PostType != PostTypePage {
		return errors.New("posts.MountAutosave: Deps.PostType must be 'post' or 'page'")
	}
	return nil
}

type autosaveHandlers struct {
	posts     Store
	autosaves AutosaveStore
	policy    policy.Policy
	postType  string
	caps      capabilitySet
}

func (h *autosaveHandlers) requireAuth(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// put handles POST /api/v1/posts/{id}/autosave. The body is an
// AutosaveInput; the response is the just-written Autosave. The
// post_lock gate is enforced inside the store — if another user
// holds the lock, we surface 423 Locked rather than the usual 403.
func (h *autosaveHandlers) put(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}

	// Load the post first so we can do the same author-vs-others
	// capability resolution the regular PATCH does. Autosave is a
	// "draft this user is producing"; the rules for who can edit a
	// post are the same here.
	existing, err := h.posts.Get(r.Context(), h.postType, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "post not found")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load post")
		return
	}

	requiredCap := h.caps.edit
	if existing.AuthorID != pr.UserID {
		requiredCap = h.caps.editOthers
	}
	if d := h.policy.Can(pr, requiredCap, &existing); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	var in AutosaveInput
	if err := decodeBody(r, &in); err != nil {
		writeValidationError(w, err)
		return
	}
	if len(in.Blocks) == 0 {
		router.WriteError(w, http.StatusBadRequest, "missing_blocks", "blocks is required")
		return
	}
	// blocks must be a valid JSON array — same contract as posts.content_blocks.
	if err := validateBlocksRawArray(in.Blocks); err != nil {
		writeValidationError(w, err)
		return
	}

	saved, err := h.autosaves.Put(r.Context(), id, pr.UserID, in.Blocks)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			// 423 Locked is the right code per RFC 4918: the resource
			// is locked by another principal, and the client can't
			// proceed until the lock releases or is stolen via the
			// regular PATCH/?steal=1 path (future endpoint).
			router.WriteError(w, http.StatusLocked, "locked", "post is locked by another user")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to save autosave")
		return
	}
	w.Header().Set(HeaderVersion, strconv.Itoa(existing.Version))
	router.WriteJSON(w, http.StatusOK, saved)
}

// get handles GET /api/v1/posts/{id}/autosave. Returns the latest
// autosave for (post, current user) or 204 No Content when no
// autosave exists. The client uses the absence to skip the recovery
// dialog entirely.
func (h *autosaveHandlers) get(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}

	// Existence + capability check, same as on put. We don't want a
	// caller with no edit cap to learn whether an autosave exists
	// for somebody else.
	existing, err := h.posts.Get(r.Context(), h.postType, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "post not found")
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load post")
		return
	}
	requiredCap := h.caps.edit
	if existing.AuthorID != pr.UserID {
		requiredCap = h.caps.editOthers
	}
	if d := h.policy.Can(pr, requiredCap, &existing); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	got, err := h.autosaves.Get(r.Context(), id, pr.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// 204 lets the client write `if (res.status === 204) skip()`
			// without parsing a body — there is no negative payload
			// to send.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load autosave")
		return
	}
	router.WriteJSON(w, http.StatusOK, got)
}

// validateBlocksRawArray asserts that raw is a JSON array. We don't
// enforce per-block shape here — that lives in validate.go and runs
// at the PATCH/POST layer; autosave deliberately stays permissive
// (the user may be mid-edit of an invalid tree, and forcing them to
// fix it before autosave defeats the purpose of autosave).
func validateBlocksRawArray(raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return validation{Code: "invalid_blocks", Detail: "blocks must be valid JSON"}
	}
	if _, ok := v.([]any); !ok {
		return validation{Code: "invalid_blocks", Detail: "blocks must be a JSON array"}
	}
	return nil
}
