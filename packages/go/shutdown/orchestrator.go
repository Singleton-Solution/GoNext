package shutdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// defaultBudget is used when Options.Budget is zero. Mirrors the
// httpx.Server fallback and matches K8s' default terminationGracePeriod.
// Holding both at 30s keeps the two pieces of code in agreement: the
// orchestrator finishes within the same window the pod has before
// SIGKILL.
const defaultBudget = 30 * time.Second

// minPerStepBudget is the floor each step gets even when the remaining
// budget is exhausted. Without a floor, a late-running drain (e.g. ctx
// already fired) would invoke every remaining closer with a 0-duration
// context, which most resource clients translate into "give up
// immediately" — defeating the best-effort guarantee we promise. 100ms
// is small enough to never extend the total drain meaningfully, large
// enough that an in-process Close (no I/O) almost always succeeds.
const minPerStepBudget = 100 * time.Millisecond

// Closer is the function shape every registered resource provides.
// It receives a context whose deadline is the orchestrator's per-step
// slice; closers are expected to respect that deadline and return
// promptly on cancellation. Returning a non-nil error does NOT stop
// the drain — subsequent closers still run (see Drain).
type Closer func(ctx context.Context) error

// Options configure an Orchestrator.
type Options struct {
	// Log receives structured drain events ("draining", "closed",
	// "step exceeded budget"). Required. Use a discard logger in tests
	// that don't care about output.
	Log *slog.Logger

	// Budget is the total time the orchestrator has to drain every
	// registered closer. Defaults to 30s if zero. The budget is split
	// proportionally across the remaining closers at the moment Drain
	// is called, so a 30s budget with 6 closers gives each ~5s — but a
	// fast closer "donates" its unused time to the next one (see Drain).
	Budget time.Duration

	// Signals lists the OS signals that trigger a Wait()-driven drain.
	// Defaults to {SIGINT, SIGTERM}. Override only if you need to add
	// SIGHUP-driven reloads or similar; the empty slice means "no
	// signal handling, drain on ctx cancel only".
	Signals []os.Signal
}

// step is one registered (name, closer) pair plus its measured duration
// once the drain has run. Stored in registration order; reversed at
// drain time.
type step struct {
	name   string
	closer Closer
}

// State is the lifecycle marker for an Orchestrator. We use it instead
// of a bool to distinguish "draining now" from "drain finished" — the
// difference matters for Register, which is rejected in either state
// but with different log levels.
type State int

const (
	// StateReady is the initial state. Register is accepted; Drain has
	// not yet been called.
	StateReady State = iota

	// StateDraining means Drain is in flight on some goroutine. New
	// Register calls are rejected with ErrDraining; concurrent Drain
	// calls block on the first one (see Drain).
	StateDraining

	// StateDone means Drain has returned (cleanly or with errors).
	// Register calls are rejected; subsequent Drain calls return the
	// same error as the first.
	StateDone
)

// ErrDraining is returned by Register when called after Drain has
// started. Registering during drain is a programming error — the order
// of resource shutdown is critical, and slipping a new resource in
// mid-drain would put it either first or last with no semantic
// justification.
var ErrDraining = errors.New("shutdown: registration rejected, drain in progress or complete")

// Orchestrator drains a set of registered resources on a shared time
// budget. See package doc for the design rationale.
//
// An Orchestrator is intended to be used exactly once per process: New,
// then Register several times, then either Drain (caller-driven) or
// Wait (signal-driven). It is safe to call Register concurrently from
// multiple goroutines during startup (the registration mutex covers it),
// but typical wiring is sequential in main().
type Orchestrator struct {
	log     *slog.Logger
	budget  time.Duration
	signals []os.Signal

	mu    sync.Mutex
	steps []step
	state State

	// drainOnce makes Drain idempotent. Multiple goroutines (e.g. a
	// signal handler and a parent ctx cancellation) may race to call
	// Drain; only the first invocation runs the closers, and subsequent
	// callers see the result via drainErr.
	drainOnce sync.Once
	drainErr  error
}

// New constructs an Orchestrator. Returns an error if Options is
// missing a logger (the orchestrator's whole job is to produce a
// readable shutdown trace; a nil logger silently swallows it).
func New(opts Options) (*Orchestrator, error) {
	if opts.Log == nil {
		return nil, errors.New("shutdown.New: Log is required")
	}
	budget := opts.Budget
	if budget <= 0 {
		budget = defaultBudget
	}
	signals := opts.Signals
	if signals == nil {
		signals = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}
	return &Orchestrator{
		log:     opts.Log,
		budget:  budget,
		signals: signals,
	}, nil
}

// Register adds a Closer to the drain list. Closers are invoked in
// REVERSE registration order at drain time (LIFO), so the call order
// of Register reads as the startup order — same as `defer`.
//
// Register returns ErrDraining if Drain has started or finished. It is
// safe to call from multiple goroutines.
//
// name is used for log lines and error wrapping; choose something
// recognizable in production logs ("http.server", "db.pool"). Duplicate
// names are allowed — the orchestrator does not deduplicate (you may
// legitimately have two pools), but tests should avoid collisions for
// readability.
func (o *Orchestrator) Register(name string, closer Closer) error {
	if closer == nil {
		return fmt.Errorf("shutdown.Register: closer for %q is nil", name)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state != StateReady {
		return ErrDraining
	}
	o.steps = append(o.steps, step{name: name, closer: closer})
	return nil
}

// MustRegister calls Register and logs at error level if the
// registration is rejected. Use this in main() for closers that cannot
// fail to register (the orchestrator was just created and no Drain has
// run) — it removes the per-call boilerplate of capturing and wrapping
// the error.
//
// MustRegister never panics; rejected closers are simply orphaned (the
// caller still owns the resource and may close it via defer). This
// matches the "shutdown must be best-effort" stance of the rest of the
// package.
func (o *Orchestrator) MustRegister(log *slog.Logger, name string, closer Closer) {
	if err := o.Register(name, closer); err != nil {
		log.Error("shutdown: register rejected",
			slog.String("step", name),
			slog.String("err", err.Error()),
		)
	}
}

// Wait blocks until ctx is canceled OR one of the configured signals
// arrives, then runs Drain. This is the typical entry point for main():
//
//	if err := orch.Wait(ctx); err != nil { ... }
//
// Wait returns whatever Drain returns. If ctx fires first, the drain
// uses a detached context with the full budget — we honor the budget
// even when the caller has already given up, so the pod still drains
// within its terminationGracePeriod window instead of being killed
// immediately.
func (o *Orchestrator) Wait(ctx context.Context) error {
	// signalCtx fires when any of the configured signals arrives.
	// Cleanup stops the signal handler regardless of how we exit, which
	// matters in tests where Wait may be called repeatedly across
	// scenarios within the same process.
	signalCtx, stop := signal.NotifyContext(ctx, o.signals...)
	defer stop()

	<-signalCtx.Done()

	reason := "context canceled"
	if errors.Is(signalCtx.Err(), context.Canceled) && ctx.Err() == nil {
		reason = "signal received"
	}
	o.log.Info("shutdown: drain triggered", slog.String("reason", reason))

	// Detached context: honor the full budget even if the parent ctx
	// has already fired. signal.NotifyContext cancels signalCtx as
	// soon as the signal arrives, so we cannot reuse it here.
	drainCtx, cancel := context.WithTimeout(context.Background(), o.budget)
	defer cancel()
	return o.Drain(drainCtx)
}

// Drain invokes every registered closer in reverse registration order
// (LIFO) and returns the first error encountered. ALL closers run, even
// if earlier ones fail — Drain is best-effort, because in the shutdown
// path the alternative is dropping in-flight work.
//
// Each closer gets a derived context with its own per-step deadline,
// computed from the remaining time in the orchestrator's total budget.
// If the supplied ctx is canceled mid-drain, remaining closers receive
// a short-budget context (minPerStepBudget) so they at least get a
// chance to flush in-process state.
//
// Drain is idempotent: subsequent calls return the same error as the
// first call without re-running the closers.
func (o *Orchestrator) Drain(ctx context.Context) error {
	o.drainOnce.Do(func() {
		o.drainErr = o.drain(ctx)
	})
	return o.drainErr
}

// drain is the unsynchronized worker behind Drain.Do. Splitting it out
// keeps the locking discipline clear: drainOnce serializes invocations,
// and inside drain we briefly take o.mu to snapshot the step list and
// flip the state.
func (o *Orchestrator) drain(ctx context.Context) error {
	o.mu.Lock()
	if o.state != StateReady {
		o.mu.Unlock()
		// Shouldn't happen — drainOnce guards single invocation — but
		// defensive: if someone calls Drain after Wait already drained,
		// surface a clean error rather than running closers twice.
		return errors.New("shutdown: already drained")
	}
	o.state = StateDraining
	// Snapshot the steps so we don't hold the lock while running
	// closers (which may be slow and would otherwise block any
	// concurrent Register calls — which should fail fast with
	// ErrDraining, not block).
	steps := make([]step, len(o.steps))
	copy(steps, o.steps)
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		o.state = StateDone
		o.mu.Unlock()
	}()

	start := time.Now()
	o.log.Info("shutdown: draining",
		slog.Int("steps", len(steps)),
		slog.Duration("budget", o.budget),
	)

	var firstErr error
	var errs []error

	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		// Per-step budget: split the *remaining* time across the
		// *remaining* steps. Earlier closers that finish fast donate
		// their unused slice to whoever runs next. Floor at
		// minPerStepBudget so even an exhausted budget still gives
		// each closer a chance.
		remainingBudget := o.budget - time.Since(start)
		remainingSteps := i + 1
		stepBudget := remainingBudget / time.Duration(remainingSteps)
		if stepBudget < minPerStepBudget {
			stepBudget = minPerStepBudget
		}

		// If the caller's ctx is canceled, switch to a detached
		// short-budget ctx. We do NOT propagate the canceled ctx
		// directly because that would tell every remaining closer
		// "give up immediately" — and the whole point of best-effort
		// drain is that in-process cleanup (flushing buffers, writing
		// the last audit record) still runs.
		stepCtx, cancel := context.WithTimeout(detachIfCanceled(ctx), stepBudget)
		stepStart := time.Now()
		err := callCloser(stepCtx, s.closer)
		cancel()
		dur := time.Since(stepStart)

		if err != nil {
			o.log.Error("shutdown: step failed",
				slog.String("step", s.name),
				slog.Duration("duration", dur),
				slog.Duration("budget", stepBudget),
				slog.String("err", err.Error()),
			)
			wrapped := fmt.Errorf("shutdown: %s: %w", s.name, err)
			errs = append(errs, wrapped)
			if firstErr == nil {
				firstErr = wrapped
			}
			continue
		}
		o.log.Info("shutdown: step done",
			slog.String("step", s.name),
			slog.Duration("duration", dur),
			slog.Duration("budget", stepBudget),
		)
	}

	total := time.Since(start)
	if firstErr != nil {
		o.log.Warn("shutdown: drain completed with errors",
			slog.Duration("total", total),
			slog.Int("errors", len(errs)),
		)
		// errors.Join collects every error so callers that care about
		// all failures (e.g. test assertions) can unwrap with
		// errors.Is; firstErr is preserved as the head error for "what
		// went wrong first" logging.
		return errors.Join(errs...)
	}
	o.log.Info("shutdown: drain complete", slog.Duration("total", total))
	return nil
}

// callCloser invokes c and recovers from panics. A panicking closer
// should not abort the rest of the drain — converting the panic to an
// error lets the orchestrator log it and move on. Without this guard,
// one buggy resource client (e.g. a Close that nil-derefs after a
// half-initialized New) would prevent every later closer from running.
func callCloser(ctx context.Context, c Closer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c(ctx)
}

// detachIfCanceled returns ctx if it's still alive, or a fresh
// Background() context if it's canceled. Used so a canceled parent ctx
// doesn't poison every per-step context with an instant deadline.
func detachIfCanceled(ctx context.Context) context.Context {
	if ctx.Err() == nil {
		return ctx
	}
	return context.Background()
}

// CloserFromIO adapts an io.Closer to the shutdown.Closer signature.
// The wrapper ignores the supplied context — io.Closer has no
// cancellation contract — but the orchestrator's per-step timeout
// still bounds wall-clock time via its enclosing deadline.
//
// Use this for resource clients whose Close() takes no argument:
//
//	orch.Register("redis", shutdown.CloserFromIO(rdb))
func CloserFromIO(c io.Closer) Closer {
	return func(_ context.Context) error {
		return c.Close()
	}
}

// CloserFromFunc adapts a no-arg, no-return cleanup function (the
// common pgxpool.Pool.Close signature) to the Closer shape.
//
//	orch.Register("db.pool", shutdown.CloserFromFunc(pool.Close))
func CloserFromFunc(f func()) Closer {
	return func(_ context.Context) error {
		f()
		return nil
	}
}
