// Package config. See doc.go.
package config

import (
	"fmt"
	"io"
	"os"

	pkgconfig "github.com/Singleton-Solution/GoNext/packages/go/config"
)

// Exit codes shared with main.go.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext config — inspect runtime configuration

Usage:
  gonext config <subcommand> [args]

Subcommands:
  dump             Print the effective configuration with secrets masked.

Environment:
  Inherits the full config surface (DATABASE_URL, GONEXT_AUTH_*, etc.).
  See packages/go/config/doc.go for the full list.

Exit codes:
  0   success
  1   config load error (missing required vars, invalid values)
  2   usage error`

const dumpUsage = `gonext config dump — print effective configuration with secrets masked

Usage:
  gonext config dump

Output:
  KEY=value lines, sorted alphabetically. Fields tagged secret (or whose
  name contains password|secret|token|key|pepper|dsn) are rendered as:
    ***REDACTED*** (len=N, sha256[:8]=xxxxxxxx)
  so operators can verify the deployed secret matches the expected one
  without seeing the plaintext.

Exit codes:
  0   success
  1   config load error
  2   usage error`

// Run dispatches `gonext config ...`. args is the slice after the literal
// "config" token. stdout/stderr are injected so tests capture output.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "dump":
		return runDump(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext config: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS wires Run to os.Stdout/os.Stderr, matching the convention used by
// the other gonext subcommand packages (migrate, plugin, theme, revisions).
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

// runDump loads the config from the environment, then writes the redacted
// dump to stdout. Load errors are printed to stderr — including them in
// the dump body would just produce a misleading "all defaults" view.
func runDump(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Fprintln(stdout, dumpUsage)
			return ExitOK
		default:
			fmt.Fprintf(stderr, "gonext config dump: unexpected argument %q\n\n%s\n", args[0], dumpUsage)
			return ExitUsage
		}
	}
	cfg, err := pkgconfig.Load()
	if err != nil {
		// Don't bail — partial loads still produce a useful dump. The
		// whole point of this command is to diagnose what was loaded vs.
		// what wasn't. Print the error to stderr (so operators see it)
		// and the dump to stdout (so it remains pipe-friendly).
		fmt.Fprintf(stderr, "gonext config dump: load reported errors:\n%v\n", err)
		if cfg == nil {
			return ExitFail
		}
		if dumpErr := pkgconfig.Dump(*cfg, stdout); dumpErr != nil {
			fmt.Fprintf(stderr, "gonext config dump: write: %v\n", dumpErr)
			return ExitFail
		}
		return ExitFail
	}
	if err := pkgconfig.Dump(*cfg, stdout); err != nil {
		fmt.Fprintf(stderr, "gonext config dump: write: %v\n", err)
		return ExitFail
	}
	return ExitOK
}
