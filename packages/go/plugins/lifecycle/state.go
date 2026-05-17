package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// State is the position of a Plugin row in the lifecycle state machine.
//
// The zero value is intentionally invalid so a forgotten initializer
// fails fast at the next Storage write rather than silently meaning
// "Installed". Use one of the State constants instead.
type State string

const (
	// StateInstalled is the landing state for a successful Install. The
	// plugin's bundle has been verified, its manifest accepted, and a
	// row has been persisted, but no plugin code has been loaded yet.
	StateInstalled State = "installed"

	// StateActive means the plugin's hooks are dispatched and its
	// exports may be invoked. This is the only state in which a Runtime
	// considers the plugin "loaded".
	StateActive State = "active"

	// StateInactive is the post-deactivate resting state: the plugin's
	// row and data are intact, but no code is running. Reactivation
	// from here calls Runtime.Load again.
	StateInactive State = "inactive"

	// StatePendingUninstall is the intermediate state Uninstall puts
	// the row in while cleanup runs (Runtime.Unload, optional
	// reverse-migrations). On clean completion the row is deleted; on
	// failure the row is left in Errored for operator inspection.
	StatePendingUninstall State = "pending_uninstall"

	// StateErrored captures a transition that failed in flight. The
	// row's LastError + ErrorAt fields explain what blew up. The only
	// path out is Manager.Reset, which moves the row back to Inactive
	// so the operator can retry from a clean baseline.
	StateErrored State = "errored"
)

// Valid reports whether s is one of the defined states. Storage
// implementations call this before persisting to refuse writes that
// would corrupt the column.
func (s State) Valid() bool {
	switch s {
	case StateInstalled, StateActive, StateInactive, StatePendingUninstall, StateErrored:
		return true
	default:
		return false
	}
}

// Plugin is one row of the plugins table — the canonical record of what
// the platform knows about an installed plugin.
//
// Fields are populated by Manager during transitions; callers receive
// Plugin values via Get / List and should treat them as read-only
// snapshots. Mutating a returned Plugin has no effect on Storage.
type Plugin struct {
	// Slug is the platform-unique identifier carried in manifest.json.
	// docs/02-plugin-system.md §2.2 specifies the regex
	// `^[a-z][a-z0-9-]{2,40}$`; the lifecycle Manager re-validates on
	// Install to keep storage from being polluted by partial code paths.
	Slug string

	// Version is the SemVer string from manifest.json. The lifecycle
	// package does not compare versions; it stores the value verbatim
	// for display + audit purposes.
	Version string

	// ABIVersion is the host ABI the plugin was compiled against. The
	// platform may support a small range of ABIVersions concurrently;
	// the WASM runtime issue (#6) is the canonical enforcer. Stored
	// here so the admin UI can render compatibility info without
	// re-parsing the manifest.
	ABIVersion int

	// Manifest is the raw manifest.json bytes the bundle shipped with.
	// Kept as json.RawMessage so we don't lock a Go struct shape into
	// this package — the manifest schema lives in the WASM/manifest
	// validation package once #44 lands.
	Manifest json.RawMessage

	// State is the current position in the state machine. Always one
	// of the State constants.
	State State

	// Capabilities is the sandbox capability list parsed out of the
	// manifest's top-level `capabilities` block. Stored separately
	// from the manifest blob so policy code can index/filter without
	// re-parsing JSON on every check. The lifecycle package only
	// reads this field; the parser populates it.
	Capabilities []string

	// LastError carries a human-readable description of the most
	// recent transition failure. Cleared by Reset.
	LastError string

	// ErrorAt is the moment LastError was recorded. Zero when there
	// has been no error or the error has been Reset.
	ErrorAt time.Time

	// InstalledAt is the moment Install succeeded. Never updated
	// after that — even an Uninstall + reinstall would create a new
	// row with a fresh timestamp.
	InstalledAt time.Time

	// ActivatedAt is the moment the most recent Activate succeeded,
	// or zero if the plugin has never been activated. Updated on
	// every successful Activate; not cleared on Deactivate (so the
	// admin UI can show "last active 3 days ago").
	ActivatedAt time.Time

	// RowVersion is bumped on every state transition. Storage
	// implementations are expected to maintain this monotonically;
	// the Manager doesn't depend on it, but exposing it makes
	// optimistic-concurrency callers outside this package possible.
	RowVersion int64

	// UpdatedAt is the moment the row was last written. Set by
	// Storage on every UpdateState / Save call.
	UpdatedAt time.Time
}

// ErrInvalidTransition is returned by Manager methods when the requested
// transition is not allowed from the current state, or when the row's
// state changed under us (the optimistic check failed).
//
// Callers can match this with errors.Is. The wrapping format includes
// the slug, the attempted transition, and the state the storage
// believes the row is in — useful for surfacing a clean message to the
// admin UI without leaking internals.
var ErrInvalidTransition = errors.New("lifecycle: invalid state transition")

// ErrNotFound is returned by Storage.Get and Manager.Get when no row
// exists for the given slug. Distinct from ErrInvalidTransition so the
// caller can decide whether to 404 vs. 409.
var ErrNotFound = errors.New("lifecycle: plugin not found")

// ErrAlreadyExists is returned by Storage.Insert when a row with the
// same slug already exists. Manager.Install translates this into a
// caller-friendly error wrapped with the slug.
var ErrAlreadyExists = errors.New("lifecycle: plugin already exists")

// transitionError builds the ErrInvalidTransition error with enough
// context for a useful log line: which slug, which attempted op, and
// the state the storage row was actually in.
//
// The "actual" parameter may be the empty string when we don't know
// (e.g., the CAS UPDATE returned 0 rows but we didn't bother re-reading
// to find out the actual state).
func transitionError(slug, op string, attempted, actual State) error {
	if actual == "" {
		return fmt.Errorf("%w: plugin %q: %s not allowed from current state (expected %q)",
			ErrInvalidTransition, slug, op, attempted)
	}
	return fmt.Errorf("%w: plugin %q: %s not allowed from %q (expected %q)",
		ErrInvalidTransition, slug, op, actual, attempted)
}
