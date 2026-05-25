// Package routes is the host-side mount point for plugin-declared HTTP
// routes (issue #136, http.serve capability).
//
// Plugins list their routes in the manifest under the http.serve scope:
//
//	{
//	  "capabilities": ["http.serve"],
//	  "http": {
//	    "serve": [
//	      {"method": "GET", "path": "/hello"},
//	      {"method": "POST", "path": "/echo"}
//	    ]
//	  }
//	}
//
// At plugin Activate, the lifecycle Manager calls Register with the
// plugin slug and the parsed RouteSpec list. The Registry mounts each
// route under /api/plugins/{slug}/{route.Path} on the shared
// http.ServeMux. Each inbound request is dispatched as a synthetic
// filter on the host's hook bus under the name "http.serve.{slug}";
// the plugin's hook handler reads the marshalled request, returns a
// response envelope, and the Registry writes that envelope back to
// the client.
//
// At Deactivate, the Manager calls Unregister(slug); the routes are
// torn down by setting a per-slug "disabled" flag on the Registry —
// http.ServeMux doesn't support unregister, so we shadow the pattern
// with a 404. A re-Register replaces the disabled state with a fresh
// route set in one pass.
//
// Rate limiting and capability gating happen INSIDE the dispatch path —
// the route mount itself is permissive (anything that matches the
// pattern is dispatched), and the per-call check rejects requests for
// a plugin that lost its grant. This avoids a torn state where a
// route exists in the mux but the plugin can't actually handle it.
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	hostbus "github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// maxRequestBodyBytes caps how much of an inbound request body we will
// read into memory before refusing the call. 1 MiB matches the plugin
// hook ABI's MaxPayloadBytes — plugins should never be passed more
// than this much per request.
const maxRequestBodyBytes = 1 << 20

// dispatchTimeout caps how long the host will wait for the plugin's
// handler to return. The plugin runtime has its own per-call CPU
// deadline; this is an additional ceiling on the HTTP-handler path so
// a runaway hook can't pin the connection for the lifetime of the
// request socket.
const dispatchTimeout = 15 * time.Second

// inboundHeaderAllowlist is the curated set of request headers the
// host forwards to the plugin handler. Everything else is stripped —
// cookies, Authorization, X-Forwarded-*, etc. — so a plugin cannot
// observe (or exfiltrate) host-level identity material.
//
// The list mirrors issue #136's "Inbound headers" acceptance criterion:
// User-Agent + Accept + Accept-Language + Content-Type + Authorization.
// Authorization is included on purpose — some plugin REST surfaces
// accept their own bearer tokens; the host already authenticated the
// request via middleware before the route was matched, and the plugin
// may want to do its own scoped check.
var inboundHeaderAllowlist = map[string]struct{}{
	"User-Agent":      {},
	"Accept":          {},
	"Accept-Language": {},
	"Content-Type":    {},
	"Authorization":   {},
}

// outboundHeaderDenylist is the set of response headers the plugin is
// NOT allowed to set. Anything in this list is dropped from the
// plugin's response envelope before the host writes the response to
// the client.
//
// The denylist (rather than the allowlist used for inbound) is
// pragmatic: plugins want to set arbitrary Content-Type / Cache-Control
// / Location / ETag / custom headers, and listing every legitimate
// header would be a maintenance burden. The denylist captures the
// security-relevant cases where the plugin must not influence
// host-level behaviour:
//
//   - Set-Cookie: plugins cannot mint host cookies
//   - Authorization: not a response header but defense-in-depth
//   - X-Forwarded-*: plugin cannot fake proxy chains
//   - Strict-Transport-Security: only the host owns transport policy
//   - Content-Security-Policy: only the host owns the page CSP
var outboundHeaderDenylist = map[string]struct{}{
	"Set-Cookie":                {},
	"Authorization":             {},
	"X-Forwarded-For":           {},
	"X-Forwarded-Host":          {},
	"X-Forwarded-Proto":         {},
	"Strict-Transport-Security": {},
	"Content-Security-Policy":   {},
}

// RouteSpec is one entry from the manifest's http.serve list. The
// Manifest parser (a sibling package) is responsible for producing
// this struct; this package only consumes it.
//
// Method is the uppercase HTTP method ("GET", "POST", ...). Path is
// the per-plugin sub-path under /api/plugins/{slug}/. It must start
// with "/" and may contain path parameter syntax compatible with
// http.ServeMux ("/posts/{id}").
type RouteSpec struct {
	Method string
	Path   string
}

// Dispatcher is the seam the Registry uses to invoke the plugin. The
// production wiring satisfies it from the host hook bus (a synthetic
// filter under "http.serve.{slug}"); tests inject a stub.
//
// Dispatch is called once per inbound request. ctx carries the
// dispatch deadline + the request's context (so a cancelled client
// connection propagates into the plugin handler). slug identifies
// which plugin owns the route. payload is the marshalled request
// envelope (Request below). The returned bytes are the marshalled
// Response envelope; an error halts the dispatch and the host returns
// 502 Bad Gateway to the client.
type Dispatcher interface {
	Dispatch(ctx context.Context, slug string, payload []byte) ([]byte, error)
}

// Request is the wire envelope the host hands to the plugin handler.
// Encoded as JSON in v1; future versions may switch to a binary codec
// without changing the field shape.
type Request struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	PathParams map[string]string `json:"path_params,omitempty"`
}

// Response is the wire envelope the plugin returns from its handler.
// Headers are subject to the outbound denylist before they reach the
// client. A zero Status falls through to 200 OK.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// HookBusDispatcher implements Dispatcher by issuing a synthetic
// filter call on the supplied hook bus. The plugin's manifest is
// expected to subscribe to "http.serve.{slug}" with a handler that
// decodes Request, produces Response, and returns the JSON-marshalled
// envelope.
//
// Construction: NewHookBusDispatcher(bus). The bus is required.
type HookBusDispatcher struct {
	bus *hostbus.Bus
}

// NewHookBusDispatcher returns a Dispatcher that fans plugin route
// requests through the host hook bus. Nil bus panics — that's a
// wiring bug.
func NewHookBusDispatcher(bus *hostbus.Bus) *HookBusDispatcher {
	if bus == nil {
		panic("routes.NewHookBusDispatcher: bus is required")
	}
	return &HookBusDispatcher{bus: bus}
}

// Dispatch implements Dispatcher. It calls bus.ApplyFilters with the
// per-plugin hook name and the marshalled payload as the threaded
// "value". The plugin handler returns a transformed value which we
// treat as the marshalled Response envelope.
//
// On bus error we return it verbatim; the caller maps that into a
// 502 Bad Gateway. On a plugin that didn't subscribe to the hook the
// bus returns the original value unchanged — we detect that by
// comparing byte length / content; an unchanged value means "no
// handler ran" and we surface ErrNoHandler.
func (d *HookBusDispatcher) Dispatch(ctx context.Context, slug string, payload []byte) ([]byte, error) {
	hookName := "http.serve." + slug
	rawIn := json.RawMessage(payload)
	out, err := d.bus.ApplyFilters(ctx, hookName, rawIn)
	if err != nil {
		return nil, fmt.Errorf("hook bus: %w", err)
	}
	// The bus is `any`-typed; the value we got back may be the
	// json.RawMessage we sent in (no handler ran), a json.RawMessage
	// the handler produced, or some other type a handler returned
	// (e.g. a Go struct that has not yet been marshaled). We convert
	// to []byte in priority order.
	var outBytes []byte
	switch v := out.(type) {
	case json.RawMessage:
		outBytes = []byte(v)
	case []byte:
		outBytes = v
	case string:
		outBytes = []byte(v)
	case nil:
		return nil, ErrNoHandler
	default:
		marshaled, mErr := json.Marshal(v)
		if mErr != nil {
			return nil, fmt.Errorf("marshal plugin response: %w", mErr)
		}
		outBytes = marshaled
	}
	// ApplyFilters returns the threaded value; if no handler subscribed
	// it returns the input. We can't reliably tell those apart from a
	// "handler ran and returned the same bytes" case, so the contract
	// is: handlers ALWAYS return a JSON object with at least a "status"
	// field. If the bus returned the raw input back, treat as
	// no-handler.
	if bytesEqual(outBytes, payload) {
		return nil, ErrNoHandler
	}
	return outBytes, nil
}

// ErrNoHandler is returned by HookBusDispatcher when the plugin did
// not subscribe a handler to the http.serve.{slug} hook. The host
// surfaces this as 404 Not Found — the route was matched, but the
// plugin lost or never registered the handler.
var ErrNoHandler = errors.New("routes: plugin did not register a handler")

// bytesEqual is a small helper so we don't import bytes for one use.
// It returns true iff a and b contain the same bytes.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Registry is the per-process store of plugin route bindings. One
// Registry per api server process. Safe for concurrent Register /
// Unregister / Dispatch — the per-slug routes map is guarded by a
// RWMutex, and the http.ServeMux pattern installation is one-shot at
// Register (we never call ServeMux.Handle twice for the same pattern
// — see InstallRoute below).
type Registry struct {
	mu sync.RWMutex
	// active holds the per-slug enable state. When a slug is in
	// active=true the dispatcher fires; when false the route's HTTP
	// handler returns 404. This shadow-disable pattern is how we
	// "unregister" from an http.ServeMux that doesn't support real
	// unregistration.
	active map[string]bool
	// specs holds the per-slug route list as last seen by Register.
	// Used by debug endpoints and tests.
	specs map[string][]RouteSpec
	// caps holds the per-slug GrantSet captured at Activate. Each
	// inbound dispatch consults the Checker built from this set to
	// enforce capability gates.
	caps map[string]capabilities.GrantSet

	dispatcher Dispatcher
	bus        *hostbus.Bus
	emitter    *audit.Emitter
	limiter    ratelimit.Limiter
	capReg     *capabilities.Registry
	logger     *slog.Logger
	mux        *http.ServeMux
}

// Options collects the wiring deps for a new Registry. All fields are
// required EXCEPT logger (defaults to slog.Default) and limiter (nil
// disables rate limiting, which is the right default for tests).
type Options struct {
	Mux        *http.ServeMux
	Dispatcher Dispatcher
	Emitter    *audit.Emitter
	Limiter    ratelimit.Limiter
	CapReg     *capabilities.Registry
	Logger     *slog.Logger
}

// NewRegistry builds the per-process route registry. The mux is the
// shared HTTP mux from buildRouter; the dispatcher is typically a
// HookBusDispatcher; the emitter records audit rows; the limiter is
// consulted on every dispatch; capReg is the capability registry the
// per-slug Checker is built against.
func NewRegistry(opts Options) (*Registry, error) {
	if opts.Mux == nil {
		return nil, errors.New("routes.NewRegistry: Mux is required")
	}
	if opts.Dispatcher == nil {
		return nil, errors.New("routes.NewRegistry: Dispatcher is required")
	}
	if opts.Emitter == nil {
		return nil, errors.New("routes.NewRegistry: Emitter is required")
	}
	if opts.CapReg == nil {
		return nil, errors.New("routes.NewRegistry: CapReg is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		active:     make(map[string]bool),
		specs:      make(map[string][]RouteSpec),
		caps:       make(map[string]capabilities.GrantSet),
		dispatcher: opts.Dispatcher,
		emitter:    opts.Emitter,
		limiter:    opts.Limiter,
		capReg:     opts.CapReg,
		logger:     logger,
		mux:        opts.Mux,
	}, nil
}

// Register binds the plugin slug's route list to the mux. Called by
// the lifecycle Manager at Activate.
//
// The granted GrantSet is the set of capability IDs the operator
// approved at install time. The Registry refuses to register routes
// for a plugin that does NOT hold "http.serve" — that's the v1 gate.
//
// Re-Register with a new spec list replaces the previous binding in
// one pass: the new specs replace the old, and any pattern that was
// in the old set but not the new gets shadow-disabled (the http.ServeMux
// pattern stays mounted but the handler closure checks active[slug]
// and the specific (method, path) tuple before dispatching).
//
// Returns an error if the plugin lacks http.serve, or if any path is
// malformed.
func (r *Registry) Register(slug string, specs []RouteSpec, granted capabilities.GrantSet) error {
	if slug == "" {
		return errors.New("routes.Register: slug is required")
	}
	if !granted.Has("http.serve") {
		return fmt.Errorf("routes.Register: plugin %q lacks http.serve capability", slug)
	}
	for i, s := range specs {
		if err := validateRouteSpec(s); err != nil {
			return fmt.Errorf("routes.Register: spec %d: %w", i, err)
		}
	}
	r.mu.Lock()
	prev, hadPrev := r.specs[slug]
	r.specs[slug] = specs
	r.caps[slug] = granted
	r.active[slug] = true
	r.mu.Unlock()

	// Mount every NEW pattern on the mux. Patterns from the prior
	// registration that survive the swap need no work — the handler
	// closure looks them up against r.specs on every call.
	prevPatterns := patternSet(slug, prev)
	for _, s := range specs {
		pattern := routePattern(slug, s)
		if _, mounted := prevPatterns[pattern]; mounted {
			continue
		}
		if err := r.installRoutePattern(pattern, slug, s); err != nil {
			// A duplicate Handle panics on http.ServeMux; we recover
			// inside installRoutePattern and translate to an error.
			// The route is logged but Register still succeeds for
			// the patterns that did mount — partial activation is
			// safer than total failure here, because the plugin can
			// then call back with a fresh manifest.
			r.logger.Warn("routes: failed to mount pattern",
				slog.String("slug", slug),
				slog.String("pattern", pattern),
				slog.Any("err", err),
			)
		}
	}
	if !hadPrev {
		r.logger.Info("routes: plugin routes registered",
			slog.String("slug", slug),
			slog.Int("routes", len(specs)),
		)
	} else {
		r.logger.Info("routes: plugin routes updated",
			slog.String("slug", slug),
			slog.Int("routes", len(specs)),
		)
	}
	return nil
}

// Unregister flips the slug's active flag off. Called by the lifecycle
// Manager at Deactivate.
//
// http.ServeMux doesn't expose a real unmount, so the route patterns
// remain in the mux but their handler closures consult active[slug]
// and return 404 when the plugin is inactive. A subsequent Register
// flips the flag back on.
//
// Returns nil even if the slug was never registered — Unregister is
// idempotent.
func (r *Registry) Unregister(slug string) {
	if slug == "" {
		return
	}
	r.mu.Lock()
	r.active[slug] = false
	r.mu.Unlock()
	r.logger.Info("routes: plugin routes unregistered",
		slog.String("slug", slug),
	)
}

// isActive reports whether the slug is currently serving requests.
func (r *Registry) isActive(slug string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active[slug]
}

// routeMatches reports whether (method, path) matches any spec
// registered for slug. Used by the per-pattern handler to defend
// against stale routes after a re-Register that dropped this entry.
func (r *Registry) routeMatches(slug, method, path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.specs[slug] {
		if strings.EqualFold(s.Method, method) && s.Path == path {
			return true
		}
	}
	return false
}

// grantSet returns the slug's currently registered grant set. nil if
// the plugin was never registered or has been unregistered.
func (r *Registry) grantSet(slug string) capabilities.GrantSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.caps[slug]
}

// installRoutePattern is the one place that calls mux.Handle. The
// http.ServeMux panics on a duplicate pattern; we wrap the call in a
// recover so the error path is observable rather than fatal.
func (r *Registry) installRoutePattern(pattern, slug string, spec RouteSpec) (retErr error) {
	defer func() {
		if rec := recover(); rec != nil {
			retErr = fmt.Errorf("install pattern %q: %v", pattern, rec)
		}
	}()
	r.mux.Handle(pattern, r.handlerFor(slug, spec))
	return nil
}

// handlerFor constructs the per-route http.Handler. The closure
// captures the slug + spec; on every request it checks the Registry's
// per-slug active flag, rate-limits, capability-checks, marshals the
// request envelope, calls Dispatcher.Dispatch, and writes the
// returned envelope.
//
// We attach one handler per (slug, spec) pair rather than a single
// shared handler so the http.ServeMux pattern dispatch works
// unchanged — net/http already routes by pattern.
func (r *Registry) handlerFor(slug string, spec RouteSpec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Active-flag check. A plugin in the disabled state shadow-404s.
		if !r.isActive(slug) {
			http.NotFound(w, req)
			return
		}
		// Re-validate the route is still in the slug's spec set. A
		// re-Register that dropped this entry would have left the
		// pattern mounted but absent from the spec list; refusing here
		// is the right answer.
		if !r.routeMatches(slug, req.Method, spec.Path) {
			http.NotFound(w, req)
			return
		}
		// Method check is implicit in the pattern when ServeMux 1.22+
		// is used with "METHOD /path" patterns, but we still
		// double-check here in case the pattern was emitted without
		// the method prefix (which would route every method to this
		// handler).
		if !strings.EqualFold(req.Method, spec.Method) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Capability gate. The Registry rejects at Register if the
		// plugin lacks http.serve, but a defense-in-depth check at
		// dispatch time covers the race where Register succeeded but
		// the operator revoked the grant before any request landed.
		granted := r.grantSet(slug)
		if !granted.Has("http.serve") {
			r.audit(req.Context(), slug, spec, http.StatusForbidden, "missing capability")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Per-plugin rate limit.
		if r.limiter != nil {
			ok, retryAfter, err := r.limiter.Allow(req.Context(), "plugin:"+slug+":http.serve")
			if err == nil && !ok {
				r.audit(req.Context(), slug, spec, http.StatusTooManyRequests, "rate limit exceeded")
				if retryAfter > 0 {
					w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
				}
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
		}
		// Body limit. We cap before unmarshalling so an oversized
		// payload doesn't allocate.
		body, err := io.ReadAll(io.LimitReader(req.Body, maxRequestBodyBytes+1))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if int64(len(body)) > maxRequestBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Build the synthetic Request envelope.
		envelope := Request{
			Method:  req.Method,
			Path:    req.URL.Path,
			Query:   req.URL.RawQuery,
			Body:    body,
			Headers: projectInboundHeaders(req.Header),
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			http.Error(w, "marshal envelope", http.StatusInternalServerError)
			return
		}
		// Dispatch with a hard timeout. The plugin runtime owns its
		// own per-call CPU deadline; this ceiling is for the HTTP
		// surface so a hung handler can't pin the connection.
		dispatchCtx, cancel := context.WithTimeout(req.Context(), dispatchTimeout)
		defer cancel()
		out, err := r.dispatcher.Dispatch(dispatchCtx, slug, payload)
		if err != nil {
			if errors.Is(err, ErrNoHandler) {
				r.audit(req.Context(), slug, spec, http.StatusNotFound, "no handler")
				http.NotFound(w, req)
				return
			}
			r.audit(req.Context(), slug, spec, http.StatusBadGateway, err.Error())
			http.Error(w, "plugin handler error", http.StatusBadGateway)
			return
		}
		// Decode the plugin's response envelope.
		var resp Response
		if uerr := json.Unmarshal(out, &resp); uerr != nil {
			r.audit(req.Context(), slug, spec, http.StatusBadGateway, "decode plugin response: "+uerr.Error())
			http.Error(w, "plugin response decode error", http.StatusBadGateway)
			return
		}
		writePluginResponse(w, resp)
		r.audit(req.Context(), slug, spec, statusOr(resp.Status, http.StatusOK), "")
	})
}

// writePluginResponse copies the plugin's response onto the
// http.ResponseWriter, applying the outbound header denylist and
// defaulting an empty status to 200.
func writePluginResponse(w http.ResponseWriter, resp Response) {
	for k, v := range resp.Headers {
		if _, denied := outboundHeaderDenylist[http.CanonicalHeaderKey(k)]; denied {
			continue
		}
		w.Header().Set(k, v)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}

// statusOr returns s if non-zero, else fallback. Used to populate the
// audit row's status field even for plugin responses that omitted it.
func statusOr(s, fallback int) int {
	if s == 0 {
		return fallback
	}
	return s
}

// projectInboundHeaders keeps only the headers in inboundHeaderAllowlist
// and folds multi-value headers with a comma. Cookies and the
// X-Forwarded-* chain are dropped.
func projectInboundHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(inboundHeaderAllowlist))
	for k := range h {
		canon := http.CanonicalHeaderKey(k)
		if _, ok := inboundHeaderAllowlist[canon]; !ok {
			continue
		}
		out[canon] = strings.Join(h.Values(k), ", ")
	}
	return out
}

// routePattern returns the http.ServeMux pattern string for one route
// belonging to slug. The Go 1.22+ "METHOD /path" pattern lets the mux
// do method-aware routing without a per-handler conditional.
//
// Example: routePattern("seo", {Method: "GET", Path: "/sitemap.xml"})
//          => "GET /api/plugins/seo/sitemap.xml"
func routePattern(slug string, s RouteSpec) string {
	return fmt.Sprintf("%s /api/plugins/%s%s", strings.ToUpper(s.Method), slug, s.Path)
}

// patternSet returns a set of routePattern() outputs for the supplied
// specs. Used by Register to identify routes carried over from a prior
// registration vs. brand-new routes that need installation.
func patternSet(slug string, specs []RouteSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		out[routePattern(slug, s)] = struct{}{}
	}
	return out
}

// validateRouteSpec returns an error if the spec is structurally bad.
// We accept any uppercase HTTP method matching the standard verbs and
// any path that starts with "/". The manifest schema enforces stricter
// constraints; this is the runtime backstop.
func validateRouteSpec(s RouteSpec) error {
	if s.Method == "" {
		return errors.New("method is required")
	}
	switch strings.ToUpper(s.Method) {
	case http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		return fmt.Errorf("method %q not supported", s.Method)
	}
	if s.Path == "" || !strings.HasPrefix(s.Path, "/") {
		return fmt.Errorf("path %q must start with /", s.Path)
	}
	return nil
}

// audit records one inbound-route event. status is the HTTP status the
// host returned to the client; reason is the optional explanation (set
// for non-2xx events).
func (r *Registry) audit(ctx context.Context, slug string, spec RouteSpec, status int, reason string) {
	if r.emitter == nil {
		return
	}
	sev := audit.SeverityInfo
	if status >= 400 {
		sev = audit.SeverityWarning
	}
	plugged := r.emitter.WithPlugin(slug)
	_ = plugged.Emit(ctx, "plugin.http.serve",
		audit.WithSeverity(sev),
		audit.WithTarget("plugin", slug),
		audit.WithMetadata(map[string]any{
			"method": spec.Method,
			"path":   spec.Path,
			"status": status,
			"reason": reason,
		}),
	)
}
