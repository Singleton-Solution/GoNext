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

func TestRun_Status_RejectsTrailingArgs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "extra"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
}
