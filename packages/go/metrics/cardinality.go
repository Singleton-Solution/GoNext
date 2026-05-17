package metrics

import (
	"log/slog"
	"sort"
	"strings"
)

// MustBoundedLabels is a register-time guardrail against unbounded
// label cardinality. Cardinality is the operational cost driver in
// Prometheus — every distinct combination of label values is a series,
// and series multiply.
//
// The check is intentionally simple: every label in labels must have an
// entry in maxValues. Labels without a documented bound get logged at
// WARN; the call still succeeds so it never blocks rollout, but the
// warning is loud enough that an operator notices.
//
// Pass the same maxValues map that documents the cardinality budget in
// docs/10-observability.md §5.4. A typical call:
//
//	metrics.MustBoundedLabels(
//	    []string{"route", "method", "status_class"},
//	    map[string]int{
//	        "route":        150,
//	        "method":       7,
//	        "status_class": 5,
//	    },
//	    logger,
//	)
//
// "Must" is in the name because, like prometheus.MustRegister, the
// intent is to surface configuration errors at startup rather than in
// production. logger is required; if nil, the call panics so the bug is
// noticed in test rather than silently swallowed.
func MustBoundedLabels(labels []string, maxValues map[string]int, logger *slog.Logger) {
	if logger == nil {
		panic("metrics.MustBoundedLabels: logger is required")
	}

	var unbounded []string
	var zeroed []string
	for _, l := range labels {
		v, ok := maxValues[l]
		if !ok {
			unbounded = append(unbounded, l)
			continue
		}
		if v <= 0 {
			zeroed = append(zeroed, l)
		}
	}

	sort.Strings(unbounded)
	sort.Strings(zeroed)

	if len(unbounded) > 0 {
		logger.Warn("metric labels have no documented cardinality bound",
			slog.String("labels", strings.Join(unbounded, ",")),
			slog.String("docs", "docs/10-observability.md#54-cardinality-budget"),
		)
	}
	if len(zeroed) > 0 {
		logger.Warn("metric labels have zero or negative cardinality bound",
			slog.String("labels", strings.Join(zeroed, ",")),
		)
	}
}
