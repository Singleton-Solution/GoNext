package email

import (
	"context"
	"log/slog"
)

// LogSender is the development-only [Sender] that writes the full
// message contents — including bodies and tags — to slog at Info
// level. It is intended for local development where standing up a
// real SMTP server is friction, and for integration tests that want
// to assert "the right verification email got built" without parsing
// SMTP wire traffic.
//
// NEVER use LogSender in production. The whole point is that it spills
// the message bodies into the log stream, which means verification
// links, reset tokens, and any other one-shot credentials end up in
// whatever log aggregator the deployment uses. The risk profile is
// roughly "you stored every user's password reset link in plaintext
// forever".
//
// A LogSender with a nil logger writes to [slog.Default].
type LogSender struct {
	// Logger is the destination. If nil, [slog.Default] is used at
	// Send time.
	Logger *slog.Logger
}

// NewLogSender returns a LogSender that emits to the given logger.
// Passing nil falls back to [slog.Default] at Send time.
func NewLogSender(logger *slog.Logger) *LogSender {
	return &LogSender{Logger: logger}
}

// Send writes msg to the logger at Info level and returns nil. The
// message Validate() check still runs first so the LogSender's
// contract matches the SMTPSender — callers can swap one for the
// other in tests without changing validation behaviour.
func (s *LogSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		slog.String("to", msg.To),
		slog.String("from", msg.From),
		slog.String("subject", msg.Subject),
		slog.String("reply_to", msg.ReplyTo),
		slog.Int("text_body_len", len(msg.TextBody)),
		slog.Int("html_body_len", len(msg.HTMLBody)),
		slog.String("text_body", msg.TextBody),
		slog.String("html_body", msg.HTMLBody),
	}
	if len(msg.Tags) > 0 {
		// slog can't directly format a map[string]string; flatten.
		tagAttrs := make([]any, 0, len(msg.Tags)*2)
		for k, v := range msg.Tags {
			tagAttrs = append(tagAttrs, slog.String(k, v))
		}
		attrs = append(attrs, slog.Group("tags", tagAttrs...))
	}
	logger.InfoContext(ctx, "email: dev send", attrs...)
	return nil
}

// Compile-time check that *LogSender satisfies Sender.
var _ Sender = (*LogSender)(nil)
