// Package webauthn wraps go-webauthn/webauthn into the GoNext-shaped
// auth surface (issue #159).
//
// The package is structured around three pieces:
//
//   - Service: holds the *webauthn.WebAuthn instance (configured from
//     the binary's site URL + relying-party id), the credentials store
//     handle, and the user resolver. The HTTP handlers under
//     apps/api/internal/auth/webauthn call into Service for every
//     state-mutating operation.
//
//   - User: a value-type adapter implementing webauthn.User so the
//     library can produce protocol-shaped assertions. It carries the
//     user's UUID handle, display name, and the loaded credential
//     list.
//
//   - Store: the persistence seam. The production implementation is
//     PgxStore (Postgres-backed via the webauthn_credentials table
//     from migration 000035); MemoryStore exists for tests and the
//     no-DB dev loop.
//
// All wire shapes (begin/finish payload bodies) are imported from the
// underlying library — we don't redeclare them. The package's job is
// to plumb identity (current-session user) + persistence into the
// library's stateless verification methods.
package webauthn
