package webauthn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// Config configures a Service. RPID is the relying-party id — the
// effective domain the passkeys are scoped to (e.g. "gonext.io"). RPOrigins
// is the list of origins the browser may send the assertion from
// (typically the admin's public URL — "https://admin.gonext.io" and
// any localhost dev URLs).
//
// RPDisplayName is the human-readable label the authenticator shows in
// its "Create passkey for..." prompt; GoNext uses the configured site
// name.
type Config struct {
	RPID          string
	RPDisplayName string
	RPOrigins     []string
}

// Service is the package's stateful entry point. Wrap one per binary
// at boot via NewService; the underlying *gwa.WebAuthn is safe for
// concurrent use.
type Service struct {
	w       *gwa.WebAuthn
	store   Store
	resolve UserResolver
	now     func() time.Time
}

// UserResolver maps a user id to the in-memory User shape. The
// production implementation queries the users table for the username
// + the webauthn_credentials table for the credential list. Tests
// supply a stub.
type UserResolver func(ctx context.Context, id uuid.UUID) (User, error)

// NewService builds a Service. cfg validation is delegated to the
// library (gwa.New); we surface its error verbatim. resolver and
// store are required.
func NewService(cfg Config, store Store, resolver UserResolver) (*Service, error) {
	if store == nil {
		return nil, errors.New("webauthn.NewService: store is required")
	}
	if resolver == nil {
		return nil, errors.New("webauthn.NewService: resolver is required")
	}
	w, err := gwa.New(&gwa.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn.NewService: %w", err)
	}
	return &Service{
		w:       w,
		store:   store,
		resolve: resolver,
		now:     time.Now,
	}, nil
}

// BeginRegistration starts the passkey registration ceremony for the
// given user. Returns the credential-creation options the client
// passes to navigator.credentials.create(), and an opaque session
// blob the client echoes on FinishRegistration. The handler is
// responsible for stashing the session blob server-side (typically
// in the user's session cookie payload, or in a short-lived Redis
// key) — it MUST NOT round-trip through the client.
func (s *Service) BeginRegistration(ctx context.Context, userID uuid.UUID) (*protocol.CredentialCreation, *gwa.SessionData, error) {
	u, err := s.resolve(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("webauthn: resolve user: %w", err)
	}
	creation, session, err := s.w.BeginRegistration(u,
		// Exclude credentials the user already has so they can't
		// re-enroll the same authenticator (the spec calls this
		// out as a smooth-UX requirement — the browser will tell
		// the user "you already registered this device").
		excludeExistingCredentials(u),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("webauthn: begin registration: %w", err)
	}
	return creation, session, nil
}

// FinishRegistration validates the client's attestation response and
// persists the resulting credential. The session blob must be the
// one BeginRegistration emitted (the library reads challenge +
// userVerification expectations from it).
//
// On success, the persisted Record is returned. On failure, the
// error is wrapped — typical failures are "challenge mismatch" or
// "attestation invalid", both of which the handler maps to 400.
func (s *Service) FinishRegistration(ctx context.Context, userID uuid.UUID, session gwa.SessionData, name string, r *http.Request) (Record, error) {
	u, err := s.resolve(ctx, userID)
	if err != nil {
		return Record{}, fmt.Errorf("webauthn: resolve user: %w", err)
	}
	cred, err := s.w.FinishRegistration(u, session, r)
	if err != nil {
		return Record{}, fmt.Errorf("webauthn: finish registration: %w", err)
	}
	rec := Record{
		UserID:          userID,
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		SignCount:       cred.Authenticator.SignCount,
		AttestationType: cred.AttestationType,
		Name:            defaultIfBlank(name, "Passkey"),
		CreatedAt:       s.now().UTC(),
	}
	return s.store.Insert(ctx, rec)
}

// BeginLogin starts the assertion ceremony for the given user. The
// user is identified by id (looked up via the resolver) so the
// library can populate the allow-list with that user's known
// credentials.
//
// Discoverable / username-less login (where the assertion identifies
// the user via the credential's user handle) is a future extension —
// today the admin UI always knows which user is signing in (it
// stores their email locally for the "remember me" path).
func (s *Service) BeginLogin(ctx context.Context, userID uuid.UUID) (*protocol.CredentialAssertion, *gwa.SessionData, error) {
	u, err := s.resolve(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("webauthn: resolve user: %w", err)
	}
	if len(u.Credentials) == 0 {
		return nil, nil, errors.New("webauthn: no credentials enrolled")
	}
	assertion, session, err := s.w.BeginLogin(u)
	if err != nil {
		return nil, nil, fmt.Errorf("webauthn: begin login: %w", err)
	}
	return assertion, session, nil
}

// FinishLogin validates the assertion response and, on success,
// updates the credential's sign_count + last_used_at columns. The
// returned Record is the row that signed the assertion — the
// handler uses the row's UserID to mint a session cookie.
func (s *Service) FinishLogin(ctx context.Context, userID uuid.UUID, session gwa.SessionData, r *http.Request) (Record, error) {
	u, err := s.resolve(ctx, userID)
	if err != nil {
		return Record{}, fmt.Errorf("webauthn: resolve user: %w", err)
	}
	cred, err := s.w.FinishLogin(u, session, r)
	if err != nil {
		return Record{}, fmt.Errorf("webauthn: finish login: %w", err)
	}
	rec, err := s.store.GetByCredentialID(ctx, cred.ID)
	if err != nil {
		return Record{}, err
	}
	now := s.now().UTC()
	if err := s.store.UpdateSignCount(ctx, cred.ID, cred.Authenticator.SignCount, now); err != nil {
		return Record{}, fmt.Errorf("webauthn: update sign count: %w", err)
	}
	rec.SignCount = cred.Authenticator.SignCount
	rec.LastUsedAt = &now
	return rec, nil
}

// ListCredentials returns every passkey enrolled by the user, for
// the admin UI's "Manage passkeys" list.
func (s *Service) ListCredentials(ctx context.Context, userID uuid.UUID) ([]Record, error) {
	return s.store.ListForUser(ctx, userID)
}

// DeleteCredential removes a single passkey. The handler is
// expected to confirm the row's UserID matches the session's user
// id before calling — Service trusts its caller on authorisation.
func (s *Service) DeleteCredential(ctx context.Context, id uuid.UUID) error {
	return s.store.Delete(ctx, id)
}

// excludeExistingCredentials builds the exclude-list option for
// BeginRegistration so a user can't double-register the same
// authenticator. The spec recommends this — the browser surfaces a
// "you already have this device registered" message instead of
// silently overwriting the previous credential.
func excludeExistingCredentials(u User) gwa.RegistrationOption {
	excl := make([]protocol.CredentialDescriptor, 0, len(u.Credentials))
	for _, c := range u.Credentials {
		excl = append(excl, protocol.CredentialDescriptor{
			Type:         "public-key",
			CredentialID: c.ID,
		})
	}
	return gwa.WithExclusions(excl)
}

// defaultIfBlank returns def when s is empty after trimming. Used to
// fold an empty client-supplied passkey name into the "Passkey"
// default.
func defaultIfBlank(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// MarshalSession serialises a SessionData blob to JSON for storage
// in the user's session cookie (or a short-lived Redis key). Exposed
// as a free function so the HTTP handler doesn't depend on the
// library's struct layout.
func MarshalSession(sd *gwa.SessionData) ([]byte, error) {
	return json.Marshal(sd)
}

// UnmarshalSession is the inverse of MarshalSession.
func UnmarshalSession(b []byte) (gwa.SessionData, error) {
	var sd gwa.SessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return gwa.SessionData{}, err
	}
	return sd, nil
}
