package dev

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// MaxManifestBytes caps the manifest part of the upload. 1 MiB is far
// more than any real manifest needs (the v1 schema is ~2 KiB worth of
// fields), but small enough that a hostile client cannot exhaust
// process memory by streaming an endless JSON document at us.
const MaxManifestBytes = 1 << 20 // 1 MiB

// MaxWASMBytes caps the wasm part. 16 MiB matches the dev-loop budget
// (the build pipeline aborts above ~12 MiB). Production .gnplugin
// bundles ship through a different surface with its own larger cap.
const MaxWASMBytes = 16 << 20 // 16 MiB

// MaxRequestBytes is the hard ceiling on the entire multipart body.
// We set it slightly above the sum of the two part caps to leave room
// for multipart boundary/header overhead — a few KiB. MaxBytesReader
// will return an error before the multipart parser ever sees more.
const MaxRequestBytes = MaxManifestBytes + MaxWASMBytes + 64<<10 // +64 KiB overhead

// Manager is the subset of lifecycle.Manager the handler depends on.
// Defined here (rather than imported as the concrete type) so tests can
// inject a fake without standing up the storage backend.
type Manager interface {
	Get(ctx context.Context, slug string) (lifecycle.Plugin, error)
	Install(ctx context.Context, bundle io.Reader) (string, error)
	Activate(ctx context.Context, slug string) error
	Deactivate(ctx context.Context, slug string) error
	Uninstall(ctx context.Context, slug string, removeData bool) error
}

// Handler is the http.Handler that services POST /_/plugins/dev/install.
//
// It is safe for concurrent use across goroutines. Per-slug reload
// races are resolved via a sync.Map of locks: the first request for a
// given slug acquires the lock and runs the deactivate → reinstall →
// reactivate sequence; concurrent requests for the same slug see the
// lock held and get a 409 with code=reload_in_progress.
//
// Different slugs proceed in parallel — the global state mutated
// during a reload is per-row, and lifecycle.Manager's storage already
// provides the CAS that prevents cross-slug interference.
type Handler struct {
	mgr    Manager
	logger *slog.Logger

	// inflight tracks slugs that currently have a reload running. We
	// use a sync.Map of *sync.Mutex (always pre-locked when stored)
	// rather than a plain map+mutex: try-lock-style code is more
	// naturally written with LoadOrStore, and the per-slug lock object
	// is allocated only on the slow path.
	//
	// Entries are removed by the goroutine that owns them when it
	// returns; readers use Load to detect the in-progress state.
	inflight sync.Map // map[slug]struct{}
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithLogger replaces the default slog.Default logger.
func WithLogger(l *slog.Logger) HandlerOption {
	return func(h *Handler) {
		if l != nil {
			h.logger = l
		}
	}
}

// NewHandler builds the handler. mgr is required — panic at boot
// rather than 500 at request time, because a nil Manager is a wiring
// bug, not a recoverable runtime condition.
func NewHandler(mgr Manager, opts ...HandlerOption) *Handler {
	if mgr == nil {
		panic("plugins/dev: NewHandler: Manager is required")
	}
	h := &Handler{
		mgr:    mgr,
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Mount returns the http.Handler for the dev-install endpoint,
// already wrapped with the auth middleware. The caller is expected to
// register this against `POST /_/plugins/dev/install` (or test equivalent)
// in its router. Wire this into the API server only when
// cfg.Plugins.DevMode == true so prod cannot expose the surface.
func Mount(cfg config.PluginsConfig, mgr Manager, opts ...HandlerOption) http.Handler {
	h := NewHandler(mgr, opts...)
	return authMiddleware(cfg.DevToken)(h)
}

// uploadResponse is the success envelope. Mirrors the contract the PR
// description spelled out:
//
//	{
//	  "plugin":       {"name":"...", "version":"..."},
//	  "action":       "installed" | "reloaded",
//	  "capabilities": ["..."],
//	  "warnings":     ["..."]
//	}
//
// warnings is always a (possibly empty) array so the CLI doesn't have
// to handle a missing-key special case.
type uploadResponse struct {
	Plugin       pluginInfo `json:"plugin"`
	Action       string     `json:"action"`
	Capabilities []string   `json:"capabilities"`
	Warnings     []string   `json:"warnings"`
}

type pluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

const (
	actionInstalled = "installed"
	actionReloaded  = "reloaded"
)

// ServeHTTP is the entry point. Top-level shape:
//
//  1. Cap the entire request body via MaxBytesReader so a hostile
//     client can't OOM us with a streamed multipart body.
//  2. Parse the multipart envelope, extracting manifest + wasm.
//  3. Validate manifest via manifest.Validate; 422 with full error
//     list on failure.
//  4. Take the per-slug lock; if held, return 409 reload_in_progress.
//  5. Choose action: install+activate (new) or
//     deactivate+uninstall+install+activate (reload).
//  6. Respond 200 with the chosen action and the plugin info.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cap before anything reads the body — including the multipart
	// parser, which streams. MaxBytesReader returns an error from Read
	// when the cap is exceeded; the parser surfaces it.
	r.Body = http.MaxBytesReader(w, r.Body, int64(MaxRequestBytes))

	ct := r.Header.Get("Content-Type")
	mediatype, params, err := mime.ParseMediaType(ct)
	if err != nil || mediatype != "multipart/form-data" {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"Content-Type must be multipart/form-data")
		return
	}
	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"multipart boundary is required")
		return
	}

	manifestBytes, wasmBytes, perr := readParts(r.Body, boundary)
	if perr != nil {
		h.writeParseError(w, perr)
		return
	}
	if len(manifestBytes) == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			`missing required part "manifest"`)
		return
	}
	if len(wasmBytes) == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			`missing required part "wasm"`)
		return
	}

	// Validate manifest. The lifecycle Manager re-runs this internally
	// on Install (it routes gonext.io/v1 manifests through the same
	// validator), but we do it here first so we can return the
	// structured 422 surface the CLI expects. Re-validation downstream
	// is a quick allocation, well worth the explicit error surface.
	mf, vErr := manifest.Validate(manifestBytes)
	if vErr != nil {
		var verrs manifest.Errors
		if errors.As(vErr, &verrs) {
			writeValidationErrors(w, verrs)
			return
		}
		// Hard parse failure (invalid JSON, empty input). Treat as
		// 422 with a single error.
		writeValidationErrors(w, manifest.Errors{{Message: "manifest: parse failed"}})
		return
	}

	slug := mf.Name

	// Per-slug lock. LoadOrStore returns (existingValue, loaded=true)
	// if the slug was already inflight; the caller bails with 409.
	if _, loaded := h.inflight.LoadOrStore(slug, struct{}{}); loaded {
		writeError(w, http.StatusConflict, codeReloadInProgress,
			fmt.Sprintf("a reload for plugin %q is already in progress", slug))
		return
	}
	defer h.inflight.Delete(slug)

	action, err := h.installOrReload(r.Context(), slug, mf, wasmBytes)
	if err != nil {
		h.logger.Error("plugins/dev: install failed",
			slog.String("slug", slug),
			slog.String("err", err.Error()),
		)
		// Map known typed errors; everything else => 500 generic.
		writeError(w, http.StatusInternalServerError, codeInternal,
			"install failed; see server logs")
		return
	}

	// Read the row back so the response carries the canonical values
	// the row was persisted with (handles the edge case where a future
	// Install normalises something the request sent verbatim).
	p, err := h.mgr.Get(r.Context(), slug)
	if err != nil {
		// Extremely unlikely — Install just succeeded — but treat
		// defensively. The plugin IS installed; the response just
		// can't carry the canonical values.
		writeJSON(w, http.StatusOK, uploadResponse{
			Plugin:       pluginInfo{Name: slug, Version: mf.Version},
			Action:       action,
			Capabilities: mf.Capabilities,
			Warnings:     []string{"installed but read-back failed"},
		})
		return
	}

	writeJSON(w, http.StatusOK, uploadResponse{
		Plugin:       pluginInfo{Name: p.Slug, Version: p.Version},
		Action:       action,
		Capabilities: nonNilStrings(p.Capabilities),
		Warnings:     []string{},
	})
}

// installOrReload runs the lifecycle transitions appropriate for
// whether the plugin already exists. Returns the action string for
// the response.
//
// New plugin path: Install → Activate.
// Existing plugin path: Deactivate (if active) → Uninstall → Install →
// Activate. We go through full Uninstall (not a re-install in place)
// because lifecycle.Install rejects a duplicate slug — the storage
// layer's Insert returns ErrAlreadyExists. The Uninstall path is the
// canonical way to make room for a fresh row.
func (h *Handler) installOrReload(ctx context.Context, slug string, mf *manifest.Manifest, wasmBytes []byte) (string, error) {
	existing, getErr := h.mgr.Get(ctx, slug)
	if getErr != nil && !errors.Is(getErr, lifecycle.ErrNotFound) {
		return "", fmt.Errorf("get existing: %w", getErr)
	}
	exists := getErr == nil

	bundleBytes, err := buildBundleBytes(mf, wasmBytes)
	if err != nil {
		return "", fmt.Errorf("build bundle: %w", err)
	}

	if !exists {
		if _, err := h.mgr.Install(ctx, bytes.NewReader(bundleBytes)); err != nil {
			return "", fmt.Errorf("install: %w", err)
		}
		if err := h.mgr.Activate(ctx, slug); err != nil {
			return "", fmt.Errorf("activate: %w", err)
		}
		return actionInstalled, nil
	}

	// Hot-reload path. Order matters:
	//
	//  1. Drive the row into a state Uninstall accepts (Inactive or
	//     Errored). Active rows need a Deactivate; Installed rows
	//     have to be Activated first, then Deactivated, since the
	//     state machine has no direct Installed → Inactive edge.
	//  2. Uninstall — sets the row to PendingUninstall, then deletes.
	//  3. Install — fresh row with new manifest / WASM.
	//  4. Activate — load + flip to Active.
	switch existing.State {
	case lifecycle.StateActive:
		if err := h.mgr.Deactivate(ctx, slug); err != nil {
			return "", fmt.Errorf("deactivate: %w", err)
		}
	case lifecycle.StateInstalled:
		// Activate first so we can then Deactivate into Inactive,
		// which is one of the two states Uninstall accepts.
		if err := h.mgr.Activate(ctx, slug); err != nil {
			return "", fmt.Errorf("pre-reload activate: %w", err)
		}
		if err := h.mgr.Deactivate(ctx, slug); err != nil {
			return "", fmt.Errorf("pre-reload deactivate: %w", err)
		}
	case lifecycle.StateInactive, lifecycle.StateErrored:
		// Already in a state Uninstall accepts.
	default:
		// PendingUninstall: a previous uninstall is mid-flight.
		// The per-slug lock should prevent two reloads from racing
		// here, but if the state is somehow stale, surface a clear
		// error rather than wedge.
		return "", fmt.Errorf("plugin %q in unexpected state %q", slug, existing.State)
	}
	// removeData=false: we keep the plugin's data tables around across
	// a hot reload. The dev loop reloads dozens of times per session;
	// nuking the schema each time would destroy migrations the
	// developer just wrote.
	if err := h.mgr.Uninstall(ctx, slug, false); err != nil {
		return "", fmt.Errorf("uninstall: %w", err)
	}
	if _, err := h.mgr.Install(ctx, bytes.NewReader(bundleBytes)); err != nil {
		return "", fmt.Errorf("reinstall: %w", err)
	}
	if err := h.mgr.Activate(ctx, slug); err != nil {
		return "", fmt.Errorf("reactivate: %w", err)
	}
	return actionReloaded, nil
}

// writeParseError converts a multipart-parse failure into the right
// HTTP shape. The two callers (initial parser open, per-part read) hit
// the same exhaustive list of failures: oversize body, malformed
// multipart, or a per-part read error.
func (h *Handler) writeParseError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge,
			fmt.Sprintf("request exceeds %d bytes", MaxRequestBytes))
		return
	}
	if errors.Is(err, errPartTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge,
			err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, codeBadRequest,
		"invalid multipart body")
}

// errPartTooLarge signals a per-part cap was exceeded. The wrapping
// caller turns this into a 413 with a friendly message; we use a
// sentinel rather than an HTTP-coupled type so readParts stays
// transport-agnostic.
var errPartTooLarge = errors.New("part exceeds size limit")

// readParts walks the multipart body once and extracts the manifest
// and wasm parts. Unknown parts are skipped silently — the CLI doesn't
// send any today, but tolerating them keeps future additions
// (signature.txt? sourcemap?) backward-compatible with this handler.
//
// Per-part caps are enforced with io.LimitReader + a "did we hit the
// limit" check: if a part reads exactly limit+1 bytes, we know it was
// truncated and return errPartTooLarge.
func readParts(body io.Reader, boundary string) (manifestBytes, wasmBytes []byte, err error) {
	mr := multipart.NewReader(body, boundary)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, nil, perr
		}
		field := part.FormName()
		switch field {
		case "manifest":
			b, rerr := readCapped(part, MaxManifestBytes)
			_ = part.Close()
			if rerr != nil {
				return nil, nil, fmt.Errorf("manifest %w", rerr)
			}
			manifestBytes = b
		case "wasm":
			b, rerr := readCapped(part, MaxWASMBytes)
			_ = part.Close()
			if rerr != nil {
				return nil, nil, fmt.Errorf("wasm %w", rerr)
			}
			wasmBytes = b
		default:
			// Discard the body so the next NextPart sees a clean
			// reader; ignore the error — an unknown part that
			// fails to read can't break a request we'd otherwise
			// have ignored its content for.
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}
	return manifestBytes, wasmBytes, nil
}

// readCapped reads up to cap bytes from r. If r has more, returns
// errPartTooLarge wrapped so the caller can include the field name.
func readCapped(r io.Reader, cap int) ([]byte, error) {
	// +1 so we can distinguish "exactly cap bytes (allowed)" from
	// "cap+1 bytes (caller has more)".
	limited := io.LimitReader(r, int64(cap)+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(b) > cap {
		return nil, fmt.Errorf("%w (max %d bytes)", errPartTooLarge, cap)
	}
	return b, nil
}

// buildBundle synthesises an in-memory .gnplugin ZIP from the two
// parts. lifecycle.Manager.Install expects a ZIP — the production
// install surface ships a real .gnplugin archive. We stay on that
// path for the dev loop too rather than introducing a parallel
// "loose-files" install method on the lifecycle Manager.
//
// The manifest written into the bundle is NOT the v1 manifest bytes
// the CLI uploaded verbatim. lifecycle.Install reads a legacy struct
// with `slug` / `abi_version` / `capabilities`-as-map fields (see
// lifecycle.Manifest); the v1 manifest has `name`, no `abi_version`,
// and `capabilities`-as-array. We bridge the two by deriving the
// legacy fields from the validated v1 Manifest and emitting a fresh
// manifest.json shaped the way lifecycle expects.
//
// The original v1 bytes are deliberately dropped from the bundle: if
// they were inside, lifecycle.Install would detect the `apiVersion`
// key, re-run manifest.Validate, and report the legacy fields we
// add as unknown (the v1 schema is additionalProperties:false). The
// bridge is one-directional on purpose.
//
// build/plugin.wasm is the path lifecycle currently ignores (issue
// #44 will start reading the WASM from `entry`); we keep the manifest's
// declared entry path so once that lands, the same code keeps working.
func buildBundleBytes(mf *manifest.Manifest, wasmBytes []byte) ([]byte, error) {
	entry := strings.TrimSpace(mf.Entry)
	if entry == "" {
		entry = "build/plugin.wasm"
	}

	// Capabilities: v1 schema is an array of strings (e.g.
	// ["kv.read", "audit.emit"]). lifecycle.Manifest decodes a map
	// (key = capability name, value = arbitrary). The map keys are
	// the index the storage layer persists. Bridge by emitting
	// {cap: true} for every capability in the v1 list.
	caps := make(map[string]any, len(mf.Capabilities))
	for _, c := range mf.Capabilities {
		caps[c] = true
	}

	legacyManifest := map[string]any{
		"slug":         mf.Name,
		"version":      mf.Version,
		"abi_version":  1,
		"capabilities": caps,
	}
	legacyBytes, err := json.Marshal(legacyManifest)
	if err != nil {
		return nil, fmt.Errorf("marshal legacy manifest: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	mfw, err := zw.Create("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("zip create manifest: %w", err)
	}
	if _, err := mfw.Write(legacyBytes); err != nil {
		return nil, fmt.Errorf("zip write manifest: %w", err)
	}

	ww, err := zw.Create(entry)
	if err != nil {
		return nil, fmt.Errorf("zip create wasm: %w", err)
	}
	if _, err := ww.Write(wasmBytes); err != nil {
		return nil, fmt.Errorf("zip write wasm: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	return buf.Bytes(), nil
}

// writeJSON is a tiny convenience wrapper. The handler has only one
// success path so duplicating the three lines from router.WriteJSON
// keeps this package free of an import on the rest router.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// nonNilStrings returns s itself when non-nil, or an empty slice. The
// JSON encoder emits a nil slice as null, which would force CLI clients
// to handle two shapes for "no capabilities". An empty array is the
// shape we promise.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
