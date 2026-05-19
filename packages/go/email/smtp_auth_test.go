package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseAuthMechanism(t *testing.T) {
	cases := []struct {
		in   string
		want AuthMechanism
		err  bool
	}{
		{"", "", false},
		{"plain", AuthMechPlain, false},
		{"PLAIN", AuthMechPlain, false},
		{"login", AuthMechLogin, false},
		{"crammd5", AuthMechCRAMMD5, false},
		{"cram-md5", AuthMechCRAMMD5, false},
		{"cram_md5", AuthMechCRAMMD5, false},
		{"bogus", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseAuthMechanism(c.in)
			if c.err {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestAuthMechanism_Valid(t *testing.T) {
	cases := map[AuthMechanism]bool{
		"":               true,
		AuthMechPlain:    true,
		AuthMechLogin:    true,
		AuthMechCRAMMD5:  true,
		"unknown-mech":   false,
	}
	for m, want := range cases {
		if got := m.Valid(); got != want {
			t.Errorf("(%q).Valid() = %v; want %v", m, got, want)
		}
	}
}

func TestLoginAuth_StartRefusesWithoutTLS(t *testing.T) {
	a := &loginAuth{username: "u", password: "p"}
	if _, _, err := a.Start(&smtp.ServerInfo{TLS: false}); err == nil {
		t.Fatal("expected refusal without TLS")
	}
	mech, ir, err := a.Start(&smtp.ServerInfo{TLS: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mech != "LOGIN" {
		t.Errorf("mechanism: got %q want LOGIN", mech)
	}
	if len(ir) != 0 {
		t.Errorf("expected empty initial response, got %q", ir)
	}
}

func TestLoginAuth_NextHandlesPrompts(t *testing.T) {
	a := &loginAuth{username: "alice", password: "s3cret"}
	for _, c := range []struct {
		prompt string
		want   string
	}{
		{"Username:", "alice"},
		{"username:", "alice"},
		{"Password:", "s3cret"},
		{"password:", "s3cret"},
	} {
		got, err := a.Next([]byte(c.prompt), true)
		if err != nil {
			t.Fatalf("Next(%q): %v", c.prompt, err)
		}
		if string(got) != c.want {
			t.Errorf("Next(%q) = %q; want %q", c.prompt, got, c.want)
		}
	}
	if _, err := a.Next([]byte("weird:"), true); err == nil {
		t.Error("expected error on unknown prompt")
	}
	if got, err := a.Next(nil, false); err != nil || got != nil {
		t.Errorf("Next(nil,false) = (%q, %v); want (nil, nil)", got, err)
	}
}

func TestNewAuth_NoUserReturnsNil(t *testing.T) {
	auth, err := newAuth(AuthMechPlain, "host", "", "")
	if err != nil {
		t.Fatalf("newAuth: %v", err)
	}
	if auth != nil {
		t.Errorf("expected nil auth when no user supplied")
	}
}

func TestNewAuth_RejectsUnknownMech(t *testing.T) {
	if _, err := newAuth("nonsense", "host", "u", "p"); err == nil {
		t.Fatal("expected error on unknown mech")
	}
}

func TestLoadSMTPConfig_AuthMech(t *testing.T) {
	t.Setenv("GONEXT_SMTP_HOST", "h")
	t.Setenv("GONEXT_SMTP_FROM", "x@y.test")
	t.Setenv("GONEXT_SMTP_USER", "u")
	t.Setenv("GONEXT_SMTP_PASSWORD", "p")
	t.Setenv("GONEXT_SMTP_AUTH_MECH", "login")
	cfg, err := LoadSMTPConfig()
	if err != nil {
		t.Fatalf("LoadSMTPConfig: %v", err)
	}
	if cfg.AuthMech != AuthMechLogin {
		t.Errorf("AuthMech: got %q want login", cfg.AuthMech)
	}
}

// authProbingServer is a STARTTLS-capable SMTP server like the one in
// smtp_sender_test.go, but it advertises an extensible AUTH list and
// captures the chosen mechanism + the (decoded) credentials it
// receives. Used to exercise the LOGIN code path end-to-end.
type authProbingServer struct {
	ln           net.Listener
	tlsCfg       *tls.Config
	mu           sync.Mutex
	chosenMech   string
	username     string
	password     string
	advertise    string // "PLAIN", "LOGIN", "PLAIN LOGIN"
	stop         chan struct{}
	wg           sync.WaitGroup
	cramChallenge string // base64-encoded challenge for CRAM-MD5
}

func newAuthProbingServer(t *testing.T, advertise string) *authProbingServer {
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
	s := &authProbingServer{
		ln:        ln,
		tlsCfg:    &tls.Config{Certificates: []tls.Certificate{tlsCert}},
		stop:      make(chan struct{}),
		advertise: advertise,
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

func (s *authProbingServer) Addr() string { return s.ln.Addr().String() }

func (s *authProbingServer) Close() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *authProbingServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *authProbingServer) handle(conn net.Conn) {
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
				write("250-STARTTLS")
			}
			write("250-AUTH " + s.advertise)
			write("250 HELP")
		case strings.HasPrefix(cmd, "STARTTLS"):
			write("220 ready to start TLS")
			tlsConn := tls.Server(conn, s.tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
			upgraded = true
		case strings.HasPrefix(cmd, "AUTH LOGIN"):
			s.mu.Lock()
			s.chosenMech = "LOGIN"
			s.mu.Unlock()
			// Two prompts: "Username:" then "Password:". Both base64.
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			userLine, _ := rw.ReadString('\n')
			user, _ := base64.StdEncoding.DecodeString(strings.TrimRight(userLine, "\r\n"))
			write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			passLine, _ := rw.ReadString('\n')
			pass, _ := base64.StdEncoding.DecodeString(strings.TrimRight(passLine, "\r\n"))
			s.mu.Lock()
			s.username = string(user)
			s.password = string(pass)
			s.mu.Unlock()
			write("235 Authentication successful")
		case strings.HasPrefix(cmd, "AUTH CRAM-MD5"):
			s.mu.Lock()
			s.chosenMech = "CRAM-MD5"
			s.mu.Unlock()
			challenge := "<12345.67890@fake.test>"
			s.cramChallenge = challenge
			write("334 " + base64.StdEncoding.EncodeToString([]byte(challenge)))
			respLine, _ := rw.ReadString('\n')
			resp, _ := base64.StdEncoding.DecodeString(strings.TrimRight(respLine, "\r\n"))
			// "username hexdigest"
			parts := strings.SplitN(string(resp), " ", 2)
			s.mu.Lock()
			if len(parts) == 2 {
				s.username = parts[0]
				s.password = parts[1] // expected hex digest
			}
			s.mu.Unlock()
			write("235 Authentication successful")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			s.mu.Lock()
			s.chosenMech = "PLAIN"
			s.mu.Unlock()
			// PLAIN supplies credentials inline; we don't decode them
			// for this test (the existing PLAIN test in smtp_sender_test
			// covers the wire round-trip).
			write("235 Authentication successful")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			write("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			write("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			write("354 End data with <CR><LF>.<CR><LF>")
			for {
				dl, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
			}
			write("250 OK queued")
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 bye")
			return
		case strings.HasPrefix(cmd, "RSET"):
			write("250 OK")
		default:
			write("500 unknown command")
		}
	}
}

func (s *authProbingServer) Snapshot() (mech, user, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chosenMech, s.username, s.password
}

func TestSMTPSender_LoginAuth(t *testing.T) {
	srv := newAuthProbingServer(t, "LOGIN")
	host, port := splitHostPort(t, srv.Addr())
	s, err := NewSMTPSender(SMTPConfig{
		Host: host, Port: port,
		User: "alice", Password: "s3cret",
		From:               "noreply@chassis.test",
		InsecureSkipVerify: true,
		AuthMech:           AuthMechLogin,
		DialTimeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), Message{
		To: "user@example.com", Subject: "S", TextBody: "B",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	mech, user, pass := srv.Snapshot()
	if mech != "LOGIN" {
		t.Errorf("mechanism: got %q want LOGIN", mech)
	}
	if user != "alice" || pass != "s3cret" {
		t.Errorf("credentials: got (%q,%q) want (alice, s3cret)", user, pass)
	}
}

func TestSMTPSender_CRAMMD5Auth(t *testing.T) {
	srv := newAuthProbingServer(t, "CRAM-MD5")
	host, port := splitHostPort(t, srv.Addr())
	s, err := NewSMTPSender(SMTPConfig{
		Host: host, Port: port,
		User: "alice", Password: "topsecret",
		From:               "noreply@chassis.test",
		InsecureSkipVerify: true,
		AuthMech:           AuthMechCRAMMD5,
		DialTimeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), Message{
		To: "user@example.com", Subject: "S", TextBody: "B",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	mech, user, hexDigest := srv.Snapshot()
	if mech != "CRAM-MD5" {
		t.Errorf("mechanism: got %q want CRAM-MD5", mech)
	}
	if user != "alice" {
		t.Errorf("username: got %q want alice", user)
	}
	want := cramMD5Hex("topsecret", srv.cramChallenge)
	if hexDigest != want {
		t.Errorf("digest mismatch: got %q want %q", hexDigest, want)
	}
}

func TestSMTPSender_InvalidAuthMechAtValidate(t *testing.T) {
	_, err := NewSMTPSender(SMTPConfig{
		Host: "h", Port: 587, From: "x@y.test",
		User: "u", Password: "p",
		AuthMech: "weird",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// silence unused import warnings.
var _ = errors.New
var _ = fmt.Sprintf
