package site_editor

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bag for Mount. Every field except Logger is
// required; validate() catches missing fields at boot rather than
// NPE'ing on the first request.
type Deps struct {
	// Source enumerates the active theme's parts and returns their
	// on-disk bytes.
	Source PartsSource

	// Overrides persists operator-saved overrides. Production wraps
	// the options table; tests use MemoryOverrideStore.
	Overrides OverrideStore

	// Validator vets the block tree on PUT. Pass nil to use
	// NewDefaultBlockValidator (the standard core-block allowlist).
	Validator BlockValidator

	// Policy resolves the theme.edit_parts capability check.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production wiring should
	// always pass a service logger.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Source == nil {
		return errors.New("admin/site_editor: Source is required")
	}
	if d.Overrides == nil {
		return errors.New("admin/site_editor: Overrides store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/site_editor: Policy is required")
	}
	return nil
}

// Handler is the resolved dependency bag passed around inside the
// package. Built once by Mount and shared across the registered
// routes.
type Handler struct {
	source    PartsSource
	overrides OverrideStore
	validator BlockValidator
	resolver  *Resolver
	policy    policy.Policy
	logger    *slog.Logger
}

// Mount wires the site_editor routes onto mux under base (typically
// "/api/v1/admin/site_editor"). Returns an error rather than
// panicking if Deps is malformed so the boot path can surface it
// cleanly.
//
// The route tree:
//
//	GET    {base}/parts                 — list all parts of the active theme
//	PUT    {base}/parts/{name}          — upsert an override for {name}
//	DELETE {base}/parts/{name}          — remove the override for {name}
//
// Every route is gated by policy.CapThemeEditParts.
func Mount(mux *http.ServeMux, base string, deps Deps) (*Handler, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Validator == nil {
		deps.Validator = NewDefaultBlockValidator()
	}

	h := &Handler{
		source:    deps.Source,
		overrides: deps.Overrides,
		validator: deps.Validator,
		resolver:  NewResolver(deps.Source, deps.Overrides),
		policy:    deps.Policy,
		logger:    deps.Logger.With(slog.String("component", "admin.site_editor")),
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/parts", h.gate(h.list))
	mux.Handle("PUT "+base+"/parts/{name}", h.gate(h.put))
	mux.Handle("DELETE "+base+"/parts/{name}", h.gate(h.delete))
	return h, nil
}

// Resolver returns the same Resolver the handler uses internally. The
// public renderer plugs into this so a request-time render walks the
// same override-first path the admin UI shows.
func (h *Handler) Resolver() *Resolver {
	return h.resolver
}

// gate wraps a handler with the auth + theme.edit_parts capability
// check. Returns 401 if no principal is on the context, 403 if the
// principal lacks the capability.
func (h *Handler) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapThemeEditParts, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}
