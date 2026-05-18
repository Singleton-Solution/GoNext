package migrate

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/migrate/importer"
)

// wpUsage is printed for `gonext migrate wp --help` and on any
// argument error. Mirrors the format of the existing `migrate up`
// help text so the CLI stays internally consistent.
const wpUsage = `gonext migrate wp — import a WordPress WXR export

Usage:
  gonext migrate wp --file <path.xml> [flags]

Flags:
  --file string         Path to a WXR (.xml) export. Required.
  --dry-run             Walk the WXR but do not write any rows.
  --on-conflict string  Conflict policy: skip | update | fail. Default "skip".
  --batch-size int      Number of post rows per transaction. Default 100.
  --skip-comments       Do not import comment threads.

Environment:
  DATABASE_URL              Required (unless --dry-run). Postgres DSN.

Exit codes:
  0   success (zero per-record errors)
  1   import failed, or per-record errors present (see report)
  2   usage error`

// runWP wires `gonext migrate wp` to the importer package.
// Returns the desired process exit code.
//
// The function deliberately keeps the surface narrow: it only
// reads the WXR from a file (no stdin), prints the Report as a
// human-readable summary on success, and surfaces per-record
// errors on stderr. Operators wanting structured JSON can pipe
// the report through a separate consumer once that exists.
func runWP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate wp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		fileFlag         string
		dryFlag          bool
		conflictFlag     string
		batchSizeFlag    int
		skipCommentsFlag bool
	)
	fs.StringVar(&fileFlag, "file", "", "Path to WXR XML export (required)")
	fs.BoolVar(&dryFlag, "dry-run", false, "Walk the WXR but write no rows")
	fs.StringVar(&conflictFlag, "on-conflict", "skip", "Conflict policy: skip | update | fail")
	fs.IntVar(&batchSizeFlag, "batch-size", 100, "Posts per transaction")
	fs.BoolVar(&skipCommentsFlag, "skip-comments", false, "Skip comment import")
	fs.Usage = func() { fmt.Fprintln(stderr, wpUsage) }

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed the cause; we just
		// surface the usage text and return.
		return ExitUsage
	}

	if fileFlag == "" {
		fmt.Fprintln(stderr, wpUsage)
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "gonext migrate wp: --file is required")
		return ExitUsage
	}

	policy, err := importer.ParseConflictPolicy(conflictFlag)
	if err != nil {
		fmt.Fprintf(stderr, "gonext migrate wp: %v\n", err)
		return ExitUsage
	}

	opts := importer.Options{
		Dryrun:       dryFlag,
		OnConflict:   policy,
		BatchSize:    batchSizeFlag,
		SkipComments: skipCommentsFlag,
	}

	f, err := os.Open(fileFlag) //nolint:gosec // operator-supplied path on a CLI; intent is to read it
	if err != nil {
		fmt.Fprintf(stderr, "gonext migrate wp: open %q: %v\n", fileFlag, err)
		return ExitFail
	}
	defer func() { _ = f.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Dry-run skips the DB entirely. We still pass a nil pool so
	// the importer's nil-pool short-circuit triggers.
	if dryFlag {
		imp := importer.New(nil, opts)
		report, runErr := imp.Run(ctx, f)
		printReport(stdout, report, true)
		if runErr != nil {
			fmt.Fprintf(stderr, "gonext migrate wp: %v\n", runErr)
			return ExitFail
		}
		if report.HasErrors() {
			printRecordErrors(stderr, report)
			return ExitFail
		}
		return ExitOK
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "gonext migrate wp: DATABASE_URL is required (or pass --dry-run)")
		return ExitUsage
	}
	pool, err := db.New(ctx, config.DatabaseConfig{URL: dsn, MaxOpenConns: 4}, logger)
	if err != nil {
		fmt.Fprintf(stderr, "gonext migrate wp: connect: %v\n", err)
		return ExitFail
	}
	defer pool.Close()

	imp := importer.New(pool, opts)
	report, runErr := imp.Run(ctx, f)
	printReport(stdout, report, false)
	if runErr != nil {
		fmt.Fprintf(stderr, "gonext migrate wp: %v\n", runErr)
		return ExitFail
	}
	if report.HasErrors() {
		printRecordErrors(stderr, report)
		return ExitFail
	}
	return ExitOK
}

// printReport renders the report counters in a single block.
// Kept here (rather than on the importer.Report) because the
// formatting is CLI-specific; the importer package is consumed
// by other surfaces (admin REST, future workers) that will want
// JSON instead.
func printReport(w io.Writer, r *importer.Report, dryrun bool) {
	if r == nil {
		return
	}
	prefix := ""
	if dryrun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(w, "%simport summary\n", prefix)
	fmt.Fprintf(w, "  authors:     %d\n", r.Authors)
	fmt.Fprintf(w, "  categories:  %d\n", r.Categories)
	fmt.Fprintf(w, "  tags:        %d\n", r.Tags)
	fmt.Fprintf(w, "  posts:       %d\n", r.Posts)
	fmt.Fprintf(w, "  attachments: %d\n", r.Attachments)
	fmt.Fprintf(w, "  comments:    %d\n", r.Comments)
	fmt.Fprintf(w, "  errors:      %d\n", len(r.Errors))
	fmt.Fprintf(w, "  took:        %s\n", r.Took)
}

// printRecordErrors lists per-record failures on stderr so an
// operator can see what went wrong without parsing JSON. Capped
// at 25 lines to avoid drowning the terminal on a very broken
// import; the report itself carries the full list for tooling.
func printRecordErrors(w io.Writer, r *importer.Report) {
	const maxLines = 25
	fmt.Fprintf(w, "per-record errors (%d total)\n", len(r.Errors))
	limit := len(r.Errors)
	if limit > maxLines {
		limit = maxLines
	}
	for i := 0; i < limit; i++ {
		e := r.Errors[i]
		fmt.Fprintf(w, "  - [%s] wp:%s slug=%q: %s\n", e.Stage, e.WPID, e.Slug, e.Reason)
	}
	if len(r.Errors) > maxLines {
		fmt.Fprintf(w, "  ... %d more\n", len(r.Errors)-maxLines)
	}
}
