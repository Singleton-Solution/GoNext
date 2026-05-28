// Package audit — `verify` subcommand implementation. The shared
// entry point (Run, RunOS, exit codes, usage banner) lives in
// audit.go; this file contributes only the verify-specific
// subcommand handler that the dispatcher calls into.
//
// Verify walks the audit_log HMAC chain and reports the first
// tampered row (if any). Heavy lifting lives in
// packages/go/audit.VerifyChain.
package audit

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgaudit "github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// runVerify handles `gonext audit verify`. Returns the desired
// process exit code (one of the Exit* constants from audit.go).
//
// Flag set:
//   --from=ID  inclusive lower-bound row ID, default first row
//   --to=ID    inclusive upper-bound row ID, default last row
//
// Required env: DATABASE_URL, GONEXT_AUDIT_HMAC_KEY (see
// packages/go/audit/chain.go for the key format).
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
