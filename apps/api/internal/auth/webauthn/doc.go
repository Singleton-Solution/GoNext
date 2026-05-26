// Package webauthn mounts the four HTTP routes that drive the
// browser's WebAuthn ceremony (issue #159):
//
//	POST /api/v1/auth/webauthn/register/begin   — anonymous? no: RequireSession
//	POST /api/v1/auth/webauthn/register/finish  — RequireSession
//	POST /api/v1/auth/webauthn/login/begin      — anonymous (body has user id)
//	POST /api/v1/auth/webauthn/login/finish     — anonymous (body has session blob)
//
// Plus the admin surface for listing + deleting credentials:
//
//	GET  /api/v1/auth/webauthn/credentials       — RequireSession
//	DELETE /api/v1/auth/webauthn/credentials/{id} — RequireSession
//
// The package is wired by main.go via Mount; the underlying
// stateless Service lives in packages/go/auth/webauthn.
//
// Session-data persistence between begin/finish is delegated to a
// SessionStore — production wiring uses Redis with a short TTL
// (5 minutes) keyed by a random ceremony id returned in the
// begin payload.
package webauthn
