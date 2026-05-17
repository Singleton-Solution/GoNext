// Package theme. See doc.go.
package theme

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/Singleton-Solution/GoNext/cli/gonext/internal/themetest"
)

// Usage is printed for `gonext theme` with no args or `--help`.
const Usage = `gonext theme — manage and validate themes

Usage:
  gonext theme <subcommand> [args]

Subcommands:
  test <dir>   Run the theme contract suite against a theme on disk

Run "gonext theme <subcommand> --help" for subcommand details.`

const testUsage = `gonext theme test — run the contract suite against a theme

Usage:
  gonext theme test [flags] <dir>

Flags:
  --json       Emit a machine-readable JSON report on stdout instead of
               the human-readable summary.
  --verbose    Include advisory (NOTE) rows in the text output.
  -h, --help   Show this help.

Exit code:
  0   every check is PASS, SKIP, or NOTE
  1   at least one check FAILed
  2   usage error (missing arg, unreadable path, etc.)

Notes:
  Several §6.1 checks (a11y, bundle size, SSR parity, full template
  resolver exercise) depend on the theme runtime which is not yet built;
  those rows appear with status SKIP. See docs/11-testing-ci.md §6 and
  the package doc in internal/themetest for the full list.`

// Run dispatches `gonext theme ...` subcommands. args is the slice
// after `theme` itself, i.e. for `gonext theme test ./x` callers pass
// []string{"test", "./x"}.
//
// Return value is the desired process exit code. stdout and stderr are
// injected so tests can capture output.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	switch args[0] {
	case "test":
		return runTest(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, Usage)
		return 0
	default:
		fmt.Fprintf(stderr, "gonext theme: unknown subcommand %q\n\n%s\n", args[0], Usage)
		return 2
	}
}

// runTest handles `gonext theme test ...`.
func runTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext theme test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit a machine-readable JSON report on stdout")
	verbose := fs.Bool("verbose", false, "include advisory (NOTE) rows in text output")
	fs.Usage = func() { fmt.Fprintln(stderr, testUsage) }

	if err := fs.Parse(args); err != nil {
		// flag prints its own message on ErrHelp.
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, testUsage)
		return 2
	}
	dir := fs.Arg(0)

	report, err := themetest.Run(dir)
	if err != nil {
		fmt.Fprintf(stderr, "gonext theme test: %v\n", err)
		return 2
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "gonext theme test: encode JSON: %v\n", err)
			return 2
		}
	} else {
		if err := report.WriteText(stdout, *verbose); err != nil {
			fmt.Fprintf(stderr, "gonext theme test: write report: %v\n", err)
			return 2
		}
	}

	if !report.Passed() {
		return 1
	}
	return 0
}
