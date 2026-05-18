package migrate

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_NoArgs_PrintsUsageWithExit2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected usage on stderr, got %q", stderr.String())
	}
}

func TestRun_Help_PrintsToStdoutWithExit0(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{arg}, &stdout, &stderr)
			if code != ExitOK {
				t.Errorf("exit: got %d, want %d", code, ExitOK)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("expected usage on stdout, got %q", stdout.String())
			}
		})
	}
}

func TestRun_UnknownSubcommand_ExitsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"frobnicate"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand on stderr, got %q", stderr.String())
	}
}

func TestRun_Up_MissingDSN_ExitsUsage(t *testing.T) {
	// loadConfig refuses to run without DATABASE_URL. We unset it for
	// the duration of the test so the result is reproducible regardless
	// of the contributor's shell environment.
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"up"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL on stderr, got %q", stderr.String())
	}
}

func TestRun_Down_RejectsNegativeSteps(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"down", "-2"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}

func TestRun_Down_RejectsNonNumericSteps(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"down", "many"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}

func TestRun_Up_RejectsTrailingArgs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"up", "extra"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}

// TestRun_Up_AcceptsSeedFlag asserts the new --seed-default-theme
// flag parses cleanly. We can't actually run the migration (no DB),
// so we drive the misconfiguration path with an unset DATABASE_URL —
// the flag must still be accepted before loadConfig refuses.
func TestRun_Up_AcceptsSeedFlag(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, arg := range []string{
		"--seed-default-theme=false",
		"--seed-default-theme=true",
		"-seed-default-theme=false",
	} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"up", arg}, &stdout, &stderr)
			// loadConfig still rejects on missing DATABASE_URL — that's
			// fine, we're proving the flag parsed without surfacing as
			// "unknown flag". The wrong outcome would be ExitUsage
			// before loadConfig, with an "unknown flag" message.
			if !strings.Contains(stderr.String(), "DATABASE_URL") {
				t.Errorf("expected DATABASE_URL error, got stderr=%q", stderr.String())
			}
			if code != ExitUsage {
				t.Errorf("exit: got %d, want %d", code, ExitUsage)
			}
		})
	}
}

// TestRun_Up_RejectsUnknownFlag pairs with the test above: an
// unrecognised flag must produce ExitUsage and the help text. We
// don't assert the exact wording because flag.FlagSet's default
// output format is the standard library's concern, not ours.
func TestRun_Up_RejectsUnknownFlag(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"up", "--nope"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}

func TestRun_Status_RejectsTrailingArgs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "extra"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}
