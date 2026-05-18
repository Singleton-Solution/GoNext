package rum

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestMemoryStore_InsertEmptyNoop(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if err := s.Insert(context.Background(), time.Now(), nil); err != nil {
		t.Fatalf("expected nil; got %v", err)
	}
}

func TestMemoryStore_PercentilesEmpty(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	res, err := s.Percentiles(context.Background(), "LCP", "", time.Unix(0, 0), time.Unix(3600, 0))
	if err != nil {
		t.Fatalf("expected no error on empty store; got %v", err)
	}
	if res.Sample != 0 || res.P50 != 0 || res.P75 != 0 || res.P95 != 0 {
		t.Fatalf("expected zero result; got %+v", res)
	}
}

func TestMemoryStore_PercentilesMonotonic(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ingest := time.Unix(1_000_000, 0)
	events := make([]Event, 0, 100)
	for i := 1; i <= 100; i++ {
		events = append(events, Event{
			Metric:    "LCP",
			Value:     float64(i),
			Rating:    "good",
			PagePath:  "/",
			SessionID: "s",
		})
	}
	if err := s.Insert(context.Background(), ingest, events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := s.Percentiles(context.Background(), "LCP", "", ingest.Add(-time.Hour), ingest.Add(time.Hour))
	if err != nil {
		t.Fatalf("percentiles: %v", err)
	}
	if res.Sample != 100 {
		t.Fatalf("expected n=100; got %d", res.Sample)
	}
	// Linear-interp percentile on [1..100]: p50 ≈ 50.5, p75 ≈ 75.25, p95 ≈ 95.05.
	if !approx(res.P50, 50.5, 0.1) {
		t.Fatalf("p50 = %v; want ≈50.5", res.P50)
	}
	if !approx(res.P75, 75.25, 0.1) {
		t.Fatalf("p75 = %v; want ≈75.25", res.P75)
	}
	if !approx(res.P95, 95.05, 0.1) {
		t.Fatalf("p95 = %v; want ≈95.05", res.P95)
	}
}

func TestMemoryStore_PercentilesPathFilter(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	ingest := time.Unix(1_000_000, 0)
	events := []Event{
		{Metric: "LCP", Value: 100, Rating: "good", PagePath: "/", SessionID: "a"},
		{Metric: "LCP", Value: 200, Rating: "good", PagePath: "/", SessionID: "b"},
		{Metric: "LCP", Value: 999, Rating: "poor", PagePath: "/other", SessionID: "c"},
	}
	if err := s.Insert(context.Background(), ingest, events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := s.Percentiles(context.Background(), "LCP", "/", ingest.Add(-time.Hour), ingest.Add(time.Hour))
	if err != nil {
		t.Fatalf("percentiles: %v", err)
	}
	if res.Sample != 2 {
		t.Fatalf("expected path filter to leave 2 rows; got %d", res.Sample)
	}
	if res.P95 == 999 {
		t.Fatal("expected /other row to be excluded under path=/ filter")
	}
}

func TestMemoryStore_PercentilesTimeFilter(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	now := time.Unix(2_000_000, 0)
	if err := s.Insert(context.Background(), now.Add(-2*time.Hour), []Event{
		{Metric: "LCP", Value: 999, Rating: "poor", PagePath: "/", SessionID: "old"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.Insert(context.Background(), now, []Event{
		{Metric: "LCP", Value: 100, Rating: "good", PagePath: "/", SessionID: "new"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := s.Percentiles(context.Background(), "LCP", "", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("percentiles: %v", err)
	}
	if res.Sample != 1 {
		t.Fatalf("expected window to leave 1 row; got %d", res.Sample)
	}
}

func TestMemoryStore_PercentilesBadInput(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if _, err := s.Percentiles(context.Background(), "", "", time.Now(), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected error for empty metric")
	}
	now := time.Now()
	if _, err := s.Percentiles(context.Background(), "LCP", "", now.Add(time.Hour), now); err == nil {
		t.Fatal("expected error for inverted time window")
	}
}

func TestMemoryStore_SlowestRoutes(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	now := time.Unix(3_000_000, 0)
	events := []Event{}
	// /fast — 5 rows around 100ms (low p75).
	for i := 0; i < 5; i++ {
		events = append(events, Event{Metric: "LCP", Value: 100 + float64(i), Rating: "good", PagePath: "/fast", SessionID: "s"})
	}
	// /slow — 5 rows around 5000ms (high p75).
	for i := 0; i < 5; i++ {
		events = append(events, Event{Metric: "LCP", Value: 5000 + float64(i*100), Rating: "poor", PagePath: "/slow", SessionID: "s"})
	}
	// /flake — only 2 rows; should be excluded by minSampleForSlowest.
	events = append(events,
		Event{Metric: "LCP", Value: 9000, Rating: "poor", PagePath: "/flake", SessionID: "s"},
		Event{Metric: "LCP", Value: 9000, Rating: "poor", PagePath: "/flake", SessionID: "s"},
	)
	if err := s.Insert(context.Background(), now, events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := s.SlowestRoutes(context.Background(), "LCP", now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("slowest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 routes (flake excluded); got %d (%+v)", len(rows), rows)
	}
	if rows[0].Path != "/slow" {
		t.Fatalf("expected /slow first; got %s", rows[0].Path)
	}
	if rows[0].P75 <= rows[1].P75 {
		t.Fatalf("expected descending p75; got %v, %v", rows[0].P75, rows[1].P75)
	}
}

func TestMemoryStore_SlowestRoutesLimit(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	now := time.Unix(4_000_000, 0)
	events := []Event{}
	for path := 0; path < 5; path++ {
		for i := 0; i < 3; i++ {
			events = append(events, Event{
				Metric:    "LCP",
				Value:     float64((5 - path) * 1000),
				Rating:    "needs-improvement",
				PagePath:  "/p" + string(rune('0'+path)),
				SessionID: "s",
			})
		}
	}
	if err := s.Insert(context.Background(), now, events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := s.SlowestRoutes(context.Background(), "LCP", now.Add(-time.Hour), now.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("slowest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected limit=2; got %d", len(rows))
	}
}

func TestMemoryStore_SlowestRoutesBadInput(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	if _, err := s.SlowestRoutes(context.Background(), "", time.Now(), time.Now().Add(time.Hour), 10); err == nil {
		t.Fatal("expected error for empty metric")
	}
	now := time.Now()
	if _, err := s.SlowestRoutes(context.Background(), "LCP", now.Add(time.Hour), now, 10); err == nil {
		t.Fatal("expected error for inverted time window")
	}
}

func TestPercentile_Endpoints(t *testing.T) {
	t.Parallel()
	if got := percentile(nil, 0.5); got != 0 {
		t.Fatalf("expected percentile(nil)=0; got %v", got)
	}
	sorted := []float64{1, 2, 3, 4, 5}
	if got := percentile(sorted, 0); got != 1 {
		t.Fatalf("expected p=0 -> 1; got %v", got)
	}
	if got := percentile(sorted, 1); got != 5 {
		t.Fatalf("expected p=1 -> 5; got %v", got)
	}
	// Out-of-range clamping.
	if got := percentile(sorted, -0.5); got != 1 {
		t.Fatalf("expected clamp on p<0; got %v", got)
	}
	if got := percentile(sorted, 1.5); got != 5 {
		t.Fatalf("expected clamp on p>1; got %v", got)
	}
}

func approx(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}
