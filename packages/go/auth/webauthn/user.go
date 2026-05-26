package webauthn

import (
	gwa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// User is the in-memory representation of a WebAuthn-enrolled user.
// It implements the [gwa.User] interface so it can be passed directly
// to BeginRegistration / BeginLogin / FinishLogin.
//
// The fields are public so the HTTP handlers can construct one from a
// session row + a Store.ListForUser call — there's no constructor on
// purpose. A future refactor that moves enrolment off the live session
// (e.g. for the discoverable-login flow) only needs to populate the
// fields it has.
type User struct {
	// ID is the user's UUID. We use the raw 16-byte representation
	// as the WebAuthn user handle; the spec allows up to 64 bytes
	// of opaque payload, and the UUID byte slice is the
	// smallest stable identifier we already have.
	ID uuid.UUID

	// Username is what we surface in the admin UI's "registered
	// as" line. We pass it as both webauthn.Name and
	// webauthn.DisplayName — separating the two is a UX nicety the
	// admin doesn't expose today.
	Username string

	// Credentials is the list of stored passkeys the user has.
	// Populated by Store.ListForUser before passing the User to
	// the library. On the registration path this can be empty (no
	// passkeys yet); on the login path it MUST include the
	// credentials the user is expected to sign with.
	Credentials []gwa.Credential
}

// WebAuthnID returns the user handle. The library stamps this onto
// the registration payload so the authenticator can later present it
// in the assertion response.
func (u User) WebAuthnID() []byte {
	// uuid.UUID is a fixed-size array; convert to slice for the
	// library's []byte expectation.
	b := u.ID
	return b[:]
}

// WebAuthnName returns the username used for the registration's
// `name` field. Per the WebAuthn spec this is a stable identifier
// (an email, a handle) intended for use in the authenticator's
// account chooser. We use the GoNext username verbatim.
func (u User) WebAuthnName() string { return u.Username }

// WebAuthnDisplayName returns the human-readable name surfaced
// alongside the account chooser. We don't have a separate display
// name in GoNext today, so we reuse Username.
func (u User) WebAuthnDisplayName() string { return u.Username }

// WebAuthnCredentials returns the user's enrolled passkeys.
func (u User) WebAuthnCredentials() []gwa.Credential { return u.Credentials }
