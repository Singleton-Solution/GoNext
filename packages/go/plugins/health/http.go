package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// PathPrefix is the base path the handler mounts under. Callers wire
// it as mux.Handle(PathPrefix, h) — the handler does its own routing
// against the request URL so it doesn't depend on a router-specific
// param-extraction API.
//
// The full surface is:
//
//	GET  PathPrefix              — list every plugin with telemetry
//	GET  PathPrefix/{name}/health — Report for one plugin
//	GET  PathPrefix/{name}/traps/{id} — single trap event (404 if evicted)
const PathPrefix = "/api/v1/plugins/"

// ErrPluginNotFound is returned by the inspection methods when the
// caller asks about a plugin the Recorder has no record of. The HTTP
// handler maps it to a 404. Tests can match with errors.Is.
var ErrPluginNotFound = errors.New("health: plugin not found")

// ErrTrapNotFound is returned when a specific trap ID has aged out of
// the ring (or never existed). 404 on the wire.
var ErrTrapNotFound = errors.New("health: trap event not found")

// Inspector is the read-side of a Recorder. The HTTP handler depends
// on this interface so test code can inject a deterministic snapshot
// without touching the live Prometheus collectors. Production code
// passes a *recorder, which satisfies this surface via BuildReport,
// FindTrap, RecentTraps, and Plugins.
type Inspector interface {
	BuildReport(plugin string) (Report, error)
	FindTrap(plugin string, id uint64) (TrapEvent, bool)
	Plugins() []string
}

// Handler is the http.Handler that serves the per-plugin health
// endpoints. One per process; safe for concurrent use because the
// underlying Inspector is.
type Handler struct {
	inspector Inspector
	logger    *slog.Logger
	// knownPlugins, when non-nil, is consulted to distinguish
	// "plugin is installed but has no telemetry yet" (return an
	// empty Report) from "plugin slug is unknown" (return 404).
	// Tests typically leave it nil and rely on the Recorder's
	// own Plugins() set.
	knownPlugins func(string) bool
}

// HandlerOption is the functional-options shape for Handler config.
// The narrow set of options reflects the narrow scope: a logger and
// a known-plugins predicate are the only things callers reasonably
// want to override.
type HandlerOption func(*Handler)

// WithLogger installs a slog.Logger for the handler's diagnostic
// output. Nil is tolerated and is the same as the default (slog
// discard).
func WithLogger(l *slog.Logger) HandlerOption {
	return func(h *Handler) {
		if l != nil {
			h.logger = l
		}
	}
}

// WithKnownPlugins lets callers supply a "is this slug installed?"
// predicate. With it, the handler can distinguish a 404 ("unknown
// plugin") from an empty-but-valid Report ("installed, never run").
// Without it, only plugins that have already produced telemetry are
// addressable.
func WithKnownPlugins(pred func(slug string) bool) HandlerOption {
	return func(h *Handler) {
		h.knownPlugins = pred
	}
}

// NewHandler builds a Handler bound to the given Inspector.
func NewHandler(inspector Inspector, opts ...HandlerOption) *Handler {
	h := &Handler{
		inspector: inspector,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ServeHTTP routes the per-plugin health requests.
//
// The router is a small dispatch tree against the path suffix; it's
// deliberately not using net/http.ServeMux's PathValue (Go 1.22+) so
// the handler stays compatible with callers still wiring it under a
// gorilla-style mux. The suffix-walk is cheap and the route count is
// fixed.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, PathPrefix)
	if suffix == r.URL.Path {
		// Prefix didn't match; let the mux figure out what to
		// do with the request. We respond 404 rather than
		// forwarding so the route table is self-contained.
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if suffix == "" || suffix == "/" {
		h.serveList(w, r)
		return
	}
	// /{name}/health  or  /{name}/traps/{id}
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	switch {
	case len(parts) == 2 && parts[1] == "health":
		h.serveReport(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "traps":
		h.serveTrap(w, r, parts[0], parts[2])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// serveList returns the slugs of every plugin the inspector has
// telemetry for. The shape is intentionally minimal — the admin UI
// uses it as a directory index, then issues a per-plugin /health
// request for the actual Report.
func (h *Handler) serveList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Plugins []string `json:"plugins"`
	}{Plugins: h.inspector.Plugins()})
}

// serveReport renders the Report for a single plugin. 404 if the
// plugin is unknown (per the WithKnownPlugins predicate, or per the
// inspector's Plugins() set if no predicate is set).
func (h *Handler) serveReport(w http.ResponseWriter, _ *http.Request, slug string) {
	if !h.pluginKnown(slug) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("plugin %q not found", slug))
		return
	}
	rep, err := h.inspector.BuildReport(slug)
	if err != nil {
		h.logger.Error("health: build report failed", "plugin", slug, "err", err)
		writeError(w, http.StatusInternalServerError, "report unavailable")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// serveTrap returns one trap event, suitable for the replay CLI's
// "fetch and re-run" flow.
func (h *Handler) serveTrap(w http.ResponseWriter, _ *http.Request, slug, idStr string) {
	if !h.pluginKnown(slug) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("plugin %q not found", slug))
		return
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid trap id %q", idStr))
		return
	}
	ev, ok := h.inspector.FindTrap(slug, id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("trap %d for plugin %q not found", id, slug))
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

// pluginKnown is the "is this slug addressable?" predicate. With a
// WithKnownPlugins predicate, the host's authoritative source
// answers; without one, we fall back to the inspector's telemetry
// set, which is a strict subset (plugins that have run at least
// once).
func (h *Handler) pluginKnown(slug string) bool {
	if h.knownPlugins != nil {
		return h.knownPlugins(slug)
	}
	for _, p := range h.inspector.Plugins() {
		if p == slug {
			return true
		}
	}
	return false
}

// writeJSON is the shared "encode and send" helper. Errors here are
// rare (a malformed Report would be a programming bug); we log them
// at WARN and continue so the connection still closes cleanly.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

// writeError emits a small JSON error envelope. We use a flat shape
// rather than reusing a richer house-style error type because this
// package has no dependency on the API's error catalog; the admin UI
// can present whatever it likes given (status, message).
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error   string `json:"error"`
		Status  int    `json:"status"`
		Message string `json:"message"`
	}{Error: http.StatusText(status), Status: status, Message: message})
}
