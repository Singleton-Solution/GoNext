// Package email is the transactional-email plumbing for GoNext.
//
// Every product flow that needs to push a message to a user — email
// verification, password reset, magic-link login, audit-digest export
// — does so through this package's [Sender] interface. The interface
// is deliberately tiny (one method) so adapters can wrap any SMTP
// server, SaaS API (Postmark, SendGrid, SES), or stub backend without
// having to hand-roll an envelope abstraction.
//
// What's here:
//
//   - [Message] is the value-typed envelope: To, Subject, TextBody,
//     HTMLBody, ReplyTo, From, Tags. It is intentionally simple — no
//     attachments in v1, no Bcc, no recipient batching. Multi-recipient
//     fan-out is the caller's job (one Send per recipient) so tracing
//     and audit semantics stay one-to-one with the message.
//
//   - [Sender] is the one-method interface that adapters implement.
//
//   - [LogSender] is the development-only adapter that writes the
//     full message contents (including bodies) to slog at Info level.
//     Never use this in production — it spills credentials and reset
//     links to the log stream.
//
//   - [NoopSender] is the unit-test adapter that drops every message
//     and returns nil. It satisfies the [Sender] interface so handlers
//     can be exercised without standing up a real SMTP server.
//
//   - [SMTPSender] is the production-ready adapter. It speaks plain
//     SMTP with STARTTLS to the configured host/port, authenticating
//     with PLAIN auth over the upgraded TLS connection. It honors
//     [SMTPConfig] from the environment (see [LoadSMTPConfig]).
//
// # Roadmap (driver stubs, not implemented in v1)
//
// The Sender interface is small enough that adding a hosted-service
// driver is a focused follow-up — each backend speaks REST + JSON and
// only the body marshalling differs. The intended adapters are:
//
//   - PostmarkSender — POSTs to https://api.postmarkapp.com/email with
//     the X-Postmark-Server-Token header. Tags map cleanly to Postmark's
//     Tag field; HTMLBody → HtmlBody, TextBody → TextBody.
//
//   - SendGridSender — POSTs to https://api.sendgrid.com/v3/mail/send
//     with Bearer auth. Tags map to SendGrid's "categories" array;
//     bodies map to the content[].value pair (text/plain + text/html).
//
//   - SESSender — uses the AWS SDK's SESv2 SendEmail API. Tags map to
//     EmailTags (SES has a hard limit of 50 tags per message; the
//     adapter trims rather than rejects).
//
// All three drivers should land behind the same [Sender] interface, so
// callers swap backends by changing one wiring line at boot.
//
// # Why STARTTLS-only on SMTPSender
//
// We deliberately do not support unencrypted SMTP. Even in private
// networks, sending verification tokens and reset links over a
// cleartext channel is a security regression we don't want to enable
// by accident. If a deployment genuinely needs cleartext SMTP (rare —
// almost always a misconfiguration), they should wrap their own
// transport rather than have the chassis hand them one. We do
// support implicit-TLS (port 465 with TLS from the first byte) via
// [SMTPConfig.ImplicitTLS], but that is also encrypted from the
// outset.
//
// # Audit and rate-limiting
//
// This package is intentionally narrow — it just sends bytes. Audit
// emission, rate-limiting per-recipient, and DSN/bounce handling are
// the caller's responsibility. The HTTP layer at
// apps/api/internal/auth/verify wires those concerns around a Sender
// for the email-verification flow.
package email
