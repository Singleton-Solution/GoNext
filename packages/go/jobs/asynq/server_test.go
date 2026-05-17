package asynq_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	miniredisv2 "github.com/alicebob/miniredis/v2"
	upstream "github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	jobsasynq "github.com/Singleton-Solution/GoNext/packages/go/jobs/asynq"
)

// quiet returns a slog logger that discards all output. Same pattern as
// packages/go/redis tests — the chassis is verbose at info level and we
// don't want tests to spam stderr.
func quiet() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// miniredisOpt boots an in-process Redis (miniredis) and returns an
// Asynq connection option pointed at it. The miniredis instance is
// torn down by t.Cleanup so tests don't leak goroutines or ports.
//
// Why miniredis: Asynq drives Redis hard at boot (it pings, reads queue
// stats, registers a server entry). A unit-style test wants to exercise
// the chassis without standing up a real Redis container. miniredis is
// good enough — Asynq's wire protocol compatibility tests already cover
// the protocol-level edge cases this package would otherwise re-test.
func miniredisOpt(t *testing.T) upstream.RedisConnOpt {
	t.Helper()
	m := miniredisv2.RunT(t)
	return upstream.RedisClientOpt{Addr: m.Addr()}
}

// TestNewBuildsServer verifies the construction path lights up without
// panicking and returns a non-nil (server, mux) pair. The asynq
// constructor has historically panicked on bad RedisConnOpt types and
// on certain Config combinations; a smoke test catches those before
// they hit a CI machine running the full integration suite.
func TestNewBuildsServer(t *testing.T) {
	srv, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{Logger: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv == nil || mux == nil {
		t.Fatalf("New: got nil server or mux (server=%v, mux=%v)", srv, mux)
	}
	if srv.Asynq() == nil {
		t.Fatal("Server.Asynq() returned nil")
	}
	if srv.Mux() != mux {
		t.Fatal("Server.Mux() should return the same instance as the second return value of New")
	}
}

// TestNewRequiresRedisOpt is a contract test: a nil RedisConnOpt
// reaches asynq.NewServer where it panics. The chassis rejects nil at
// the front door so the error is actionable.
func TestNewRequiresRedisOpt(t *testing.T) {
	_, _, err := jobsasynq.New(nil, jobsasynq.Config{Logger: quiet()})
	if err == nil {
		t.Fatal("New(nil opt): want error, got nil")
	}
}

// TestNewRequiresLogger guards against passing a zero Config — the
// asynq constructor would crash later inside the slog adapter.
func TestNewRequiresLogger(t *testing.T) {
	_, _, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{})
	if err == nil {
		t.Fatal("New(no logger): want error, got nil")
	}
}

// TestMuxHandleRoundTrip exercises the mux returned by New end-to-end:
// register a handler, dispatch a synthetic task, observe the handler
// fired. This is the chassis equivalent of an http.ServeMux smoke test
// — if it breaks, every downstream task package breaks with it.
func TestMuxHandleRoundTrip(t *testing.T) {
	_, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{Logger: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var called bool
	mux.HandleFunc("email:send", func(_ context.Context, task *upstream.Task) error {
		called = true
		if task.Type() != "email:send" {
			t.Errorf("handler got Type %q, want %q", task.Type(), "email:send")
		}
		return nil
	})

	task := upstream.NewTask("email:send", []byte(`{"to":"a@b"}`))
	if err := mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if !called {
		t.Fatal("registered handler was not invoked")
	}
}

// TestMuxNotFoundFallback validates the package's headline contract:
// unknown task types route to Asynq's NotFound handler (which returns
// ErrHandlerNotFound), not to a panic. Without this, an unknown task
// would crash the worker pool.
func TestMuxNotFoundFallback(t *testing.T) {
	_, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{Logger: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	task := upstream.NewTask("nope:unknown", nil)
	err = mux.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("ProcessTask on unknown type: want error, got nil")
	}
	if !errors.Is(err, upstream.ErrHandlerNotFound) {
		t.Errorf("ProcessTask error should wrap ErrHandlerNotFound; got %v", err)
	}
}

// TestMetricsRegistered checks the four collectors land on the
// supplied registry under their canonical names. Dashboards and alert
// rules reference these names, so renames must go through a deliberate
// review (this test catches the typo case).
//
// Strategy: a Prometheus registry rejects a second collector with the
// same fully-qualified name regardless of label set. We attempt to
// re-register a probe with each canonical name and assert the registry
// already has one. The registry returns
// prometheus.AlreadyRegisteredError on collision, which is unambiguous.
// (A non-AlreadyRegistered error — e.g. "same fqName, different help" —
// is also evidence the name is taken, so we treat any non-nil Register
// error as the metric being present.)
func TestMetricsRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, _, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:  quiet(),
		Metrics: reg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	names := []string{
		"gonext_jobs_processed_total",
		"gonext_jobs_failed_total",
		"gonext_jobs_inflight",
		"gonext_jobs_unknown_total",
	}
	for _, name := range names {
		probe := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: "probe"})
		regErr := reg.Register(probe)
		if regErr == nil {
			// Registration succeeded → the name was free → the chassis
			// failed to register it. Clean up so the rest of the test
			// continues without state leakage.
			reg.Unregister(probe)
			t.Errorf("metric %q not registered by jobsasynq.New", name)
		}
	}
}

// TestMetricsNilRegistryNoOps confirms that omitting Metrics is
// supported — unit tests using New shouldn't have to build a registry
// just to silence a nil-deref.
func TestMetricsNilRegistryNoOps(t *testing.T) {
	srv, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{Logger: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux.HandleFunc("noop", func(context.Context, *upstream.Task) error { return nil })
	if err := mux.ProcessTask(context.Background(), upstream.NewTask("noop", nil)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if !srv.Healthy() {
		t.Error("Healthy(): want true during boot grace, got false")
	}
}

// TestHealthyBootGrace and TestHealthyTracksRecorded together describe
// the readiness contract. Healthy() starts true (we don't want the
// first /readyz probe to fail purely because Asynq hasn't completed
// its first health check yet), then mirrors the most recent recorded
// outcome. The state is observed via the public Healthy() method —
// we don't poke internals.
func TestHealthyBootGrace(t *testing.T) {
	srv, _, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:              quiet(),
		HealthCheckInterval: 50 * time.Millisecond, // doesn't affect boot grace
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !srv.Healthy() {
		t.Fatal("Healthy(): want true during boot grace, got false")
	}
}

// TestCloseIsNonBlocking sanity-checks that Close on a never-started
// server returns promptly and doesn't deadlock. asynq's Stop+Shutdown
// no-ops on a non-running server; we want to confirm that contract
// holds at the chassis boundary too.
func TestCloseIsNonBlocking(t *testing.T) {
	srv, _, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{Logger: quiet()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = srv.Close(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s on a never-started server")
	}
}

// TestRunStopsOnClose drives the lifecycle end-to-end against
// miniredis: Run blocks, Close triggers Stop+Shutdown, Run returns.
// This is the contract main.go relies on; if Run hangs after Close,
// the worker fails to drain on SIGTERM.
func TestRunStopsOnClose(t *testing.T) {
	srv, _, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:              quiet(),
		ShutdownTimeout:     500 * time.Millisecond,
		HealthCheckInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr <- srv.Run()
	}()

	// Give Asynq a moment to actually start its workers; without this
	// Stop is a no-op (it only stops the Active state) and Shutdown
	// races the boot path.
	time.Sleep(300 * time.Millisecond)

	if err := srv.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s after Close")
	}
	wg.Wait()
}

// counterValue extracts the float value of a single-series CounterVec
// (queue label). Returns 0 when no series exists yet — collectors
// don't materialize a series until WithLabelValues bumps it.
func counterValue(t *testing.T, reg *prometheus.Registry, name, queue string) float64 {
	t.Helper()
	got, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range got {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if hasLabel(m, "queue", queue) {
				return m.Counter.GetValue()
			}
		}
	}
	return 0
}

func hasLabel(m *dto.Metric, key, value string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == key && l.GetValue() == value {
			return true
		}
	}
	return false
}

// TestUnknownTaskBumpsUnknownCounter covers the chassis's
// distinguishing feature: the NotFound dispatch increments
// gonext_jobs_unknown_total, not gonext_jobs_failed_total. (Failed
// also bumps because Asynq still counts it as a handler error — both
// are correct.) The middleware uses errors.Is, so wrapping it in
// fmt.Errorf elsewhere wouldn't break this assertion.
func TestUnknownTaskBumpsUnknownCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:  quiet(),
		Metrics: reg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Process via the mux directly so we don't need to enqueue through
	// a real client (which would require richer Redis emulation than
	// miniredis offers for some Asynq commands). The middleware is
	// applied by mux.Handler() regardless of the dispatch path.
	err = mux.ProcessTask(context.Background(), upstream.NewTask("does:not:exist", nil))
	if err == nil || !errors.Is(err, upstream.ErrHandlerNotFound) {
		t.Fatalf("ProcessTask: want ErrHandlerNotFound, got %v", err)
	}

	// Asynq's GetQueueName returns "" without a server context, so the
	// middleware falls back to QueueDefault — that's the label we
	// expect on the counter.
	if got := counterValue(t, reg, "gonext_jobs_unknown_total", "default"); got != 1 {
		t.Errorf("unknown_total{queue=default} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "gonext_jobs_failed_total", "default"); got != 1 {
		t.Errorf("failed_total{queue=default} = %v, want 1 (NotFound still counts as failure)", got)
	}
	if got := counterValue(t, reg, "gonext_jobs_processed_total", "default"); got != 0 {
		t.Errorf("processed_total{queue=default} = %v, want 0", got)
	}
}

// TestSuccessIncrementsProcessed exercises the happy path through the
// metrics middleware: the handler returns nil, processed gets bumped,
// failed/unknown stay at 0. Together with TestUnknownTaskBumpsUnknownCounter
// this gives full coverage of the three observation paths.
func TestSuccessIncrementsProcessed(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:  quiet(),
		Metrics: reg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux.HandleFunc("ok", func(context.Context, *upstream.Task) error { return nil })

	if err := mux.ProcessTask(context.Background(), upstream.NewTask("ok", nil)); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if got := counterValue(t, reg, "gonext_jobs_processed_total", "default"); got != 1 {
		t.Errorf("processed_total{queue=default} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "gonext_jobs_failed_total", "default"); got != 0 {
		t.Errorf("failed_total{queue=default} = %v, want 0", got)
	}
}

// TestFailureIncrementsFailedNotUnknown distinguishes "handler ran and
// returned error" from "no handler found". The two cases share an
// outcome (Asynq retries, then archives) but are diagnostically very
// different and the chassis preserves that distinction in metrics.
func TestFailureIncrementsFailedNotUnknown(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, mux, err := jobsasynq.New(miniredisOpt(t), jobsasynq.Config{
		Logger:  quiet(),
		Metrics: reg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := errors.New("kaboom")
	mux.HandleFunc("boom", func(context.Context, *upstream.Task) error { return want })

	err = mux.ProcessTask(context.Background(), upstream.NewTask("boom", nil))
	if !errors.Is(err, want) {
		t.Fatalf("ProcessTask: got %v, want %v", err, want)
	}
	if got := counterValue(t, reg, "gonext_jobs_failed_total", "default"); got != 1 {
		t.Errorf("failed_total{queue=default} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "gonext_jobs_unknown_total", "default"); got != 0 {
		t.Errorf("unknown_total{queue=default} = %v, want 0 (real handler error, not NotFound)", got)
	}
}

// TestQueueConstantsExported keeps the queue names from drifting via
// import — if a refactor renames QueueCritical to QueueCrit, this
// test breaks before the renamed symbol reaches the dashboards.
func TestQueueConstantsExported(t *testing.T) {
	names := []string{
		jobsasynq.QueueCritical,
		jobsasynq.QueueWebhook,
		jobsasynq.QueueEmail,
		jobsasynq.QueueMedia,
		jobsasynq.QueueMigration,
		jobsasynq.QueuePlugin,
		jobsasynq.QueueDefault,
	}
	want := strings.Join([]string{"critical", "webhook", "email", "media", "migration", "plugin", "default"}, ",")
	got := strings.Join(names, ",")
	if got != want {
		t.Fatalf("queue constants: got %q, want %q", got, want)
	}
}
