// budget.go is the loader-side instrumentation for the GraphQL query-
// budget CI check (issue #115).
//
// The check runs as a Go benchmark:
//
//   go test -run='^$' -bench BenchmarkGraphQLBudgets ./apps/api/...
//
// Each scenario fires a representative GraphQL operation through a
// fake repo set and captures a dataloader.Snapshot. The runner compares
// the snapshot's batch-call counts against tools/graphql-budgets.yml;
// CI fails when any operation exceeds its budget.
//
// The thing being measured is "how many batches of database round-
// trips would this query produce in production?" — NOT how many
// fields resolved. A correct loader yields one batch per
// (resolver, request-tick) pair regardless of how many parent rows
// the query selects.
package dataloader

import (
	"fmt"
)

// Budget is one operation's batch-round-trip ceiling. Decoded from
// tools/graphql-budgets.yml.
type Budget struct {
	MaxBatchRoundTrips int `yaml:"maxBatchRoundTrips"`
}

// BudgetConfig is the decoded YAML root. Stored alongside the bench
// runner so the budget file is the single source of truth.
type BudgetConfig struct {
	DefaultMaxBatchRoundTrips int               `yaml:"defaultMaxBatchRoundTrips"`
	Operations                map[string]Budget `yaml:"operations"`
	MaxRequestMillis          int               `yaml:"maxRequestMillis"`
}

// CheckSnapshot reports whether the snapshot satisfies the budget for
// the named operation. Returns nil on pass, a descriptive error on
// fail (suitable for `t.Fatalf` / CI surface).
//
// Operations not listed in cfg.Operations fall through to the default
// budget — the same "deliberate growth" posture as the bundle-budget
// tool.
func (cfg BudgetConfig) CheckSnapshot(operation string, snap Snapshot) error {
	budget := cfg.DefaultMaxBatchRoundTrips
	if op, ok := cfg.Operations[operation]; ok && op.MaxBatchRoundTrips > 0 {
		budget = op.MaxBatchRoundTrips
	}
	total := totalBatchCalls(snap)
	if total > int64(budget) {
		return fmt.Errorf(
			"graphql budget: operation %q used %d batch round-trips, budget is %d "+
				"(UserBatch=%d TermBatch=%d MediaBatch=%d TermsByPostBatch=%d) — "+
				"either a resolver lost its dataloader wiring or a new N+1 dimension was added",
			operation, total, budget,
			snap.UserBatchCalls,
			snap.TermBatchCalls,
			snap.MediaBatchCalls,
			snap.TermsByPostBatch,
		)
	}
	return nil
}

// totalBatchCalls sums the per-resolver batch counters. A "batch
// call" is a single Postgres round-trip — that's the unit the
// budget guards.
func totalBatchCalls(s Snapshot) int64 {
	return s.UserBatchCalls + s.TermBatchCalls + s.MediaBatchCalls + s.TermsByPostBatch
}
