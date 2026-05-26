package webauthn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	wapkg "github.com/Singleton-Solution/GoNext/packages/go/auth/webauthn"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/google/uuid"
)

// SessionStore persists the ceremony's SessionData between the
// begin and finish requests. We don't store it in the user's
// session cookie because (a) the cookie path is ours, not the
// browser's, and (b) the login flow's begin/finish happens BEFORE
// any session cookie exists (the user hasn't signed in yet).
//
// The production wiring is Redis (TTL = 5 minutes); a MemoryStore
// is provided for tests.
type SessionStore interface {
	Put(ctx context.Context, key string, blob []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// Deps is the dependency bag for Mount.
type Deps struct {
	// Service is the underlying packages/go/auth/webauthn Service.
	// Required.
	Service *wapkg.Service

	// Sessions is the ceremony-state store. Required.
	Sessions SessionStore

	// Policy is the capability checker for the admin (list/delete)
	// routes. Required.
	Policy policy.Policy

	// CurrentUserID extracts the signed-in user id from the
	// request. For the register / list / delete routes this is
	// required to be non-nil (those routes run behind
	// RequireSession). For the login route, the user id is
	// taken from the request body — see beginLoginRequest.
	CurrentUserID func(r *http.Request) (uuid.UUID, bool)

	// SessionTTL is how long a ceremony-state blob lives before
	// it's auto-expired. Default 5 minutes.
	SessionTTL time.Duration

	// Logger receives non-fatal handler diagnostics. Required.
	Logger *slog.Logger
}

// Mount registers all webauthn routes on mux. Returns an error if
// Deps is incomplete.
func Mount(mux *http.ServeMux, d Deps) error {
	if d.Service == nil {
		return errors.New("webauthn.Mount: Service is required")
	}
	if d.Sessions == nil {
		return errors.New("webauthn.Mount: Sessions is required")
	}
	if d.CurrentUserID == nil {
		return errors.New("webauthn.Mount: CurrentUserID is required")
	}
	if d.Logger == nil {
		return errors.New("webauthn.Mount: Logger is required")
	}
	if d.SessionTTL == 0 {
		d.SessionTTL = 5 * time.Minute
	}
	h := &handler{d: d}
	mux.HandleFunc("POST /api/v1/auth/webauthn/register/begin", h.beginRegister)
	mux.HandleFunc("POST /api/v1/auth/webauthn/register/finish", h.finishRegister)
	mux.HandleFunc("POST /api/v1/auth/webauthn/login/begin", h.beginLogin)
	mux.HandleFunc("POST /api/v1/auth/webauthn/login/finish", h.finishLogin)
	mux.HandleFunc("GET /api/v1/auth/webauthn/credentials", h.listCredentials)
	mux.HandleFunc("DELETE /api/v1/auth/webauthn/credentials/{id}", h.deleteCredential)
	return nil
}

type handler struct {
	d Deps
}

// beginRegisterRequest is intentionally empty — the user is
// identified by the current session and the friendly name is
// supplied at the finish step (so the user can decide what to call
// the passkey AFTER the browser confirmed it).
type beginRegisterRequest struct{}

// beginRegisterResponse carries the credential-creation options
// (passed verbatim to the browser) plus the ceremony id the client
// will echo on the finish request.
type beginRegisterResponse struct {
	CeremonyID string `json:"ceremony_id"`
	Options    any    `json:"options"`
}

func (h *handler) beginRegister(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.d.CurrentUserID(r)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	creation, session, err := h.d.Service.BeginRegistration(r.Context(), uid)
	if err != nil {
		h.d.Logger.Warn("webauthn.beginRegister failed", slog.Any("err", err))
		writeJSONErr(w, http.StatusBadRequest, "begin_failed")
		return
	}
	blob, err := wapkg.MarshalSession(session)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	cid, err := newCeremonyID()
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	if err := h.d.Sessions.Put(r.Context(), keyRegister(uid, cid), blob, h.d.SessionTTL); err != nil {
		h.d.Logger.Warn("webauthn: store ceremony", slog.Any("err", err))
		writeJSONErr(w, http.StatusInternalServerError, "store_failed")
		return
	}
	writeJSON(w, http.StatusOK, beginRegisterResponse{
		CeremonyID: cid,
		Options:    creation,
	})
}

// finishRegisterRequest is the body sent by the client on
// /register/finish. CeremonyID is the opaque key returned at begin;
// AttestationResponse is the raw browser payload (the library
// re-parses it from the HTTP body — we don't decode it here).
// Name is the user-chosen friendly name ("Phone", "YubiKey 5C",
// ...); the handler defaults it to "Passkey" when empty.
type finishRegisterRequest struct {
	CeremonyID string `json:"ceremony_id"`
	Name       string `json:"name,omitempty"`
}

func (h *handler) finishRegister(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.d.CurrentUserID(r)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// We can't ParseForm() AND have the library parse the
	// attestation later; the library reads the raw body via
	// r.Body so we need to teach it about the ceremony id via a
	// header instead — but to keep the wire simple, the client
	// posts a multipart-ish blob: ceremony_id + name come in
	// query params, the attestation JSON is the body.
	cid := r.URL.Query().Get("ceremony_id")
	name := r.URL.Query().Get("name")
	if cid == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_ceremony_id")
		return
	}
	blob, err := h.d.Sessions.Get(r.Context(), keyRegister(uid, cid))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "expired_ceremony")
		return
	}
	session, err := wapkg.UnmarshalSession(blob)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_ceremony")
		return
	}
	rec, err := h.d.Service.FinishRegistration(r.Context(), uid, session, name, r)
	if err != nil {
		h.d.Logger.Warn("webauthn.finishRegister failed", slog.Any("err", err))
		writeJSONErr(w, http.StatusBadRequest, "finish_failed")
		return
	}
	_ = h.d.Sessions.Delete(r.Context(), keyRegister(uid, cid))
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         rec.ID.String(),
		"name":       rec.Name,
		"created_at": rec.CreatedAt,
	})
}

type beginLoginRequest struct {
	UserID string `json:"user_id"`
}

func (h *handler) beginLogin(w http.ResponseWriter, r *http.Request) {
	var req beginLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_user_id")
		return
	}
	assertion, session, err := h.d.Service.BeginLogin(r.Context(), uid)
	if err != nil {
		h.d.Logger.Warn("webauthn.beginLogin failed", slog.Any("err", err))
		// Don't leak "no credentials enrolled" vs "user not found".
		writeJSONErr(w, http.StatusBadRequest, "begin_failed")
		return
	}
	blob, err := wapkg.MarshalSession(session)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	cid, err := newCeremonyID()
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	if err := h.d.Sessions.Put(r.Context(), keyLogin(uid, cid), blob, h.d.SessionTTL); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "store_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ceremony_id": cid,
		"options":     assertion,
	})
}

func (h *handler) finishLogin(w http.ResponseWriter, r *http.Request) {
	cid := r.URL.Query().Get("ceremony_id")
	uidStr := r.URL.Query().Get("user_id")
	if cid == "" || uidStr == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_params")
		return
	}
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_user_id")
		return
	}
	blob, err := h.d.Sessions.Get(r.Context(), keyLogin(uid, cid))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "expired_ceremony")
		return
	}
	session, err := wapkg.UnmarshalSession(blob)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_ceremony")
		return
	}
	rec, err := h.d.Service.FinishLogin(r.Context(), uid, session, r)
	if err != nil {
		h.d.Logger.Warn("webauthn.finishLogin failed", slog.Any("err", err))
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	_ = h.d.Sessions.Delete(r.Context(), keyLogin(uid, cid))
	// Session minting on the API side happens via the login
	// service in apps/api/internal/auth/login. To keep this
	// handler decoupled we emit the user id; main.go's wiring is
	// expected to chain a session-mint step BEFORE this handler
	// (when the full passkey wiring lands the login flow becomes
	// one cohesive POST that returns Set-Cookie). For now we
	// return the matched user id so the client can either redirect
	// to a password-less login flow or surface "credential
	// verified".
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       rec.UserID.String(),
		"credential_id": rec.ID.String(),
	})
}

func (h *handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.d.CurrentUserID(r)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	recs, err := h.d.Service.ListCredentials(r.Context(), uid)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed")
		return
	}
	type item struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		CreatedAt  time.Time  `json:"created_at"`
		LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	}
	out := make([]item, 0, len(recs))
	for _, r := range recs {
		out = append(out, item{
			ID:         r.ID.String(),
			Name:       r.Name,
			CreatedAt:  r.CreatedAt,
			LastUsedAt: r.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *handler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.d.CurrentUserID(r)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	// Ownership check: confirm the credential belongs to the
	// caller before deleting. Without this a signed-in user
	// could delete any other user's passkey.
	recs, err := h.d.Service.ListCredentials(r.Context(), uid)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed")
		return
	}
	owned := false
	for _, rec := range recs {
		if rec.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		writeJSONErr(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.d.Service.DeleteCredential(r.Context(), id); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "delete_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// keyRegister + keyLogin produce the SessionStore keys for the two
// ceremonies. We include the user id in the key so a stale ceremony
// for user A cannot be consumed by user B even if they guess the
// ceremony id.
func keyRegister(uid uuid.UUID, cid string) string {
	return "webauthn:reg:" + uid.String() + ":" + cid
}

func keyLogin(uid uuid.UUID, cid string) string {
	return "webauthn:login:" + uid.String() + ":" + cid
}

// newCeremonyID returns a fresh 16-byte hex id for a registration /
// login ceremony. We use crypto/rand directly rather than uuid.New()
// so the id is byte-aligned (no UUID dashes / versioning) — the
// SessionStore keys it as an opaque tag.
func newCeremonyID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
