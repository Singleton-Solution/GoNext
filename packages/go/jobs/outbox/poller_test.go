package outbox

import (
	"testing"
	"time"
)

// TestPoller_BackoffFor pins the exponential-with-cap behaviour so a
// future tweak to the schedule is visible in the diff. The function
// is pure, so we test it directly rather than threading it through
// the SQL layer.
func TestPoller_BackoffFor(t *testing.T) {
	p := &Poller{
		BackoffMin: 1 * time.Second,
		BackoffMax: 8 * time.Second,
	}
	cases := []struct {
		prev int
		want time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 8 * time.Second}, // capped
		{100, 8 * time.Second},
		{-1, 1 * time.Second}, // defensive: negative attempts clamps to 0
	}
	for _, tc := range cases {
		got := p.backoffFor(tc.prev)
		if got != tc.want {
			t.Errorf("backoffFor(%d): got %v want %v", tc.prev, got, tc.want)
		}
	}
}

// TestPoller_BackoffFor_Defaults covers the zero-value fallback.
func TestPoller_BackoffFor_Defaults(t *testing.T) {
	p := &Poller{}
	if got := p.backoffFor(0); got != DefaultBackoffMin {
		t.Errorf("backoffFor(0) with zero config: got %v want %v", got, DefaultBackoffMin)
	}
	// Large attempt count should clamp to DefaultBackoffMax.
	if got := p.backoffFor(50); got != DefaultBackoffMax {
		t.Errorf("backoffFor(50) with zero config: got %v want %v", got, DefaultBackoffMax)
	}
}

// TestPoller_BackoffFor_InvertedClampsToMin guards against an
// operator configuring max < min. We fall back to min for both rather
// than producing nonsense or panicking.
func TestPoller_BackoffFor_InvertedClampsToMin(t *testing.T) {
	p := &Poller{
		BackoffMin: 10 * time.Second,
		BackoffMax: 1 * time.Second,
	}
	// First call returns BackoffMin (the floor). Second doubles to
	// 20s, which exceeds max(=10s after the swap inside the function)
	// so it returns max — which is now equal to min.
	if got := p.backoffFor(0); got != 10*time.Second {
		t.Errorf("backoffFor(0) inverted: got %v", got)
	}
	if got := p.backoffFor(5); got != 10*time.Second {
		t.Errorf("backoffFor(5) inverted: got %v", got)
	}
}

func TestPoller_BatchSize_Default(t *testing.T) {
	p := &Poller{}
	if got := p.batchSize(); got != DefaultBatchSize {
		t.Errorf("batchSize default: got %d want %d", got, DefaultBatchSize)
	}
	p.BatchSize = 7
	if got := p.batchSize(); got != 7 {
		t.Errorf("batchSize override: got %d want 7", got)
	}
}

func TestPoller_LeaseSec_Default(t *testing.T) {
	p := &Poller{}
	if got := p.leaseSec(); got != DefaultClaimLeaseSec {
		t.Errorf("leaseSec default: got %d want %d", got, DefaultClaimLeaseSec)
	}
	p.ClaimLeaseSec = 42
	if got := p.leaseSec(); got != 42 {
		t.Errorf("leaseSec override: got %d want 42", got)
	}
}

func TestPoller_Now_RespectsNowFunc(t *testing.T) {
	fixed := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	p := &Poller{NowFunc: func() time.Time { return fixed }}
	if got := p.now(); !got.Equal(fixed) {
		t.Errorf("now: got %v want %v", got, fixed)
	}
}

func TestPoller_Validate(t *testing.T) {
	// Each required field individually missing should error. Poller
	// embeds an atomic counter so we can't shallow-copy — build a
	// fresh value per case instead.
	t.Run("ok", func(t *testing.T) {
		good := &Poller{
			Pool:     &fakePool{},
			Enqueuer: &recordingEnqueuer{},
			WorkerID: "test",
		}
		if err := good.validate(); err != nil {
			t.Errorf("validate: %v", err)
		}
	})
	t.Run("missing Pool", func(t *testing.T) {
		p := &Poller{Enqueuer: &recordingEnqueuer{}, WorkerID: "x"}
		if err := p.validate(); err == nil {
			t.Error("expected error for missing Pool")
		}
	})
	t.Run("missing Enqueuer", func(t *testing.T) {
		p := &Poller{Pool: &fakePool{}, WorkerID: "x"}
		if err := p.validate(); err == nil {
			t.Error("expected error for missing Enqueuer")
		}
	})
	t.Run("missing WorkerID", func(t *testing.T) {
		p := &Poller{Pool: &fakePool{}, Enqueuer: &recordingEnqueuer{}}
		if err := p.validate(); err == nil {
			t.Error("expected error for missing WorkerID")
		}
	})
}
