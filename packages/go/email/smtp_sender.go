package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig holds the connection and authentication parameters for
// [SMTPSender]. All four of Host/Port/From/User are required for an
// authenticated submission server (port 587 in the typical case);
// Password is required when User is set.
//
// Defaults align with the IETF submission convention (port 587 +
// STARTTLS, the format every modern provider exposes). Implicit-TLS
// (port 465) is supported via ImplicitTLS for the rare deployment
// that needs it.
type SMTPConfig struct {
	// Host is the SMTP server hostname (e.g. "smtp.example.com"). No
	// default; required.
	Host string

	// Port is the SMTP server port. Default 587 (submission with
	// STARTTLS). Set to 465 with ImplicitTLS=true for legacy SMTPS.
	Port int

	// User and Password are the SMTP AUTH PLAIN credentials. Auth is
	// performed AFTER the STARTTLS upgrade (or, with ImplicitTLS=true,
	// over the already-encrypted connection from the first byte) so
	// credentials are never sent in the clear.
	//
	// User may be empty for open-relay-style internal deployments,
	// in which case the AUTH step is skipped entirely.
	User     string
	Password string

	// From is the envelope sender address (MAIL FROM). It is also
	// used as the default "From:" header when [Message.From] is
	// empty. Required.
	From string

	// ImplicitTLS turns on legacy SMTPS (TLS from the first byte) on
	// the configured port. Default false — modern submission uses
	// STARTTLS on port 587 and that's what we recommend. Set this
	// only if your provider mandates port 465.
	ImplicitTLS bool

	// InsecureSkipVerify disables TLS hostname / chain verification.
	// EXCLUSIVELY for development against self-signed test SMTP
	// servers (e.g. MailHog with a generated cert). Production
	// deployments MUST leave this false; the package will not warn
	// at runtime because the cost of the warning per-send is
	// non-trivial and the misuse pattern is configuration, not code.
	InsecureSkipVerify bool

	// DialTimeout bounds the TCP+TLS handshake. Default 10 seconds.
	DialTimeout time.Duration
}

// LoadSMTPConfig reads SMTP configuration from environment variables
// with the GONEXT_SMTP_ prefix. Returns the populated config and a
// validation error if any required field is missing.
//
// Recognized variables:
//
//	GONEXT_SMTP_HOST                 — required
//	GONEXT_SMTP_PORT                 — optional, default 587
//	GONEXT_SMTP_USER                 — optional (skip AUTH if empty)
//	GONEXT_SMTP_PASSWORD             — required when User is set
//	GONEXT_SMTP_FROM                 — required
//	GONEXT_SMTP_IMPLICIT_TLS         — "1"/"true" enable, default false
//	GONEXT_SMTP_INSECURE_SKIP_VERIFY — DEV ONLY, default false
//	GONEXT_SMTP_DIAL_TIMEOUT         — e.g. "10s", default 10s
//
// LoadSMTPConfig does not perform a network round-trip; the first
// connection attempt is during Send. A successful load only proves
// the local environment is shaped correctly.
func LoadSMTPConfig() (SMTPConfig, error) {
	cfg := SMTPConfig{
		Host:        os.Getenv("GONEXT_SMTP_HOST"),
		User:        os.Getenv("GONEXT_SMTP_USER"),
		Password:    os.Getenv("GONEXT_SMTP_PASSWORD"),
		From:        os.Getenv("GONEXT_SMTP_FROM"),
		Port:        587,
		DialTimeout: 10 * time.Second,
	}
	if v := os.Getenv("GONEXT_SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			return SMTPConfig{}, fmt.Errorf("email: invalid GONEXT_SMTP_PORT %q", v)
		}
		cfg.Port = n
	}
	if v := os.Getenv("GONEXT_SMTP_IMPLICIT_TLS"); v != "" {
		cfg.ImplicitTLS = parseBool(v)
	}
	if v := os.Getenv("GONEXT_SMTP_INSECURE_SKIP_VERIFY"); v != "" {
		cfg.InsecureSkipVerify = parseBool(v)
	}
	if v := os.Getenv("GONEXT_SMTP_DIAL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return SMTPConfig{}, fmt.Errorf("email: invalid GONEXT_SMTP_DIAL_TIMEOUT %q: %w", v, err)
		}
		cfg.DialTimeout = d
	}
	if err := cfg.validate(); err != nil {
		return SMTPConfig{}, err
	}
	return cfg, nil
}

// parseBool tolerates "1", "true", "TRUE", "yes" as true. Anything
// else is false. We don't use strconv.ParseBool here because we want
// "yes" to work — twelve-factor env files commonly use it.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c SMTPConfig) validate() error {
	if c.Host == "" {
		return errors.New("email: SMTPConfig.Host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("email: SMTPConfig.Port must be 1..65535, got %d", c.Port)
	}
	if c.From == "" {
		return errors.New("email: SMTPConfig.From is required")
	}
	if c.User != "" && c.Password == "" {
		return errors.New("email: SMTPConfig.Password is required when User is set")
	}
	return nil
}

// SMTPSender is the production-ready [Sender]. It connects to the
// configured server, performs STARTTLS (unless ImplicitTLS is on),
// authenticates with PLAIN, and submits one message per Send call.
//
// SMTPSender opens a fresh connection per Send. Connection pooling is
// out of scope for v1 — the email-verification flow this package
// supports is bursty-low (one mail per user action, not a transactional
// firehose), and a per-send connection keeps the failure model
// trivially observable. A hot path that needs pooling should wrap
// SMTPSender with its own pool.
type SMTPSender struct {
	cfg SMTPConfig

	// dialFn is the seam tests use to substitute a fake connection.
	// Production uses net.Dialer{Timeout: cfg.DialTimeout}.DialContext.
	dialFn func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewSMTPSender returns an SMTPSender configured with cfg. The
// constructor validates cfg and returns an error if any required
// field is missing.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	d := &net.Dialer{Timeout: cfg.DialTimeout}
	return &SMTPSender{
		cfg:    cfg,
		dialFn: d.DialContext,
	}, nil
}

// Send connects to the SMTP server, performs the TLS upgrade (or uses
// implicit-TLS), authenticates, and submits the message.
//
// The full session is bounded by ctx — both the dial timeout and the
// blocking SMTP commands respect cancellation. A cancelled context
// returns an error wrapped with context.Canceled / context.DeadlineExceeded.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))

	// Dial the raw TCP connection. We pass ctx so the dial cooperates
	// with cancellation; net.Dialer.DialContext honors both ctx and
	// its own Timeout (whichever fires first).
	conn, err := s.dialFn(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}

	if s.cfg.ImplicitTLS {
		// Wrap the raw conn in TLS before any SMTP traffic.
		tlsConn := tls.Client(conn, s.tlsConfig())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("email: tls handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: smtp greeting: %w", err)
	}
	defer func() {
		// Quit politely; if the client errors here we can't do
		// anything useful — close the underlying conn either way.
		_ = client.Quit()
		_ = conn.Close()
	}()

	if !s.cfg.ImplicitTLS {
		// STARTTLS upgrade: required for any auth or message
		// submission. If the server doesn't advertise it, we refuse
		// to continue — sending credentials in cleartext is exactly
		// the failure mode this adapter is built to prevent.
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("email: server at %s does not advertise STARTTLS", addr)
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	if s.cfg.User != "" {
		// PLAIN auth is safe over the TLS-upgraded channel.
		auth := smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	from := msg.From
	if from == "" {
		from = s.cfg.From
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := wc.Write(buildMIME(msg, from)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: finalize body: %w", err)
	}
	return nil
}

// tlsConfig returns the TLS settings for both the implicit-TLS dial
// and the STARTTLS upgrade. ServerName drives certificate hostname
// verification; tests that need to talk to "127.0.0.1" set
// InsecureSkipVerify=true to bypass the SAN check.
func (s *SMTPSender) tlsConfig() *tls.Config {
	return &tls.Config{
		ServerName:         s.cfg.Host,
		InsecureSkipVerify: s.cfg.InsecureSkipVerify, //nolint:gosec // documented dev-only escape hatch
		MinVersion:         tls.VersionTLS12,
	}
}

// buildMIME assembles the RFC 5322 message bytes. When both bodies
// are set we emit multipart/alternative; otherwise we emit a single
// Content-Type matching whichever body is present.
//
// The implementation is intentionally hand-rolled rather than using
// mime/multipart so we can keep boundaries deterministic for tests
// and the dependency surface small.
func buildMIME(msg Message, from string) []byte {
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(from)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(msg.To)
	b.WriteString("\r\n")
	if msg.ReplyTo != "" {
		b.WriteString("Reply-To: ")
		b.WriteString(msg.ReplyTo)
		b.WriteString("\r\n")
	}
	b.WriteString("Subject: ")
	b.WriteString(msg.Subject)
	b.WriteString("\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if msg.TextBody != "" && msg.HTMLBody != "" {
		const boundary = "GoNextEmailBoundary000"
		b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"`)
		b.WriteString("\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.TextBody)
		b.WriteString("\r\n--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTMLBody)
		b.WriteString("\r\n--" + boundary + "--\r\n")
	} else if msg.HTMLBody != "" {
		b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.HTMLBody)
	} else {
		b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(msg.TextBody)
	}
	return []byte(b.String())
}

// Compile-time check that *SMTPSender satisfies Sender.
var _ Sender = (*SMTPSender)(nil)
