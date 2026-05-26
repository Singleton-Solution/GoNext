package webauthn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	wapkg "github.com/Singleton-Solution/GoNext/packages/go/auth/webauthn"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/google/uuid"
)

// memSessionStore is a tiny in-memory SessionStore for the handler
// tests. We don't bother with TTL expiry — the tests cover the
// success path; expiry behaviour is exercised by the production
// Redis store in its own package.
type memSessionStore struct {
	mu  sync.Mutex
	bag map[string][]byte
}

func newMemSessionStore() *memSessionStore {
	return &memSessionStore{bag: map[string][]byte{}}
}

func (m *memSessionStore) Put(_ context.Context, k string, b []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bag[k] = b
	return nil
}

func (m *memSessionStore) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bag[k]
	if !ok {
		return nil, io.EOF
	}
	return b, nil
}

func (m *memSessionStore) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.bag, k)
	return nil
}

// newTestService builds a Service backed by an in-memory store with a
// stub resolver — the resolver returns a User with the supplied id
// and no enrolled credentials.
func newTestService(t *testing.T, uid uuid.UUID) *wapkg.Service {
	t.Helper()
	svc, err := wapkg.NewService(wapkg.Config{
		RPID:          "localhost",
		RPDisplayName: "Test",
		RPOrigins:     []string{"https://localhost"},
	}, wapkg.NewMemoryStore(),
		func(_ context.Context, id uuid.UUID) (wapkg.User, error) {
			return wapkg.User{ID: id, Username: "user@example.com"}, nil
		})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_ = uid
	return svc
}

// TestMount_RegisterBeginRequiresSession asserts the auth gate on the
// register/begin path. Without a CurrentUserID the handler must 401.
func TestMount_RegisterBeginRequiresSession(t *testing.T) {
	uid := uuid.New()
	mux := http.NewServeMux()
	if err := Mount(mux, Deps{
		Service:       newTestService(t, uid),
		Sessions:      newMemSessionStore(),
		Policy:        policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		CurrentUserID: func(_ *http.Request) (uuid.UUID, bool) { return uuid.Nil, false },
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/webauthn/register/begin",
		bytes.NewBuffer([]byte("{}")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d (body=%s)", rec.Code, rec.Body)
	}
}

// TestMount_RegisterBeginIssuesCeremonyID exercises the happy path:
// with a session, the handler returns 200 with a ceremony id and
// stashes a SessionData blob under that id. We don't verify the
// SessionData contents (that's the library's job); we only confirm
// the wire payload shape.
func TestMount_RegisterBeginIssuesCeremonyID(t *testing.T) {
	uid := uuid.New()
	mux := http.NewServeMux()
	store := newMemSessionStore()
	if err := Mount(mux, Deps{
		Service:       newTestService(t, uid),
		Sessions:      store,
		Policy:        policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		CurrentUserID: func(_ *http.Request) (uuid.UUID, bool) { return uid, true },
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/webauthn/register/begin",
		bytes.NewBuffer([]byte("{}")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d (body=%s)", rec.Code, rec.Body)
	}
	var body struct {
		CeremonyID string `json:"ceremony_id"`
		Options    any    `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.CeremonyID == "" {
		t.Fatal("expected non-empty ceremony id")
	}
	if body.Options == nil {
		t.Fatal("expected non-nil options")
	}
	// SessionStore should now have a blob keyed under the registration prefix.
	if got, err := store.Get(context.Background(), "webauthn:reg:"+uid.String()+":"+body.CeremonyID); err != nil || len(got) == 0 {
		t.Fatalf("expected SessionData blob stored; got err=%v len=%d", err, len(got))
	}
}

// TestMount_DeleteCredential_OwnershipCheck guards the per-row
// authorisation: a signed-in user can ONLY delete their own
// credentials. We seed two users, attempt to delete user-B's
// credential while signed in as user-A, and assert 404.
func TestMount_DeleteCredential_OwnershipCheck(t *testing.T) {
	userA := uuid.New()
	userB := uuid.New()

	// Build a store with one credential owned by user B.
	memStore := wapkg.NewMemoryStore()
	rec, err := memStore.Insert(context.Background(), wapkg.Record{
		UserID:       userB,
		CredentialID: []byte("c"),
		PublicKey:    []byte("p"),
		Name:         "B's phone",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc, err := wapkg.NewService(wapkg.Config{
		RPID:          "localhost",
		RPDisplayName: "Test",
		RPOrigins:     []string{"https://localhost"},
	}, memStore,
		func(_ context.Context, id uuid.UUID) (wapkg.User, error) {
			return wapkg.User{ID: id, Username: "x"}, nil
		})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mux := http.NewServeMux()
	if err := Mount(mux, Deps{
		Service:       svc,
		Sessions:      newMemSessionStore(),
		Policy:        policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		CurrentUserID: func(_ *http.Request) (uuid.UUID, bool) { return userA, true },
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/auth/webauthn/credentials/"+rec.ID.String(), nil)
	rrec := httptest.NewRecorder()
	mux.ServeHTTP(rrec, req)
	if rrec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner delete; got %d", rrec.Code)
	}
	// The credential should still exist.
	if _, err := memStore.GetByCredentialID(context.Background(), []byte("c")); err != nil {
		t.Fatalf("credential was deleted despite ownership check; %v", err)
	}
}
