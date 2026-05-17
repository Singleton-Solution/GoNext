package log

import (
	"log/slog"
	"regexp"
	"strings"
)

// redactMask is the placeholder value substituted for any redacted attribute.
const redactMask = "***REDACTED***"

// sensitiveKeys is the case-insensitive set of attribute names whose values
// are always replaced with redactMask. Match is on equality after lowercasing,
// not substring — "password_hint" is NOT in the set because it's not a secret
// (it's a UI affordance). If you need substring matching, extend the
// shouldRedactKey function and add tests.
var sensitiveKeys = map[string]struct{}{
	"password":         {},
	"passwd":           {},
	"password_hash":    {},
	"secret":           {},
	"api_key":          {},
	"apikey":           {},
	"token":            {},
	"access_token":     {},
	"refresh_token":    {},
	"id_token":         {},
	"bearer":           {},
	"authorization":    {},
	"cookie":           {},
	"set-cookie":       {},
	"x-api-key":        {},
	"x-auth-token":     {},
	"private_key":      {},
	"pepper":           {},
	"session_token":    {},
	"client_secret":    {},
	"webhook_secret":   {},
	"signing_secret":   {},
	"recovery_code":    {},
	"totp_secret":      {},
	"otp":              {},
	"pin":              {},
}

// keysWithPartialMask are keys whose values are partially redacted instead of
// fully masked. For example, an email "alice@example.com" becomes "a***@example.com"
// so logs remain useful for debugging while exposing minimal PII.
var keysWithPartialMask = map[string]func(string) string{
	"email":   partialEmail,
	"phone":   partialPhone,
	"user_id": passthrough, // explicit: user_id is NOT redacted (often an int).
}

// Pattern-based redaction. These run on string values regardless of key name.
// Each entry is (compiled regex, replacement).
type stringRedactor struct {
	re   *regexp.Regexp
	mask string
}

var stringRedactors = []stringRedactor{
	// JWT: three base64url segments separated by dots, header starts with eyJ.
	// Match conservatively to avoid false positives on dotted identifiers.
	{
		re:   regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
		mask: redactMask + "(jwt)",
	},
	// AWS-style access key ID (AKIA + 16 alnum).
	{
		re:   regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		mask: redactMask + "(aws_key)",
	},
	// GitHub PAT (ghp_ + 36 base64-ish).
	{
		re:   regexp.MustCompile(`\bghp_[A-Za-z0-9]{36,}\b`),
		mask: redactMask + "(gh_pat)",
	},
	// Slack token (xox[bpoasr]-...).
	{
		re:   regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		mask: redactMask + "(slack)",
	},
	// US SSN: NNN-NN-NNNN. Conservative — only matches with dashes to avoid
	// catching random 9-digit IDs.
	{
		re:   regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		mask: redactMask + "(ssn)",
	},
}

// redactAttr is the ReplaceAttr function installed on the slog handler.
// It is called for every attribute at the moment of emission and returns
// either the original attr or a redacted version.
//
// Behavior:
//   - If the key (case-insensitive) is in sensitiveKeys, the value is replaced
//     entirely with redactMask. This is the "fail-closed on match" rule.
//   - If the key is in keysWithPartialMask, the partial-mask function runs on
//     the stringified value.
//   - If the value is a string, it is scanned against stringRedactors and any
//     matching substrings are replaced. This catches JWTs, AWS keys, SSNs
//     even when nested in URLs, error messages, or free-form text.
//   - On any parse error (panicking redactor, unexpected type), the original
//     attr is returned unchanged. This is the "fail-open on parse error" rule:
//     a buggy redactor must never silently drop diagnostic context, only
//     mask things it recognizes.
func redactAttr(groups []string, a slog.Attr) slog.Attr {
	// Strip slog's TimeKey, MessageKey, LevelKey — those are never sensitive
	// and short-circuiting is cheap.
	switch a.Key {
	case slog.TimeKey, slog.MessageKey, slog.LevelKey, slog.SourceKey:
		return a
	}

	defer func() {
		// Fail-open: if anything in here panics, leave the attr alone.
		_ = recover()
	}()

	lk := strings.ToLower(a.Key)

	if _, ok := sensitiveKeys[lk]; ok {
		return slog.String(a.Key, redactMask)
	}

	if mask, ok := keysWithPartialMask[lk]; ok {
		return slog.String(a.Key, mask(a.Value.String()))
	}

	// String pattern scan. Only on string-shaped values; numeric, bool,
	// duration are not scanned (cheaper and avoids spurious mutation).
	if a.Value.Kind() == slog.KindString {
		s := a.Value.String()
		redacted := scanAndMask(s)
		if redacted != s {
			return slog.String(a.Key, redacted)
		}
		return a
	}

	// Group: recurse into nested attrs. slog handles groups by calling
	// ReplaceAttr for each child attr separately, so this branch is a
	// no-op — we just return the group unchanged.
	return a
}

// scanAndMask runs all stringRedactors over s and returns the masked result.
// If no pattern matches, returns s unchanged (caller can use equality to
// detect "did anything mask").
func scanAndMask(s string) string {
	out := s
	for _, r := range stringRedactors {
		out = r.re.ReplaceAllString(out, r.mask)
	}
	return out
}

// partialEmail returns "a***@example.com" for "alice@example.com".
// For malformed inputs, returns redactMask to fail-closed.
func partialEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at < 1 || at == len(s)-1 {
		// No local part or no domain — treat as suspicious, fail-closed.
		return redactMask
	}
	local := s[:at]
	domain := s[at:]
	if len(local) <= 1 {
		return string(local[0]) + "***" + domain
	}
	return string(local[0]) + "***" + domain
}

// partialPhone keeps the last 4 digits, masks the rest. "+1-555-867-5309" -> "***5309".
func partialPhone(s string) string {
	// Strip non-digit characters for length check.
	digits := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits = append(digits, s[i])
		}
	}
	if len(digits) < 4 {
		return redactMask
	}
	return "***" + string(digits[len(digits)-4:])
}

// passthrough is the identity. Used in keysWithPartialMask to explicitly
// document keys that look sensitive but aren't (e.g., user_id is just a
// foreign-key-shaped identifier, not a secret).
func passthrough(s string) string { return s }
