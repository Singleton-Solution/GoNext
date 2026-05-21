package initcmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// withFakeSetup swaps the package-level setupFn for a controllable
// double. Returns a restore func the test must defer.
//
// The double records the last-seen SetupOptions so tests can assert
// on what the CLI built from flags + env + prompts without spinning
// up a Postgres container for every case.
type recordedCall struct {
	opts    SetupOptions
	already bool
	err     error
}

func withFakeSetup(t *testing.T, ret recordedCall) *recordedCall {
	t.Helper()
	prev := setupFn
	seen := &recordedCall{}
	setupFn = func(_ context.Context, opts SetupOptions) (bool, error) {
		seen.opts = opts
		return ret.already, ret.err
	}
	t.Cleanup(func() { setupFn = prev })
	return seen
}

func TestRun_Help_PrintsUsage(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{arg}, strings.NewReader(""), &stdout, &stderr, -1)
			if code != ExitOK {
				t.Errorf("exit=%d want %d", code, ExitOK)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Errorf("expected usage on stdout, got %q", stdout.String())
			}
		})
	}
}

func TestRun_MissingDATABASE_URL_ExitsUsage(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL on stderr, got %q", stderr.String())
	}
}

func TestRun_MissingPepper_ExitsUsage(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "GONEXT_AUTH_PEPPER") {
		t.Errorf("expected pepper on stderr, got %q", stderr.String())
	}
}

func TestRun_MutuallyExclusivePasswordFlags(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--admin-password-stdin",
		"--non-interactive",
	}, strings.NewReader("anything\n"), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive msg, got %q", stderr.String())
	}
}

func TestRun_NonInteractive_MissingEmail(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "admin-email") {
		t.Errorf("expected admin-email msg, got %q", stderr.String())
	}
}

func TestRun_PasswordTooShort_ExitsUsage(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "tooshort",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "12 characters") {
		t.Errorf("expected 12-char rejection, got %q", stderr.String())
	}
}

func TestRun_InvalidEmail_ExitsUsage(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "not-an-email",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
}

func TestRun_NonInteractive_HappyPath_CallsSetup(t *testing.T) {
	seen := withFakeSetup(t, recordedCall{already: false, err: nil})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	t.Setenv("GONEXT_MIGRATION_DIR", "/some/migrations")
	t.Setenv("GONEXT_THEME_DIR", "/some/themes")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", " ALICE@example.com  ",
		"--admin-password", "verylongpassword",
		"--site-name", "  Test Site  ",
		"--site-url", "https://example.com/",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitOK {
		t.Errorf("exit=%d want %d, stderr=%q", code, ExitOK, stderr.String())
	}
	if seen.opts.AdminEmail != "ALICE@example.com" {
		t.Errorf("AdminEmail=%q, want %q", seen.opts.AdminEmail, "ALICE@example.com")
	}
	if seen.opts.SiteName != "Test Site" {
		t.Errorf("SiteName=%q", seen.opts.SiteName)
	}
	if seen.opts.SiteURL != "https://example.com" {
		t.Errorf("SiteURL=%q (trailing slash should be stripped)", seen.opts.SiteURL)
	}
	if seen.opts.DSN != "postgres://example/db" {
		t.Errorf("DSN=%q", seen.opts.DSN)
	}
	if string(seen.opts.Pepper) != "pepperpepperpepper" {
		t.Errorf("Pepper=%q", string(seen.opts.Pepper))
	}
	if seen.opts.MigrationDir != "/some/migrations" {
		t.Errorf("MigrationDir=%q", seen.opts.MigrationDir)
	}
	if seen.opts.ThemeDir != "/some/themes" {
		t.Errorf("ThemeDir=%q", seen.opts.ThemeDir)
	}
	if !strings.Contains(stdout.String(), "complete") {
		t.Errorf("expected completion message, got %q", stdout.String())
	}
}

func TestRun_AlreadyComplete_ReportsIdempotent(t *testing.T) {
	withFakeSetup(t, recordedCall{already: true, err: nil})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitOK {
		t.Errorf("exit=%d want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "already complete") {
		t.Errorf("expected idempotent message, got %q", stdout.String())
	}
}

func TestRun_SetupFails_SurfacesFailingStep(t *testing.T) {
	withFakeSetup(t, recordedCall{
		err: &stepFailure{step: "migrate", err: errors.New("boom")},
	})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitFail {
		t.Errorf("exit=%d want %d", code, ExitFail)
	}
	if !strings.Contains(stderr.String(), "migrate") {
		t.Errorf("expected step in stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("expected underlying error in stderr, got %q", stderr.String())
	}
}

func TestRun_AdminExists_FromSetup_ExitsFail(t *testing.T) {
	withFakeSetup(t, recordedCall{
		err: &stepFailure{step: "admin", err: ErrAdminExists},
	})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password", "verylongpassword",
		"--non-interactive",
	}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitFail {
		t.Errorf("exit=%d want %d", code, ExitFail)
	}
	if !strings.Contains(stderr.String(), "admin already exists") {
		t.Errorf("expected admin-exists message, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "admin-password-reset") {
		t.Errorf("expected reset hint, got %q", stderr.String())
	}
}

func TestRun_PasswordStdin(t *testing.T) {
	seen := withFakeSetup(t, recordedCall{})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"--admin-email", "a@example.com",
		"--admin-password-stdin",
		"--non-interactive",
	}, strings.NewReader("piped-very-long-password\n"), &stdout, &stderr, -1)
	if code != ExitOK {
		t.Errorf("exit=%d want %d, stderr=%q", code, ExitOK, stderr.String())
	}
	if seen.opts.AdminPassword != "piped-very-long-password" {
		t.Errorf("AdminPassword=%q", seen.opts.AdminPassword)
	}
}

func TestRun_Interactive_PromptsForMissingFields(t *testing.T) {
	seen := withFakeSetup(t, recordedCall{})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	// Order: email, password, confirm, site name, site URL.
	stdin := strings.NewReader(strings.Join([]string{
		"alice@example.com",
		"interactive-pass-12",
		"interactive-pass-12",
		"My Site",
		"https://my.example",
		"",
	}, "\n") + "\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{}, stdin, &stdout, &stderr, -1)
	if code != ExitOK {
		t.Errorf("exit=%d want %d, stderr=%q", code, ExitOK, stderr.String())
	}
	if seen.opts.AdminEmail != "alice@example.com" {
		t.Errorf("AdminEmail=%q", seen.opts.AdminEmail)
	}
	if seen.opts.AdminPassword != "interactive-pass-12" {
		t.Errorf("AdminPassword=%q", seen.opts.AdminPassword)
	}
	if seen.opts.SiteName != "My Site" {
		t.Errorf("SiteName=%q", seen.opts.SiteName)
	}
	if seen.opts.SiteURL != "https://my.example" {
		t.Errorf("SiteURL=%q", seen.opts.SiteURL)
	}
}

func TestRun_Interactive_PasswordMismatch(t *testing.T) {
	withFakeSetup(t, recordedCall{})

	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")

	stdin := strings.NewReader(strings.Join([]string{
		"alice@example.com",
		"interactive-pass-12",
		"different-pass-12345",
	}, "\n") + "\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{}, stdin, &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "do not match") {
		t.Errorf("expected mismatch msg, got %q", stderr.String())
	}
}

func TestRun_UnknownArgument(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"unexpected"}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
}

func TestRun_UnknownFlag(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x@x/x")
	t.Setenv("GONEXT_AUTH_PEPPER", "pepperpepperpepper")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--bogus-flag"}, strings.NewReader(""), &stdout, &stderr, -1)
	if code != ExitUsage {
		t.Errorf("exit=%d want %d", code, ExitUsage)
	}
}

func TestStepFailure_UnwrapAndString(t *testing.T) {
	inner := errors.New("inner")
	sf := &stepFailure{step: "phase", err: inner}
	if !errors.Is(sf, inner) {
		t.Errorf("errors.Is should unwrap")
	}
	if sf.Error() != "phase: inner" {
		t.Errorf("Error=%q", sf.Error())
	}
	if failedStep(sf) != "phase" {
		t.Errorf("failedStep=%q", failedStep(sf))
	}
	if failedStep(errors.New("not a stepFailure")) != "" {
		t.Errorf("failedStep for plain error should be empty")
	}
}
