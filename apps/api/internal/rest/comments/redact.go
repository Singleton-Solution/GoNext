// redact.go is the IP-redaction cron's data-access side. The
// scheduler entry (in packages/go/jobs/cron, wired by the operator at
// boot) fires this function once a day; it zeroes the last octet of
// every comments.author_ip row older than the redaction threshold.
//
// Why redact instead of delete:
//
//   - We still need to count comments-per-/24 for moderator triage
//     and for the rate limiter's longer windows. Zeroing the last
//     octet preserves the /24 signal while removing the identifying
//     detail of the source.
//   - The audit trail of "who said what when" is preserved; the
//     missing detail is which exact device — at 30 days that's
//     functionally PII rather than a forensics signal.
//
// IPv6 redaction zeroes the last 80 bits (the bottom 5 groups in the
// canonical text representation), keeping the /48 prefix that most
// abuse reports key off.
package comments

import (
	"context"
	"net"
	"strings"
	"time"
)

// DefaultRedactionAge is the threshold past which a comment's IP is
// truncated to its prefix. 30 days matches GoNext's general PII
// retention policy (see docs/06-auth-permissions.md §5.3).
const DefaultRedactionAge = 30 * 24 * time.Hour

// IPRedactor is the store-facing interface the redaction cron drives.
// MemoryStore implements it; the Postgres store provides a SQL-
// backed variant.
type IPRedactor interface {
	// RedactIPsBefore zeroes the last octet (IPv4) or last 80 bits
	// (IPv6) of every author_ip stored on a comment older than the
	// given cutoff. Returns the count of rows updated.
	RedactIPsBefore(ctx context.Context, cutoff time.Time) (int, error)
}

// RunRedactionCron is the cron-callback the scheduler invokes. It
// translates the configured age into a cutoff and delegates to the
// store. Designed to be the literal value passed as the
// taskspec.HandlerFunc for a "comments.redact_ip.daily" schedule.
//
// `age` is the lookback; pass DefaultRedactionAge unless the operator
// has a policy reason to vary it. `now` is the wall-clock function;
// tests inject a deterministic clock.
func RunRedactionCron(ctx context.Context, store IPRedactor, age time.Duration, now func() time.Time) (int, error) {
	if now == nil {
		now = time.Now
	}
	cutoff := now().Add(-age)
	return store.RedactIPsBefore(ctx, cutoff)
}

// redactIP applies the in-place truncation to a textual IP. Returns
// the original string when net.ParseIP cannot recognise the input
// (malformed addresses are rare but possible — e.g. a "::1" from a
// localhost test in a real row); the redactor logs and moves on
// rather than dropping the row entirely.
func redactIP(in string) string {
	ip := net.ParseIP(in)
	if ip == nil {
		return in
	}
	// IPv4 (or IPv4-in-IPv6): zero the last octet. We re-use the
	// To4() check rather than peeking at the dotted form because a
	// caller might supply "::ffff:192.0.2.1" and we want the same
	// /24-preservation behaviour.
	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	// IPv6: zero the bottom 80 bits (10 bytes), keeping the /48
	// prefix. The /48 is the prefix scale abuse reports key off
	// (every RIR's recommended end-site allocation).
	for i := 6; i < 16; i++ {
		ip[i] = 0
	}
	return ip.String()
}

// MemoryStore satisfies IPRedactor.

// RedactIPsBefore zeroes the trailing octet/bytes on every row older
// than cutoff. Returns the count of rows updated.
func (s *MemoryStore) RedactIPsBefore(_ context.Context, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, row := range s.rows {
		if row.CreatedAt.After(cutoff) {
			continue
		}
		if strings.TrimSpace(row.AuthorIP) == "" {
			continue
		}
		redacted := redactIP(row.AuthorIP)
		if redacted == row.AuthorIP {
			continue
		}
		row.AuthorIP = redacted
		s.rows[id] = row
		n++
	}
	return n, nil
}

// DuplicateContent satisfies the DupChecker contract using the
// in-memory row table. It walks every row tagged with the input ip
// and matches on a normalised content fingerprint. O(n) where n is
// the per-IP row count; the production Postgres store will swap in a
// (ip, content_fingerprint) index for the same lookup.
func (s *MemoryStore) DuplicateContent(_ context.Context, ip, fingerprint string, window time.Duration) (bool, error) {
	if ip == "" || fingerprint == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	since := s.now().Add(-window)
	for _, row := range s.rows {
		if row.AuthorIP != ip {
			continue
		}
		if row.CreatedAt.Before(since) {
			continue
		}
		if contentFingerprint(row.Content) == fingerprint {
			return true, nil
		}
	}
	return false, nil
}
