// Package wprest is a live WordPress REST API source for the
// migration importer.
//
// Where the wxr package consumes an offline XML export, wprest hits
// a running WordPress install over HTTP and walks the standard
// REST endpoints under /wp-json/wp/v2/:
//
//   - /posts        → wxr.Post (PostType="post")
//   - /pages        → wxr.Post (PostType="page")
//   - /categories   → wxr.Category
//   - /tags         → wxr.Tag
//   - /users        → wxr.Author
//   - /media        → wxr.Post (PostType="attachment")
//
// Each endpoint is iterated with ?per_page=100 and the page-counter
// pagination protocol WP encodes in the X-WP-TotalPages response
// header. Authentication is via Application Passwords (basic auth
// with the WP user's login + an admin-issued app password).
//
// The package emits *wxr.* record values so the rest of the importer
// pipeline doesn't need to care which source it's reading from. A
// caller swaps wxr.NewParser for wprest.NewClient and uses the same
// importer downstream.
//
// Network failure handling is intentionally minimal here: the Client
// surfaces transport errors and lets the caller decide whether to
// retry or fail the migration. The Client is concurrency-safe for
// reads.
//
// See issue #163.
package wprest
