package email

import (
	"context"
	"errors"
)

// Message is the value-typed envelope passed to [Sender.Send]. The
// shape is intentionally minimal: one recipient, one subject, two
// optional bodies (text + HTML), and a tag map for downstream filtering
// or analytics.
//
// Multi-recipient fan-out (Cc/Bcc, address lists) is out of scope.
// Callers that need to send the same message to multiple recipients
// should call Send once per recipient so tracing, audit, and bounce
// semantics map one-to-one with delivery attempts. Attachments and
// inline images are also out of scope for v1.
//
// Encoding: bodies are UTF-8. The adapter is responsible for any wire
// encoding (quoted-printable, base64) required by the transport.
type Message struct {
	// To is the destination address in RFC 5322 form (e.g.
	// "alice@example.com" or "Alice <alice@example.com>"). Required.
	To string

	// Subject is the message subject line. Required. Adapters MAY
	// reject empty subjects per their backend's policy; the SMTPSender
	// in this package sends them through unchanged.
	Subject string

	// TextBody is the plain-text alternative. At least one of TextBody
	// or HTMLBody must be set. When both are set, adapters emit a
	// multipart/alternative envelope so clients can pick the form
	// they render best.
	TextBody string

	// HTMLBody is the HTML alternative. See TextBody for the
	// at-least-one requirement.
	HTMLBody string

	// ReplyTo, when non-empty, populates the Reply-To header. Useful
	// for "do-not-reply"-style senders that still want bounces and
	// replies to land on a real inbox.
	ReplyTo string

	// From overrides the per-adapter default sender. Most callers
	// leave this empty and let the adapter's configured From field
	// drive it.
	From string

	// Tags is a free-form map for downstream analytics (open rates,
	// categorization, deliverability cohorts). Adapters translate the
	// map to whatever shape their backend expects — SMTPSender drops
	// it on the floor, the LogSender includes it, future driver
	// stubs map it to Postmark Tags / SendGrid categories / SES
	// EmailTags.
	Tags map[string]string
}

// Validate reports whether the message has the required fields.
// Returns nil when the message is structurally valid. Adapters call
// this before any wire work so cheap rejection happens close to the
// caller.
func (m Message) Validate() error {
	if m.To == "" {
		return ErrMissingRecipient
	}
	if m.Subject == "" {
		return ErrMissingSubject
	}
	if m.TextBody == "" && m.HTMLBody == "" {
		return ErrMissingBody
	}
	return nil
}

// Sender is the one-method interface every email backend implements.
// Implementations are safe for concurrent use by multiple goroutines
// unless their documentation says otherwise.
//
// Send returns an error if the message could not be handed off to the
// transport. A nil return means "the transport accepted the message
// for delivery", NOT "the recipient received it" — async bounce
// handling is the caller's job and lives outside this package.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Sentinel errors returned by [Message.Validate] and adapters.
// Wrapped with %w in adapter return paths so callers can errors.Is
// against them.
var (
	// ErrMissingRecipient is returned when Message.To is empty.
	ErrMissingRecipient = errors.New("email: missing recipient (To)")

	// ErrMissingSubject is returned when Message.Subject is empty.
	ErrMissingSubject = errors.New("email: missing subject")

	// ErrMissingBody is returned when both TextBody and HTMLBody are
	// empty.
	ErrMissingBody = errors.New("email: missing body (TextBody or HTMLBody required)")
)
