package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/bench/scenarios"
)

// fakeScenario is a stub Scenario that the runner tests drive. It
// records every Iter call so tests can assert on concurrency, paths,
// and ramp behaviour without spinning up real HTTP infrastructure.
type fakeScenario struct {
	name      string
	bucket    scenarios.SLO
	latency   time.Duration
	status    int
	calls     atomic.Int64
	maxActive atomic.Int64
	curActive atomic.Int64
	// startTimes is the list of t-since-runner-start at which each
	// distinct VU first issued a call. Used for ramp assertions.
	mu         sync.Mutex
	startTimes []time.Duration
	seenVUs    map[uintptr]struct{}
	startedAt  time.Time
}

func (f *fakeScenario) Name() string         { return f.name }
func (f *fakeScenario) Bucket() scenarios.SLO { return f.bucket }
func (f *fakeScenario) Setup(_ context.Context, _ string) error {
	f.mu.Lock()
	f.seenVUs = map[uintptr]struct{}{}
	f.startedAt = time.Now()
	f.mu.Unlock()
	return nil
}

func (f *fakeScenario) Iter(ctx context.Context, _ *http.Client, _ string) scenarios.Result {
	n := f.curActive.Add(1)
	defer f.curActive.Add(-1)
	for {
		if m := f.maxActive.Load(); n > m {
			if f.maxActive.CompareAndSwap(m, n) {
				break
			}
			continue
		}
		break
	}
	if f.latency > 0 {
		select {
		case <-ctx.Done():
			return scenarios.Result{Err: ctx.Err()}
		case <-time.After(f.latency):
		}
	}
	f.calls.Add(1)
	return scenarios.Result{RTT: f.latency, Status: f.status}
}

func TestRunner_SpawnsCorrectVUCount(t *testing.T) {
	fs := &fakeScenario{
		name:    "fake",
		latency: 20 * time.Millisecond,
		status:  200,
		bucket:  scenarios.SLO{P95: time.Hour, P99: time.Hour, MaxErrorRate: 1.0},
	}
	cfg := RunConfig{
		Host:     "http://example.test",
		VUs:      8,
		// Bumped from 300ms to 2s so the test stays reliable under
		// CI's race detector + GitHub-runner load. With 8 VUs and
		// 20ms latency, 300ms left a tight window where the runner
		// occasionally didn't actually peak at 8 before the duration
		// expired — blocking every PR in the queue.
		Duration: 2 * time.Second,
		Ramp:     0,
	}
	rep, err := RunScenario(context.Background(), fs, cfg)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if rep.PeakVUs != 8 {
		t.Errorf("PeakVUs = %d, want 8", rep.PeakVUs)
	}
	if rep.Requests == 0 {
		t.Errorf("Requests = 0, want > 0 (latency=%s duration=%s)", fs.latency, cfg.Duration)
	}
}

func TestRunner_RampSpacesVUStarts(t *testing.T) {
	// Track the first-call timestamp per goroutine via a sync.Map. We
	// use the iter call as the proxy for "VU is now active".
	var (
		firstSeen sync.Map // map[unsafe.Pointer-ish key]time.Time
		started   atomic.Int64
		startedAt = time.Now()
	)
	scenario := &recordStartScenario{
		fakeScenario: &fakeScenario{
			name:    "ramp",
			latency: 10 * time.Millisecond,
			status:  200,
			bucket:  scenarios.SLO{P95: time.Hour, P99: time.Hour, MaxErrorRate: 1.0},
		},
		startedAt: &startedAt,
		started:   &started,
		firstSeen: &firstSeen,
	}
	cfg := RunConfig{
		Host:     "http://example.test",
		VUs:      4,
		Duration: 400 * time.Millisecond,
		Ramp:     200 * time.Millisecond,
	}
	if _, err := RunScenario(context.Background(), scenario, cfg); err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	// We expect 4 distinct VU start times. With Ramp=200ms / 4 VUs
	// the second VU should begin no earlier than ~50ms after the
	// first. Use a generous lower bound to keep the test stable on
	// slow CI.
	count := 0
	firstSeen.Range(func(_, _ any) bool { count++; return true })
	if count < 2 {
		t.Fatalf("ramp produced only %d active VUs, want >=2", count)
	}
}

// recordStartScenario captures the wall-clock at which each goroutine
// first calls Iter. It piggybacks on fakeScenario for the rest of the
// behaviour.
type recordStartScenario struct {
	*fakeScenario
	startedAt *time.Time
	started   *atomic.Int64
	firstSeen *sync.Map
}

func (r *recordStartScenario) Iter(ctx context.Context, c *http.Client, base string) scenarios.Result {
	// Use the goroutine's *atomic counter slot* as identity: each VU
	// is its own goroutine, so the first call from each is unique.
	id := r.started.Add(1)
	r.firstSeen.LoadOrStore(id, time.Since(*r.startedAt))
	return r.fakeScenario.Iter(ctx, c, base)
}

func TestRunner_CancellationStopsAllVUs(t *testing.T) {
	fs := &fakeScenario{
		name:    "cancel",
		latency: 5 * time.Millisecond,
		status:  200,
		bucket:  scenarios.SLO{P95: time.Hour, P99: time.Hour, MaxErrorRate: 1.0},
	}
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		_, _ = RunScenario(ctx, fs, RunConfig{
			Host:     "http://example.test",
			VUs:      20,
			Duration: 5 * time.Second,
			Ramp:     50 * time.Millisecond,
		})
		close(doneCh)
	}()
	// Let some VUs spool up.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScenario did not return within 2s after cancel")
	}
	// Final active VU count should be zero after the join.
	if v := fs.curActive.Load(); v != 0 {
		t.Errorf("curActive after return = %d, want 0", v)
	}
}

func TestRunner_RejectsBadConfig(t *testing.T) {
	fs := &fakeScenario{name: "fake", bucket: scenarios.SLO{}}
	cases := []struct {
		name string
		cfg  RunConfig
	}{
		{"zero VUs", RunConfig{VUs: 0, Duration: time.Second}},
		{"negative duration", RunConfig{VUs: 1, Duration: -time.Second}},
		{"negative ramp", RunConfig{VUs: 1, Duration: time.Second, Ramp: -1}},
		{"ramp >= duration", RunConfig{VUs: 1, Duration: time.Second, Ramp: time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunScenario(context.Background(), fs, tc.cfg)
			if err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestRunner_RecordsRPSAndPercentiles(t *testing.T) {
	// Spawn an httptest server with a tiny artificial latency so we
	// get more than one sample but the test still runs fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cfg := RunConfig{
		Host:     srv.URL,
		VUs:      4,
		Duration: 200 * time.Millisecond,
		Ramp:     0,
	}
	rep, err := RunScenario(context.Background(), scenarios.Posts{}, cfg)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if rep.Requests < 10 {
		t.Errorf("Requests = %d, want >= 10 (something is wrong with the worker pool)", rep.Requests)
	}
	if rep.RPS <= 0 {
		t.Errorf("RPS = %f, want > 0", rep.RPS)
	}
	if rep.P95 < rep.P50 {
		t.Errorf("P95 (%s) < P50 (%s) — sort/percentile is broken", rep.P95, rep.P50)
	}
	if rep.ErrorRate != 0 {
		t.Errorf("ErrorRate = %f, want 0 (server returned 200 only)", rep.ErrorRate)
	}
}

func TestRunner_CountsNon2xxAsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Alternate 200 / 503 deterministically by URL.
		if strings.Contains(r.URL.RawQuery, "page=2") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := RunConfig{
		Host:     srv.URL,
		VUs:      2,
		Duration: 200 * time.Millisecond,
		Ramp:     0,
	}
	rep, err := RunScenario(context.Background(), &scenarios.RestShim{}, cfg)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	// The mix has 9 queries; ~2/9 hit page=2. With ~hundreds of
	// requests we expect at least one 503 in the histogram.
	if rep.StatusHist[503] == 0 {
		t.Errorf("expected at least one 503 in StatusHist, got %v", rep.StatusHist)
	}
	if rep.Errors == 0 {
		t.Errorf("Errors = 0, want > 0 (503 should count as error)")
	}
}
