package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

// Report is the aggregated outcome of one scenario run. It is the
// payload of `gonext bench --output json`, so the field tags are part
// of the public surface — do not rename them without a release note.
type Report struct {
	Scenario   string             `json:"scenario"`
	Bucket     scenarios.SLO      `json:"bucket"`
	Config     RunConfig          `json:"config"`
	PeakVUs    int                `json:"peak_vus"`
	Requests   int                `json:"requests"`
	Errors     int                `json:"errors"`
	ErrorRate  float64            `json:"error_rate"`
	RPS        float64            `json:"rps"`
	P50        time.Duration      `json:"p50_ns"`
	P95        time.Duration      `json:"p95_ns"`
	P99        time.Duration      `json:"p99_ns"`
	Wall       time.Duration      `json:"wall_ns"`
	StatusHist map[int]int        `json:"status_hist"`
	SLO        SLOVerdict         `json:"slo"`
}

// WriteText prints a compact, human-readable table for one or more
// reports. The layout is dense on purpose — a screenshot of one run
// fits in a code review comment.
func WriteText(w io.Writer, reps []Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCENARIO\tREQS\tRPS\tP50\tP95\tP99\tERR%\tSLO")
	for _, r := range reps {
		fmt.Fprintf(tw,
			"%s\t%d\t%.1f\t%s\t%s\t%s\t%.2f\t%s\n",
			r.Scenario,
			r.Requests,
			r.RPS,
			fmtDur(r.P50), fmtDur(r.P95), fmtDur(r.P99),
			r.ErrorRate*100,
			sloLabel(r.SLO),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// SLO breakdown — only printed when at least one report failed,
	// so a green run stays at one table.
	for _, r := range reps {
		if r.SLO.Passed {
			continue
		}
		fmt.Fprintf(w, "\n%s SLO violations:\n", r.Scenario)
		for _, v := range r.SLO.Violations {
			fmt.Fprintf(w, "  - %s\n", v)
		}
		// Show the top status codes when the error budget popped — it
		// is almost always the first thing the reader wants.
		if r.Errors > 0 && len(r.StatusHist) > 0 {
			fmt.Fprintln(w, "  status histogram:")
			type kv struct {
				k int
				v int
			}
			pairs := make([]kv, 0, len(r.StatusHist))
			for k, v := range r.StatusHist {
				pairs = append(pairs, kv{k, v})
			}
			sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
			for _, p := range pairs {
				label := fmt.Sprintf("%d", p.k)
				if p.k == 0 {
					label = "transport-err"
				}
				fmt.Fprintf(w, "    %s: %d\n", label, p.v)
			}
		}
	}
	return nil
}

// WriteJSON emits the reports as a pretty-printed JSON array. Stable
// for scripting against.
func WriteJSON(w io.Writer, reps []Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(reps)
}

// fmtDur prints durations with one decimal of ms, which is the
// resolution at which p95/p99 values are interesting. Stays under 8
// chars so the tabwriter stays narrow.
func fmtDur(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}

func sloLabel(v SLOVerdict) string {
	if v.Passed {
		return "PASS"
	}
	return "FAIL"
}
