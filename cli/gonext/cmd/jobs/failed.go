package jobs

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/hibiken/asynq"
)

const failedUsage = `gonext jobs failed — list archived (failed) tasks

Usage:
  gonext jobs failed [flags]

Flags:
  --queue Q   Restrict to one queue. Default: list every queue with archived rows.
  --limit N   Maximum rows to print per queue (default 50, max 500).
  --json      Emit JSON instead of the default table.

Environment:
  REDIS_URL   Required.`

func runFailed(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jobs failed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	queue := fs.String("queue", "", "")
	limit := fs.Int("limit", 50, "")
	emitJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, failedUsage)
		return ExitOK
	}
	if *limit < 1 || *limit > 500 {
		fmt.Fprintf(stderr, "gonext jobs failed: --limit must be 1..500 (got %d)\n", *limit)
		return ExitUsage
	}

	insp, err := inspectorFactory()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs failed: %v\n", err)
		return ExitFail
	}
	defer insp.Close()

	queues := []string{*queue}
	if *queue == "" {
		names, err := insp.Queues()
		if err != nil {
			fmt.Fprintf(stderr, "gonext jobs failed: list queues: %v\n", err)
			return ExitFail
		}
		queues = names
	}

	type row struct {
		Queue     string    `json:"queue"`
		ID        string    `json:"id"`
		Type      string    `json:"type"`
		LastErr   string    `json:"last_err"`
		Retried   int       `json:"retried"`
		LastFail  time.Time `json:"last_failed_at"`
	}
	var all []row

	for _, q := range queues {
		tasks, err := insp.ListArchivedTasks(q, asynq.PageSize(*limit))
		if err != nil {
			fmt.Fprintf(stderr, "gonext jobs failed: list %s: %v\n", q, err)
			continue
		}
		for _, t := range tasks {
			all = append(all, row{
				Queue:    q,
				ID:       t.ID,
				Type:     t.Type,
				LastErr:  t.LastErr,
				Retried:  t.Retried,
				LastFail: t.LastFailedAt,
			})
		}
	}

	if *emitJSON {
		writeJSON(stdout, all)
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "QUEUE\tID\tTYPE\tRETRIED\tLAST FAILURE\tERROR")
	for _, r := range all {
		when := "-"
		if !r.LastFail.IsZero() {
			when = r.LastFail.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			r.Queue, r.ID, r.Type, r.Retried, when, r.LastErr)
	}
	_ = tw.Flush()
	return ExitOK
}
