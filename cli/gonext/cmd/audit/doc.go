// Package audit is the `gonext audit` CLI subtree. It surfaces the
// operator-facing audit log to the terminal, primarily as a `tail`
// command modelled on `tail -f` — print the last N events and (with
// --follow) keep streaming.
//
// Subcommands:
//
//	gonext audit tail [--follow] [--limit N] [--type T] [--actor U]
//
// The command opens DATABASE_URL, instantiates the Postgres audit
// store, and runs the store's List query with the supplied filter.
// With --follow set, the command polls every 1s using the last
// observed event's time as the lower bound for the next query so
// events that landed during the previous tick aren't missed.
//
// Output format: one event per line, columns separated by a tab.
// The columns are:
//
//	timestamp (RFC3339) | severity | event_type | actor | resource | metadata
//
// The format is intentionally line-per-event and tab-delimited so
// `gonext audit tail | grep` and `gonext audit tail | awk` both work
// without parser ceremony.
package audit
