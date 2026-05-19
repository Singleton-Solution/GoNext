# GoNext OpenAPI

This directory holds the **canonical OpenAPI 3.1 description** of the GoNext core HTTP API.

| File | Purpose |
|---|---|
| `openapi.yaml` | The source of truth. OpenAPI 3.1, YAML, schemas in `components`. |
| `gonext.openapi.json` | Generated mirror, JSON form. Embedded into the binary; served at `/openapi.json`. |
| `README.md` | You are here. |

The Go integration that serves the spec at runtime lives in [`../internal/openapi/`](../internal/openapi/).

## Status

Comprehensive coverage as of issue #310. Every endpoint shipped by `apps/api/cmd/server/main.go` and the per-resource `Mount(mux, ...)` registrations is documented here: auth (login, sessions, verify, refresh, PATs), posts, pages, users, comments, terms, media (in flight), plugins admin, jobs DLQ admin, settings, search, webhooks, RUM, and the WordPress `/wp-json/wp/v2/*` shim.

## House style

- **OpenAPI 3.1**, not 3.0. We use 3.1's JSON Schema 2020-12 alignment (`examples` as arrays, `summary` on `info`, `license.identifier`, etc.).
- **YAML is source of truth**; the JSON mirror is generated from it. The YAML form is human-friendlier for the long-form path/schema definitions and tooling consumes whichever serialization it prefers.
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

1. Edit `openapi.yaml`. Put new schemas in `components.schemas`. Use existing responses (`BadRequest`, etc.) for standard errors.
2. **Regenerate the JSON mirror and the embedded copies**:

   ```bash
   python3 -c "import json, yaml; \
     d=yaml.safe_load(open('apps/api/openapi/openapi.yaml')); \
     json.dump(d, open('apps/api/openapi/gonext.openapi.json','w'), indent=2); \
     open('apps/api/openapi/gonext.openapi.json','a').write('\n')"
   cp apps/api/openapi/gonext.openapi.json apps/api/internal/openapi/spec/gonext.openapi.json
   cp apps/api/openapi/openapi.yaml         apps/api/internal/openapi/spec/openapi.yaml
   ```

   The unit test `TestSpec_MirrorMatchesCanonical` fails if you forget. (Why three copies: `go:embed` can only reach files inside the package directory tree, and we want the canonical path to be the documentation-friendly one.)
3. Run the local checks:

   ```bash
   # 1. Spec validates and every operationId is reachable:
   cd tools/openapi-validate && go test ./...

   # 2. Go build + tests stay green:
   cd apps/api && go build ./... && go vet ./... && go test -race -count=1 ./internal/openapi/...
   ```
4. Commit with a `feat(api):` or `docs(api):` prefix depending on whether handlers are landing too.

## Regenerating from code

The spec is **hand-authored** YAML. A code-generated pipeline is the long-term plan — see [`tools/openapi-validate`](../../tools/openapi-validate) for the validator that today enforces the shape (every operationId is unique, every `$ref` resolves, no orphan schemas). Once the code-gen pipeline lands the validator stays in place as the post-gen check.

## Mounting in `main.go`

```go
import "github.com/Singleton-Solution/GoNext/apps/api/internal/openapi"

// inside buildRouter, alongside the existing GET /{$} handler:
mux.Handle("GET /openapi.json",   openapi.Handler())
mux.Handle("GET /api/openapi.yaml", openapi.YAMLHandler())

// Dev only — gate on env in the caller:
if cfg.Env != config.EnvProd {
    mux.Handle("GET /docs/", openapi.SwaggerUIHandler())
}
```
