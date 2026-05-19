package email

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// AuditSender wraps any [Sender] and emits an audit event on every
// Send call — `email.sent` for the success path, `email.failed` for
// the error path. It is the recommended way to compose audit emission
// with whichever transport the deployment uses (LogSender in dev,
// SMTPSender in prod, NoopSender in tests).
//
// The wrapper is intentionally narrow: it only fires the two events
// and forwards the message bytes to the underlying Sender. It does
// NOT classify which template a message belongs to (the caller's
// Tags map is the canonical channel) and it does NOT decorate the
// audit row with actor / IP / UA — those are bound on the [audit.Emitter]
// the wrapper carries. If a caller wants per-request decoration it
// should pass `auditEmitter.WithActor(...).WithHTTP(r)` and rebuild a
// short-lived AuditSender for that call, but the common case is a
// single long-lived AuditSender at process boot.
//
// Audit failures NEVER block the send: the underlying transport's
// error (or nil) is returned verbatim and audit problems are logged
// at WARN. This matches the policy used across the codebase — audit
// is best-effort and must not regress user-facing reliability.
type AuditSender struct {
	// Inner is the actual transport. Required.
	Inner Sender

	// Emitter is the audit emitter the wrapper writes through. May be
	// nil; when nil, the wrapper degrades to a pass-through (with a
	// single WARN log on first use so the operator notices).
	Emitter *audit.Emitter

	// Log is the destination for audit-emit failures. Defaults to
	// [slog.Default].
	Log *slog.Logger
}

// NewAuditSender returns an AuditSender that wraps inner. emitter may
// be nil; the wrapper will then act as a pass-through (with a one-line
// WARN). Returns an error if inner is nil — that's a wiring mistake we
// want to catch at boot.
func NewAuditSender(inner Sender, emitter *audit.Emitter, log *slog.Logger) (*AuditSender, error) {
	if inner == nil {
		return nil, errors.New("email: NewAuditSender requires a non-nil inner Sender")
	}
	if log == nil {
		log = slog.Default()
	}
	if emitter == nil {
		log.Warn("email: AuditSender constructed with nil audit emitter; events will not be recorded")
	}
	return &AuditSender{Inner: inner, Emitter: emitter, Log: log}, nil
}

// Send forwards msg to the inner transport and emits exactly one
// audit row. The event type is `email.sent` on success and
// `email.failed` on transport error. Validation failures still emit
// `email.failed` so attacks shaped as "trigger a thousand bogus
// sends" are visible in the audit log.
//
// Metadata captured on every row:
//
//	to       — masked recipient (a***@domain.tld)
//	subject  — full subject line (Subject is product-controlled text,
//	           not user-controlled, so persisting it is safe)
//	template — value of msg.Tags["template"] if set; otherwise ""
//	flow     — value of msg.Tags["flow"] if set; otherwise ""
//
// On the failure path the wrapper additionally records an "error"
// field carrying the error text. Severity climbs from Info -> Warning
// for failures so an operator filter on "email.* severity>=warning"
// surfaces send problems without drowning in successful sends.
func (s *AuditSender) Send(ctx context.Context, msg Message) error {
	sendErr := s.Inner.Send(ctx, msg)

	if s.Emitter == nil {
		return sendErr
	}

	meta := map[string]any{
		"to":       maskEmailForAudit(msg.To),
		"subject":  msg.Subject,
		"template": msg.Tags["template"],
		"flow":     msg.Tags["flow"],
	}

	event := "email.sent"
	severity := audit.SeverityInfo
	if sendErr != nil {
		event = "email.failed"
		severity = audit.SeverityWarning
		meta["error"] = sendErr.Error()
	}

	if err := s.Emitter.Emit(ctx, event,
		audit.WithSeverity(severity),
		audit.WithMetadata(meta),
	); err != nil {
		// Audit emit failed — log and continue. We never propagate
		// audit errors back to the caller because that would couple
		// the user-facing send to the audit store's health.
		s.Log.WarnContext(ctx, "email: audit emit failed",
			slog.String("err", err.Error()),
			slog.String("event", event),
		)
	}
	return sendErr
}

// maskEmailForAudit returns a logger-safe form of addr. We keep the
// first character of the local part and the full domain so an audit
// reader can spot a flurry targeted at one user or one domain without
// the audit row itself disclosing the recipient.
//
//	"alice@example.com"  -> "a***@example.com"
//	"x@example.com"      -> "*@example.com"
//	"" / no @            -> returned unchanged
//
// The verify package has its own near-identical helper. We duplicate
// it here because the email package cannot depend on a sibling under
// apps/api (that would invert the layering) and the function is short
// enough that a tiny utility package isn't worth the import surface.
func maskEmailForAudit(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at < 1 {
		return addr
	}
	local, domain := addr[:at], addr[at:]
	if len(local) == 1 {
		return "*" + domain
	}
	return local[:1] + "***" + domain
}

// Compile-time check that *AuditSender satisfies Sender.
var _ Sender = (*AuditSender)(nil)
