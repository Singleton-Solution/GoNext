package initcmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestValidateEmail(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isErr bool
	}{
		{"alice@example.com", "alice@example.com", false},
		{"  alice@example.com  ", "alice@example.com", false},
		{"Alice <alice@example.com>", "alice@example.com", false},
		{"", "", true},
		{"not-an-email", "", true},
		{"alice@", "", true},
		{"@example.com", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := validateEmail(tc.in)
			if (err != nil) != tc.isErr {
				t.Fatalf("validateEmail(%q) err=%v want isErr=%v", tc.in, err, tc.isErr)
			}
			if !tc.isErr && got != tc.want {
				t.Errorf("validateEmail(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.isErr && !errors.Is(err, ErrInvalidEmail) {
				t.Errorf("validateEmail(%q) err = %v, want wrapping ErrInvalidEmail", tc.in, err)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		in    string
		isErr bool
	}{
		{"correct horse battery staple", false},
		{"abcdefghijkl", false}, // exactly 12 chars
		{"abcdefghijk", true},   // 11 chars
		{"", true},
		{"short", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := validatePassword(tc.in)
			if (err != nil) != tc.isErr {
				t.Errorf("validatePassword(%q) err=%v want isErr=%v", tc.in, err, tc.isErr)
			}
			if tc.isErr && !errors.Is(err, ErrPasswordTooShort) {
				t.Errorf("validatePassword(%q) err=%v want ErrPasswordTooShort", tc.in, err)
			}
		})
	}
}

func TestStringPrompter_ReadsInOrder(t *testing.T) {
	var out bytes.Buffer
	p := newStringPrompter(&out, []string{"alice@example.com", "very-long-password"})

	got1, err := p.readLine("Email: ")
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got1 != "alice@example.com" {
		t.Errorf("first readLine = %q, want alice@example.com", got1)
	}
	got2, err := p.readPassword("Password: ")
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if got2 != "very-long-password" {
		t.Errorf("readPassword = %q, want very-long-password", got2)
	}

	// Exhausted.
	_, err = p.readLine("More: ")
	if !errors.Is(err, io.EOF) {
		t.Errorf("exhausted prompter err=%v, want io.EOF", err)
	}

	// Mirror behavior: the label and the answer should be in out.
	if !strings.Contains(out.String(), "Email: ") {
		t.Errorf("expected label echoed in out, got %q", out.String())
	}
	if !strings.Contains(out.String(), "alice@example.com") {
		t.Errorf("expected answer echoed in out, got %q", out.String())
	}
}

func TestOSPrompter_ReadLine(t *testing.T) {
	in := strings.NewReader("alice@example.com\n")
	var out bytes.Buffer
	// fd=-1 keeps us off the TTY path.
	p := newOSPrompter(in, &out, -1)

	got, err := p.readLine("Email: ")
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got != "alice@example.com" {
		t.Errorf("readLine = %q", got)
	}
	if !strings.Contains(out.String(), "Email:") {
		t.Errorf("label missing in out=%q", out.String())
	}
}

func TestOSPrompter_ReadPassword_NoTTY_ReadsFromStdin(t *testing.T) {
	in := strings.NewReader("super-secret-password\n")
	var out bytes.Buffer
	p := newOSPrompter(in, &out, -1)
	got, err := p.readPassword("Password: ")
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if got != "super-secret-password" {
		t.Errorf("readPassword = %q", got)
	}
}

func TestOSPrompter_ReadLine_StripsCRLF(t *testing.T) {
	in := strings.NewReader("alice@example.com\r\n")
	var out bytes.Buffer
	p := newOSPrompter(in, &out, -1)
	got, err := p.readLine("Email: ")
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got != "alice@example.com" {
		t.Errorf("readLine CRLF: got %q want %q", got, "alice@example.com")
	}
}
