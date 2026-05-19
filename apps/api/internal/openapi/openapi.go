// Package openapi exposes the GoNext API's OpenAPI 3.1 description and a
// minimal Swagger UI page for dev environments.
//
// The canonical spec lives at apps/api/openapi/gonext.openapi.json and is
// embedded into the binary at build time, so the served document and the
// repository copy never drift.
//
// Two handlers are provided:
//
//   - Handler() — serves the raw spec as application/json. Mount on
//     /openapi.json (or the legacy /api/v1/openapi.json) so downstream
//     tooling (Redoc, Spectral, generated SDKs) can fetch it.
//   - SwaggerUIHandler() — serves a single-page Swagger UI loaded from a
//     public CDN, pointed at the same spec. Intended for non-prod use; the
//     caller is responsible for gating it on environment.
//
// Routes are intentionally NOT mounted in main.go by this package. Wire-up
// is a one-line addition once the route table stabilizes — see this
// package's README for the snippet.
package openapi

import (
	_ "embed"
	"net/http"
	"strconv"
	"time"
)

// spec is the OpenAPI 3.1 description embedded at build time, JSON form.
//
// The path is relative to this Go source file, which means it is the same
// JSON checked into apps/api/openapi/. Keep them in sync by editing the
// YAML at apps/api/openapi/openapi.yaml — the JSON is derived from it.
//
//go:embed spec/gonext.openapi.json
var spec []byte

// specYAML is the YAML form of the same document, served at
// /api/openapi.yaml as a convenience for tooling that prefers YAML over JSON.
//
//go:embed spec/openapi.yaml
var specYAML []byte

// specETag is computed once at process start so conditional GETs (If-None-Match)
// can short-circuit. The spec is read-only at runtime, so a startup-time
// stamp based on the embedded bytes' length is sufficient — a content hash
// would be marginally stronger but pulls in crypto for no real benefit on
// a static asset.
var specETag = `"openapi-` + strconv.Itoa(len(spec)) + `"`

// specYAMLETag is the ETag for the YAML serialization. Distinct from the
// JSON ETag because the bytes differ — a client that GET-then-conditional
// against both surfaces should see independent caching.
var specYAMLETag = `"openapi-yaml-` + strconv.Itoa(len(specYAML)) + `"`

// startedAt is the Last-Modified value we advertise. It's set at package
// init, which is close enough to "when the binary was built" for caching
// purposes without needing to plumb buildinfo through.
var startedAt = time.Now().UTC()

// Handler returns an http.Handler that serves the embedded OpenAPI 3.1
// document as application/json.
//
// The handler only accepts GET and HEAD; other methods get 405. Conditional
// requests are honoured via ETag / Last-Modified.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", specETag)
		w.Header().Set("Cache-Control", "public, max-age=60")

		// http.ServeContent handles ETag/If-None-Match, Last-Modified, and
		// HEAD semantics for us. It needs a ReadSeeker, so wrap the embedded
		// bytes in a bytes.Reader via a thin helper.
		http.ServeContent(w, r, "gonext.openapi.json", startedAt, newSpecReader())
	})
}

// YAMLHandler returns an http.Handler that serves the embedded OpenAPI 3.1
// document as application/yaml.
//
// The contract mirrors Handler(): GET / HEAD only, conditional requests
// honoured via ETag / Last-Modified, a public 60-second cache. Mount at
// /api/openapi.yaml — see this package's README for the wire-up.
func YAMLHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("ETag", specYAMLETag)
		w.Header().Set("Cache-Control", "public, max-age=60")

		http.ServeContent(w, r, "openapi.yaml", startedAt, newSpecYAMLReader())
	})
}

// SpecYAMLBytes returns a copy of the embedded YAML spec. Same contract as
// SpecBytes — never share the backing array across callers.
func SpecYAMLBytes() []byte {
	out := make([]byte, len(specYAML))
	copy(out, specYAML)
	return out
}

// SwaggerUIHandler returns an http.Handler that serves a single-page
// Swagger UI loaded from the swagger-ui-dist CDN, configured to fetch the
// spec from /openapi.json on the same host.
//
// The page is intentionally minimal: no build step, no embedded JS bundle,
// just the canonical CDN URLs pinned to a known-good major version. It is
// designed for local development and staging — production deployments
// should leave this route unmounted or gate it behind admin auth.
//
// The CDN URLs are pinned to swagger-ui@5 to keep the surface stable; bump
// in a follow-up if a security advisory requires it.
func SwaggerUIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		// CSP allows the swagger-ui-dist CDN assets and inline init script.
		// Tightening this further (e.g. SRI hashes) is a follow-up if/when
		// we promote /docs to a non-dev surface.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"style-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
				"script-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'")

		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(swaggerUIHTML))
	})
}

// SpecBytes returns a copy of the embedded spec, useful for tests or for
// code that needs to parse the document without going through the HTTP
// handler.
func SpecBytes() []byte {
	out := make([]byte, len(spec))
	copy(out, spec)
	return out
}

// swaggerUIHTML is the static page served by SwaggerUIHandler. It loads
// the swagger-ui assets from jsDelivr (pinned to v5) and points at
// /openapi.json on the same origin. The path is hard-coded because the
// expected mount point is the project root; if the spec is mounted
// elsewhere, callers should write their own HTML wrapper.
const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>GoNext API — Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
  <style>
    html, body { margin: 0; padding: 0; background: #fafafa; }
    #swagger-ui { max-width: 1200px; margin: 0 auto; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
  <script>
    window.addEventListener('load', function () {
      window.ui = SwaggerUIBundle({
        url: '/openapi.json',
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: 'StandaloneLayout'
      });
    });
  </script>
</body>
</html>
`
