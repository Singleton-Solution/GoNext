package rum

import (
	"math"
	"strings"
	"testing"
)

func TestValidateBatch_Empty(t *testing.T) {
	t.Parallel()
	if err := validateBatch(Batch{}); err == nil {
		t.Fatal("expected error for empty batch")
	}
}

func TestValidateBatch_TooLarge(t *testing.T) {
	t.Parallel()
	events := make([]Event, MaxBatchSize+1)
	for i := range events {
		events[i] = goodEvent()
	}
	if err := validateBatch(Batch{Events: events}); err == nil {
		t.Fatal("expected error for oversize batch")
	}
}

func TestValidateBatch_ExactlyMax(t *testing.T) {
	t.Parallel()
	events := make([]Event, MaxBatchSize)
	for i := range events {
		events[i] = goodEvent()
	}
	if err := validateBatch(Batch{Events: events}); err != nil {
		t.Fatalf("expected MaxBatchSize batch to validate; got %v", err)
	}
}

func TestValidateEvent_UnknownMetric(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.Metric = "ZZZ"
	if err := validateEvent(e); err == nil {
		t.Fatal("expected unknown metric to fail")
	}
}

func TestValidateEvent_UnknownRating(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.Rating = "fast"
	if err := validateEvent(e); err == nil {
		t.Fatal("expected unknown rating to fail")
	}
}

func TestValidateEvent_NonFiniteValue(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		e := goodEvent()
		e.Value = v
		if err := validateEvent(e); err == nil {
			t.Fatalf("expected non-finite value (%v) to fail", v)
		}
	}
}

func TestValidateEvent_NegativeValue(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.Value = -1
	if err := validateEvent(e); err == nil {
		t.Fatal("expected negative value to fail")
	}
}

func TestValidateEvent_EmptyPagePath(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.PagePath = ""
	if err := validateEvent(e); err == nil {
		t.Fatal("expected empty page_path to fail")
	}
}

func TestValidateEvent_LongPagePath(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.PagePath = strings.Repeat("/x", MaxPagePathLen)
	if err := validateEvent(e); err == nil {
		t.Fatal("expected oversize page_path to fail")
	}
}

func TestValidateEvent_LongSessionID(t *testing.T) {
	t.Parallel()
	e := goodEvent()
	e.SessionID = strings.Repeat("a", MaxSessionIDLen+1)
	if err := validateEvent(e); err == nil {
		t.Fatal("expected oversize session_id to fail")
	}
}

func TestValidateEvent_BadCountry(t *testing.T) {
	t.Parallel()
	long := "ABCDE"
	e := goodEvent()
	e.Country = &long
	if err := validateEvent(e); err == nil {
		t.Fatal("expected oversize country to fail")
	}
}

func TestValidateEvent_BadConn(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", MaxConnLen+1)
	e := goodEvent()
	e.Conn = &long
	if err := validateEvent(e); err == nil {
		t.Fatal("expected oversize conn to fail")
	}
}

func TestValidateEvent_AllMetrics(t *testing.T) {
	t.Parallel()
	for _, m := range allowedMetrics {
		e := goodEvent()
		e.Metric = m
		if err := validateEvent(e); err != nil {
			t.Fatalf("expected %s to validate; got %v", m, err)
		}
	}
}

func TestValidateEvent_AllRatings(t *testing.T) {
	t.Parallel()
	for _, r := range allowedRatings {
		e := goodEvent()
		e.Rating = r
		if err := validateEvent(e); err != nil {
			t.Fatalf("expected rating %s to validate; got %v", r, err)
		}
	}
}

func TestIsFinite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want bool
	}{
		{0, true},
		{-0, true},
		{1.5, true},
		{1e30, true},
		{math.NaN(), false},
		{math.Inf(1), false},
		{math.Inf(-1), false},
	}
	for _, tc := range cases {
		if got := isFinite(tc.in); got != tc.want {
			t.Fatalf("isFinite(%v) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

// goodEvent returns an Event that passes validateEvent. Tests
// mutate one field at a time to exercise a specific edge.
func goodEvent() Event {
	return Event{
		Metric:    "LCP",
		Value:     2500,
		Rating:    "needs-improvement",
		PagePath:  "/",
		SessionID: "abcd1234",
	}
}
