// Package static serves theme assets (style.css, JS, etc.) over HTTP
// directly from the on-disk theme directory.
//
// The route shape is:
//
//	GET /themes/{slug}/{file...}
//
// where {slug} is either a real theme directory name (e.g. "gn-hello")
// or the virtual sentinel "active", which the package resolves through
// the supplied ActiveResolver against the live core.active_theme option.
//
// The handler is a thin static-file server with a few targeted hardenings:
//
//   - It rejects any path containing ".." or an absolute prefix BEFORE
//     touching the filesystem (path-traversal guard).
//   - It refuses to serve files outside the configured ThemeDir even if
//     a symlink inside a theme directory pointed elsewhere — every
//     served path is rebuilt with filepath.Clean and re-checked against
//     the canonical ThemeDir prefix.
//   - It sets Content-Type from the file extension (css, js, woff2,
//     png, etc.) rather than sniffing.
//   - It sets Cache-Control: public, max-age=3600 so the public site +
//     CDN can cache the response; theme installs bump the slug, which
//     transparently busts the cached path.
//
// The package intentionally does NOT depend on the customizer or
// admin/themes packages — both expose the same options-table key but
// import paths there carry full read+write Store surfaces. The caller
// supplies a closure that performs whatever lookup it wants. In
// production main.go wires this against the existing PgxActiveStore;
// tests pass a literal closure.
package static
