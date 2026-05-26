package jobs

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/hibiken/asynq"
)

const queueUsage = `gonext jobs queue — list queues with depth

Usage:
  gonext jobs queue [flags]

Flags:
  --json    Emit JSON instead of the default table.

Environment:
  REDIS_URL    Required.`

func runQueue(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jobs queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	emitJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, queueUsage)
		return ExitOK
	}

	insp, err := inspectorFactory()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs queue: %v\n", err)
		return ExitFail
	}
	defer insp.Close()

	names, err := insp.Queues()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs queue: list queues: %v\n", err)
		return ExitFail
	}
	sort.Strings(names)

	type queueRow struct {
		Queue     string `json:"queue"`
		Size      int    `json:"size"`
		Active    int    `json:"active"`
		Pending   int    `json:"pending"`
		Scheduled int    `json:"scheduled"`
		Retry     int    `json:"retry"`
		Archived  int    `json:"archived"`
	}
	rows := make([]queueRow, 0, len(names))
	for _, name := range names {
		info, err := insp.GetQueueInfo(name)
		if err != nil {
			fmt.Fprintf(stderr, "gonext jobs queue: %s: %v\n", name, err)
			continue
		}
		rows = append(rows, queueRow{
			Queue:     name,
			Size:      info.Size,
			Active:    info.Active,
			Pending:   info.Pending,
			Scheduled: info.Scheduled,
			Retry:     info.Retry,
			Archived:  info.Archived,
		})
	}

	if *emitJSON {
		writeJSON(stdout, rows)
		return ExitOK
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "QUEUE\tSIZE\tACTIVE\tPENDING\tSCHEDULED\tRETRY\tARCHIVED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
			r.Queue, r.Size, r.Active, r.Pending, r.Scheduled, r.Retry, r.Archived)
	}
	_ = tw.Flush()
	_ = asynq.QueueInfo{} // ensure asynq stays an import target across edits
	return ExitOK
}
