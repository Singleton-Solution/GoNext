package rum

import (
	"errors"
	"math"
	"strings"
	"time"
)

// Allowed metric names. The Core Web Vitals set is the canonical
// trio (LCP, INP, CLS) plus the two "supporting" timings (TTFB,
// FCP) that web-vitals.js ships subscribers for. Custom timings
// land via a follow-up issue with a different code path; the
// current beacon handler rejects anything outside this set so a
// drive-by curl can't pollute the table with arbitrary metric
// names.
//
// The set is small enough to keep as a slice + linear scan. A map
// would be marginally faster but the cost is one allocation per
// process boot vs. five string compares per request — the compares
// win on a hot path.
var allowedMetrics = []string{"LCP", "INP", "CLS", "TTFB", "FCP"}

// Allowed rating bucket names. Mirrors the web-vitals.js
// classification — we don't re-derive thresholds in Go because the
// browser already did it (and the thresholds occasionally shift
// upstream). Storing the rating saves the admin handler from a
// per-metric threshold lookup at render time.
var allowedRatings = []string{"good", "needs-improvement", "poor"}

// Event is the on-wire shape of a single RUM observation. Field
// names are JSON-lowercase to match what the beacon library emits
// directly; we keep the struct field names Go-idiomatic via tags.
//
// Pointer types for the optional fields (Country, Conn) so a
// missing JSON key serialises to NULL in Postgres rather than the
// empty string. The handler is the gatekeeper on lengths and
// allowed values; the table's CHECK constraints are the second
// line of defence.
type Event struct {
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Rating    string  `json:"rating"`
	PagePath  string  `json:"page_path"`
	SessionID string  `json:"session_id"`
	// Optional. The browser sends these only when the operator has
	// enabled geo / connection enrichment in the beacon library.
	Country *string `json:"country,omitempty"`
	Conn    *string `json:"conn,omitempty"`
}

// Batch is the on-wire shape of a beacon POST body. We wrap the
// event list in an envelope (rather than accepting a bare array)
// so a future addition (a top-level schema version, a digest, a
// tracing header echo) doesn't break the JSON contract.
type Batch struct {
	Events []Event `json:"events"`
}

// Validation limits. Keeping them as package-level consts means
// both the handler and the tests can reference the same numbers
// without drift.
const (
	// MaxBodyBytes caps the request body. 16 KiB easily holds the
	// 50-event batch worst case (each event ~150 bytes, plus the
	// envelope), and the cap shields the ingest path from a single
	// abusive client pinning a goroutine on a slow reader.
	MaxBodyBytes int64 = 16 * 1024

	// MaxBatchSize caps the per-request event count. A 5-second
	// flush window with five Core Web Vitals + per-pageview
	// resends rarely exceeds 10; 50 is the comfortable headroom.
	MaxBatchSize = 50

	// MaxPagePathLen mirrors the table CHECK. The beacon library
	// normalises to pathname-only before sending, so even a
	// pathologically long URL should land well below this.
	MaxPagePathLen = 2048

	// MaxSessionIDLen mirrors the table CHECK. SHA-256 hex is 64
	// chars; the cap is the exact column ceiling.
	MaxSessionIDLen = 64

	// MaxMetricLen and MaxRatingLen mirror the table CHECKs and
	// guard against an attacker stuffing a long string into a
	// short column to produce a wide error.
	MaxMetricLen = 32
	MaxRatingLen = 32

	// MaxCountryLen mirrors the table CHECK. Two-letter ISO with
	// one char of headroom for sentinel codes ("ZZ", "T1") some
	// CDN edges emit.
	MaxCountryLen = 3

	// MaxConnLen mirrors the table CHECK. Free-form lowercase
	// short strings: "4g", "wifi", "slow-2g".
	MaxConnLen = 16
)

// validateBatch returns nil if b is a well-formed beacon body
// suitable for INSERT, else an error explaining the rejection.
//
// Validation is split out from the handler so the unit tests can
// exercise edge cases (empty events, oversize string fields,
// unknown metric, unknown rating, finite-only floats) without
// stitching together HTTP plumbing.
func validateBatch(b Batch) error {
	if len(b.Events) == 0 {
		return errors.New("beacon: events array is empty")
	}
	if len(b.Events) > MaxBatchSize {
		return errors.New("beacon: batch exceeds max size")
	}
	for i := range b.Events {
		if err := validateEvent(b.Events[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateEvent runs the per-event invariants. Keeping each check
// terse and ordered "cheapest first" so the common "valid event"
// path bails out of the function in a handful of compares.
func validateEvent(e Event) error {
	if e.Metric == "" || len(e.Metric) > MaxMetricLen {
		return errors.New("beacon: metric must be 1..32 chars")
	}
	if !contains(allowedMetrics, e.Metric) {
		return errors.New("beacon: unknown metric")
	}
	if e.Rating == "" || len(e.Rating) > MaxRatingLen {
		return errors.New("beacon: rating must be 1..32 chars")
	}
	if !contains(allowedRatings, e.Rating) {
		return errors.New("beacon: unknown rating")
	}
	if e.PagePath == "" || len(e.PagePath) > MaxPagePathLen {
		return errors.New("beacon: page_path must be 1..2048 chars")
	}
	if e.SessionID == "" || len(e.SessionID) > MaxSessionIDLen {
		return errors.New("beacon: session_id must be 1..64 chars")
	}
	// Reject NaN/Inf — Postgres would accept them but they make
	// percentile aggregation nonsensical, and there is no real-
	// world web-vitals event that produces a non-finite value.
	if !isFinite(e.Value) {
		return errors.New("beacon: value must be a finite number")
	}
	// Negative values are not strictly nonsensical for some
	// custom timings, but for the canonical CWV set they signal
	// a clock skew bug client-side. Reject defensively.
	if e.Value < 0 {
		return errors.New("beacon: value must be non-negative")
	}
	if e.Country != nil {
		c := strings.TrimSpace(*e.Country)
		if len(c) == 0 || len(c) > MaxCountryLen {
			return errors.New("beacon: country must be 1..3 chars")
		}
	}
	if e.Conn != nil {
		c := strings.TrimSpace(*e.Conn)
		if len(c) == 0 || len(c) > MaxConnLen {
			return errors.New("beacon: conn must be 1..16 chars")
		}
	}
	return nil
}

// contains is a tiny linear-scan helper. We don't reach for
// slices.Contains because the allowed-metric set is fixed and the
// function is exercised on the hot path; the inline loop avoids
// importing the slices package into this file.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// isFinite returns false for NaN and +/-Inf. Thin wrapper around the
// math helpers so the validator reads as "is this number sane?"
// rather than as two separate negations.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// PercentileResult is the JSON-rendered shape returned by the
// GET /api/v1/admin/rum/percentiles handler. Sample is the
// underlying event count so the UI can show "p50=2400ms (n=812)"
// — a percentile over <50 samples is statistically iffy and the
// frontend dims the band when the count is small.
type PercentileResult struct {
	Metric string    `json:"metric"`
	Path   string    `json:"path,omitempty"`
	Period string    `json:"period"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	P50    float64   `json:"p50"`
	P75    float64   `json:"p75"`
	P95    float64   `json:"p95"`
	Sample int       `json:"sample"`
}

// RouteSlowRow is one row of the "top slowest routes" table the
// admin Performance page renders below the per-metric charts.
// Path + metric + p75 (the operator-visible Core Web Vitals
// threshold) is the minimum useful shape; sample is included so
// low-traffic routes can be downweighted in the UI.
type RouteSlowRow struct {
	Path   string  `json:"path"`
	Metric string  `json:"metric"`
	P75    float64 `json:"p75"`
	Sample int     `json:"sample"`
}
