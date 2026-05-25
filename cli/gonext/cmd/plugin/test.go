package plugin

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/Singleton-Solution/GoNext/cli/gonext/internal/plugintest"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/conformance"
)

// runTest implements `gonext plugin test [--json] [--suite=conformance]
// [--record-fixtures=DIR] <bundle>`. It returns the process exit code
// (see [ExitOK], [ExitFail], [ExitUsage]).
//
// The two suites:
//
//   - default suite (no --suite): runs the bundle contract checks from
//     [plugintest.Run] (manifest schema, layout, capability vocabulary,
//     WASM header). Fast, host-independent, suitable for every save.
//   - --suite=conformance: also runs the in-memory conformance scenarios
//     under [conformance.NewSuite] against a fakehost.Host. Slower, more
//     thorough, suitable for CI and pre-publish.
//
// We keep `default` and `conformance` as separate codepaths so a plugin
// author who is fine-tuning their bundle layout doesn't pay the cost of
// the behavioural scenarios — and so the marketplace's ingest pipeline
// stays on the stable `default` shape until conformance graduates.
func runTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, testUsage)
	}
	jsonOut := fs.Bool("json", false, "emit the report as a single JSON object on stdout")
	suite := fs.String("suite", "", "named suite to run; only `conformance` is recognised today")
	recordFixtures := fs.String("record-fixtures", "",
		"with --suite=conformance: directory to dump one JSON fixture per scenario")

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
	bundle := rest[0]

	// --record-fixtures only makes sense in --suite=conformance mode.
	// Rejecting it loudly elsewhere prevents the silent-no-op trap.
	if *recordFixtures != "" && *suite != "conformance" {
		fmt.Fprintln(stderr,
			"gonext plugin test: --record-fixtures requires --suite=conformance")
		return ExitUsage
	}

	switch *suite {
	case "", "default":
		return runDefaultSuite(bundle, *jsonOut, stdout, stderr)
	case "conformance":
		return runConformanceSuite(bundle, *jsonOut, *recordFixtures, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext plugin test: unknown suite %q (want: default | conformance)\n",
			*suite)
		return ExitUsage
	}
}

// runDefaultSuite runs the bundle-contract checks via plugintest. This
// is the original `gonext plugin test` behaviour and remains the
// fastest path.
func runDefaultSuite(bundle string, jsonOut bool, stdout, stderr io.Writer) int {
	report, err := plugintest.Run(bundle)
	if err != nil {
		// Couldn't even open the bundle — that's a usage-class
		// problem, not a contract failure. Make it visible
		// regardless of format.
		if jsonOut {
			// Emit an empty-checks report so the marketplace
			// ingestor still gets a valid JSON document.
			report.Pass = false
			_ = writeJSON(stdout, report)
		}
		fmt.Fprintf(stderr, "gonext plugin test: %s\n", err)
		return ExitFail
	}

	if jsonOut {
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

// runConformanceSuite runs the in-memory conformance scenarios. The
// suite mounts a fakehost.Host per scenario and asserts on the
// recorded host-call trace.
func runConformanceSuite(bundle string, jsonOut bool, recordDir string, stdout, stderr io.Writer) int {
	s := conformance.NewSuite()
	s.RecordFixtures = recordDir
	report, err := s.Run(context.Background(), bundle)
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin test: conformance: %s\n", err)
		return ExitFail
	}

	if jsonOut {
		if err := report.WriteJSON(stdout); err != nil {
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
  gonext plugin test [--json] [--suite=conformance] [--record-fixtures=DIR] <bundle>

Arguments:
  <bundle>             Path to a plugin bundle. Either a directory
                       containing manifest.json at its root, or a
                       .gnplugin (zip) archive.

Flags:
  --json               Emit the report as a single JSON object on stdout.
                       Without this flag, a human-readable PASS/FAIL/SKIP
                       table is printed.

  --suite=NAME         Choose which suite to run.
                         (empty | default)  Bundle contract checks only —
                                            fast, host-independent.
                         conformance        Adds scenario-based assertions
                                            against an in-memory fake host
                                            (declared caps match usage,
                                            init+teardown idempotent,
                                            1s synthetic-job budget, etc.).

  --record-fixtures=DIR  Only valid with --suite=conformance. After the run
                         finishes, dump one JSON fixture per scenario into
                         DIR for later replay. Existing files are
                         overwritten.

Exit codes:
  0   all checks passed (skipped checks do not count against pass)
  1   at least one check failed, or the bundle could not be opened
  2   usage error (bad flags or missing argument)

Default suite: manifest schema validation, bundle layout, capability
vocabulary, and a read-only WebAssembly header check. The remaining
contract checks from docs/11-testing-ci.md §7.1 are emitted as skipped
rows until the host lands.

Conformance suite: parses the manifest (v1 OR legacy), runs the built-in
scenarios from packages/go/plugins/conformance, and (with
--record-fixtures) writes the recorded fake-host event trace to disk.`
