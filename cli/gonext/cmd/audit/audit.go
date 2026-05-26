// Package audit. See doc.go for the package overview.
package audit

import (
	"fmt"
	"io"
	"os"
)

// Exit codes for the subtree.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// Run is the entry point for `gonext audit ...`. args is the slice
// after the literal `audit` token. Returns the desired exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "tail":
		return runTail(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext audit: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS wires Run to the real OS streams.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

const usage = `gonext audit — inspect the audit log

Usage:
  gonext audit <subcommand> [args]

Subcommands:
  tail [flags]    Print the most recent audit events. Tail by default.

Run 'gonext audit tail --help' for the tail-specific flags.

Environment:
  DATABASE_URL    Required. Postgres DSN for the GoNext install.`
