package migrate

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/verify"
)

// verifyUsage is printed for `gonext migrate verify --help` and on
// any argument error. Mirrors the migrate wp usage style so the
// CLI surface is symmetric across import + verify.
const verifyUsage = `gonext migrate verify — check a WordPress import for fidelity

Usage:
  gonext migrate verify --file <path.xml> [flags]

After running 'gonext migrate wp --file <path.xml>', this command walks
the same WXR a second time and compares every record to the destination
DB. It prints a per-check report and exits non-zero if the fidelity
falls below the configured minimum.

Flags:
  --file string           Path to the WXR (.xml) export used at import. Required.
  --min-fidelity float    Lower bound on Passed/ChecksTotal. Default 0.95.

Environment:
  DATABASE_URL              Required. Postgres DSN of the migrated database.

Exit codes:
  0   verification passed (fidelity >= min)
  1   verification failed (fidelity < min, or a fatal verify error)
  2   usage error`

// runVerify wires `gonext migrate verify` to the verify package.
//
// The function keeps the surface narrow: it reads the WXR from a
// file (no stdin), prints a per-check summary on stdout, and the
// per-record failure list (capped at 25 lines) on stderr.
// Operators can pipe stdout for tooling — the format is line-
// oriented and stable.
func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		fileFlag string
		minFid   float64
	)
	fs.StringVar(&fileFlag, "file", "", "Path to WXR XML export (required)")
	fs.Float64Var(&minFid, "min-fidelity", verify.DefaultMinFidelity, "Minimum acceptable fidelity (0..1)")
	fs.Usage = func() { fmt.Fprintln(stderr, verifyUsage) }

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	if fileFlag == "" {
		fmt.Fprintln(stderr, verifyUsage)
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "gonext migrate verify: --file is required")
		return ExitUsage
	}
	if minFid < 0 || minFid > 1 {
		fmt.Fprintf(stderr, "gonext migrate verify: --min-fidelity must be in [0, 1], got %v\n", minFid)
		return ExitUsage
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "gonext migrate verify: DATABASE_URL is required")
		return ExitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pool, err := db.New(ctx, config.DatabaseConfig{URL: dsn, MaxOpenConns: 4}, logger)
	if err != nil {
		fmt.Fprintf(stderr, "gonext migrate verify: connect: %v\n", err)
		return ExitFail
	}
	defer pool.Close()

	v := &verify.Verifier{
		DB: pool,
		SourceReader: func() (io.Reader, error) {
			return os.Open(fileFlag) //nolint:gosec // operator-supplied path on a CLI; intent is to read it
		},
	}
	report, runErr := v.Run(ctx)
	printVerifyReport(stdout, report)
	if runErr != nil {
		fmt.Fprintf(stderr, "gonext migrate verify: %v\n", runErr)
		return ExitFail
	}
	if report.HasErrors() {
		printVerifyFailures(stderr, report)
	}

	ok, gateErr := verify.Gate{MinFidelity: minFid}.Decide(report)
	if !ok {
		if gateErr != nil {
			fmt.Fprintf(stderr, "gonext migrate verify: %v\n", gateErr)
		}
		return ExitFail
	}
	_ = errors.Is // referenced by callers; quiet unused-import linters
	return ExitOK
}

// printVerifyReport renders the report counters as a single block.
// Format intentionally line-oriented so a simple grep/awk pipeline
// can pull a field out for CI annotations.
func printVerifyReport(w io.Writer, r *verify.Report) {
	if r == nil {
		return
	}
	fmt.Fprintf(w, "verify summary\n")
	fmt.Fprintf(w, "  checks_total: %d\n", r.ChecksTotal)
	fmt.Fprintf(w, "  passed:       %d\n", r.Passed)
	fmt.Fprintf(w, "  failed:       %d\n", r.Failed)
	fmt.Fprintf(w, "  fidelity:     %.4f\n", r.Fidelity)
	fmt.Fprintf(w, "  took:         %s\n", r.Took)
}

// printVerifyFailures lists the per-record failure log on stderr.
// Capped at 25 entries to keep the terminal scrollback usable on a
// very broken import; the full list is still on the in-memory
// Report for tooling consumers.
func printVerifyFailures(w io.Writer, r *verify.Report) {
	const maxLines = 25
	fmt.Fprintf(w, "verify failures (%d total)\n", len(r.Failures))
	limit := len(r.Failures)
	if limit > maxLines {
		limit = maxLines
	}
	for i := 0; i < limit; i++ {
		f := r.Failures[i]
		fmt.Fprintf(w, "  - [%s/%s] source=%q target=%q: %s\n",
			f.CheckName, f.Severity, f.Source, f.Target, f.Reason)
	}
	if len(r.Failures) > maxLines {
		fmt.Fprintf(w, "  ... %d more\n", len(r.Failures)-maxLines)
	}
}
