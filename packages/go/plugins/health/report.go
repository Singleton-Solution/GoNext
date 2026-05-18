package health

import (
	"fmt"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Report is the per-plugin health snapshot the HTTP handler renders
// as JSON. It is intentionally flat and self-describing so an admin
// UI can grok it without joining against other endpoints.
//
// All numeric fields are non-negative. A plugin that has never run
// returns Invocations == 0 and Latency == zero (P50/P95/P99 all 0);
// the handler does NOT 404 in that case — an installed-but-quiet
// plugin is a legitimate health state.
type Report struct {
	// Plugin is the slug the report belongs to.
	Plugin string `json:"plugin"`

	// Invocations is the cumulative dispatch count across all
	// hooks for this plugin (sum of every result-labelled series
	// for the plugin).
	Invocations uint64 `json:"invocations"`

	// Errors is the count of non-OK invocations — every result
	// except ResultOK contributes. Operators read this as the
	// numerator for an error-rate ratio.
	Errors uint64 `json:"errors"`

	// Traps is the cumulative trap count (sum of every
	// reason-labelled series for the plugin). Traps are also
	// counted in Errors via the "trap" result label on the
	// invocations counter, but the distinct number is useful for
	// dashboards.
	Traps uint64 `json:"traps"`

	// CapabilityDenied is the cumulative denial count.
	CapabilityDenied uint64 `json:"capability_denied"`

	// Latency carries the in-process-computed percentiles.
	Latency LatencyReport `json:"latency"`

	// RecentTraps is the newest-first slice of trap events from
	// the in-memory ring. Capped at DefaultRingSize.
	RecentTraps []TrapEvent `json:"recent_traps"`
}

// LatencyReport carries the three canonical latency percentiles. All
// values are in seconds, matching the histogram's native unit.
// Zero means "no observations yet" rather than "every call was
// instantaneous"; the handler does not normalise the two.
type LatencyReport struct {
	P50 float64 `json:"p50_seconds"`
	P95 float64 `json:"p95_seconds"`
	P99 float64 `json:"p99_seconds"`
}

// BuildReport composes a Report for the plugin from the Recorder's
// state. It reads the Prometheus collectors directly (via the
// dto.Metric Write API) so the same numbers are visible at
// /api/v1/plugins/{name}/health and /metrics.
//
// This is intentionally in-process: a real-time admin dashboard
// shouldn't have to query an external Prometheus deployment to find
// out whether a plugin just trapped. The cost is per-call linear in
// the number of histogram buckets, which is small (HTTPLatencyBuckets
// has 12 entries).
func (r *recorder) BuildReport(plugin string) (Report, error) {
	rep := Report{Plugin: plugin}

	inv, errs, err := readInvocationSums(r.metrics.invocations, plugin)
	if err != nil {
		return Report{}, fmt.Errorf("read invocations: %w", err)
	}
	rep.Invocations = inv
	rep.Errors = errs

	traps, err := readCounterByPlugin(r.metrics.traps, plugin)
	if err != nil {
		return Report{}, fmt.Errorf("read traps: %w", err)
	}
	rep.Traps = traps

	denials, err := readCounterByPlugin(r.metrics.capabilityDenials, plugin)
	if err != nil {
		return Report{}, fmt.Errorf("read denials: %w", err)
	}
	rep.CapabilityDenied = denials

	lat, err := readHistogramQuantiles(r.metrics.duration, plugin)
	if err != nil {
		return Report{}, fmt.Errorf("read latency: %w", err)
	}
	rep.Latency = lat

	rep.RecentTraps = r.RecentTraps(plugin)
	return rep, nil
}

// drainCollector pulls every prometheus.Metric a Collector emits into
// a slice. We use a buffered channel sized for typical vec widths
// (24) and grow naturally if a particular Vec carries more series.
//
// The Collect method is the documented way to read live values out
// of a *CounterVec / *HistogramVec without a registry round-trip.
// dto.Metric is the wire shape; we walk the labels to filter by
// plugin and read Counter or Histogram off the same struct.
func drainCollector(c prometheus.Collector) []prometheus.Metric {
	ch := make(chan prometheus.Metric, 24)
	done := make(chan struct{})
	var out []prometheus.Metric
	go func() {
		for m := range ch {
			out = append(out, m)
		}
		close(done)
	}()
	c.Collect(ch)
	close(ch)
	<-done
	return out
}

// labelValue returns the value of the named label, or "" if absent.
func labelValue(dtom *dto.Metric, name string) string {
	for _, l := range dtom.Label {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// readInvocationSums walks the invocations counter and returns
// (total, errors) for the plugin. "errors" is every series whose
// result label is not ResultOK.
func readInvocationSums(vec *prometheus.CounterVec, plugin string) (uint64, uint64, error) {
	var total, errors uint64
	for _, m := range drainCollector(vec) {
		var dtom dto.Metric
		if err := m.Write(&dtom); err != nil {
			return 0, 0, err
		}
		if labelValue(&dtom, "plugin") != plugin {
			continue
		}
		if dtom.Counter == nil {
			continue
		}
		v := uint64(dtom.Counter.GetValue())
		total += v
		if labelValue(&dtom, "result") != ResultOK {
			errors += v
		}
	}
	return total, errors, nil
}

// readCounterByPlugin sums every series of vec whose plugin label
// equals plugin. Used for traps and capability denials.
func readCounterByPlugin(vec *prometheus.CounterVec, plugin string) (uint64, error) {
	var sum uint64
	for _, m := range drainCollector(vec) {
		var dtom dto.Metric
		if err := m.Write(&dtom); err != nil {
			return 0, err
		}
		if labelValue(&dtom, "plugin") != plugin {
			continue
		}
		if dtom.Counter == nil {
			continue
		}
		sum += uint64(dtom.Counter.GetValue())
	}
	return sum, nil
}

// histBucket is one (upper-bound, cumulative-count) pair for the
// quantile interpolation. Kept as a named type so the helper
// functions below can pass it around without naked structs in their
// signatures.
type histBucket struct {
	upper      float64
	cumulative uint64
}

// readHistogramQuantiles computes the P50/P95/P99 percentiles from
// the duration histogram for the plugin. We aggregate every hook's
// histogram into a single bucket map first, then interpolate the
// quantiles linearly within the bucket that crosses the target.
//
// Linear interpolation is the same algorithm Prometheus's
// histogram_quantile uses; the implementation is short enough to
// inline rather than vendoring promql.
func readHistogramQuantiles(vec *prometheus.HistogramVec, plugin string) (LatencyReport, error) {
	var count uint64
	bucketIndex := map[float64]uint64{}
	for _, m := range drainCollector(vec) {
		var dtom dto.Metric
		if err := m.Write(&dtom); err != nil {
			return LatencyReport{}, err
		}
		if labelValue(&dtom, "plugin") != plugin {
			continue
		}
		if dtom.Histogram == nil {
			continue
		}
		count += dtom.Histogram.GetSampleCount()
		for _, b := range dtom.Histogram.Bucket {
			bucketIndex[b.GetUpperBound()] += b.GetCumulativeCount()
		}
	}
	if count == 0 || len(bucketIndex) == 0 {
		return LatencyReport{}, nil
	}

	buckets := make([]histBucket, 0, len(bucketIndex))
	for ub, cum := range bucketIndex {
		buckets = append(buckets, histBucket{upper: ub, cumulative: cum})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].upper < buckets[j].upper })

	return LatencyReport{
		P50: interpolateQuantile(buckets, count, 0.50),
		P95: interpolateQuantile(buckets, count, 0.95),
		P99: interpolateQuantile(buckets, count, 0.99),
	}, nil
}

// interpolateQuantile is a textbook linear interpolation across
// cumulative histogram buckets. q is the quantile in [0, 1]; count
// is the total observation count. Returns 0 for an empty histogram.
func interpolateQuantile(buckets []histBucket, count uint64, q float64) float64 {
	if count == 0 || len(buckets) == 0 {
		return 0
	}
	rank := q * float64(count)
	var prevUpper, prevCum float64
	for _, b := range buckets {
		if float64(b.cumulative) >= rank {
			if b.upper == 0 {
				return 0
			}
			delta := float64(b.cumulative) - prevCum
			if delta <= 0 {
				return prevUpper
			}
			return prevUpper + (b.upper-prevUpper)*(rank-prevCum)/delta
		}
		prevUpper = b.upper
		prevCum = float64(b.cumulative)
	}
	// Quantile beyond every finite bucket — return the last finite
	// upper bound (operators read "above 10s" rather than "+Inf").
	return buckets[len(buckets)-1].upper
}
