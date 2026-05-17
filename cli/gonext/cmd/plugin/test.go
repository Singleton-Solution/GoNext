package plugin

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/Singleton-Solution/GoNext/cli/gonext/internal/plugintest"
)

// runTest implements `gonext plugin test [--json] <bundle>`. It returns the
// process exit code (see [ExitOK], [ExitFail], [ExitUsage]).
func runTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, testUsage)
	}
	jsonOut := fs.Bool("json", false, "emit the report as a single JSON object on stdout")

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed the error via fs.SetOutput.
		// Distinguish `-h`/`--help` (which returns flag.ErrHelp) from
		// genuine bad flags.
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "gonext plugin test: missing bundle path")
		fmt.Fprintln(stderr, testUsage)
		return ExitUsage
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "gonext plugin test: unexpected extra arguments: %v\n", rest[1:])
		fmt.Fprintln(stderr, testUsage)
		return ExitUsage
	}

	report, err := plugintest.Run(rest[0])
	if err != nil {
		// Couldn't even open the bundle — that's a usage-class problem, not
		// a contract failure. Make it visible regardless of format.
		if *jsonOut {
			// Emit an empty-checks report so the marketplace ingestor still
			// gets a valid JSON document.
			report.Pass = false
			_ = writeJSON(stdout, report)
		}
		fmt.Fprintf(stderr, "gonext plugin test: %s\n", err)
		return ExitFail
	}

	if *jsonOut {
		if err := writeJSON(stdout, report); err != nil {
			fmt.Fprintf(stderr, "gonext plugin test: writing JSON: %s\n", err)
			return ExitFail
		}
	} else {
		if err := report.WriteHuman(stdout); err != nil {
			fmt.Fprintf(stderr, "gonext plugin test: writing report: %s\n", err)
			return ExitFail
		}
	}

	if !report.Pass {
		return ExitFail
	}
	return ExitOK
}

// writeJSON emits the report as a pretty-printed JSON document followed by a
// trailing newline.
func writeJSON(w io.Writer, r plugintest.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

const testUsage = `gonext plugin test — run the plugin contract checks against a bundle

Usage:
  gonext plugin test [--json] <bundle>

Arguments:
  <bundle>   Path to a plugin bundle. Either a directory containing
             manifest.json at its root, or a .gnplugin (zip) archive.

Flags:
  --json     Emit the report as a single JSON object on stdout. Without
             this flag, a human-readable PASS/FAIL/SKIP table is printed.

Exit codes:
  0   all checks passed (skipped checks do not count against pass)
  1   at least one check failed, or the bundle could not be opened
  2   usage error (bad flags or missing argument)

Today the runner performs the checks that don't require the WASM host:
manifest schema validation, bundle layout, capability vocabulary, and a
read-only WebAssembly header check. The other contract checks from
docs/11-testing-ci.md §7.1 are emitted as rows with status "skipped" and
reason "runtime-not-available" until the host lands.`
