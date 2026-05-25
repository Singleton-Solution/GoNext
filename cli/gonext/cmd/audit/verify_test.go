package audit

import (
	"bytes"
	"strings"
	"testing"
)

// TestRun_NoArgs prints usage and exits with ExitUsage.
func TestRun_NoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "gonext audit") {
		t.Errorf("usage not printed; got %q", stderr.String())
	}
}

// TestRun_UnknownSubcommand exits with ExitUsage and prints the
// "unknown subcommand" line.
func TestRun_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"frobnicate"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("error line missing; got %q", stderr.String())
	}
}

// TestRun_Help exits OK and prints usage to stdout.
func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"help"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "gonext audit") {
		t.Errorf("usage not on stdout: %q", stdout.String())
	}
}

// TestVerify_MissingKey reports an error when the HMAC key env var
// is unset.
func TestVerify_MissingKey(t *testing.T) {
	t.Setenv("GONEXT_AUDIT_HMAC_KEY", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"verify"}, &stdout, &stderr)
	if code != ExitFail {
		t.Errorf("exit code = %d, want %d", code, ExitFail)
	}
	if !strings.Contains(stderr.String(), "GONEXT_AUDIT_HMAC_KEY") &&
		!strings.Contains(stderr.String(), "HMAC key") &&
		!strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("missing expected error message; got %q", stderr.String())
	}
}
