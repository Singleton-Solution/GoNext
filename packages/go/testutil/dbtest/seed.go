package dbtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// seedTimeout caps the time we'll let a fixture SQL block run. Most
// fixtures are a handful of INSERTs; anything beyond 30s is almost
// certainly a stuck connection rather than a real test fixture.
const seedTimeout = 30 * time.Second

// Seed applies a fixture SQL string inside tx. The SQL can contain
// any number of statements separated by semicolons — Seed feeds the
// whole blob to a single Exec, which pgx happily parses as a
// multi-statement simple query.
//
// Anything Seed writes lives inside the test's transaction, so it
// vanishes at cleanup just like the rest of the test's work. There
// is no separate "teardown fixture" call to remember.
//
// Empty / whitespace-only input is a no-op. This lets test helpers
// pass a constant string that may be empty for some cases without
// guarding at every call site.
//
// Failure modes:
//   - tx is nil: t.Fatal. The caller forgot BeginIsolated.
//   - Exec returns an error: t.Fatalf with the failed SQL trimmed
//     for the error message. The full SQL is still in the test
//     source — we just include the first line so the assertion
//     site is easy to find.
func Seed(t testing.TB, tx pgx.Tx, fixtures string) {
	t.Helper()
	if tx == nil {
		t.Fatal("dbtest.Seed: tx is nil (did you forget BeginIsolated?)")
	}

	trimmed := strings.TrimSpace(fixtures)
	if trimmed == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), seedTimeout)
	defer cancel()

	if _, err := tx.Exec(ctx, trimmed); err != nil {
		// Truncate the SQL preview so a 200-line fixture doesn't drown
		// out the actual error in the failure log. The full fixture is
		// in the test source; the preview just helps locate it.
		preview := firstLine(trimmed)
		t.Fatalf("dbtest.Seed: exec %q: %v", preview, err)
	}
}

// firstLine returns the first non-empty line of s, trimmed and
// capped at 80 chars. Used for compact error messages — we'd rather
// keep the test failure log short and obvious than dump a multi-line
// fixture into it.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const cap = 80
		if len(line) > cap {
			return line[:cap] + "..."
		}
		return line
	}
	return ""
}
