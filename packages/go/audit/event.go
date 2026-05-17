package audit

import (
	"time"
)

// Severity classifies an audit event for filtering and alerting.
//
// SeverityInfo is the default — routine privileged actions (login,
// password change, post published). Operators are not paged for these.
//
// SeverityWarning signals an event that may warrant human attention but
// isn't an emergency: repeated policy denials, a 2FA disable, a
// password reset triggered for an admin.
//
// SeverityCritical is the page-the-operator tier: an impersonation
// started, an admin role granted, a credential cloning signal
// (auth.webauthn.counter_regression), bulk user export, etc. SIEM
// forwarders typically route these to a separate sink.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Valid reports whether s is one of the defined severities. Used by
// stores to reject malformed input before it lands on disk.
func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	default:
		return false
	}
}

// Event is one row in the audit log.
//
// The field set mirrors the audit_log columns documented in
// docs/06-auth-permissions.md §13 plus the PrevHash slot reserved for a
// future tamper-evidence chain. Stores are responsible for filling in
// any zero-valued required fields with their defaults (ID generation,
// Time = now if zero) and for validating Severity / EventType shape.
type Event struct {
	// ID is the row identifier. Memory store assigns a monotonic int64
	// cast to string; Postgres store leaves this empty and lets the
	// BIGSERIAL on the table populate it (the returned ID is read back
	// via INSERT ... RETURNING).
	ID string

	// Time is the moment the event occurred. If zero at Emit time, the
	// store sets it to time.Now().UTC() — callers should normally let
	// this default unless they're back-filling.
	Time time.Time

	// ActorUserID is the authenticated user who triggered the event, or
	// empty for pre-auth events (failed login) and system-internal
	// actions. The audit_log column is NULL-able for the same reason.
	ActorUserID string

	// ActorPluginSlug, when non-empty, identifies the plugin acting on
	// the user's behalf (or autonomously). Maps to audit_log.actor_label
	// when actor_kind = 'plugin'. See docs/06-auth-permissions.md §14.6.
	ActorPluginSlug string

	// EventType is the dotted event name: 'auth.login.success',
	// 'plugin.activated', 'policy.denied', 'http.request', etc. Stores
	// don't validate against a closed enum because plugins are allowed
	// to emit `{slug}.{noun}.{verb}` events (see §14.6).
	EventType string

	// ResourceType / ResourceID identify what was acted on, when
	// applicable. ResourceType is a free-form kind (e.g. "post",
	// "user", "setting", "plugin"). ResourceID is the string form of
	// the target's primary key. Both may be empty for non-targeted
	// events like a failed login.
	ResourceType string
	ResourceID   string

	// IP is the network address the request came from, as a string in
	// canonical textual form. Stored full per docs/06-auth-permissions.md
	// §5.3; the admin UI is responsible for truncating before display.
	IP string

	// UserAgent is the raw User-Agent header. Truncated to a sensible
	// upper bound by the Postgres store to keep TOAST overflow off the
	// hot path.
	UserAgent string

	// Metadata is event-specific extra context, marshaled to JSONB on
	// disk. Keep it small and bounded — the audit row is on the hot
	// path of every privileged action.
	Metadata map[string]any

	// Severity classifies the event for filtering and alerting.
	// Defaults to SeverityInfo when zero-valued; stores normalize.
	Severity Severity

	// PrevHash is reserved for tamper-evidence. v1 leaves this nil; a
	// follow-up issue will populate it from an HMAC chain (each row
	// hashes the previous row's hash, keyed by a server-side secret).
	// Leaving the slot in place now avoids a schema rev later.
	PrevHash []byte
}

// userAgentMax bounds how much of the User-Agent header we persist.
// Real-world UAs run to a few hundred bytes; anything longer is almost
// always abuse and bloats the audit row. The store truncates rather
// than rejects so the audit trail isn't lost on a malformed request.
const userAgentMax = 1024

// normalize fills in defaults and trims oversized inputs. Returns a
// copy with the same semantics so the caller's Event is not mutated.
func (e Event) normalize(now func() time.Time) Event {
	out := e
	if out.Time.IsZero() {
		out.Time = now().UTC()
	} else {
		out.Time = out.Time.UTC()
	}
	if out.Severity == "" {
		out.Severity = SeverityInfo
	}
	if len(out.UserAgent) > userAgentMax {
		out.UserAgent = out.UserAgent[:userAgentMax]
	}
	return out
}
