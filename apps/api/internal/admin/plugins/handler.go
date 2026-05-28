// Package plugins exposes the lifecycle.Manager state machine over a
// REST surface for the admin Plugins page.
//
// The admin's plugins/page.tsx and plugins/actions.ts call the following
// routes (mounted under /api/v1/plugins):
//
//	GET    /api/v1/plugins                       — list installed plugins
//	GET    /api/v1/plugins/{name}                — detail
//	POST   /api/v1/plugins/install               — multipart bundle upload
//	POST   /api/v1/plugins/{name}/activate       — flip Installed/Inactive → Active
//	POST   /api/v1/plugins/{name}/deactivate     — flip Active → Inactive
//	DELETE /api/v1/plugins/{name}                — uninstall (Inactive/Errored → deleted)
//
// Auth: every route requires a session; the read endpoints are open to
// any authenticated principal so the admin shell can render the list.
// Write endpoints gate on policy capabilities (CapInstallPlugins for
// install/uninstall, CapActivatePlugins for activate/deactivate),
// mirroring how admin/marketplace gates its install path.
//
// The handler delegates persistence and state transitions to
// lifecycle.Manager; the audit trail is emitted by the Manager itself
// (plugin.installed / plugin.activated / plugin.deactivated /
// plugin.uninstalled), so this package does NOT double-emit. Issue
// #500.
package plugins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Manager is the narrow lifecycle.Manager surface this package needs.
// Defined locally so tests can substitute a fake without dragging the
// storage and audit backends into the test binary. *lifecycle.Manager
// satisfies the interface by virtue of its concrete method set.
type Manager interface {
	List(ctx context.Context) ([]lifecycle.Plugin, error)
	Get(ctx context.Context, slug string) (lifecycle.Plugin, error)
	Install(ctx context.Context, bundle io.Reader) (string, error)
	Activate(ctx context.Context, slug string) error
	Deactivate(ctx context.Context, slug string) error
	Uninstall(ctx context.Context, slug string, removeData bool) error
}

// Deps is the dependency bag for Mount. Every required field must be
// non-nil; validate() reports the missing one cleanly at boot.
type Deps struct {
	// Manager is the lifecycle.Manager that owns the state machine.
	Manager Manager

	// Policy gates write endpoints on capabilities.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Manager == nil {
		return errors.New("admin/plugins: Manager is required")
	}
	if d.Policy == nil {
		return errors.New("admin/plugins: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the package.
type handlers struct {
	mgr    Manager
	policy policy.Policy
	logger *slog.Logger
}

// Mount wires the plugin-lifecycle routes onto mux under base
// (typically "/api/v1/plugins"). Returns an error rather than panicking
// so the boot path can surface a misconfiguration.
//
// Route tree:
//
//	GET    {base}                       — list
//	GET    {base}/{name}                — detail
//	POST   {base}/install               — multipart bundle upload
//	POST   {base}/{name}/activate       — activate
//	POST   {base}/{name}/deactivate     — deactivate
//	DELETE {base}/{name}                — uninstall
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{mgr: deps.Manager, policy: deps.Policy, logger: deps.Logger}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.authed(h.list))
	// The "install" segment must register BEFORE the {name} catch-all
	// or net/http's mux would route POST /install through the {name}
	// activate/deactivate branches first. ServeMux pattern precedence
	// already preserves literal segments over wildcards, but listing
	// the literal first makes the precedence intent explicit.
	mux.Handle("POST "+base+"/install", h.gated(policy.CapInstallPlugins, h.install))
	mux.Handle("GET "+base+"/{name}", h.authed(h.get))
	mux.Handle("POST "+base+"/{name}/activate", h.gated(policy.CapActivatePlugins, h.activate))
	mux.Handle("POST "+base+"/{name}/deactivate", h.gated(policy.CapActivatePlugins, h.deactivate))
	mux.Handle("DELETE "+base+"/{name}", h.gated(policy.CapInstallPlugins, h.uninstall))
	return nil
}

// authed wraps a handler that requires a logged-in principal but no
// further capability check. Returns 401 if no principal is on the
// context. Mirrors admin/marketplace.authed.
func (h *handlers) authed(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// gated wraps a handler that requires both authentication and a
// specific capability. Returns 401/403 with the standard error
// envelope.
func (h *handlers) gated(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// PluginRecord is the wire shape for a single installed plugin. Mirrors
// the admin's PluginRecord interface in apps/admin/.../plugins/types.ts.
//
// The host's lifecycle column is `slug`; the wire field is `name`
// because that's the URL segment the admin uses (and the manifest's
// v1 schema calls it `name` too). Zero timestamps become nil so the
// admin can render "never activated" without parsing the zero-time
// sentinel.
type PluginRecord struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	ABIVersion  int            `json:"abi_version"`
	State       string         `json:"state"`
	InstalledAt *time.Time     `json:"installedAt"`
	ActivatedAt *time.Time     `json:"activatedAt"`
	Capabilities []string      `json:"capabilities"`
	LastError   *PluginError   `json:"lastError,omitempty"`
	Manifest    map[string]any `json:"-"`
	ManifestRaw string         `json:"manifestRaw,omitempty"`
}

// PluginError mirrors the admin's PluginErrorInfo shape.
type PluginError struct {
	Message string `json:"message"`
	At      string `json:"at"`
}

// toRecord projects a lifecycle.Plugin onto the wire shape. The list
// endpoint and the detail endpoint share the projection; the detail
// endpoint additionally fills ManifestRaw (the list view omits it to
// keep the response compact).
func toRecord(p lifecycle.Plugin, withManifestRaw bool) PluginRecord {
	rec := PluginRecord{
		Name:         p.Slug,
		Version:      p.Version,
		ABIVersion:   p.ABIVersion,
		State:        string(p.State),
		Capabilities: nonNilStrings(p.Capabilities),
	}
	if !p.InstalledAt.IsZero() {
		t := p.InstalledAt
		rec.InstalledAt = &t
	}
	if !p.ActivatedAt.IsZero() {
		t := p.ActivatedAt
		rec.ActivatedAt = &t
	}
	if p.LastError != "" {
		rec.LastError = &PluginError{
			Message: p.LastError,
			At:      p.ErrorAt.UTC().Format(time.RFC3339Nano),
		}
	}
	if withManifestRaw && len(p.Manifest) > 0 {
		rec.ManifestRaw = string(p.Manifest)
	}
	return rec
}

// nonNilStrings coerces a nil capability slice to []string{} so the JSON
// encoder emits `[]` instead of `null`. The admin maps over the field
// without a nil guard.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// list handles GET {base}. Returns router.Page[PluginRecord] so the
// envelope matches the {data, pagination} convention every other admin
// list endpoint uses (and benefits from the Page[T] nil-coerce that
// landed in #505 — empty result emits "data":[], not "data":null).
//
// Pagination is a stub: every plugin row is returned, no cursors. The
// volume of installed plugins is small (typically <100) so a single
// page is fine until a future issue introduces filtering.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	ctx := r.Context()
	plugins, err := h.mgr.List(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/plugins: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list plugins")
		return
	}
	out := make([]PluginRecord, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, toRecord(p, false))
	}
	router.WriteJSON(w, http.StatusOK, router.Page[PluginRecord]{Data: out})
}

// get handles GET {base}/{name}.
func (h *handlers) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if name == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_name", "plugin name is required")
		return
	}
	ctx := r.Context()
	p, err := h.mgr.Get(ctx, name)
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "plugin not found")
			return
		}
		h.logger.ErrorContext(ctx, "admin/plugins: get failed",
			slog.String("name", name), slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to fetch plugin")
		return
	}
	router.WriteJSON(w, http.StatusOK, toRecord(p, true))
}

// install handles POST {base}/install. The contract from
// apps/admin/.../plugins/actions.ts is a multipart/form-data body with
// either a "bundle" file (.gnplugin ZIP) or a "manifest" JSON blob.
//
// The lifecycle.Manager.Install API takes a single io.Reader holding
// the bundle bytes, so the "bundle" part is the only one we currently
// know how to forward — the "manifest"-only path (marketplace stub
// install) belongs on the admin/marketplace surface, not here.
//
// Implementation: parse the multipart body, locate the bundle part,
// hand the part reader directly to Manager.Install. We do NOT buffer
// the bundle in memory ourselves — the Manager reads up to its own
// internal 64 MiB cap (lifecycle.maxBundleSize) and rejects anything
// larger. The multipart parser holds whatever bytes haven't been read
// yet; for a 1 MB bundle that's negligible, for a 50 MB bundle it's
// still well below the host's process budget.
func (h *handlers) install(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	ctx := r.Context()

	// Cap the request body first so a hostile client cannot exhaust
	// process memory by streaming an endless multipart body. 80 MiB is
	// the dev-mode bundle ceiling plus a comfortable multipart boundary
	// overhead — production bundles ship through admin/marketplace,
	// which fetches from object storage rather than a multipart upload.
	const maxBody = 80 << 20 // 80 MiB
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBody))

	ct := r.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(ct)
	if err != nil || mediatype != "multipart/form-data" {
		router.WriteError(w, http.StatusBadRequest, "invalid_content_type",
			"Content-Type must be multipart/form-data")
		return
	}

	// Use r.MultipartReader so the part can be streamed straight into
	// the Manager without materialising the full bundle in memory.
	mr, err := r.MultipartReader()
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_multipart",
			"failed to parse multipart body")
		return
	}

	var slug string
	var foundBundle bool
	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			if errors.Is(perr, &http.MaxBytesError{}) {
				router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
					"request body exceeds size limit")
				return
			}
			router.WriteError(w, http.StatusBadRequest, "invalid_multipart",
				"failed to read multipart part")
			_ = part
			return
		}
		field := part.FormName()
		switch field {
		case "bundle":
			foundBundle = true
			s, iErr := h.mgr.Install(ctx, part)
			_ = part.Close()
			if iErr != nil {
				if errors.Is(iErr, lifecycle.ErrAlreadyExists) {
					router.WriteError(w, http.StatusConflict, "already_installed",
						"a plugin with this slug is already installed")
					return
				}
				h.logger.ErrorContext(ctx, "admin/plugins: install failed",
					slog.Any("err", iErr))
				router.WriteError(w, http.StatusBadRequest, "install_failed", iErr.Error())
				return
			}
			slug = s
			// Drain any remaining parts so the body's MaxBytesReader
			// doesn't leave bytes in the buffer that a misbehaving
			// middleware might re-read. We don't actually need them.
			for {
				next, nErr := mr.NextPart()
				if errors.Is(nErr, io.EOF) || nErr != nil {
					break
				}
				_, _ = io.Copy(io.Discard, next)
				_ = next.Close()
			}
		default:
			// Unknown part — discard and continue. Keeps the
			// multipart envelope forward-compatible (e.g. a future
			// "signature" sidecar) without forcing a handler bump.
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}

	if !foundBundle {
		router.WriteError(w, http.StatusBadRequest, "missing_bundle",
			`multipart body must include a "bundle" part containing the .gnplugin archive`)
		return
	}

	// Re-read the row so the response carries the canonical fields
	// the Manager persisted. Extremely unlikely to fail since Install
	// just succeeded; on the off chance it does, return a minimal
	// success envelope so the admin doesn't think the install failed.
	plugin, gErr := h.mgr.Get(ctx, slug)
	if gErr != nil {
		h.logger.WarnContext(ctx, "admin/plugins: install read-back failed",
			slog.String("name", slug), slog.Any("err", gErr))
		router.WriteJSON(w, http.StatusCreated, PluginRecord{
			Name:         slug,
			State:        string(lifecycle.StateInstalled),
			Capabilities: []string{},
		})
		return
	}
	router.WriteJSON(w, http.StatusCreated, toRecord(plugin, true))
}

// activate handles POST {base}/{name}/activate.
func (h *handlers) activate(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if name == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_name", "plugin name is required")
		return
	}
	ctx := r.Context()
	if err := h.mgr.Activate(ctx, name); err != nil {
		h.writeTransitionError(ctx, w, name, "activate", err)
		return
	}
	h.writeRow(ctx, w, name, http.StatusOK)
}

// deactivate handles POST {base}/{name}/deactivate.
func (h *handlers) deactivate(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if name == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_name", "plugin name is required")
		return
	}
	ctx := r.Context()
	if err := h.mgr.Deactivate(ctx, name); err != nil {
		h.writeTransitionError(ctx, w, name, "deactivate", err)
		return
	}
	h.writeRow(ctx, w, name, http.StatusOK)
}

// uninstall handles DELETE {base}/{name}. The admin's actions don't
// expose a `removeData` toggle yet, so we default to false — the
// operator deactivates first (the state machine forces this), and the
// plugin's data tables are preserved so a re-install can pick them up.
func (h *handlers) uninstall(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	name := r.PathValue("name")
	if name == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_name", "plugin name is required")
		return
	}
	ctx := r.Context()
	if err := h.mgr.Uninstall(ctx, name, false); err != nil {
		h.writeTransitionError(ctx, w, name, "uninstall", err)
		return
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"name":    name,
		"deleted": true,
	})
}

// writeTransitionError maps a lifecycle error to the appropriate HTTP
// status + code. The state machine's two interesting failure modes are
// ErrNotFound (404) and ErrInvalidTransition (409, since the resource
// exists but the requested operation conflicts with its current state).
func (h *handlers) writeTransitionError(ctx context.Context, w http.ResponseWriter, name, op string, err error) {
	if errors.Is(err, lifecycle.ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	if errors.Is(err, lifecycle.ErrInvalidTransition) {
		router.WriteError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	h.logger.ErrorContext(ctx, "admin/plugins: transition failed",
		slog.String("op", op),
		slog.String("name", name),
		slog.Any("err", err))
	router.WriteError(w, http.StatusInternalServerError, "internal_error", "plugin transition failed")
}

// writeRow re-reads the row after a successful transition so the
// response carries the post-transition state. A read failure after a
// successful transition is logged but doesn't change the operator's
// view that the action worked — we return 200 with a minimal envelope.
func (h *handlers) writeRow(ctx context.Context, w http.ResponseWriter, name string, status int) {
	plugin, err := h.mgr.Get(ctx, name)
	if err != nil {
		h.logger.WarnContext(ctx, "admin/plugins: read-back after transition failed",
			slog.String("name", name), slog.Any("err", err))
		router.WriteJSON(w, status, PluginRecord{Name: name, Capabilities: []string{}})
		return
	}
	router.WriteJSON(w, status, toRecord(plugin, false))
}
