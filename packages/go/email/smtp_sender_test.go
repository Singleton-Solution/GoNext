package email

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSMTPConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		cfg  SMTPConfig
		ok   bool
	}{
		{
			name: "valid no-auth",
			cfg:  SMTPConfig{Host: "smtp.test", Port: 587, From: "noreply@chassis.test"},
			ok:   true,
		},
		{
			name: "valid auth",
			cfg: SMTPConfig{
				Host: "smtp.test", Port: 587, From: "noreply@chassis.test",
				User: "u", Password: "p",
			},
			ok: true,
		},
		{name: "missing host", cfg: SMTPConfig{Port: 587, From: "x@y.test"}},
		{name: "bad port", cfg: SMTPConfig{Host: "h", Port: -1, From: "x@y.test"}},
		{name: "missing from", cfg: SMTPConfig{Host: "h", Port: 587}},
		{name: "user without password",
			cfg: SMTPConfig{Host: "h", Port: 587, From: "x@y.test", User: "u"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.validate()
			if c.ok && err != nil {
				t.Errorf("validate: unexpected error %v", err)
			}
			if !c.ok && err == nil {
				t.Errorf("validate: expected error, got nil")
			}
		})
	}
}

func TestLoadSMTPConfig(t *testing.T) {
	t.Setenv("GONEXT_SMTP_HOST", "smtp.test")
	t.Setenv("GONEXT_SMTP_PORT", "2525")
	t.Setenv("GONEXT_SMTP_FROM", "noreply@chassis.test")
	t.Setenv("GONEXT_SMTP_USER", "user1")
	t.Setenv("GONEXT_SMTP_PASSWORD", "pw")
	t.Setenv("GONEXT_SMTP_IMPLICIT_TLS", "yes")
	t.Setenv("GONEXT_SMTP_DIAL_TIMEOUT", "3s")

	cfg, err := LoadSMTPConfig()
	if err != nil {
		t.Fatalf("LoadSMTPConfig: %v", err)
	}
	if cfg.Host != "smtp.test" || cfg.Port != 2525 || cfg.From != "noreply@chassis.test" {
		t.Errorf("bad config: %+v", cfg)
	}
	if !cfg.ImplicitTLS {
		t.Errorf("ImplicitTLS not set")
	}
	if cfg.DialTimeout != 3*time.Second {
		t.Errorf("DialTimeout: got %v want 3s", cfg.DialTimeout)
	}
}

func TestLoadSMTPConfig_BadPort(t *testing.T) {
	t.Setenv("GONEXT_SMTP_HOST", "h")
	t.Setenv("GONEXT_SMTP_FROM", "x@y.test")
	t.Setenv("GONEXT_SMTP_PORT", "not-a-port")
	if _, err := LoadSMTPConfig(); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildMIME_PlainOnly(t *testing.T) {
	body := buildMIME(Message{
		To: "a@b.test", Subject: "S", TextBody: "hello",
	}, "from@x.test")
	s := string(body)
	if !strings.Contains(s, "Content-Type: text/plain") {
		t.Errorf("expected text/plain; got:\n%s", s)
	}
	if strings.Contains(s, "multipart") {
		t.Errorf("did not expect multipart for plain-only:\n%s", s)
	}
	if !strings.Contains(s, "From: from@x.test") {
		t.Errorf("missing From header:\n%s", s)
	}
}

func TestBuildMIME_HTMLOnly(t *testing.T) {
	body := buildMIME(Message{
		To: "a@b.test", Subject: "S", HTMLBody: "<p>hi</p>",
	}, "from@x.test")
	s := string(body)
	if !strings.Contains(s, "Content-Type: text/html") {
		t.Errorf("expected text/html; got:\n%s", s)
	}
}

func TestBuildMIME_Multipart(t *testing.T) {
	body := buildMIME(Message{
		To:       "a@b.test",
		Subject:  "S",
		TextBody: "hello",
		HTMLBody: "<p>hi</p>",
		ReplyTo:  "support@x.test",
	}, "from@x.test")
	s := string(body)
	if !strings.Contains(s, "multipart/alternative") {
		t.Errorf("expected multipart/alternative; got:\n%s", s)
	}
	if !strings.Contains(s, "Reply-To: support@x.test") {
		t.Errorf("missing Reply-To:\n%s", s)
	}
	if !strings.Contains(s, "hello") || !strings.Contains(s, "<p>hi</p>") {
		t.Errorf("missing body content:\n%s", s)
	}
}

// fakeSMTPServer is a minimal SMTP server that speaks just enough of
// the protocol to exercise SMTPSender.Send through the STARTTLS
// happy path. It captures the submitted message bytes so tests can
// assert on what would have hit the wire.
type fakeSMTPServer struct {
	ln          net.Listener
	tlsCfg      *tls.Config
	mu          sync.Mutex
	got         strings.Builder
	tlsHandshaked bool
	authSawPLAIN  bool
	stop        chan struct{}
	wg          sync.WaitGroup
	starttlsErr bool // when true, the server replies 502 to STARTTLS
}

func newFakeSMTPServer(t *testing.T, opts ...func(*fakeSMTPServer)) *fakeSMTPServer {
	t.Helper()
	cert, key := generateSelfSignedCert(t)
	tlsCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatalf("x509 keypair: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{
		ln:     ln,
		tlsCfg: &tls.Config{Certificates: []tls.Certificate{tlsCert}, ServerName: "127.0.0.1"},
		stop:   make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

func withSTARTTLSError() func(*fakeSMTPServer) {
	return func(s *fakeSMTPServer) { s.starttlsErr = true }
}

func (s *fakeSMTPServer) Addr() string { return s.ln.Addr().String() }

func (s *fakeSMTPServer) Close() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *fakeSMTPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	write := func(line string) {
		_, _ = rw.WriteString(line + "\r\n")
		_ = rw.Flush()
	}
	write("220 fake.test ESMTP ready")

	upgraded := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			write("250-fake.test")
			if !upgraded {
				if s.starttlsErr {
					// Advertise STARTTLS so the client tries, then we
					// reject it below.
					write("250-STARTTLS")
				} else {
					write("250-STARTTLS")
				}
			}
			write("250-AUTH PLAIN")
			write("250 HELP")
		case strings.HasPrefix(cmd, "STARTTLS"):
			if s.starttlsErr {
				write("502 STARTTLS not available right now")
				continue
			}
			write("220 ready to start TLS")
			tlsConn := tls.Server(conn, s.tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
			upgraded = true
			s.mu.Lock()
			s.tlsHandshaked = true
			s.mu.Unlock()
		case strings.HasPrefix(cmd, "AUTH"):
			s.mu.Lock()
			s.authSawPLAIN = strings.Contains(cmd, "PLAIN")
			s.mu.Unlock()
			write("235 Authentication successful")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			write("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			write("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			write("354 End data with <CR><LF>.<CR><LF>")
			var buf strings.Builder
			for {
				dl, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
				buf.WriteString(dl)
			}
			s.mu.Lock()
			s.got.WriteString(buf.String())
			s.mu.Unlock()
			write("250 OK queued")
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 bye")
			return
		case strings.HasPrefix(cmd, "RSET"):
			write("250 OK")
		case strings.HasPrefix(cmd, "NOOP"):
			write("250 OK")
		default:
			write("500 unknown command")
		}
	}
}

func (s *fakeSMTPServer) Got() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got.String()
}

// generateSelfSignedCert returns a fresh self-signed cert + key in
// PEM form, valid for 127.0.0.1. Suitable for ad-hoc TLS tests.
func generateSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"127.0.0.1", "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return certPEM, keyPEM
}

func TestSMTPSender_SendWithSTARTTLS(t *testing.T) {
	srv := newFakeSMTPServer(t)
	host, port := splitHostPort(t, srv.Addr())
	s, err := NewSMTPSender(SMTPConfig{
		Host: host, Port: port,
		User: "alice", Password: "secret",
		From:               "noreply@chassis.test",
		InsecureSkipVerify: true,
		DialTimeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), Message{
		To:       "user@example.com",
		Subject:  "Verify",
		TextBody: "click here",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	body := srv.Got()
	if !strings.Contains(body, "Subject: Verify") {
		t.Errorf("expected subject in transmitted bytes; got:\n%s", body)
	}
	if !strings.Contains(body, "click here") {
		t.Errorf("expected text body in transmitted bytes; got:\n%s", body)
	}

	// Confirm the STARTTLS upgrade actually happened. If this is
	// false, the message went over a cleartext channel — exactly
	// what the SMTPSender contract is designed to prevent.
	srv.mu.Lock()
	upgraded := srv.tlsHandshaked
	sawAuthPlain := srv.authSawPLAIN
	srv.mu.Unlock()
	if !upgraded {
		t.Errorf("expected STARTTLS handshake to complete")
	}
	if !sawAuthPlain {
		t.Errorf("expected AUTH PLAIN over the encrypted channel")
	}
}

func TestSMTPSender_RequiresSTARTTLSAdvertisement(t *testing.T) {
	srv := newFakeSMTPServer(t, withSTARTTLSError())
	host, port := splitHostPort(t, srv.Addr())
	s, err := NewSMTPSender(SMTPConfig{
		Host: host, Port: port,
		From:               "noreply@chassis.test",
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), Message{
		To: "user@example.com", Subject: "x", TextBody: "y",
	})
	if err == nil {
		t.Fatal("expected error when STARTTLS fails")
	}
	if !strings.Contains(err.Error(), "starttls") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSMTPSender_RejectsInvalidMessage(t *testing.T) {
	s, err := NewSMTPSender(SMTPConfig{Host: "h", Port: 25, From: "x@y.test"})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), Message{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSMTPSender_ContextCancellation(t *testing.T) {
	// Substitute a dialer that blocks until ctx is cancelled.
	s, err := NewSMTPSender(SMTPConfig{
		Host: "127.0.0.1", Port: 1, From: "x@y.test",
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	s.dialFn = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.Send(ctx, Message{To: "a@b.test", Subject: "x", TextBody: "y"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port := 0
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		t.Fatalf("port parse: %v", err)
	}
	return host, port
}

// silence unused import warnings if the file shifts: io import is
// kept so future test bodies can compose with the bufio readers.
var _ = io.EOF
