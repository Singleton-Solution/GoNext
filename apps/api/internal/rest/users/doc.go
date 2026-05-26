// Package users serves the public read-only `/api/v1/users` REST
// surface. Unlike the admin user management endpoints (which require
// list_users / edit_users capabilities and surface PII), this package
// only exposes the public profile fields:
//
//	id, handle, display_name, created_at
//
// Email, capabilities, role memberships, and password material never
// appear in this surface. This is deliberate — the public API mirrors
// what a public-facing site renders on an author page (the bylines,
// the avatar, the join date), and PII leakage at this layer would be
// a privacy regression vs. the admin surface.
//
// The contract intentionally matches the posts/comments REST shape:
//
//	GET  /api/v1/users         — list users (cursor pagination)
//	GET  /api/v1/users/{id}    — fetch a single user by id OR by handle
//
// "By handle" is a convenience the posts surface doesn't have: handles
// are short and stable enough that linkrot is unlikely, while UUIDs
// are clumsy in URLs. The handler dispatches on whether the path
// param parses as a UUID; non-UUID strings are treated as handles.
//
// Authentication: the public surface is anonymous-friendly. The auth
// middleware decorates the request opportunistically when a session
// cookie is present (the email field would be populated for the
// viewer themselves), but no principal is required for the read path.
package users
