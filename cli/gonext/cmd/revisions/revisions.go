// Package revisions. See doc.go.
package revisions

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgrev "github.com/Singleton-Solution/GoNext/packages/go/revisions"
)

// Exit codes shared with main.go and tests. Same conventions as the
// migrate subcommand: 0 success, 1 runtime failure, 2 usage.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext revisions — manage block-editor revision history

Usage:
  gonext revisions <subcommand> [args]

Subcommands:
  prune    Apply the retention policy to post_revisions.

Environment:
  DATABASE_URL              Required. Postgres DSN.

Exit codes:
  0   success
  1   prune failure
  2   usage error`

const pruneUsage = `gonext revisions prune — apply retention to post_revisions

Usage:
  gonext revisions prune [flags]

Flags:
  --keep-last N       Keep the latest N revisions per post (default 30).
                      Pass 0 to disable the count cap.
  --keep-within D     Keep every revision newer than duration D
                      (default 168h, i.e. 7 days). Pass 0 to disable
                      the age cap.
  --dry-run           Compute and print what WOULD be deleted without
                      issuing any DELETE statements.
  --batch N           Cap the number of posts handled per Run pass
                      (default 0 = all posts).

Environment:
  DATABASE_URL  Required. Postgres DSN.

A revision is deleted only if it falls outside BOTH the --keep-last
window and the --keep-within window. Revisions marked is_permanent
in the database are never deleted, regardless of policy.

Exit codes:
  0   success (prints stats line on stdout)
  1   prune failure
  2   usage error`

// Run dispatches `gonext revisions ...`. args is the slice after the
// literal `revisions` token. Returns the desired process exit code.
//
// stdout/stderr are injected so tests can capture output without
// poking at os.Stdout.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "prune":
		return runPrune(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext revisions: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS is a convenience that wires Run to os.Stdout / os.Stderr.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

// pruneRunner is the seam tests use to swap the real Postgres path
// for an in-memory fake. The zero value runs the production wiring.
type pruneRunner struct {
	// open swaps the pgxpool.New call.
	open func(ctx context.Context, dsn string) (poolish, error)
	// makePruner swaps the production Pruner wiring.
	makePruner func(pool poolish) prunerish
}

// poolish lets the test swap the pgxpool.Pool. The Close method is
// the only direct call from the CLI; everything else is hidden behind
// the pkgrev calls.
type poolish interface {
	Close()
}

// prunerish is the test-visible surface of *pkgrev.Pruner.
type prunerish interface {
	Run(ctx context.Context, policy pkgrev.Policy, opts pkgrev.PrunerOptions) (pkgrev.Stats, error)
}

// realPool wraps *pgxpool.Pool as a poolish. We carry the pool itself
// so makePruner can fish it back out for the production wire-up.
type realPool struct{ *pgxpool.Pool }

// runPrune parses flags, opens the pool, and runs the Pruner.
//
// The function is split out of Run so the test seam (pruneRunner)
// stays small. Production callers go through runPrune with a zero-
// value runner; tests override the runner fields.
func runPrune(args []string, stdout, stderr io.Writer) int {
	return runPruneWith(args, stdout, stderr, pruneRunner{})
}

// runPruneWith is the testable core of `gonext revisions prune`. The
// runner argument is the seam.
func runPruneWith(args []string, stdout, stderr io.Writer, runner pruneRunner) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keepLast := fs.Int("keep-last", 30, "keep the latest N revisions per post (0 disables)")
	keepWithin := fs.Duration("keep-within", 7*24*time.Hour, "keep every revision newer than this duration (0 disables)")
	dryRun := fs.Bool("dry-run", false, "compute deletions without issuing DELETE statements")
	batch := fs.Int("batch", 0, "cap posts handled per Run pass (0 = all)")
	if err := fs.Parse(args); err != nil {
		// flag.Parse already wrote the error and usage to stderr; we
		// just propagate the usage exit code.
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gonext revisions prune: unexpected argument %q\n\n%s\n", fs.Arg(0), pruneUsage)
		return ExitUsage
	}
	if *keepLast < 0 {
		fmt.Fprintln(stderr, "gonext revisions prune: --keep-last must be >= 0")
		return ExitUsage
	}
	if *keepWithin < 0 {
		fmt.Fprintln(stderr, "gonext revisions prune: --keep-within must be >= 0")
		return ExitUsage
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "gonext revisions prune: DATABASE_URL is required")
		return ExitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	open := runner.open
	if open == nil {
		open = func(ctx context.Context, dsn string) (poolish, error) {
			p, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return nil, err
			}
			return realPool{Pool: p}, nil
		}
	}
	pool, err := open(ctx, dsn)
	if err != nil {
		fmt.Fprintf(stderr, "gonext revisions prune: open pool: %v\n", err)
		return ExitFail
	}
	defer pool.Close()

	makePruner := runner.makePruner
	if makePruner == nil {
		makePruner = func(p poolish) prunerish {
			rp := p.(realPool).Pool
			store := pkgrev.NewPostgresStore(rp)
			lister := pkgrev.NewPostgresPostLister(rp)
			return pkgrev.NewPruner(store, lister)
		}
	}
	p := makePruner(pool)

	policy := pkgrev.Policy{
		KeepLast:   *keepLast,
		KeepWithin: *keepWithin,
	}
	stats, err := p.Run(ctx, policy, pkgrev.PrunerOptions{
		DryRun:    *dryRun,
		BatchSize: *batch,
	})
	if err != nil {
		// Print stats even on partial failure so the operator sees
		// what got done before the error.
		fmt.Fprintln(stderr, formatStats(stats))
		fmt.Fprintf(stderr, "gonext revisions prune: %v\n", err)
		return ExitFail
	}
	fmt.Fprintln(stdout, formatStats(stats))
	return ExitOK
}

// formatStats renders Stats as a single line for the CLI's stdout.
// The format is stable: ops scripts can grep it. Order matches the
// struct field order for easy mental cross-reference.
func formatStats(s pkgrev.Stats) string {
	var b strings.Builder
	if s.DryRun {
		b.WriteString("revisions prune (dry-run): ")
	} else {
		b.WriteString("revisions prune: ")
	}
	fmt.Fprintf(&b, "posts=%d scanned=%d deleted=%d skipped=%d duration=%s",
		s.PostsScanned, s.Scanned, s.Deleted, s.Skipped, s.Duration.Round(time.Millisecond))
	for _, n := range s.Notes {
		b.WriteString(" note=")
		b.WriteString(strings.ReplaceAll(n, " ", "_"))
	}
	return b.String()
}
