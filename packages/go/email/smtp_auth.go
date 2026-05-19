package email

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
)

// AuthMechanism identifies the SMTP AUTH variant the sender should use.
//
// PLAIN is the default and what stdlib smtp.PlainAuth exposes. It sends
// the credentials in one base64-encoded blob over the (already
// TLS-upgraded) channel. Every modern provider supports it.
//
// LOGIN is the older Microsoft/Outlook-flavored variant that prompts
// for username and password as separate base64 strings. Some legacy
// relays (Office365 hybrid configurations, on-prem Exchange) only
// advertise LOGIN even when PLAIN would work just as well.
//
// CRAMMD5 is RFC 2195 challenge/response. The server sends a challenge,
// the client returns HMAC-MD5(secret, challenge). Useful for niche
// internal relays that mandate it; not a security upgrade over PLAIN
// because PLAIN runs over TLS in this package anyway.
//
// Empty string defaults to PLAIN — the value validated at config-load
// time so a misspelled mechanism crashes boot rather than the first
// send.
type AuthMechanism string

const (
	AuthMechPlain   AuthMechanism = "plain"
	AuthMechLogin   AuthMechanism = "login"
	AuthMechCRAMMD5 AuthMechanism = "crammd5"
)

// Valid reports whether m is one of the recognised mechanisms. The
// empty string is also valid (treated as "use the default", PLAIN).
func (m AuthMechanism) Valid() bool {
	switch m {
	case "", AuthMechPlain, AuthMechLogin, AuthMechCRAMMD5:
		return true
	default:
		return false
	}
}

// ParseAuthMechanism normalises a string to an AuthMechanism. It is
// case-insensitive and tolerates a few common spellings
// ("cram-md5", "cram_md5"). Returns an error for unknown values so
// the loader can fail closed rather than silently default.
func ParseAuthMechanism(s string) (AuthMechanism, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", nil
	case "plain":
		return AuthMechPlain, nil
	case "login":
		return AuthMechLogin, nil
	case "crammd5", "cram-md5", "cram_md5":
		return AuthMechCRAMMD5, nil
	default:
		return "", fmt.Errorf("email: unknown AUTH mechanism %q (want plain|login|crammd5)", s)
	}
}

// newAuth builds the smtp.Auth implementation for mech. host is the
// SMTP server name (used by PLAIN for the SASL identity, ignored by
// LOGIN and CRAMMD5).
//
// Returns nil if user is empty (i.e. no auth requested). That mirrors
// the legacy SMTPSender behaviour: passing no User skips the AUTH
// command entirely.
func newAuth(mech AuthMechanism, host, user, password string) (smtp.Auth, error) {
	if user == "" {
		return nil, nil
	}
	switch mech {
	case "", AuthMechPlain:
		return smtp.PlainAuth("", user, password, host), nil
	case AuthMechLogin:
		return &loginAuth{username: user, password: password}, nil
	case AuthMechCRAMMD5:
		return smtp.CRAMMD5Auth(user, password), nil
	default:
		return nil, fmt.Errorf("email: unknown AUTH mechanism %q", mech)
	}
}

// loginAuth implements the LOGIN SASL mechanism that the standard
// library does not ship with. The server sends two prompts ("Username:"
// then "Password:") and we reply with the credentials as separate
// base64-encoded blobs.
//
// We restrict LOGIN to TLS-upgraded connections: refusing to send
// credentials over a cleartext channel is the same policy the
// stdlib's PlainAuth enforces (TLS bool on smtp.Client). The check
// happens in Start by reading server.TLS — if the server reports the
// connection is not TLS, we error out.
type loginAuth struct {
	username string
	password string
}

// Start implements smtp.Auth. It returns "LOGIN" as the chosen
// mechanism and an empty initial response — LOGIN does not support
// SASL initial response, so the first server reply is the username
// prompt.
func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("email: LOGIN auth requires a TLS-secured connection")
	}
	return "LOGIN", nil, nil
}

// Next implements smtp.Auth. The server prompts in two stages:
// "Username:" then "Password:". The stdlib smtp.Client passes the
// raw challenge bytes (after base64-decoding) so we match against the
// lowercased prompt to tolerate the small variation in prompt strings
// across SMTP servers ("Username:", "username:", "User Name:").
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.HasPrefix(prompt, "user"):
		return []byte(a.username), nil
	case strings.HasPrefix(prompt, "pass"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("email: unexpected LOGIN auth prompt %q", string(fromServer))
	}
}

// cramMD5Hex is exposed for tests so the test server can compute the
// expected response without re-implementing the algorithm. The actual
// auth path uses smtp.CRAMMD5Auth from the standard library.
func cramMD5Hex(secret, challenge string) string {
	h := hmac.New(md5.New, []byte(secret))
	h.Write([]byte(challenge))
	return hex.EncodeToString(h.Sum(nil))
}
