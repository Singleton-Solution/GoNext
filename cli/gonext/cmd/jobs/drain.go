package jobs

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const drainUsage = `gonext jobs drain — drain the dead-letter queue (archived rows)

Usage:
  gonext jobs drain [flags]

Flags:
  --queue Q   Restrict to one queue. Default: drain every queue.
  --yes       Skip the interactive confirmation prompt.

Environment:
  REDIS_URL   Required.

This command deletes ALL archived tasks from the targeted queue(s).
The action is irreversible — once drained, the failed tasks are
gone. Use 'gonext jobs failed' to inspect them first.`

func runDrain(args []string, stdout, stderr io.Writer) int {
	return runDrainWithStdin(args, stdout, stderr, os.Stdin)
}

// runDrainWithStdin is the test seam — passes the user-input stream
// in explicitly so tests can pre-load a "yes" or "no" line.
func runDrainWithStdin(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("jobs drain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	queue := fs.String("queue", "", "")
	yes := fs.Bool("yes", false, "")
	help := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, drainUsage)
		return ExitOK
	}

	insp, err := inspectorFactory()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs drain: %v\n", err)
		return ExitFail
	}
	defer insp.Close()

	queues := []string{*queue}
	if *queue == "" {
		names, err := insp.Queues()
		if err != nil {
			fmt.Fprintf(stderr, "gonext jobs drain: list queues: %v\n", err)
			return ExitFail
		}
		queues = names
	}

	if !*yes {
		fmt.Fprintf(stdout, "About to delete every archived task in queues: %s\n",
			strings.Join(queues, ", "))
		fmt.Fprint(stdout, "Type 'yes' to continue: ")
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(stderr, "gonext jobs drain: aborted (no input)")
			return ExitFail
		}
		if strings.TrimSpace(strings.ToLower(line)) != "yes" {
			fmt.Fprintln(stdout, "Aborted.")
			return ExitOK
		}
	}

	totalDeleted := 0
	for _, q := range queues {
		n, err := insp.DeleteAllArchivedTasks(q)
		if err != nil {
			fmt.Fprintf(stderr, "gonext jobs drain: %s: %v\n", q, err)
			continue
		}
		fmt.Fprintf(stdout, "drained %d archived tasks from %s\n", n, q)
		totalDeleted += n
	}
	fmt.Fprintf(stdout, "total: %d\n", totalDeleted)
	return ExitOK
}
