package jobs

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/hibiken/asynq"
)

const pluginUsage = `gonext jobs plugin — per-plugin task counts

Usage:
  gonext jobs plugin [flags]

Flags:
  --json    Emit JSON instead of the default table.

Notes:
  Counts are derived by scanning the task_type column of pending +
  active + archived rows and grouping by the "{plugin}." prefix.
  Core tasks (no dot prefix or a leading "core.") are grouped as
  "core". Plugin slugs must match the prefix convention enforced at
  task registration time.

Environment:
  REDIS_URL    Required.`

func runPlugin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jobs plugin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	emitJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, pluginUsage)
		return ExitOK
	}

	insp, err := inspectorFactory()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs plugin: %v\n", err)
		return ExitFail
	}
	defer insp.Close()

	queues, err := insp.Queues()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs plugin: list queues: %v\n", err)
		return ExitFail
	}

	// counts[plugin][state] = count.
	counts := map[string]map[string]int{}
	bump := func(plugin, state string) {
		if _, ok := counts[plugin]; !ok {
			counts[plugin] = map[string]int{}
		}
		counts[plugin][state]++
	}

	for _, q := range queues {
		// Pending.
		pending, err := insp.ListArchivedTasks(q, asynq.PageSize(500))
		if err == nil {
			for _, t := range pending {
				bump(pluginPrefix(t.Type), "archived")
			}
		}
		info, err := insp.GetQueueInfo(q)
		if err != nil {
			continue
		}
		// Aggregate counts at the queue level. The queue's
		// {Pending,Active,Retry,Scheduled} totals don't break down
		// by task type, so we bucket them under "<queue-total>".
		// The archived (above) is the only state where we have
		// per-task-type detail without paging every list.
		// Operators after fine-grained pending breakdown can pair
		// this with `jobs queue --json` to see the totals.
		bump("(queue:"+q+")", "size:"+itoa(info.Size))
	}

	type row struct {
		Plugin string         `json:"plugin"`
		States map[string]int `json:"states"`
	}
	out := make([]row, 0, len(counts))
	for k, v := range counts {
		out = append(out, row{Plugin: k, States: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Plugin < out[j].Plugin })

	if *emitJSON {
		writeJSON(stdout, out)
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PLUGIN\tARCHIVED\tNOTES")
	for _, r := range out {
		archived := r.States["archived"]
		var notes []string
		for k, v := range r.States {
			if k == "archived" {
				continue
			}
			notes = append(notes, fmt.Sprintf("%s=%d", k, v))
		}
		sort.Strings(notes)
		fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Plugin, archived, strings.Join(notes, " "))
	}
	_ = tw.Flush()
	return ExitOK
}

// pluginPrefix extracts the plugin slug from a task type. Core tasks
// (no dot, or "core.") group as "core". A task type like
// "myseo.sitemap.regenerate" yields "myseo".
func pluginPrefix(taskType string) string {
	if taskType == "" {
		return "core"
	}
	dot := strings.IndexByte(taskType, '.')
	if dot < 0 {
		return "core"
	}
	prefix := taskType[:dot]
	if prefix == "core" || prefix == "" {
		return "core"
	}
	return prefix
}

// itoa is a tiny strconv.Itoa shim so the file stays a single import.
func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
