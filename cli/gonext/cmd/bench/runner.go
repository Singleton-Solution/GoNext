package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

// RunConfig is the knob-set for one scenario run. The CLI parses flags
// into this struct.
type RunConfig struct {
	// Host is the base URL passed to each scenario's Iter. The
	// homepage scenario will swap in GONEXT_WEB_BASE_URL if set.
	Host string
	// VUs is the maximum concurrent virtual users.
	VUs int
	// Duration is the wall-clock run time, measured from the moment
	// the first VU starts.
	Duration time.Duration
	// Ramp is the linear ramp-up interval. If zero all VUs start at
	// once. Must be < Duration.
	Ramp time.Duration
}

// RunScenario executes a single Scenario under cfg and returns the
// aggregated Report. The function blocks until either the duration
// elapses or ctx is cancelled. On cancel the runner stops every VU and
// returns whatever samples it already had — that path is exercised by
// TestRunner_CancellationStopsAllVUs.
//
// The runner intentionally uses one HTTP client per scenario (built by
// scenarios.DefaultClient) so connection-pool reuse across VUs works
// the same way k6 amortises connections.
func RunScenario(ctx context.Context, s scenarios.Scenario, cfg RunConfig) (Report, error) {
	if cfg.VUs <= 0 {
		return Report{}, errors.New("VUs must be > 0")
	}
	if cfg.Duration <= 0 {
		return Report{}, errors.New("Duration must be > 0")
	}
	if cfg.Ramp < 0 {
		return Report{}, errors.New("Ramp must be >= 0")
	}
	if cfg.Ramp >= cfg.Duration {
		return Report{}, errors.New("Ramp must be < Duration")
	}

	// Resolve the effective base URL per scenario. Homepage talks to
	// the Next.js front-end; the REST/login scenarios talk to the API.
	baseURL := cfg.Host
	if s.Name() == "homepage" {
		if v := os.Getenv("GONEXT_WEB_BASE_URL"); v != "" {
			baseURL = v
		}
	}

	client := scenarios.DefaultClient()
	defer client.CloseIdleConnections()

	setupCtx, cancelSetup := context.WithTimeout(ctx, 15*time.Second)
	if err := s.Setup(setupCtx, baseURL); err != nil {
		cancelSetup()
		return Report{}, fmt.Errorf("setup: %w", err)
	}
	cancelSetup()

	// runCtx is the context every VU shares. It is cancelled either
	// when the duration elapses or when the parent ctx is cancelled.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	samples := newSampleCollector()

	// startVU is the per-goroutine main loop.
	startVU := func(id int) func() {
		return func() {
			for {
				select {
				case <-runCtx.Done():
					return
				default:
				}
				res := s.Iter(runCtx, client, baseURL)
				// Ignore samples produced *after* cancellation —
				// they're cancellation noise, not load measurements.
				if runCtx.Err() != nil {
					return
				}
				samples.add(res)
			}
		}
	}

	// Stagger VU starts over Ramp. The schedule is linear: with N VUs
	// and a Ramp of R, VU i starts at i * R/N. When Ramp is zero every
	// VU starts immediately. This matches the spirit of k6's
	// `stages: [{duration: Ramp, target: VUs}]`.
	var wg sync.WaitGroup
	wg.Add(cfg.VUs)
	startedAt := time.Now()
	// activeMax records the peak active-VU count for the report.
	var activeMax atomic.Int64
	var activeCur atomic.Int64
	step := time.Duration(0)
	if cfg.VUs > 1 && cfg.Ramp > 0 {
		step = cfg.Ramp / time.Duration(cfg.VUs)
	}
	for i := 0; i < cfg.VUs; i++ {
		i := i
		go func() {
			defer wg.Done()
			if step > 0 {
				// Sleep until this VU's start slot — but bail early
				// if the run is already over.
				wait := step * time.Duration(i)
				select {
				case <-runCtx.Done():
					return
				case <-time.After(wait):
				}
			}
			n := activeCur.Add(1)
			for {
				if m := activeMax.Load(); n > m {
					activeMax.CompareAndSwap(m, n)
				}
				break
			}
			defer activeCur.Add(-1)
			startVU(i)()
		}()
	}

	// Stop after Duration. If parent ctx is cancelled first the timer
	// fires harmlessly afterwards.
	deadline := time.NewTimer(cfg.Duration)
	defer deadline.Stop()
	select {
	case <-deadline.C:
	case <-ctx.Done():
	}
	cancelRun()

	// Bound the join: even if a VU is wedged in a Body.Read with a
	// long timeout we want the function to return within ~1s so the
	// CLI is responsive on Ctrl-C. The HTTP client's per-request
	// timeout is the backstop for genuinely stuck reads.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		// Some VUs are still in-flight. They will exit when their
		// outstanding request completes (or hits the client timeout).
		// We do not block the caller further.
	}

	wallElapsed := time.Since(startedAt)
	rep := samples.finalize(s, wallElapsed)
	rep.Config = cfg
	rep.PeakVUs = int(activeMax.Load())
	return rep, nil
}

// sampleCollector is a tiny concurrent-safe accumulator. We could use
// a channel-fan-in, but a mutex + slice is simpler and the lock is
// only taken for a couple of pointer assignments per request — well
// below the cost of the HTTP round-trip we're measuring.
type sampleCollector struct {
	mu      sync.Mutex
	rtts    []time.Duration
	statuses []int
	errs    int
	non2xx  int
	count   int64
	startTS time.Time
}

func newSampleCollector() *sampleCollector {
	return &sampleCollector{startTS: time.Now()}
}

func (c *sampleCollector) add(r scenarios.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.rtts = append(c.rtts, r.RTT)
	c.statuses = append(c.statuses, r.Status)
	if r.Err != nil {
		c.errs++
		return
	}
	if r.Status < 200 || r.Status >= 300 {
		c.non2xx++
	}
}

func (c *sampleCollector) finalize(s scenarios.Scenario, wall time.Duration) Report {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Sort once for percentile math.
	rtts := make([]time.Duration, len(c.rtts))
	copy(rtts, c.rtts)
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })

	rep := Report{
		Scenario: s.Name(),
		Bucket:   s.Bucket(),
		Requests: int(c.count),
		Errors:   c.errs + c.non2xx,
		Wall:     wall,
	}
	if c.count > 0 {
		rep.RPS = float64(c.count) / wall.Seconds()
		rep.P50 = percentile(rtts, 0.50)
		rep.P95 = percentile(rtts, 0.95)
		rep.P99 = percentile(rtts, 0.99)
		rep.ErrorRate = float64(rep.Errors) / float64(rep.Requests)
		// Status histogram — useful when the SLO check trips and the
		// reader wants to know whether it was 429s or 503s.
		hist := map[int]int{}
		for _, st := range c.statuses {
			hist[st]++
		}
		rep.StatusHist = hist
	}
	return rep
}

// percentile returns the q-percentile of an already-sorted slice using
// the "nearest rank" method. We deliberately avoid linear interpolation
// — it makes assertions in tests harder for synthetic samples and the
// extra precision is not meaningful at the sample counts a bench run
// produces.
//
// Exported so report.go can reuse it on the test path.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	// nearest-rank: index = ceil(q * N) - 1, clamped.
	idx := int(q*float64(len(sorted)) + 0.999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

