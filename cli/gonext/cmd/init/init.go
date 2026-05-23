// Package initcmd. See doc.go.
package initcmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Exit codes shared with main.go and tests.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext init — first-run bootstrap

Usage:
  gonext init [flags]

Flags:
  --admin-email STRING        Initial super_admin email (required).
  --admin-password STRING     Initial super_admin password.
                              Mutually exclusive with --admin-password-stdin.
  --admin-password-stdin      Read password from stdin (newline-terminated).
  --site-name STRING          Human-facing site name (optional).
  --site-url STRING           Canonical site URL (optional, no trailing slash).
  --skip-migrations           Don't run 'migrate up' (DB already migrated).
  --skip-theme-seed           Don't install the bundled default theme.
  --non-interactive           Don't prompt for missing fields; fail fast.

Environment:
  DATABASE_URL             Required. Postgres DSN.
  GONEXT_AUTH_PEPPER       Required. HMAC key for password hashing.
  GONEXT_MIGRATION_DIR     Migration directory. Default: ./migrations.
  GONEXT_THEME_DIR         Runtime theme directory. Default: ./themes.

Behavior:
  - Re-running on an already-initialized install is a no-op (idempotent).
  - Passwords must be at least 12 characters; emails must parse as a
    valid RFC 5322 address.

Exit codes:
  0  success (including idempotent re-run)
  1  setup failure (database, migration, seed, admin, write)
  2  usage error (bad flags, validation, missing field non-interactive)`

// flags captures every parseable input. We split this from
// SetupOptions so init.go owns the user-facing surface (with prompts
// and validation) and setup.go can be driven by tests without
// touching the flag package.
type flags struct {
	adminEmail         string
	adminPassword      string
	adminPasswordStdin bool
	siteName           string
	siteURL            string
	skipMigrations     bool
	skipThemeSeed      bool
	nonInteractive     bool
}

// Run dispatches `gonext init ...`. args is the slice after the
// literal `init` token. stdin/stdout/stderr are injected so tests can
// capture output without fighting with os.Stdin/Stdout.
//
// stdinFD is the file descriptor of stdin (used for password
// echo-suppression). Tests pass -1 to bypass the TTY check.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, stdinFD int) int {
	if len(args) >= 1 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Fprintln(stdout, usage)
			return ExitOK
		}
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f flags
	fs.StringVar(&f.adminEmail, "admin-email", "", "initial super_admin email")
	fs.StringVar(&f.adminPassword, "admin-password", "", "initial super_admin password")
	fs.BoolVar(&f.adminPasswordStdin, "admin-password-stdin", false, "read password from stdin")
	fs.StringVar(&f.siteName, "site-name", "", "human-facing site name")
	fs.StringVar(&f.siteURL, "site-url", "", "canonical site URL")
	fs.BoolVar(&f.skipMigrations, "skip-migrations", false, "don't run migrate up")
	fs.BoolVar(&f.skipThemeSeed, "skip-theme-seed", false, "don't install the bundled default theme")
	fs.BoolVar(&f.nonInteractive, "non-interactive", false, "don't prompt for missing fields")
	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already wrote the parse error; include
		// the usage block so the operator sees the surrounding command.
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gonext init: unexpected argument %q\n\n%s\n", fs.Arg(0), usage)
		return ExitUsage
	}

	if f.adminPassword != "" && f.adminPasswordStdin {
		fmt.Fprintln(stderr, "gonext init: --admin-password and --admin-password-stdin are mutually exclusive")
		return ExitUsage
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "gonext init: DATABASE_URL is required")
		return ExitUsage
	}
	pepper := readPepperFromEnv()
	if len(pepper) == 0 {
		fmt.Fprintln(stderr, "gonext init: GONEXT_AUTH_PEPPER is required")
		return ExitUsage
	}

	// Build a prompter. In non-interactive mode we still construct
	// one (so password-stdin reads still work) but every missing
	// field becomes a usage error rather than a prompt.
	prompt := newOSPrompter(stdin, stdout, stdinFD)
	if f.adminPasswordStdin {
		// stdin password: read a single newline-terminated line. We
		// bypass the prompter because there's no label to print and
		// no echo-control to do — the caller is piping bytes.
		pw, err := readStdinPassword(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "gonext init: read password from stdin: %v\n", err)
			return ExitUsage
		}
		f.adminPassword = pw
	}

	if err := gatherInputs(&f, prompt, stderr); err != nil {
		fmt.Fprintf(stderr, "gonext init: %v\n", err)
		return ExitUsage
	}

	// Validate the assembled inputs once, before we open a pool.
	// validation errors here are usage problems, not runtime
	// failures.
	normEmail, err := validateEmail(f.adminEmail)
	if err != nil {
		fmt.Fprintf(stderr, "gonext init: %v\n", err)
		return ExitUsage
	}
	f.adminEmail = normEmail
	if err := validatePassword(f.adminPassword); err != nil {
		fmt.Fprintf(stderr, "gonext init: %v\n", err)
		return ExitUsage
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := SetupOptions{
		DSN:            dsn,
		MigrationDir:   os.Getenv("GONEXT_MIGRATION_DIR"),
		ThemeDir:       os.Getenv("GONEXT_THEME_DIR"),
		Pepper:         pepper,
		AdminEmail:     f.adminEmail,
		AdminPassword:  f.adminPassword,
		SiteName:       strings.TrimSpace(f.siteName),
		SiteURL:        strings.TrimRight(strings.TrimSpace(f.siteURL), "/"),
		SkipMigrations: f.skipMigrations,
		SkipThemeSeed:  f.skipThemeSeed,
		Logger:         logger,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	already, err := setupFn(ctx, opts)
	if err != nil {
		// Surface the failing step so the operator can read the
		// error without parsing the wrapped chain.
		step := failedStep(err)
		if step != "" {
			fmt.Fprintf(stderr, "gonext init: failed at %s: %v\n", step, errors.Unwrap(err))
		} else {
			fmt.Fprintf(stderr, "gonext init: %v\n", err)
		}
		// Validation-shaped errors (password too short, admin exists)
		// are usage errors even when they surface inside a step.
		if errors.Is(err, ErrPasswordTooShort) || errors.Is(err, ErrInvalidEmail) {
			return ExitUsage
		}
		return ExitFail
	}
	if already {
		fmt.Fprintln(stdout, "gonext init: already complete (idempotent re-run)")
		return ExitOK
	}
	fmt.Fprintln(stdout, "gonext init: complete — sign in with the admin credentials you just set")
	return ExitOK
}

// setupFn is the indirection seam that lets tests substitute a fake
// orchestrator for the heavy DB-driven Setup. Production code points
// it at Setup (defined in setup.go); tests can replace it from
// TestMain or per-test t.Cleanup.
var setupFn = Setup

// gatherInputs fills in missing flag values from prompts. In
// non-interactive mode, missing required fields are errors. Optional
// fields (site name/URL) are left empty when not supplied.
//
// The order matches the natural flow: email first, then password
// (and a confirmation), then the optional site fields. We don't
// confirm the password when it came from --admin-password-stdin —
// the caller piped exactly the bytes they wanted.
func gatherInputs(f *flags, prompt prompter, stderr io.Writer) error {
	if f.adminEmail == "" {
		if f.nonInteractive {
			return errors.New("--admin-email is required in non-interactive mode")
		}
		v, err := prompt.readLine("Admin email: ")
		if err != nil {
			return fmt.Errorf("read admin email: %w", err)
		}
		f.adminEmail = strings.TrimSpace(v)
	}

	if f.adminPassword == "" {
		if f.nonInteractive {
			return errors.New("--admin-password or --admin-password-stdin is required in non-interactive mode")
		}
		pw, err := prompt.readPassword("Admin password: ")
		if err != nil {
			return fmt.Errorf("read admin password: %w", err)
		}
		confirm, err := prompt.readPassword("Confirm password: ")
		if err != nil {
			return fmt.Errorf("read password confirmation: %w", err)
		}
		if pw != confirm {
			fmt.Fprintln(stderr, "gonext init: passwords do not match")
			return errors.New("passwords do not match")
		}
		f.adminPassword = pw
	}

	if f.siteName == "" && !f.nonInteractive {
		v, err := prompt.readLine("Site name (optional, press Enter to skip): ")
		if err != nil {
			return fmt.Errorf("read site name: %w", err)
		}
		f.siteName = strings.TrimSpace(v)
	}
	if f.siteURL == "" && !f.nonInteractive {
		v, err := prompt.readLine("Site URL (optional, press Enter to skip): ")
		if err != nil {
			return fmt.Errorf("read site url: %w", err)
		}
		f.siteURL = strings.TrimSpace(v)
	}

	return nil
}

// readStdinPassword reads a single newline-terminated line from the
// supplied reader. Used for the --admin-password-stdin flow where
// the caller pipes credentials in from a secrets store; we
// deliberately don't echo-suppress here because the caller is in
// charge of redirecting the stream.
func readStdinPassword(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// RunOS is the convenience entry point main.go calls. It wires the
// OS streams + stdin file descriptor for password hiding.
func RunOS(args []string) int {
	return Run(args, os.Stdin, os.Stdout, os.Stderr, int(os.Stdin.Fd()))
}
