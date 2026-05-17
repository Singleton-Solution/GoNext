# GoNext OpenAPI

This directory holds the **canonical OpenAPI 3.1 description** of the GoNext core HTTP API.

| File | Purpose |
|---|---|
| `gonext.openapi.json` | The spec itself. OpenAPI 3.1, JSON, schemas in `components`. |
| `README.md` | You are here. |

The Go integration that serves the spec at runtime lives in [`../internal/openapi/`](../internal/openapi/).

## Status

This is the **scaffold** delivered by [issue #29](https://github.com/Singleton-Solution/GoNext/issues/29). Only one path (`GET /` — the server identity payload from `cmd/server/main.go`) is described concretely. Resource sections for `/api/v1/posts`, `/api/v1/pages`, `/api/v1/users`, and `/api/v1/comments` are reserved with comments and empty schemas, to be filled in by the handler-shipping issues that follow.

## House style

- **OpenAPI 3.1**, not 3.0. We use 3.1's JSON Schema 2020-12 alignment (`examples` as arrays, `summary` on `info`, `license.identifier`, etc.).
- **JSON**, not YAML. Easier to diff, easier to round-trip through tooling, no anchor-and-alias gotchas.
- **Schemas live in `components.schemas`.** Path-local schemas are forbidden — anything reusable goes in components.
- **Errors are RFC 7807-flavoured** (see `docs/05-admin-api.md` §3.1). Use the `Error` schema and the shared `BadRequest` / `Unauthorized` / `Forbidden` / `NotFound` / `TooManyRequests` / `InternalError` responses rather than redefining them per path.
- **Operation IDs are camelCase verbs**, e.g. `listPosts`, `createPost`. They become method names in generated SDKs.

## Auth schemes

Three first-class schemes are pre-defined in `components.securitySchemes`:

| Name | Type | Notes |
|---|---|---|
| `CookieSession` | apiKey-in-cookie | `__Host-gn_session`; paired with `__Host-gn_csrf` double-submit + `X-CSRF-Token` header on writes. |
| `BearerJWT` | http-bearer (JWT) | 15-minute access token, refreshed via `POST /api/v1/auth/refresh`. |
| `ApplicationPassword` | http-basic | WordPress-compatible Application Password for programmatic clients. |

Apply per-operation with a `security:` block; the top-level `security: []` means "no auth required by default".

## Adding a new path

1. Edit `gonext.openapi.json`. Put new schemas in `components.schemas`. Use existing responses (`BadRequest`, etc.) for standard errors.
2. **Mirror the file into the Go package**: every time the canonical spec changes you must keep `../internal/openapi/spec/gonext.openapi.json` in sync:

   ```bash
   cp apps/api/openapi/gonext.openapi.json apps/api/internal/openapi/spec/gonext.openapi.json
   ```

   The unit test `TestSpec_MirrorMatchesCanonical` fails if you forget. (Why two copies: `go:embed` can only reach files inside the package directory tree, and we want the canonical path to be the documentation-friendly one.)
3. Run the local checks:

   ```bash
   # 1. JSON parses, openapi version is still 3.1.x:
   python3 -c "import json; d=json.load(open('apps/api/openapi/gonext.openapi.json')); assert d['openapi'].startswith('3.1')"

   # 2. Go build + tests stay green:
   cd apps/api && go build ./... && go vet ./... && go test -race -count=1 ./...
   ```
4. Commit with a `feat(api):` or `docs(api):` prefix depending on whether handlers are landing too.

## Regenerating from code

We don't (yet). The spec is **hand-authored** for now — handlers will be added against existing OpenAPI fragments rather than generated from Go types. If that changes, this section will document the codegen pipeline. Until then: a hand-edited JSON file is the source of truth, and the embedded copy is the build artifact.

## Mounting in `main.go`

Not yet. Issue #29 deliberately leaves the route table alone to avoid conflicts with concurrent work on `cmd/server/main.go`. The one-line wire-up (for the follow-up):

```go
import "github.com/Singleton-Solution/GoNext/apps/api/internal/openapi"

// inside buildRouter, alongside the existing GET /{$} handler:
mux.Handle("GET /openapi.json", openapi.Handler())

// Dev only — gate on env in the caller:
if cfg.Env != config.EnvProd {
    mux.Handle("GET /docs/", openapi.SwaggerUIHandler())
}
```
