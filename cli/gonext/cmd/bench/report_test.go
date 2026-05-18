package bench

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

func TestPercentile_KnownInputs(t *testing.T) {
	// 10 samples 100..1000ms.
	samples := []time.Duration{}
	for i := 1; i <= 10; i++ {
		samples = append(samples, time.Duration(i)*100*time.Millisecond)
	}
	cases := []struct {
		q    float64
		want time.Duration
	}{
		// nearest-rank: idx = ceil(q*N)-1
		{0.5, 500 * time.Millisecond},  // ceil(5)-1 = 4  -> 500
		{0.95, 1000 * time.Millisecond}, // ceil(9.5)-1 = 9 -> 1000
		{0.99, 1000 * time.Millisecond}, // ceil(9.9)-1 = 9 -> 1000
		{0.0, 100 * time.Millisecond},
		{1.0, 1000 * time.Millisecond},
	}
	for _, tc := range cases {
		got := percentile(samples, tc.q)
		if got != tc.want {
			t.Errorf("percentile(q=%v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestPercentile_EmptyReturnsZero(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil) = %v, want 0", got)
	}
}

func TestPercentile_LargeSampleSet(t *testing.T) {
	// 1000 samples 1..1000ms.
	samples := make([]time.Duration, 1000)
	for i := range samples {
		samples[i] = time.Duration(i+1) * time.Millisecond
	}
	// p50 over 1..1000 should be the 500th smallest = 500ms.
	if got := percentile(samples, 0.50); got != 500*time.Millisecond {
		t.Errorf("p50 = %v, want 500ms", got)
	}
	if got := percentile(samples, 0.95); got != 950*time.Millisecond {
		t.Errorf("p95 = %v, want 950ms", got)
	}
	if got := percentile(samples, 0.99); got != 990*time.Millisecond {
		t.Errorf("p99 = %v, want 990ms", got)
	}
}

func TestWriteText_RendersHeader(t *testing.T) {
	rep := Report{
		Scenario:  "homepage",
		Bucket:    scenarios.SLO{P95: 250 * time.Millisecond, P99: 500 * time.Millisecond, MaxErrorRate: 0.01},
		Requests:  100,
		Errors:    0,
		RPS:       50.0,
		P50:       80 * time.Millisecond,
		P95:       180 * time.Millisecond,
		P99:       220 * time.Millisecond,
		ErrorRate: 0.0,
		SLO:       SLOVerdict{Passed: true},
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, []Report{rep}); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	s := buf.String()
	for _, want := range []string{"SCENARIO", "RPS", "homepage", "PASS"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing %q\n%s", want, s)
		}
	}
}

func TestWriteText_PrintsViolationsOnFail(t *testing.T) {
	rep := Report{
		Scenario:  "posts",
		Bucket:    scenarios.SLO{P95: 100 * time.Millisecond, MaxErrorRate: 0.01},
		Requests:  10,
		Errors:    2,
		RPS:       5.0,
		P50:       50 * time.Millisecond,
		P95:       200 * time.Millisecond,
		ErrorRate: 0.2,
		StatusHist: map[int]int{200: 8, 500: 2},
	}
	EvaluateSLO(&rep)
	var buf bytes.Buffer
	if err := WriteText(&buf, []Report{rep}); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	s := buf.String()
	for _, want := range []string{"FAIL", "violations", "p95", "status histogram", "500"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing %q\n%s", want, s)
		}
	}
}

func TestWriteJSON_RoundTrips(t *testing.T) {
	in := []Report{{
		Scenario: "homepage",
		Bucket:   scenarios.SLO{P95: 250 * time.Millisecond, P99: 500 * time.Millisecond, MaxErrorRate: 0.01},
		Requests: 10,
		RPS:      5.0,
		P95:      time.Millisecond * 100,
		SLO:      SLOVerdict{Passed: true},
	}}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, in); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var out []Report
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if len(out) != 1 || out[0].Scenario != "homepage" {
		t.Errorf("round-trip lost data: %+v", out)
	}
}
