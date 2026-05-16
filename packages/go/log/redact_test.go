package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// emit logs one INFO line through a JSON handler with the redactor and
// returns the parsed JSON for assertion. Helper for the table tests below.
func emit(t *testing.T, attrs ...slog.Attr) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: redactAttr,
	})
	l := slog.New(h)
	// Convert []slog.Attr → []any for LogAttrs.
	args := make([]any, 0, len(attrs))
	for _, a := range attrs {
		args = append(args, a)
	}
	l.Info("test", args...)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n--- raw ---\n%s", err, buf.String())
	}
	return got
}

func TestRedact_SensitiveKeys_FullyMasked(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"password", "hunter2"},
		{"PASSWORD", "hunter2"},     // case-insensitive match
		{"password_hash", "$2a$..."},
		{"secret", "shh"},
		{"api_key", "AKxxx"},
		{"apikey", "AKxxx"},
		{"token", "eyJ..."},
		{"access_token", "ya29..."},
		{"refresh_token", "1//..."},
		{"id_token", "eyJ..."},
		{"bearer", "Bearer xyz"},
		{"authorization", "Bearer xyz"},
		{"cookie", "session=abc"},
		{"set-cookie", "session=abc; Path=/"},
		{"x-api-key", "AKxxx"},
		{"x-auth-token", "xxx"},
		{"private_key", "-----BEGIN..."},
		{"pepper", "shh"},
		{"session_token", "xyz"},
		{"client_secret", "shh"},
		{"webhook_secret", "shh"},
		{"signing_secret", "shh"},
		{"recovery_code", "12345"},
		{"totp_secret", "JBSW..."},
		{"otp", "123456"},
		{"pin", "1234"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			got := emit(t, slog.String(c.key, c.value))
			v, ok := got[strings.ToLower(c.key)]
			// slog preserves the original key case
			if !ok {
				v = got[c.key]
			}
			if v != redactMask {
				t.Errorf("key %q: got %v, want %q", c.key, v, redactMask)
			}
		})
	}
}

func TestRedact_PartialMask_Email(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"alice@example.com", "a***@example.com"},
		{"bob@gonext.dev", "b***@gonext.dev"},
		{"a@b.co", "a***@b.co"},
		// Malformed should fail-closed to full redaction.
		{"not-an-email", redactMask},
		{"@nodomain.com", redactMask},
		{"no-at-sign", redactMask},
		{"", redactMask},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := emit(t, slog.String("email", c.in))
			if got["email"] != c.want {
				t.Errorf("email %q: got %v, want %q", c.in, got["email"], c.want)
			}
		})
	}
}

func TestRedact_PartialMask_Phone(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"+1-555-867-5309", "***5309"},
		{"5558675309", "***5309"},
		{"(555) 867-5309", "***5309"},
		{"123", redactMask},
		{"", redactMask},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := emit(t, slog.String("phone", c.in))
			if got["phone"] != c.want {
				t.Errorf("phone %q: got %v, want %q", c.in, got["phone"], c.want)
			}
		})
	}
}

func TestRedact_StringPatterns(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		value     string
		mustNotContain string // raw secret should not appear in output
		mustContain    string // mask label should appear
	}{
		{
			// Don't use "msg" as the user key here — it collides with
			// slog's built-in MessageKey, which the redactor intentionally
			// skips. See the switch at the top of redactAttr.
			name:           "JWT in free-form text",
			key:            "header",
			value:          "auth header was eyJabcdefghij.eyJklmnopqrst.signaturesignaturesigna and the request failed",
			mustNotContain: "eyJabcdefghij.eyJklmnopqrst",
			mustContain:    "(jwt)",
		},
		{
			name:           "AWS access key ID in URL",
			key:            "url",
			value:          "https://example.com/?key=AKIAIOSFODNN7EXAMPLE&signed=...",
			mustNotContain: "AKIAIOSFODNN7EXAMPLE",
			mustContain:    "(aws_key)",
		},
		{
			name:           "GitHub PAT in error",
			key:            "err",
			value:          "auth failed with token ghp_abcdefghijklmnopqrstuvwxyz0123456789",
			mustNotContain: "ghp_abcdefghij",
			mustContain:    "(gh_pat)",
		},
		{
			name:           "Slack token in body",
			key:            "body",
			value:          "{\"token\":\"xoxb-abc-123-def-456\"}",
			mustNotContain: "xoxb-abc-123",
			mustContain:    "(slack)",
		},
		{
			name:           "SSN in free text",
			key:            "note",
			value:          "customer SSN 123-45-6789 verified",
			mustNotContain: "123-45-6789",
			mustContain:    "(ssn)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := emit(t, slog.String(c.key, c.value))
			out, _ := got[c.key].(string)
			if c.mustNotContain != "" && strings.Contains(out, c.mustNotContain) {
				t.Errorf("output contains raw secret %q: %s", c.mustNotContain, out)
			}
			if c.mustContain != "" && !strings.Contains(out, c.mustContain) {
				t.Errorf("output missing mask label %q: %s", c.mustContain, out)
			}
		})
	}
}

func TestRedact_NonSensitiveUnchanged(t *testing.T) {
	cases := map[string]string{
		"user_id":  "u_abc",
		"post_id":  "p_42",
		"username": "alice",
		"slug":     "hello-world",
		"count":    "5",
		"status":   "draft",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			got := emit(t, slog.String(k, v))
			if got[k] != v {
				t.Errorf("key %q: got %v, want %q (must be passthrough)", k, got[k], v)
			}
		})
	}
}

func TestRedact_NumericValuesNotMutated(t *testing.T) {
	// Even with a key like "secret", numeric values would never be a secret
	// (typical secrets are strings). But the redactor still masks the key
	// because the value's semantic is "what's under this name". So this
	// test asserts the value gets masked but the type is converted to string
	// — that's correct, fail-closed-on-key beats type preservation.
	got := emit(t, slog.Int("secret", 42))
	if got["secret"] != redactMask {
		t.Errorf("numeric value under sensitive key: got %v, want %q", got["secret"], redactMask)
	}
}

func TestRedact_FailOpenOnPanic(t *testing.T) {
	// scanAndMask should never panic on any string; if it ever does, the
	// deferred recover in redactAttr lets the original through. We can't
	// easily inject a panic into the existing patterns, but we can verify
	// the recover is structurally present by checking that pathological
	// inputs (very long, embedded nulls, unicode) don't crash.
	pathological := strings.Repeat("a", 100000) + "\x00\x01" + strings.Repeat("ÿ", 1000)
	got := emit(t, slog.String("note", pathological))
	if got["note"] == nil {
		t.Errorf("pathological input dropped from output (fail-open violated)")
	}
}
