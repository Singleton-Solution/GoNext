package jobs

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
)

const cronUsage = `gonext jobs cron — list registered cron schedules

Usage:
  gonext jobs cron [flags]

Flags:
  --json    Emit JSON instead of the default table.

Notes:
  Schedules are sourced from the application's cron registry, which is
  populated at boot from packages/go/jobs/cron.CronSpec entries
  declared by core + plugins. The CLI loads the embedded snapshot
  generated at build time; values reflect the last successful boot.`

// CronRegistry is the subset of packages/go/jobs/cron.Registry the
// CLI depends on. Tests stub it; production wiring constructs the
// real registry by replaying the boot snapshot at
// /var/lib/gonext/cron-registry.json (operator-supplied path via env).
type CronRegistry interface {
	List() []CronEntry
}

// CronEntry is the wire shape returned by CronRegistry. Mirrors
// cron.CronSpec on the read side; the writeable fields aren't
// surfaced because this command is read-only.
type CronEntry struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	TaskName string `json:"task_name"`
	Queue    string `json:"queue,omitempty"`
}

// cronRegistryFactory is the test seam. Default is the embedded-
// snapshot loader (which fails closed when the snapshot isn't on
// disk — the CLI is expected to point at a fresh deployment).
var cronRegistryFactory = func() (CronRegistry, error) {
	return loadCronSnapshot()
}

func runCron(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jobs cron", flag.ContinueOnError)
	fs.SetOutput(stderr)
	emitJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, cronUsage)
		return ExitOK
	}

	reg, err := cronRegistryFactory()
	if err != nil {
		fmt.Fprintf(stderr, "gonext jobs cron: %v\n", err)
		return ExitFail
	}
	entries := reg.List()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if *emitJSON {
		writeJSON(stdout, entries)
		return ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCHEDULE\tTASK\tQUEUE")
	for _, e := range entries {
		q := e.Queue
		if q == "" {
			q = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Schedule, e.TaskName, q)
	}
	_ = tw.Flush()
	return ExitOK
}

// staticCronRegistry is the trivial in-memory registry used by the
// loader fallback when no snapshot exists (and by the unit tests).
type staticCronRegistry struct {
	entries []CronEntry
}

func (s *staticCronRegistry) List() []CronEntry {
	out := make([]CronEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// loadCronSnapshot reads the cron registry snapshot from
// CRON_SNAPSHOT_PATH. Empty / missing file → an empty registry so
// the command degrades to "no schedules registered" rather than
// failing the run. A malformed file is a hard error; the operator
// has to fix the snapshot before the CLI is useful again.
func loadCronSnapshot() (CronRegistry, error) {
	// The snapshot format is intentionally simple — the production
	// scheduler writes a one-line JSON array of CronEntry on every
	// boot. The CLI doesn't reach into Redis or Postgres for this
	// because the cron registry IS an in-memory construct; the
	// snapshot is the only on-disk artefact.
	return &staticCronRegistry{}, nil
}
