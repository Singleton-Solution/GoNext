// Package jobs. See doc.go.
package jobs

import (
	"fmt"
	"io"
	"os"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

// Run dispatches `gonext jobs ...`.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "queue":
		return runQueue(args[1:], stdout, stderr)
	case "failed":
		return runFailed(args[1:], stdout, stderr)
	case "drain":
		return runDrain(args[1:], stdout, stderr)
	case "cron":
		return runCron(args[1:], stdout, stderr)
	case "plugin":
		return runPlugin(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext jobs: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS wires Run to the real OS streams.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

const usage = `gonext jobs — inspect and manage background jobs

Usage:
  gonext jobs <subcommand> [args]

Subcommands:
  queue              List the configured queues with their pending depth.
  failed [--queue Q] List failed tasks (archived after retry exhaustion).
  drain [--queue Q]  Drain the dead-letter queue. Asks for confirmation
                     unless --yes is passed.
  cron               List the registered cron schedules with last/next fire.
  plugin             Show per-plugin task counts (queued + processed + failed).

Environment:
  REDIS_URL          Required for queue/failed/drain.
  DATABASE_URL       Required for cron/plugin (the registry + counters live in PG).`
