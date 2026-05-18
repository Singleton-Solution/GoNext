package config

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

func TestRun_DumpHelp_PrintsToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"dump", "--help"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("exit: got %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "redacted") && !strings.Contains(stdout.String(), "masked") {
		t.Errorf("expected redacted/masked mention in help: %q", stdout.String())
	}
}

func TestRun_Dump_MissingRequired_FailsButStillReports(t *testing.T) {
	// With no env set, Load returns an aggregated error but a non-nil cfg.
	// The dump command should: print the load error to stderr, exit 1.
	// We unset everything that Load reads via t.Setenv("", "") — Go's
	// t.Setenv with empty value behaves like unset for our env source.
	for _, k := range []string{
		"DATABASE_URL",
		"GONEXT_AUTH_PEPPER",
		"GONEXT_AUTH_SESSION_SECRET",
		"GONEXT_AUTH_CSRF_SECRET",
	} {
		t.Setenv(k, "")
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"dump"}, &stdout, &stderr)
	if code != ExitFail {
		t.Errorf("exit: got %d, want %d (with errors)", code, ExitFail)
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL in stderr, got %q", stderr.String())
	}
	// Even on partial load, the dump should still print something useful.
	if stdout.Len() == 0 {
		t.Errorf("expected partial dump on stdout even with errors")
	}
}

func TestRun_Dump_RedactsSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:hunter2@h:5432/d")
	t.Setenv("GONEXT_AUTH_PEPPER", strings.Repeat("a", 32))
	t.Setenv("GONEXT_AUTH_SESSION_SECRET", strings.Repeat("b", 32))
	t.Setenv("GONEXT_AUTH_CSRF_SECRET", strings.Repeat("c", 32))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dump"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("exit: got %d, want 0; stderr: %s", code, stderr.String())
	}
	// Plaintext secrets must NOT appear.
	for _, plaintext := range []string{"hunter2", strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)} {
		if strings.Contains(stdout.String(), plaintext) {
			t.Errorf("plaintext %q leaked into dump:\n%s", plaintext, stdout.String())
		}
	}
	// And the redacted-mask sentinel must appear.
	if !strings.Contains(stdout.String(), "***REDACTED***") {
		t.Errorf("expected ***REDACTED*** in dump output:\n%s", stdout.String())
	}
}

func TestRun_Dump_RejectsExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"dump", "garbage"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Errorf("expected 'unexpected argument' on stderr, got %q", stderr.String())
	}
}
