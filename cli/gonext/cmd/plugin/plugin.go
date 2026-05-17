package plugin

import (
	"fmt"
	"io"
	"os"
)

// Exit codes for the subtree. Documented here so `main.go` and tests share
// the contract:
//
//   - 0: all checks passed (or, for help/version, success)
//   - 1: at least one contract check failed
//   - 2: usage error (bad flags, missing argument, unknown subcommand)
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// Run is the entry point for the `gonext plugin ...` subtree. args is the
// slice after the literal `plugin` token — i.e. for `gonext plugin test
// ./bundle`, args = ["test", "./bundle"]. It returns the process exit code.
//
// stdout and stderr are passed in so tests can capture output without
// fighting with os.Stdout. The CLI's main.go wires them to the real OS
// streams.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "test":
		return runTest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext plugin: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS is a thin convenience that wires Run to os.Stdout/os.Stderr. main.go
// calls this directly.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

const usage = `gonext plugin — manage plugins

Usage:
  gonext plugin <subcommand> [args]

Subcommands:
  test       Run the plugin contract checks against a bundle.

Subcommands planned (not yet implemented):
  install    Install a plugin bundle from path or marketplace.
  activate   Activate or deactivate an installed plugin.
  list       List installed plugins.
  dev        Run the plugin author dev loop (build + watch).

Run 'gonext plugin <subcommand> --help' for subcommand-specific help.`
