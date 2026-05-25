package runtime

import (
	"sync"
)

// DefaultCardinalityLimit is the per-plugin, per-tag cap on the number
// of distinct values gn_metric_observe will accept before dropping
// further observations.
//
// Prometheus storage cost grows linearly with the cardinality of the
// label set: every distinct (slug, metric, tag1=v1, tag2=v2, ...) tuple
// is a series. A misbehaving plugin that observes a metric tagged with
// e.g. {user_id: <every visitor>} would explode the series count
// faster than Prometheus can compact. The dam draws the line at 1000
// distinct values per tag — generous enough that legitimate uses
// (HTTP status codes, post types, locales) sail through, strict enough
// that user-id-style labels are caught quickly.
const DefaultCardinalityLimit = 1000

// CardinalityDam tracks the per-(slug, metric, tag) distinct-value
// count and reports when a new combination would exceed the limit.
//
// One CardinalityDam per Runtime is the intended pattern — the dam
// itself is shared across every plugin so the limit is a per-plugin
// upper bound, not a per-runtime one.
//
// CardinalityDam is safe for concurrent use.
type CardinalityDam struct {
	limit int

	// state[slug][metric][tagKey] is a set of observed tag values.
	// We use nested maps over a flat keyed map because the typical
	// access pattern walks one tag at a time; the nesting lets us
	// short-circuit cleanly once any tag is over budget.
	mu    sync.Mutex
	state map[string]map[string]map[string]map[string]struct{}
}

// NewCardinalityDam returns a dam with the supplied per-tag limit.
// limit <= 0 falls back to DefaultCardinalityLimit so a misconfigured
// runtime still has a budget — silently disabling the dam would be
// the worst possible default.
func NewCardinalityDam(limit int) *CardinalityDam {
	if limit <= 0 {
		limit = DefaultCardinalityLimit
	}
	return &CardinalityDam{
		limit: limit,
		state: make(map[string]map[string]map[string]map[string]struct{}),
	}
}

// Limit returns the configured per-tag-value ceiling.
func (d *CardinalityDam) Limit() int {
	if d == nil {
		return 0
	}
	return d.limit
}

// Admit records a (slug, metric, tags) observation against the dam
// and returns the first tag key whose value count would exceed the
// limit, or empty string when the observation is admitted.
//
// The dam mutates state ONLY when the admission succeeds — a rejected
// observation does NOT leave a partial trace, so retrying with a
// different tag set isn't penalised.
//
// tags may be nil; a metric with no tags is admitted trivially (it
// can only ever have one series per (slug, metric) pair).
func (d *CardinalityDam) Admit(slug, metric string, tags map[string]string) (overflowingTag string, admitted bool) {
	if d == nil {
		return "", true
	}
	if slug == "" || metric == "" {
		return "", true
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// First pass: figure out which tag would be the first to overflow.
	// We need this BEFORE we mutate state — otherwise an admit/reject
	// race against the same metric would leave an inconsistent set
	// (some tags recorded, the overflowing one not, and a later retry
	// with a different value still over budget).
	//
	// Walk the tags in Go's intentional non-deterministic order — the
	// "which tag overflowed first" report is for human-readable
	// logging, not control flow; it doesn't have to be stable across
	// runs.
	for k, v := range tags {
		// Empty-string tag values are admitted: they're the typical
		// "unset" sentinel and don't contribute meaningful cardinality.
		if v == "" {
			continue
		}
		count, alreadyHave := d.countValues(slug, metric, k, v)
		if alreadyHave {
			continue
		}
		if count >= d.limit {
			return k, false
		}
	}

	// Second pass: actually record. We re-enter the nested maps,
	// creating each level on demand. This is also where the cardinality
	// counter goes up.
	bySlug, ok := d.state[slug]
	if !ok {
		bySlug = make(map[string]map[string]map[string]struct{})
		d.state[slug] = bySlug
	}
	byMetric, ok := bySlug[metric]
	if !ok {
		byMetric = make(map[string]map[string]struct{})
		bySlug[metric] = byMetric
	}
	for k, v := range tags {
		if v == "" {
			continue
		}
		values, ok := byMetric[k]
		if !ok {
			values = make(map[string]struct{})
			byMetric[k] = values
		}
		values[v] = struct{}{}
	}
	return "", true
}

// countValues returns the current count of distinct values seen for
// (slug, metric, tagKey) and whether `value` is already in the set.
// Called under d.mu.
func (d *CardinalityDam) countValues(slug, metric, tagKey, value string) (count int, alreadyHave bool) {
	bySlug, ok := d.state[slug]
	if !ok {
		return 0, false
	}
	byMetric, ok := bySlug[metric]
	if !ok {
		return 0, false
	}
	values, ok := byMetric[tagKey]
	if !ok {
		return 0, false
	}
	if _, ok := values[value]; ok {
		return len(values), true
	}
	return len(values), false
}

// Forget drops all state for slug. Called from the runtime when a
// plugin is uninstalled so its cardinality budget resets cleanly.
// Idempotent.
func (d *CardinalityDam) Forget(slug string) {
	if d == nil || slug == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.state, slug)
}

// Snapshot returns the current per-(slug, metric, tag) distinct-value
// counts. Used by admin endpoints and tests; the returned structure
// is a copy and may be mutated freely.
func (d *CardinalityDam) Snapshot() map[string]map[string]map[string]int {
	out := make(map[string]map[string]map[string]int)
	if d == nil {
		return out
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for slug, bySlug := range d.state {
		mSlug := make(map[string]map[string]int, len(bySlug))
		for metric, byMetric := range bySlug {
			mMetric := make(map[string]int, len(byMetric))
			for tagKey, values := range byMetric {
				mMetric[tagKey] = len(values)
			}
			mSlug[metric] = mMetric
		}
		out[slug] = mSlug
	}
	return out
}
