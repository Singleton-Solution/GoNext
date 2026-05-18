package schemas

import (
	"errors"
	"fmt"
)

// Mode selects between the registry's two enforcement modes. The default
// is [ModeLoose] — unregistered hooks pass through, registered ones must
// match. [ModeStrict] is the opt-in variant for tests and hardened
// hosts where every hook MUST have a contract.
type Mode uint8

const (
	// ModeLoose is the default. A hook with no registered schema is
	// accepted unconditionally; a hook with a registered schema must
	// match it. Choose loose mode for unmanaged plugin ecosystems
	// where a new hook may appear before its schema is shipped.
	ModeLoose Mode = iota

	// ModeStrict requires every hook name to have a registered schema.
	// Calls with an unregistered hookName are rejected with a wrapped
	// [ErrUnregisteredHook]. Choose strict mode when you want a
	// "schemas-or-bust" policy — typically tests and security-sensitive
	// hosts.
	ModeStrict
)

// String returns the lowercase mode name, matching the convention the
// hook bus uses for kind labels. Useful in log/error context.
func (m Mode) String() string {
	switch m {
	case ModeLoose:
		return "loose"
	case ModeStrict:
		return "strict"
	default:
		return "unknown"
	}
}

// Enforcer is the middleware-ready handle wrapping a [Registry] with a
// chosen [Mode]. The bus accepts an *Enforcer (or constructs one from a
// Registry via [WithMode]) and consults it on every Apply/Do call to
// validate the payload before fan-out.
//
// Enforcer is safe for concurrent use; it composes over the
// already-concurrent-safe [Registry].
type Enforcer struct {
	reg  *Registry
	mode Mode
}

// NewEnforcer returns an [Enforcer] in the given mode. A nil reg returns
// nil — callers that want validation must supply a non-nil registry. The
// nil-return is the signal the bus uses to skip the validation step
// entirely (zero cost when validation isn't wired in).
func NewEnforcer(reg *Registry, mode Mode) *Enforcer {
	if reg == nil {
		return nil
	}
	return &Enforcer{reg: reg, mode: mode}
}

// WithMode returns a fresh [Enforcer] with the requested mode, sharing
// the underlying registry. Chains nicely off [BuiltinRegistry] when
// strict mode is needed.
func (e *Enforcer) WithMode(mode Mode) *Enforcer {
	if e == nil {
		return nil
	}
	return &Enforcer{reg: e.reg, mode: mode}
}

// Mode returns the configured enforcement mode. Read-only — to change
// modes construct a new Enforcer via [WithMode].
func (e *Enforcer) Mode() Mode {
	if e == nil {
		return ModeLoose
	}
	return e.mode
}

// Registry returns the wrapped registry. Callers that want to add
// schemas after construction (e.g. plugin host detected a new schema
// declaration in a manifest) can do so via the returned pointer.
func (e *Enforcer) Registry() *Registry {
	if e == nil {
		return nil
	}
	return e.reg
}

// Validate is the single entry point the bus middleware calls. It
// dispatches between the loose and strict validators based on Mode.
// A nil receiver is treated as "no enforcement" and always returns nil
// — this is what lets the bus call e.Validate unconditionally and still
// be free of overhead when no validator is configured.
//
// Returned errors are wrapped with hook context so a log line carries
// the offending hook name without the caller having to weave it in.
// The wrapping shape is:
//
//	"hook %q: %w"
//
// where the inner error is [ErrInvalidPayload] or [ErrUnregisteredHook].
func (e *Enforcer) Validate(hookName string, payload any) error {
	if e == nil {
		return nil
	}
	var err error
	switch e.mode {
	case ModeStrict:
		err = e.reg.ValidateStrict(hookName, payload)
	default:
		err = e.reg.ValidatePayload(hookName, payload)
	}
	if err == nil {
		return nil
	}
	// The inner ValidatePayload already includes the hookName in its
	// formatted message; we keep the wrapping minimal here to avoid
	// "hook %q: schemas: hook %q: ..." duplication. The wrappers are
	// preserved so errors.Is / errors.As still find the sentinels.
	return err
}

// IsContractError reports whether err originated from this package's
// validator — either a payload mismatch or an unregistered hook in
// strict mode. The bus uses this for the "schema_rejected" metric so the
// label is stable regardless of error wrapping depth.
func IsContractError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrInvalidPayload) {
		return true
	}
	if errors.Is(err, ErrUnregisteredHook) {
		return true
	}
	return false
}

// Describe returns a short human-readable summary of the enforcer's
// state. Used by the bus's `WithSchemas` log line and by tests asserting
// on initial wiring.
func (e *Enforcer) Describe() string {
	if e == nil {
		return "no schemas configured"
	}
	return fmt.Sprintf("schemas: mode=%s, registered=%d", e.mode, len(e.reg.Names()))
}
