package migrate

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunVerify_MissingFile rejects the call before any DB access
// when --file is omitted. Mirrors the wp command's usage check.
func TestRunVerify_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"verify"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--file is required") {
		t.Errorf("expected --file is required, got %q", stderr.String())
	}
}

// TestRunVerify_OutOfRangeFidelity rejects fractional values
// outside [0, 1].
func TestRunVerify_OutOfRangeFidelity(t *testing.T) {
	tmp := writeTempWXR(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"verify", "--file", tmp, "--min-fidelity", "1.5"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "min-fidelity") {
		t.Errorf("expected min-fidelity error, got %q", stderr.String())
	}
}

// TestRunVerify_NoDSN rejects the call when DATABASE_URL is unset.
// The verifier requires a live DB; there's no --dry-run.
func TestRunVerify_NoDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	tmp := writeTempWXR(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"verify", "--file", tmp}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL error, got %q", stderr.String())
	}
}

// TestRunVerify_HelpListed confirms 'verify' is in the top-level
// migrate help text.
func TestRunVerify_HelpListed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"help"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("exit: got %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "verify") {
		t.Errorf("expected verify subcommand in help, got %q", stdout.String())
	}
}
