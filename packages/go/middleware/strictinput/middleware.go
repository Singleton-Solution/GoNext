package strictinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// EnvVar is the environment variable that flips strict mode on. Set
// to "1" to engage; any other value (including empty) leaves the
// middleware permissive.
const EnvVar = "GONEXT_STRICT_INPUT"

// Config tunes the middleware. Defaults are sensible — the only
// dial most operators touch is Enabled.
type Config struct {
	// Enabled, when true, engages all shape checks. The constructor
	// of the API server reads os.Getenv(EnvVar) and sets this; tests
	// flip the flag directly.
	Enabled bool

	// MaxJSONBytes is the body-size ceiling enforced before decode.
	// Defaults to 1 MiB. Zero falls back to the default.
	MaxJSONBytes int64

	// MaxGraphQLVariableDepth caps the nesting depth of the
	// "variables" JSON object on a GraphQL request. Defaults to 8.
	// Past this we 400 — a 100-level deep variable tree is almost
	// always a fuzzer probe.
	MaxGraphQLVariableDepth int

	// MaxGraphQLVariableKeys caps the total number of distinct
	// variable keys (recursively counted) on a GraphQL request.
	// Defaults to 100.
	MaxGraphQLVariableKeys int

	// GraphQLPath is the literal URL path that routes to the
	// GraphQL handler. Defaults to "/api/graphql". Requests to
	// this path get the GraphQL shape check; everything else gets
	// the generic JSON shape check.
	GraphQLPath string

	// RESTPathPrefix is the URL prefix all REST routes share.
	// Defaults to "/api/v1/". The middleware only inspects request
	// bodies for paths under this prefix — non-API routes (theme
	// admin form posts, the OAuth callback) keep their existing
	// shape gates.
	RESTPathPrefix string
}

// Default fills zero-valued fields with safe defaults. Mutates and
// returns the receiver to keep call sites compact.
func (c Config) defaults() Config {
	if c.MaxJSONBytes == 0 {
		c.MaxJSONBytes = 1 << 20
	}
	if c.MaxGraphQLVariableDepth == 0 {
		c.MaxGraphQLVariableDepth = 8
	}
	if c.MaxGraphQLVariableKeys == 0 {
		c.MaxGraphQLVariableKeys = 100
	}
	if c.GraphQLPath == "" {
		c.GraphQLPath = "/api/graphql"
	}
	if c.RESTPathPrefix == "" {
		c.RESTPathPrefix = "/api/v1/"
	}
	return c
}

// Middleware wraps next with the strict-input checks. When
// cfg.Enabled is false the returned handler is next verbatim — no
// allocation, no per-request branching.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	cfg = cfg.defaults()
	if !cfg.Enabled {
		return passthrough
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bodyless methods bypass the check.
			if !methodHasBody(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			// Empty body is fine — that's the handler's call to make
			// (some routes accept an empty PATCH).
			if r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
			}
			// JSON content type only — multipart uploads have their
			// own shape rules, and the middleware here is the wrong
			// layer to police them.
			if !isJSON(r.Header.Get("Content-Type")) {
				next.ServeHTTP(w, r)
				return
			}

			body, err := readBody(r, cfg.MaxJSONBytes)
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge,
					"body_too_large", err.Error())
				return
			}

			// GraphQL: validate the body shape against the four-key
			// envelope + bounded variables. REST: validate that the
			// body is at least valid JSON (the per-handler decode
			// covers unknown-field rejection).
			switch {
			case r.URL.Path == cfg.GraphQLPath:
				if err := validateGraphQLBody(body, cfg); err != nil {
					writeError(w, http.StatusBadRequest,
						"graphql_strict_input", err.Error())
					return
				}
			case strings.HasPrefix(r.URL.Path, cfg.RESTPathPrefix):
				if err := validateJSONShape(body); err != nil {
					writeError(w, http.StatusBadRequest,
						"strict_input", err.Error())
					return
				}
			default:
				// Out-of-scope path; do not gate.
			}

			// Re-attach the body so the downstream handler can
			// consume it. readBody already buffered the full payload
			// into memory under MaxJSONBytes; the re-read is cheap.
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}

// passthrough is the no-op middleware constructor used when the
// strict gate is disabled.
func passthrough(next http.Handler) http.Handler { return next }

// methodHasBody returns whether method conventionally carries a
// request body. We treat POST/PUT/PATCH as "body-bearing"; DELETE
// can carry one but most APIs don't, and a strict-mode check on a
// bodyless DELETE would just chum the false positives.
func methodHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// isJSON returns whether the Content-Type header advertises JSON.
// We match the type/subtype prefix; charset suffixes and the
// problem+json variant both qualify.
func isJSON(ct string) bool {
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i > 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch ct {
	case "application/json",
		"application/problem+json",
		"application/vnd.api+json":
		return true
	}
	return strings.HasSuffix(ct, "+json")
}

// readBody buffers the request body up to maxBytes. Larger payloads
// are rejected with an io.ErrUnexpectedEOF-shaped error that the
// caller maps to a 413.
func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errors.New("request body exceeded the maximum size")
	}
	return body, nil
}

// validateJSONShape parses the body and rejects malformed JSON. The
// per-handler decode covers unknown-field rejection; this layer just
// asserts "the bytes parse as JSON at all" so the handler isn't the
// first to surface the parse error.
func validateJSONShape(body []byte) error {
	var probe any
	dec := json.NewDecoder(bytes.NewReader(body))
	// Strict mode disallows trailing data — clients sometimes send
	// concatenated objects by accident.
	if err := dec.Decode(&probe); err != nil {
		return errors.New("request body must be valid JSON: " + err.Error())
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

// graphqlAllowedKeys is the closed set of top-level keys a GraphQL
// request body may carry. Per the GraphQL-over-HTTP spec, any other
// key MUST be ignored — but ignoring with a silent drop is the
// posture the strict mode is explicitly trying to undo. We 400.
var graphqlAllowedKeys = map[string]struct{}{
	"query":         {},
	"variables":     {},
	"operationName": {},
	"extensions":    {},
}

// validateGraphQLBody asserts the request body is the four-key
// envelope and that variables + extensions are within the depth/key
// budget. The query string itself isn't validated here — gqlgen
// does that — but a query that doesn't parse triggers gqlgen's own
// 200-with-errors response, which is the spec-mandated behaviour.
func validateGraphQLBody(body []byte, cfg Config) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return errors.New("body must be a JSON object: " + err.Error())
	}
	for k := range raw {
		if _, ok := graphqlAllowedKeys[k]; !ok {
			return errors.New("unknown top-level field: " + k)
		}
	}
	for _, k := range []string{"variables", "extensions"} {
		raw, ok := raw[k]
		if !ok {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return errors.New(k + ": invalid JSON")
		}
		depth, keys := measureJSON(v, 0)
		if depth > cfg.MaxGraphQLVariableDepth {
			return errors.New(k + ": nesting depth exceeds the strict-mode budget")
		}
		if keys > cfg.MaxGraphQLVariableKeys {
			return errors.New(k + ": total key count exceeds the strict-mode budget")
		}
	}
	return nil
}

// measureJSON walks v counting (max depth, total keys). The walker
// is iterative-free because the budget keeps the depth small; even
// at depth=1000 the recursion is bounded by Go's default 1MB
// goroutine stack.
func measureJSON(v any, depth int) (maxDepth, totalKeys int) {
	switch t := v.(type) {
	case map[string]any:
		maxDepth = depth
		totalKeys = len(t)
		for _, vv := range t {
			d, k := measureJSON(vv, depth+1)
			if d > maxDepth {
				maxDepth = d
			}
			totalKeys += k
		}
	case []any:
		maxDepth = depth
		for _, vv := range t {
			d, k := measureJSON(vv, depth+1)
			if d > maxDepth {
				maxDepth = d
			}
			totalKeys += k
		}
	default:
		maxDepth = depth
	}
	return
}

// writeError mirrors router.WriteError shape but is implemented
// here to avoid a dependency on apps/api/internal/rest/router.
// The package would otherwise sit above its own consumers.
func writeError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
		"code":   code,
	}
	_ = json.NewEncoder(w).Encode(body)
}
