// Package bench. See doc.go.
package bench

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

// Exit codes are shared between main.go and the tests.
//
//	0 — every scenario passed its SLO budget.
//	1 — at least one SLO check failed.
//	2 — usage error (bad flags, unknown scenario, etc.).
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext bench — run synthetic load against a GoNext install

Usage:
  gonext bench [flags] [scenario]

If [scenario] is omitted, all built-in scenarios run sequentially.

Scenarios:
  homepage    Cached public homepage           (SLO: p95 < 250ms, p99 < 500ms)
  posts       GET /wp-json/wp/v2/posts         (SLO: p95 < 400ms, p99 < 800ms)
  login       POST /api/v1/auth/login          (SLO: p95 < 800ms, p99 < 1500ms)
  restshim    Mix of WP-compat REST queries    (SLO: p95 < 400ms, p99 < 800ms)

Flags:
  --host <url>      Base URL of the GoNext stack. Default http://localhost:8080.
                    For homepage the web base falls back to GONEXT_WEB_BASE_URL,
                    or --host if it is not set.
  --vus <n>         Number of concurrent virtual users. Default 10.
  --duration <d>    Total run time. Default 30s. Accepts any Go duration.
  --ramp <d>        Time over which VUs ramp up linearly. Default 5s.
                    Use 0 to start all VUs immediately.
  --output text|json
                    Report format. Default text.
  --no-slo          Run the scenarios but do not exit non-zero on SLO miss.
  -h, --help        Show this help.

Exit codes:
  0  every scenario passed its SLO budget (or --no-slo was set)
  1  at least one SLO check failed
  2  usage error

Examples:
  gonext bench --host http://localhost:8080
  gonext bench homepage --vus 50 --duration 2m --ramp 30s
  gonext bench restshim --output json | jq .`

// Run is the entry point for the bench subcommand tree. args is the
// slice after the literal "bench" token. stdout and stderr are injected
// so tests can capture output without fighting os.Stdout. The return
// value is the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return runWithSignals(context.Background(), args, stdout, stderr)
}

// RunOS wires Run to os.Stdout/os.Stderr. main.go calls this directly.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

// runWithSignals installs a SIGINT/SIGTERM handler that cancels the
// run context. It is split out from Run so tests can drive the
// underlying logic with their own context.
func runWithSignals(parent context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return RunCtx(ctx, args, stdout, stderr)
}

// RunCtx is the dependency-injection seam. It accepts a context so
// tests can cancel the run on a tight deadline.
func RunCtx(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, usage) }

	host := fs.String("host", "http://localhost:8080", "base URL of the GoNext stack")
	vus := fs.Int("vus", 10, "number of concurrent virtual users")
	duration := fs.Duration("duration", 30*time.Second, "total run time")
	ramp := fs.Duration("ramp", 5*time.Second, "time over which VUs ramp up linearly")
	output := fs.String("output", "text", "report format: text or json")
	noSLO := fs.Bool("no-slo", false, "do not exit non-zero on SLO miss")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}

	// Flag validation.
	if *vus <= 0 {
		fmt.Fprintf(stderr, "gonext bench: --vus must be > 0, got %d\n", *vus)
		return ExitUsage
	}
	if *duration <= 0 {
		fmt.Fprintf(stderr, "gonext bench: --duration must be > 0, got %s\n", *duration)
		return ExitUsage
	}
	if *ramp < 0 {
		fmt.Fprintf(stderr, "gonext bench: --ramp must be >= 0, got %s\n", *ramp)
		return ExitUsage
	}
	if *ramp >= *duration {
		fmt.Fprintf(stderr, "gonext bench: --ramp (%s) must be < --duration (%s)\n", *ramp, *duration)
		return ExitUsage
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(stderr, "gonext bench: --output must be 'text' or 'json', got %q\n", *output)
		return ExitUsage
	}

	rest := fs.Args()
	var picked []scenarios.Scenario
	available := scenarios.All()
	switch len(rest) {
	case 0:
		picked = available
	case 1:
		want := strings.ToLower(rest[0])
		for _, s := range available {
			if s.Name() == want {
				picked = []scenarios.Scenario{s}
				break
			}
		}
		if picked == nil {
			names := make([]string, 0, len(available))
			for _, s := range available {
				names = append(names, s.Name())
			}
			sort.Strings(names)
			fmt.Fprintf(stderr, "gonext bench: unknown scenario %q (available: %s)\n",
				rest[0], strings.Join(names, ", "))
			return ExitUsage
		}
	default:
		fmt.Fprintf(stderr, "gonext bench: unexpected extra arguments: %v\n", rest[1:])
		return ExitUsage
	}

	cfg := RunConfig{
		Host:     *host,
		VUs:      *vus,
		Duration: *duration,
		Ramp:     *ramp,
	}

	reports := make([]Report, 0, len(picked))
	for _, s := range picked {
		// Surface progress on stderr so JSON output on stdout stays
		// machine-readable.
		fmt.Fprintf(stderr, "running %s (vus=%d duration=%s ramp=%s)...\n",
			s.Name(), cfg.VUs, cfg.Duration, cfg.Ramp)
		rep, err := RunScenario(ctx, s, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "gonext bench: scenario %s failed: %v\n", s.Name(), err)
			return ExitFail
		}
		reports = append(reports, rep)
	}

	// Evaluate SLOs before emitting so the JSON form carries the verdict.
	allPass := true
	for i := range reports {
		EvaluateSLO(&reports[i])
		if !reports[i].SLO.Passed {
			allPass = false
		}
	}

	switch *output {
	case "json":
		if err := WriteJSON(stdout, reports); err != nil {
			fmt.Fprintf(stderr, "gonext bench: write json: %v\n", err)
			return ExitFail
		}
	default:
		if err := WriteText(stdout, reports); err != nil {
			fmt.Fprintf(stderr, "gonext bench: write text: %v\n", err)
			return ExitFail
		}
	}

	if !allPass && !*noSLO {
		return ExitFail
	}
	return ExitOK
}
