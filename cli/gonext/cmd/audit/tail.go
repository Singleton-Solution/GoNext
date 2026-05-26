package audit

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

const tailUsage = `gonext audit tail — print recent audit events

Usage:
  gonext audit tail [flags]

Flags:
  --limit N        Maximum events to print on the initial dump (default: 50).
  --follow, -f     After the initial dump, poll every 1s for new events.
                   Stop with Ctrl-C; exit status is 0.
  --type T         Filter to events with EventType == T.
  --actor U        Filter to events emitted by user-id U.
  --plugin S       Filter to events emitted by plugin slug S.
  --severity LEVEL Filter to events with severity LEVEL (info|warning|critical).
  --since DUR      Initial lookback window (e.g. "1h", "24h"). Default: 24h.
  --json           Emit one JSON object per line instead of the tab-delimited
                   default. Useful for piping into jq.

Environment:
  DATABASE_URL     Required.`

// tailDeps lets tests inject the audit store (so we don't need a live
// Postgres for unit coverage). nil dialString → use DATABASE_URL +
// audit.PostgresStore.
type tailDeps struct {
	openStore func(ctx context.Context) (audit.Store, func(), error)
	now       func() time.Time
	tickEvery time.Duration

	// signals is a channel that, when closed, terminates the --follow
	// loop. Production wires it to a SIGINT/SIGTERM trap; tests close
	// it after a fixed number of ticks.
	signals <-chan os.Signal
}

func defaultTailDeps() tailDeps {
	return tailDeps{
		openStore: openPostgresAuditStore,
		now:       time.Now,
		tickEvery: 1 * time.Second,
	}
}

// runTail parses the flag set and dispatches.
func runTail(args []string, stdout, stderr io.Writer) int {
	return runTailWithDeps(args, stdout, stderr, defaultTailDeps())
}

func runTailWithDeps(args []string, stdout, stderr io.Writer, deps tailDeps) int {
	fs := flag.NewFlagSet("audit tail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 50, "")
	follow := fs.Bool("follow", false, "")
	fs.BoolVar(follow, "f", false, "")
	typ := fs.String("type", "", "")
	actor := fs.String("actor", "", "")
	plugin := fs.String("plugin", "", "")
	severity := fs.String("severity", "", "")
	since := fs.Duration("since", 24*time.Hour, "")
	emitJSON := fs.Bool("json", false, "")
	help := fs.Bool("help", false, "")

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already printed; bare usage exit.
		return ExitUsage
	}
	if *help {
		fmt.Fprintln(stdout, tailUsage)
		return ExitOK
	}

	if *limit < 1 || *limit > 1000 {
		fmt.Fprintf(stderr, "gonext audit tail: --limit must be 1..1000 (got %d)\n", *limit)
		return ExitUsage
	}
	if *severity != "" && !audit.Severity(*severity).Valid() {
		fmt.Fprintf(stderr, "gonext audit tail: --severity must be one of info|warning|critical (got %q)\n", *severity)
		return ExitUsage
	}

	ctx := context.Background()
	store, closeStore, err := deps.openStore(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "gonext audit tail: %v\n", err)
		return ExitFail
	}
	defer closeStore()

	now := deps.now
	if now == nil {
		now = time.Now
	}

	filter := audit.Filter{
		EventType:   *typ,
		ActorUserID: *actor,
		PluginSlug:  *plugin,
		Limit:       *limit,
		Start:       now().Add(-*since),
	}
	if *severity != "" {
		filter.Severity = audit.Severity(*severity)
	}

	// Initial dump.
	events, err := store.List(ctx, filter)
	if err != nil {
		fmt.Fprintf(stderr, "gonext audit tail: list: %v\n", err)
		return ExitFail
	}
	// Reverse so the printout is oldest-first; the store returns
	// most-recent-first.
	sort.Slice(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	for _, e := range events {
		printEvent(stdout, e, *emitJSON)
	}

	if !*follow {
		return ExitOK
	}

	// --follow: poll on a 1s tick. The cursor moves to the most-
	// recent event we've printed so a slow Postgres + a burst of new
	// events doesn't drop rows between ticks.
	cursor := now()
	if len(events) > 0 {
		cursor = events[len(events)-1].Time
	}

	signals := deps.signals
	if signals == nil {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)
		signals = sigChan
	}

	tickEvery := deps.tickEvery
	if tickEvery == 0 {
		tickEvery = 1 * time.Second
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()

	for {
		select {
		case <-signals:
			return ExitOK
		case <-t.C:
			batch, err := store.List(ctx, audit.Filter{
				EventType:   *typ,
				ActorUserID: *actor,
				PluginSlug:  *plugin,
				Severity:    filter.Severity,
				Start:       cursor.Add(1 * time.Nanosecond),
				Limit:       1000,
			})
			if err != nil {
				fmt.Fprintf(stderr, "gonext audit tail: poll: %v\n", err)
				continue
			}
			sort.Slice(batch, func(i, j int) bool { return batch[i].Time.Before(batch[j].Time) })
			for _, e := range batch {
				printEvent(stdout, e, *emitJSON)
				if e.Time.After(cursor) {
					cursor = e.Time
				}
			}
		}
	}
}

// printEvent emits one event. JSON mode emits a single-line object;
// the default mode emits a tab-delimited row.
func printEvent(w io.Writer, e audit.Event, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(w).Encode(e)
		return
	}
	actor := e.ActorUserID
	if actor == "" && e.ActorPluginSlug != "" {
		actor = "plugin:" + e.ActorPluginSlug
	}
	if actor == "" {
		actor = "-"
	}
	resource := "-"
	if e.ResourceType != "" || e.ResourceID != "" {
		resource = strings.TrimSpace(e.ResourceType + ":" + e.ResourceID)
	}
	metadata := ""
	if len(e.Metadata) > 0 {
		raw, _ := json.Marshal(e.Metadata)
		metadata = string(raw)
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
		e.Time.UTC().Format(time.RFC3339),
		string(e.Severity),
		e.EventType,
		actor,
		resource,
		metadata,
	)
}

// openPostgresAuditStore is the production wiring: DATABASE_URL ->
// pgxpool -> audit.PostgresStore.
func openPostgresAuditStore(ctx context.Context) (audit.Store, func(), error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, nil, errors.New("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	store := audit.NewPostgresStore(pool)
	return store, pool.Close, nil
}
