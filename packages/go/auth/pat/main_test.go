package pat

import (
	"os"
	"testing"
)

// TestMain swaps the argon2 parameters down to a cheap test-only set
// for the duration of the test run. The production cost knobs are
// documented in docs/06-auth-permissions.md §2.2 and are validated by
// password package tests; here we exercise the wiring with a cheaper
// variant so a parallel test suite completes inside the CI budget
// without bricking the host laptop.
func TestMain(m *testing.M) {
	restore := useTestParams()
	defer restore()
	os.Exit(m.Run())
}
