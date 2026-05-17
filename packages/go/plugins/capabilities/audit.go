package capabilities

import (
	"context"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// auditEmitter is the narrow subset of audit.Emitter that the Checker
// needs to emit denial events. Defining it as an interface — rather
// than depending on the concrete *audit.Emitter directly in the Checker
// struct — keeps the test surface manageable: unit tests can plug in a
// recording fake without spinning up a real audit Store.
//
// The signature matches audit.Emitter.Emit exactly so the production
// wiring is a single-line satisfy:
//
//	chk := capabilities.NewChecker(reg, granted,
//	    capabilities.WithAuditEmitter(rootEmitter.WithPlugin(slug)))
//
// rootEmitter.WithPlugin returns *audit.Emitter, which satisfies this
// interface via its Emit method.
type auditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// capabilityDeniedEvent is the audit event type emitted on every
// Checker.MustAllow denial. The vocabulary matches the dotted-name
// scheme documented in docs/06-auth-permissions.md §13.1.
//
// Choosing `capability.denied` (singular noun, past tense) keeps it
// consistent with `plugin.installed`, `plugin.activated`, etc.; the
// task brief reads "capability_denied" in shorthand but the wire-level
// name uses the dotted form for grep parity with the rest of the audit
// catalog.
const capabilityDeniedEvent = "capability.denied"

// emitDenial records one denial through the Checker's audit emitter.
//
// The cap ID is propagated as event metadata under the "capability"
// key so downstream queries can filter denials by cap without parsing
// the message string. Severity is Warning: a single denial is
// noteworthy but not paging-critical; an alerting layer can promote
// to Critical based on rate, distribution, or specific cap IDs
// (denials on http.fetch are more alarming than denials on
// hooks.subscribe).
//
// Audit emission is best-effort. If the emitter returns an error
// (store unavailable, context cancelled), we swallow it: the denial
// itself is the authoritative signal, and a failed audit row must not
// transform a denial into an accidental allow. The audit package's
// own Middleware records emit failures separately; that signal is the
// right place to alert on systemic audit problems.
//
// A nil emitter is tolerated and produces no audit row. This is the
// supported test-mode wiring: a Checker without WithAuditEmitter still
// enforces grants, it just doesn't tell the world about it.
func (c *Checker) emitDenial(ctx context.Context, id string) {
	if c.emitter == nil {
		return
	}
	_ = c.emitter.Emit(ctx, capabilityDeniedEvent,
		audit.WithSeverity(audit.SeverityWarning),
		audit.WithTarget("capability", id),
		audit.WithMetadata(map[string]any{
			"capability": id,
		}),
	)
}
