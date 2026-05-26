// Package audit provides the `gonext audit` CLI subcommand surface.
// At present it exposes a single `verify` subcommand that walks the
// audit_log HMAC chain and reports the first tampered row (if any).
//
// Wiring: cli/gonext/main.go dispatches `audit <subcommand>` here via
// RunOS. The package is intentionally thin — heavy lifting lives in
// packages/go/audit.VerifyChain.
package audit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgaudit "github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// Exit codes shared with the rest of the CLI.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext audit — inspect the audit log

Usage:
  gonext audit verify [--from=ID] [--to=ID]

Subcommands:
  verify   Walk the HMAC chain and report any tampered rows.

Environment:
  DATABASE_URL              Required. Postgres DSN.
  GONEXT_AUDIT_HMAC_KEY     Required. The HMAC chain key (raw bytes or hex).
                            See packages/go/audit/chain.go.

Exit codes:
  0  Chain verified intact.
  1  Chain broken (a tamper was detected) or another runtime error.
  2  Misuse: missing subcommand, malformed flags, etc.
`

// RunOS is the os.Args / os.Stdout / os.Stderr entry point dispatched
// from cli/gonext/main.go. Returns the process exit code.
func RunOS(args []string) int {
	return Run(args, os.Stdout, os.Stderr)
}

// Run is the testable entry point: callers pass their own writers and
// args so the package can be exercised without poking at os.Args.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "gonext audit: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "Inclusive lower-bound row ID. Default: first row.")
	to := fs.String("to", "", "Inclusive upper-bound row ID. Default: last row.")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "audit verify: load config: %v\n", err)
		return ExitFail
	}
	if cfg.Database.URL == "" {
		fmt.Fprintln(stderr, "audit verify: DATABASE_URL is unset")
		return ExitFail
	}
	key, err := pkgaudit.HMACKeyFromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "audit verify: %v\n", err)
		return ExitFail
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		fmt.Fprintf(stderr, "audit verify: connect: %v\n", err)
		return ExitFail
	}
	defer pool.Close()

	store := pkgaudit.NewPostgresStore(pool)
	if err := pkgaudit.VerifyChain(ctx, store, key, *from, *to); err != nil {
		if errors.Is(err, pkgaudit.ErrChainBroken) {
			fmt.Fprintf(stderr, "audit verify: %v\n", err)
			return ExitFail
		}
		fmt.Fprintf(stderr, "audit verify: %v\n", err)
		return ExitFail
	}
	fmt.Fprintln(stdout, "audit verify: chain intact")
	return ExitOK
}
