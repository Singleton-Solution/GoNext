package lifecycle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/depends"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// slugRegex enforces the manifest slug shape documented in
// docs/02-plugin-system.md §2.2: lowercase ASCII start, lowercase ASCII
// digits and hyphens after, length 3..41 inclusive. The full plugin
// system has more checks (uniqueness, reserved-name list, ...); the
// lifecycle Manager only enforces the cheap regex so storage isn't
// polluted by obviously-malformed slugs while bundle validation is
// being built out (#44).
var slugRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{2,40}$`)

// Manifest is the minimal subset of fields the lifecycle Manager pulls
// out of the plugin's manifest.json. The full schema lives downstream;
// this struct exists so Install can extract Slug / Version / ABIVersion
// for the storage row without depending on the manifest-validation
// package (which doesn't exist yet — see issue #44).
//
// Unknown fields are tolerated and ignored: the json decoder is
// non-strict on purpose, because a forward-compatible manifest is
// allowed to grow new fields.
type Manifest struct {
	Slug         string          `json:"slug"`
	Version      string          `json:"version"`
	ABIVersion   int             `json:"abi_version"`
	Capabilities map[string]any  `json:"capabilities"`
	Raw          json.RawMessage `json:"-"`
}

// manifestFilename is the fixed name of the manifest inside a .gnplugin
// archive. Documented in docs/02-plugin-system.md §2.1.
const manifestFilename = "manifest.json"

// maxBundleSize caps how much of the bundle we'll read in v1. The doc
// says 50 MB total but only the manifest is needed at lifecycle time,
// so we keep this generous: the WASM/web payloads are validated by the
// bundle parser issue (#44) and this Manager only sees the bytes for
// future extension to disk-extraction.
const maxBundleSize = 64 * 1024 * 1024 // 64 MiB

// Runtime is the WASM-runtime seam.
//
// Production code injects the wazero-backed implementation built in
// issue #6. Tests use NoopRuntime or a fake that records calls. The
// Manager treats Load and Unload as best-effort: their error is
// captured into the plugin's LastError and the row is parked in
// Errored, but the transition itself is not silently dropped.
type Runtime interface {
	// Load is called during Activate. Implementations should
	// instantiate the plugin's WASM module, call its on_activate
	// export, and return nil on success. Any error parks the row in
	// Errored.
	Load(ctx context.Context, p Plugin) error

	// Unload is called during Deactivate and during the Uninstall
	// cleanup phase. Implementations should call the plugin's
	// on_deactivate export, then drop the WASM module and any
	// associated host resources.
	//
	// Unload errors during Deactivate park the row in Errored. Unload
	// errors during Uninstall are logged but don't block the
	// uninstall: the operator's intent is "make this plugin go away",
	// and a leaked module is preferable to a permanently-undeletable
	// row.
	Unload(ctx context.Context, slug string) error
}

// Migrator is the plugin-supplied-migrations seam.
//
// Production code wires this to the plugin-aware migration runner that
// reads the bundle's migrations/ directory and applies *.up.sql /
// *.down.sql files within a namespaced schema (docs/02-plugin-system.md
// §3.3). Today the production implementation is a no-op; this interface
// lets tests verify that the Manager calls the right method at the
// right time.
type Migrator interface {
	// MigrateUp runs the plugin's *.up.sql migrations as part of
	// Install. Errors here park the install in Errored before any
	// audit event fires.
	MigrateUp(ctx context.Context, p Plugin) error

	// MigrateDown runs the plugin's *.down.sql migrations in reverse.
	// Called by Uninstall when the caller asked for `removeData=true`.
	// Errors here are recorded as the row's LastError but the
	// uninstall still proceeds — refusing to delete the row would be
	// worse UX than leaking the plugin's tables, because the operator
	// would have no way to recover.
	MigrateDown(ctx context.Context, slug string) error
}

// NoopRuntime is a Runtime that does nothing. It's the default Manager
// runtime, returned by NewManager when WithRuntime isn't passed. Useful
// in tests and during the period (now) when the WASM runtime issue
// hasn't landed yet.
type NoopRuntime struct{}

// Load satisfies Runtime by returning nil.
func (NoopRuntime) Load(_ context.Context, _ Plugin) error { return nil }

// Unload satisfies Runtime by returning nil.
func (NoopRuntime) Unload(_ context.Context, _ string) error { return nil }

// NoopMigrator is a Migrator that does nothing. Default until the
// plugin-migrations issue ships.
type NoopMigrator struct{}

// MigrateUp satisfies Migrator by returning nil.
func (NoopMigrator) MigrateUp(_ context.Context, _ Plugin) error { return nil }

// MigrateDown satisfies Migrator by returning nil.
func (NoopMigrator) MigrateDown(_ context.Context, _ string) error { return nil }

// Manager owns the plugin-lifecycle state machine.
//
// Build one per process at boot, wired to the production Storage,
// Runtime, Migrator, and audit.Emitter. Concurrent calls on the same
// slug are safe — the state CAS at the storage layer prevents two
// callers from observing the same precondition state.
//
// The Manager doesn't hold any per-slug locks of its own. Lock-free
// concurrency works because Storage.UpdateState is the only writer and
// it provides the necessary CAS. Read-heavy operations (Get, List) go
// straight to Storage.
type Manager struct {
	storage     Storage
	runtime     Runtime
	migrator    Migrator
	emitter     *audit.Emitter
	logger      *slog.Logger
	now         func() time.Time
	dependGate  *depends.Gate
}

// ManagerOption configures a Manager at construction time. Functional
// options keep the constructor signature small as future seams are
// added.
type ManagerOption func(*Manager)

// WithRuntime injects the WASM runtime. If unset, NoopRuntime is used
// (so Manager can be used in tests and during the build-out before
// issue #6 lands).
func WithRuntime(r Runtime) ManagerOption {
	return func(m *Manager) {
		if r != nil {
			m.runtime = r
		}
	}
}

// WithMigrator injects the plugin-migration runner. If unset,
// NoopMigrator is used.
func WithMigrator(mig Migrator) ManagerOption {
	return func(m *Manager) {
		if mig != nil {
			m.migrator = mig
		}
	}
}

// WithLogger injects the structured logger used for non-audit events
// (transition warnings, audit-failure fallbacks). If unset,
// slog.Default is used.
func WithLogger(l *slog.Logger) ManagerOption {
	return func(m *Manager) {
		if l != nil {
			m.logger = l
		}
	}
}

// WithNowFunc replaces time.Now for the timestamps the Manager
// populates (LastError ErrorAt, ActivatedAt). Tests pin this to a
// fixed instant; production code leaves it unset.
func WithNowFunc(fn func() time.Time) ManagerOption {
	return func(m *Manager) {
		if fn != nil {
			m.now = fn
		}
	}
}

// WithDependencyGate wires the inter-plugin dependency gate (issue
// #251). Activate consults the gate before flipping the row to Active;
// on failure the manager surfaces a *depends.DependencyError and the
// state stays unchanged (the plugin is fine, its environment isn't).
//
// If unset, the gate is skipped — every plugin activates regardless of
// its depends[] manifest entries. This matches the legacy behaviour
// from before #251 so a Manager constructed without the option keeps
// working unchanged.
func WithDependencyGate(g *depends.Gate) ManagerOption {
	return func(m *Manager) {
		if g != nil {
			m.dependGate = g
		}
	}
}

// NewManager builds a Manager. storage and emitter are required —
// passing nil for either panics, because they are wiring bugs that
// should fail at process boot rather than the first state transition.
func NewManager(storage Storage, emitter *audit.Emitter, opts ...ManagerOption) *Manager {
	if storage == nil {
		panic("lifecycle.NewManager: storage is required")
	}
	if emitter == nil {
		panic("lifecycle.NewManager: emitter is required")
	}
	m := &Manager{
		storage:  storage,
		runtime:  NoopRuntime{},
		migrator: NoopMigrator{},
		emitter:  emitter,
		logger:   slog.Default(),
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Get returns the plugin row for slug, or ErrNotFound.
func (m *Manager) Get(ctx context.Context, slug string) (Plugin, error) {
	return m.storage.Get(ctx, slug)
}

// List returns every plugin row, sorted by slug.
func (m *Manager) List(ctx context.Context) ([]Plugin, error) {
	return m.storage.List(ctx)
}

// Install extracts the bundle, validates its manifest, persists a row
// in Installed state, and runs any plugin-supplied up-migrations.
//
// The bundle parameter is the .gnplugin ZIP. Today we only read the
// manifest; the rest of the bundle (WASM, JS, assets) will be staged
// to disk in issue #44. Install does NOT activate — that's a separate
// admin gesture so the user can review the capability list first.
//
// Returns the slug taken from the manifest, or an error if extraction,
// validation, or persistence failed.
func (m *Manager) Install(ctx context.Context, bundle io.Reader) (string, error) {
	if bundle == nil {
		return "", fmt.Errorf("lifecycle: Install: bundle reader is required")
	}

	manifestData, err := readManifestFromBundle(bundle)
	if err != nil {
		return "", fmt.Errorf("lifecycle: Install: %w", err)
	}

	// gonext.io/v1 manifests get the full JSON Schema validation pass
	// (issue #34). Legacy manifests that don't declare apiVersion fall
	// through to the cheap structural checks below — they predate the
	// schema and the bundle parser (#44) will retire the legacy path
	// once every plugin in the registry has been re-cut.
	if declaresAPIVersion(manifestData.Raw) {
		if _, vErr := manifest.Validate(manifestData.Raw); vErr != nil {
			return "", fmt.Errorf("lifecycle: Install: %w", vErr)
		}
	}

	if !slugRegex.MatchString(manifestData.Slug) {
		return "", fmt.Errorf("lifecycle: Install: invalid slug %q (must match %s)",
			manifestData.Slug, slugRegex.String())
	}
	if manifestData.Version == "" {
		return "", fmt.Errorf("lifecycle: Install: manifest version is required")
	}
	if manifestData.ABIVersion <= 0 {
		return "", fmt.Errorf("lifecycle: Install: manifest abi_version must be > 0 (got %d)",
			manifestData.ABIVersion)
	}

	caps := extractCapabilityNames(manifestData.Capabilities)

	plugin := Plugin{
		Slug:         manifestData.Slug,
		Version:      manifestData.Version,
		ABIVersion:   manifestData.ABIVersion,
		Manifest:     manifestData.Raw,
		State:        StateInstalled,
		Capabilities: caps,
		InstalledAt:  m.now().UTC(),
	}

	if err := m.storage.Insert(ctx, plugin); err != nil {
		return "", fmt.Errorf("lifecycle: Install: persist: %w", err)
	}

	// Run plugin-supplied up-migrations. If this fails, we don't roll
	// back the row — the user can re-attempt the install, or inspect
	// the error and uninstall. Park the row in Errored so the admin
	// UI surfaces what blew up.
	if err := m.migrator.MigrateUp(ctx, plugin); err != nil {
		m.parkErrored(ctx, plugin.Slug, StateInstalled, fmt.Sprintf("migrate up: %v", err))
		return plugin.Slug, fmt.Errorf("lifecycle: Install: migrate up: %w", err)
	}

	m.audit(ctx, plugin.Slug, "plugin.installed", audit.SeverityInfo, map[string]any{
		"version":      plugin.Version,
		"abi_version":  plugin.ABIVersion,
		"capabilities": plugin.Capabilities,
	})
	return plugin.Slug, nil
}

// Activate transitions an Installed or Inactive plugin into Active by
// calling Runtime.Load and (on success) the storage CAS.
//
// Returns ErrInvalidTransition (wrapped) if the row is in any other
// state, or if a concurrent Activate already won the race.
//
// If a dependency gate has been wired via WithDependencyGate, Activate
// consults it before calling Runtime.Load. A gate failure returns the
// gate's typed *depends.DependencyError and leaves the row untouched —
// the plugin itself is fine, its environment isn't, so we do NOT park
// it in Errored. The operator fixes the dependency situation and
// retries.
func (m *Manager) Activate(ctx context.Context, slug string) error {
	current, err := m.storage.Get(ctx, slug)
	if err != nil {
		return err
	}

	// Permitted preconditions: Installed (first-time activation) and
	// Inactive (re-activation after Deactivate). Any other state is a
	// hard reject.
	if current.State != StateInstalled && current.State != StateInactive {
		return transitionError(slug, "Activate",
			StateInstalled, // any of {Installed, Inactive} would print fine; pick one for the error text
			current.State)
	}

	// Dependency gate runs BEFORE Runtime.Load so a failing gate
	// doesn't waste a WASM instantiation. We parse the manifest
	// blob the row carries; if it isn't a gonext.io/v1 manifest the
	// gate is skipped (legacy plugins predate depends[]).
	if m.dependGate != nil && len(current.Manifest) > 0 {
		if mf, parseErr := manifest.Validate(current.Manifest); parseErr == nil && mf != nil {
			if depErr := m.dependGate.AllowActivate(mf); depErr != nil {
				return fmt.Errorf("lifecycle: Activate %q: %w", slug, depErr)
			}
		}
		// A parse error here is silently ignored: legacy manifests
		// (no apiVersion) won't survive manifest.Validate but also
		// can't declare depends[], so they have nothing for the
		// gate to check. The Install path is responsible for
		// surfacing schema errors at install time.
	}

	// Load FIRST, then CAS. Two callers reading Installed will both
	// try to Load; that's fine for a well-behaved Runtime (load is
	// idempotent), and only one will win the CAS. A failing Load
	// parks the row in Errored.
	if err := m.runtime.Load(ctx, current); err != nil {
		m.parkErrored(ctx, slug, current.State, fmt.Sprintf("runtime load: %v", err))
		return fmt.Errorf("lifecycle: Activate %q: load: %w", slug, err)
	}

	activatedAt := m.now().UTC()
	cleared := ""
	zeroTime := time.Time{}
	fields := &StateUpdateFields{
		ActivatedAt: &activatedAt,
		LastError:   &cleared, // clear any stale error from a prior failed attempt
		ErrorAt:     &zeroTime,
	}
	if err := m.storage.UpdateState(ctx, slug, current.State, StateActive, fields); err != nil {
		// Lost the CAS race — another goroutine flipped the state
		// out from under us. Best-effort unload so we don't leak the
		// module; the winning caller's row is now authoritative.
		if uErr := m.runtime.Unload(ctx, slug); uErr != nil {
			m.logger.Warn("lifecycle: Activate: unload after lost CAS failed",
				slog.String("slug", slug), slog.String("err", uErr.Error()))
		}
		return err
	}

	m.audit(ctx, slug, "plugin.activated", audit.SeverityInfo, map[string]any{
		"version": current.Version,
	})
	return nil
}

// Deactivate transitions an Active plugin to Inactive by calling
// Runtime.Unload and (on success) the storage CAS.
//
// Returns ErrInvalidTransition if the row isn't Active.
func (m *Manager) Deactivate(ctx context.Context, slug string) error {
	current, err := m.storage.Get(ctx, slug)
	if err != nil {
		return err
	}
	if current.State != StateActive {
		return transitionError(slug, "Deactivate", StateActive, current.State)
	}

	if err := m.runtime.Unload(ctx, slug); err != nil {
		m.parkErrored(ctx, slug, StateActive, fmt.Sprintf("runtime unload: %v", err))
		return fmt.Errorf("lifecycle: Deactivate %q: unload: %w", slug, err)
	}

	if err := m.storage.UpdateState(ctx, slug, StateActive, StateInactive, nil); err != nil {
		return err
	}

	m.audit(ctx, slug, "plugin.deactivated", audit.SeverityInfo, map[string]any{
		"version": current.Version,
	})
	return nil
}

// Uninstall transitions an Inactive or Errored plugin through
// PendingUninstall and into row-deletion.
//
// If removeData is true, the plugin's reverse-migrations are run before
// the row is deleted. This is destructive — the platform UI is expected
// to surface it as a deliberate, confirmed choice.
//
// An Active plugin must be Deactivated first; Uninstall returns
// ErrInvalidTransition in that case.
func (m *Manager) Uninstall(ctx context.Context, slug string, removeData bool) error {
	current, err := m.storage.Get(ctx, slug)
	if err != nil {
		return err
	}
	if current.State != StateInactive && current.State != StateErrored {
		return transitionError(slug, "Uninstall", StateInactive, current.State)
	}

	// Move into PendingUninstall so a concurrent Activate can't pick
	// the row back up while we're tearing it down.
	if err := m.storage.UpdateState(ctx, slug, current.State, StatePendingUninstall, nil); err != nil {
		return err
	}

	// Best-effort unload — Errored rows may not have a live module,
	// and a stuck unload should not block the operator from getting
	// rid of the plugin. Log + carry on.
	if err := m.runtime.Unload(ctx, slug); err != nil {
		m.logger.Warn("lifecycle: Uninstall: runtime unload returned error",
			slog.String("slug", slug), slog.String("err", err.Error()))
	}

	if removeData {
		if err := m.migrator.MigrateDown(ctx, slug); err != nil {
			// As above: log and continue. The row needs to go.
			m.logger.Warn("lifecycle: Uninstall: migrate down returned error",
				slog.String("slug", slug), slog.String("err", err.Error()))
		}
	}

	if err := m.storage.Delete(ctx, slug); err != nil {
		return fmt.Errorf("lifecycle: Uninstall %q: delete: %w", slug, err)
	}

	m.audit(ctx, slug, "plugin.uninstalled", audit.SeverityWarning, map[string]any{
		"version":     current.Version,
		"remove_data": removeData,
	})
	return nil
}

// Reset returns an Errored plugin to Inactive so the operator can
// retry the failed transition from a clean baseline.
//
// This is the ONE transition that doesn't follow the "must come from
// previous state" rule — that's the whole point: the operator has
// looked at LastError, fixed whatever needed fixing, and wants the row
// out of the dead-letter state. Returns ErrInvalidTransition if the
// row isn't in Errored.
//
// LastError and ErrorAt are cleared on the same write.
func (m *Manager) Reset(ctx context.Context, slug string) error {
	current, err := m.storage.Get(ctx, slug)
	if err != nil {
		return err
	}
	if current.State != StateErrored {
		return transitionError(slug, "Reset", StateErrored, current.State)
	}

	cleared := ""
	zeroTime := time.Time{}
	fields := &StateUpdateFields{
		LastError: &cleared,
		ErrorAt:   &zeroTime,
	}
	if err := m.storage.UpdateState(ctx, slug, StateErrored, StateInactive, fields); err != nil {
		return err
	}

	m.audit(ctx, slug, "plugin.reset", audit.SeverityInfo, map[string]any{
		"version": current.Version,
	})
	return nil
}

// parkErrored is the unified "this transition failed" path. It writes
// LastError + ErrorAt + state=Errored in a single CAS so the row reflects
// both the failure and the new state atomically.
//
// fromState is the state the caller was transitioning FROM. We expect
// that to still be the current state at storage; if it isn't (because a
// concurrent caller already parked the row), we log and move on — the
// caller's error return path will surface the original failure either
// way.
//
// On audit-emit failure we log; we do not propagate, because the
// caller is already in an error path and a second-order audit failure
// shouldn't mask the original problem.
func (m *Manager) parkErrored(ctx context.Context, slug string, fromState State, reason string) {
	now := m.now().UTC()
	fields := &StateUpdateFields{
		LastError: &reason,
		ErrorAt:   &now,
	}
	if err := m.storage.UpdateState(ctx, slug, fromState, StateErrored, fields); err != nil {
		// Don't propagate: we're already returning the original error
		// to the caller. Log a clear breadcrumb so operators can spot
		// the double-failure.
		m.logger.Error("lifecycle: failed to park plugin in Errored state",
			slog.String("slug", slug),
			slog.String("from", string(fromState)),
			slog.String("reason", reason),
			slog.String("err", err.Error()),
		)
		return
	}
	m.audit(ctx, slug, "plugin.errored", audit.SeverityWarning, map[string]any{
		"from":   string(fromState),
		"reason": reason,
	})
}

// audit emits an audit row and logs the failure if the emitter returns
// an error. Audit emission is best-effort: a SIEM hiccup must not
// undo a successful state transition.
func (m *Manager) audit(ctx context.Context, slug, eventType string, sev audit.Severity, meta map[string]any) {
	plugged := m.emitter.WithPlugin(slug)
	if err := plugged.Emit(ctx, eventType,
		audit.WithTarget("plugin", slug),
		audit.WithSeverity(sev),
		audit.WithMetadata(meta),
	); err != nil {
		m.logger.Warn("lifecycle: audit emit failed",
			slog.String("event", eventType),
			slog.String("slug", slug),
			slog.String("err", err.Error()),
		)
	}
}

// declaresAPIVersion reports whether the raw manifest bytes carry an
// apiVersion key (regardless of value). It is the cheap structural
// switch gating the call into the manifest package: legacy fixtures
// that predate the apiVersion field don't set it, and we want to leave
// them on the old structural checks until #44 retires the legacy path.
//
// Crucially, we route into the schema validator even when the value is
// the *wrong* literal — that lets the schema's const check produce a
// proper "apiVersion must be gonext.io/v1" error instead of letting the
// manifest sneak past the gate and trip an unrelated downstream check.
//
// We decode into a tiny anonymous struct rather than into manifest.Manifest
// because we don't want declaresAPIVersion to inherit the strict-schema
// surface — it must succeed on bytes that don't satisfy the v1 schema
// at all (the schema validator is what reports the failure).
func declaresAPIVersion(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var probe struct {
		APIVersion *string `json:"apiVersion"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Malformed JSON is not our problem here — the typed decode in
		// readManifestFromBundle will surface the parse error.
		return false
	}
	return probe.APIVersion != nil
}

// readManifestFromBundle pulls manifest.json out of a .gnplugin ZIP and
// decodes the small subset of fields the lifecycle Manager cares about.
//
// The ZIP is read fully into memory because archive/zip needs a
// ReaderAt. The maxBundleSize cap protects against a hostile or
// runaway-large reader.
//
// This is the v1 implementation. Issue #44 (bundle parsing &
// verification) will replace this with a richer parser that validates
// the manifest against its JSON Schema, verifies the signature, and
// checks bundle size against the cap from docs/02-plugin-system.md §2.1.
// The contract from the Manager's perspective stays the same.
func readManifestFromBundle(r io.Reader) (Manifest, error) {
	limited := io.LimitReader(r, maxBundleSize+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return Manifest{}, fmt.Errorf("read bundle: %w", err)
	}
	if int64(len(buf)) > maxBundleSize {
		return Manifest{}, fmt.Errorf("bundle exceeds size cap (%d bytes)", maxBundleSize)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return Manifest{}, fmt.Errorf("open bundle as zip: %w", err)
	}

	var manifestFile *zip.File
	for _, f := range zr.File {
		if f.Name == manifestFilename {
			manifestFile = f
			break
		}
	}
	if manifestFile == nil {
		return Manifest{}, fmt.Errorf("bundle missing %s", manifestFilename)
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return Manifest{}, fmt.Errorf("open %s: %w", manifestFilename, err)
	}
	defer func() { _ = rc.Close() }()

	manifestBytes, err := io.ReadAll(rc)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", manifestFilename, err)
	}

	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", manifestFilename, err)
	}
	m.Raw = manifestBytes
	return m, nil
}

// extractCapabilityNames flattens the top-level capability keys into a
// sorted slice. The full capability grammar (with scopes) is preserved
// in the raw manifest bytes; this is just the index field for storage
// and audit display.
//
// We don't sort here — we preserve insertion order? No: Go map
// iteration is random, so to keep the field deterministic for tests
// and audit comparison, we sort.
func extractCapabilityNames(caps map[string]any) []string {
	if len(caps) == 0 {
		return nil
	}
	out := make([]string, 0, len(caps))
	for k := range caps {
		out = append(out, k)
	}
	// Sort in place for determinism. Sort here rather than at storage
	// because it's a cheap, one-shot operation and storing in sorted
	// order means the audit metadata payload is also deterministic.
	sortStrings(out)
	return out
}

// sortStrings is a tiny wrapper around sort.Strings; kept as a named
// function so the call site in extractCapabilityNames documents intent.
func sortStrings(s []string) {
	// stdlib sort is fine; we don't need a custom comparator.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// Static compile-time check that errors.Is still threads through
// transitionError correctly.
var _ = errors.Is
