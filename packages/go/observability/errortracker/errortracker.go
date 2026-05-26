// Package errortracker is a thin wrapper around getsentry/sentry-go that
// gives the GoNext binaries a single place to wire error reporting,
// flush on shutdown, and plugin-aware grouping.
//
// Contract:
//
//   - When the configured DSN is empty (the default), the package
//     installs a no-op tracker. Every Capture call returns immediately
//     and the Shutdown closer is a no-op. The rest of the codebase
//     calls into the package unconditionally; a partially-deployed
//     observability rollout costs nothing in the unset-DSN case.
//
//   - When the DSN is set (typically via GONEXT_SENTRY_DSN), the package
//     calls sentry.Init with a sensible production default set
//     (release = build version, environment = config env, server_name
//     = service+hostname). The returned Shutdown drains the in-flight
//     event queue via sentry.Flush.
//
//   - Capture/CaptureMessage/Recover accept a ctx so the caller can
//     thread plugin context through. WithPluginSlug stamps a
//     gonext.plugin.slug tag onto the event; the host pipeline reads
//     this tag for plugin-aware grouping (every event from
//     plugin:acme-seo groups separately from host code) and the
//     pre-built dashboards filter on it.
//
// The package deliberately does NOT expose sentry-go primitives (Hub,
// Scope, Event) — callers should be able to swap GlitchTip / a vendored
// reporter / a different SaaS without touching every handler. The
// surface here is: Init, Shutdown, Capture, CaptureMessage, Recover,
// WithPluginSlug. Anything more leaks into callers and locks us into
// sentry-go for the lifetime of the project.
//
// Issue #202.
package errortracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
)

// DSNEnv is the env var read when Options.DSN is empty. Exported so the
// `--print-config` dump can surface the active value alongside its
// sibling knobs (tracing.EndpointEnv, etc.).
const DSNEnv = "GONEXT_SENTRY_DSN"

// TagPluginSlug is the tag key set by WithPluginSlug. Exported so
// dashboards and alert routing rules in the operations runbook can
// reference one source of truth.
const TagPluginSlug = "gonext.plugin.slug"

// defaultFlushTimeout bounds the Shutdown flush. The Asynq worker's
// drain budget is 4 minutes and the API's is 30 seconds; either way,
// the error-tracker is a non-essential closer (we'd rather lose a
// handful of events than wedge the drain). 5 seconds is enough to
// flush a healthy queue against the Sentry SaaS over the public
// internet, and short enough to never noticeably slow shutdown.
const defaultFlushTimeout = 5 * time.Second

// Options configures Init. Every field has a defaulting policy so an
// empty Options value is valid for "boot with the env-var-derived
// defaults".
type Options struct {
	// DSN is the Sentry/GlitchTip ingest URL. When empty, Init reads
	// the DSNEnv variable. When both are empty, the package installs
	// the no-op tracker and Shutdown is a no-op.
	DSN string

	// Environment populates the `environment` tag on every event. Set
	// from cfg.Env so events filter by deployment tier in the
	// dashboard. Required when DSN is set; empty fails Init validation
	// to prevent silent "events with no environment" bugs.
	Environment string

	// Release populates the `release` tag. Set from buildinfo.Version
	// so events group by code revision; the binary that produced the
	// event is identifiable without correlating with a separate
	// deployment log.
	Release string

	// ServerName populates the `server_name` tag. Typically the
	// service name + os.Hostname() (e.g. "api@web-7d4f"); the package
	// fills in os.Hostname() when this is empty.
	ServerName string

	// SampleRate is the fraction of events to send. Defaults to 1.0
	// (every event). Operators tune this down during noisy incidents
	// without redeploying — see docs/10-observability.md §6 for the
	// runbook.
	SampleRate float64

	// FlushTimeout bounds the in-flight event queue drain in Shutdown.
	// Defaults to defaultFlushTimeout.
	FlushTimeout time.Duration

	// Logger receives one-line boot diagnostics and shutdown
	// completion lines. Required — a misconfigured DSN should be
	// loud, not silent.
	Logger *slog.Logger

	// Debug enables sentry-go's debug stdout writer. Off by default;
	// useful only when chasing a "did my event reach Sentry?" issue
	// in a controlled environment.
	Debug bool

	// initFunc is a seam for testing: the production caller leaves it
	// nil and Init uses sentry.Init; tests inject a fake to verify
	// the ClientOptions composition without standing up an HTTP
	// transport. Unexported so the seam doesn't bleed into the
	// public API.
	initFunc func(sentry.ClientOptions) error
}

// Tracker is the package's exported surface. One instance per binary;
// constructed by Init. Methods are safe for concurrent use.
//
// The no-op tracker (returned when DSN is empty) is a value-receiver
// pointer with enabled=false; every method short-circuits before any
// sentry-go call so there's zero overhead on the unset-DSN path.
type Tracker struct {
	enabled      bool
	flushTimeout time.Duration
	logger       *slog.Logger
}

// Init constructs the Tracker, installs the sentry-go global client
// when enabled, and returns a Shutdown closer.
//
// When opts.DSN (or the DSNEnv fallback) is empty, the returned
// Tracker is the no-op tracker and the Shutdown closer is a no-op.
// Production wiring registers the Shutdown closer with the shutdown
// orchestrator unconditionally — the no-op path costs nothing.
func Init(opts Options) (*Tracker, Shutdown, error) {
	if opts.Logger == nil {
		return nil, nil, errors.New("errortracker.Init: Logger is required")
	}
	if opts.FlushTimeout <= 0 {
		opts.FlushTimeout = defaultFlushTimeout
	}

	dsn := opts.DSN
	if dsn == "" {
		dsn = os.Getenv(DSNEnv)
	}
	if dsn == "" {
		opts.Logger.Info("errortracker: disabled (DSN unset)")
		return &Tracker{
			enabled:      false,
			flushTimeout: opts.FlushTimeout,
			logger:       opts.Logger,
		}, noopShutdown, nil
	}

	if opts.Environment == "" {
		return nil, nil, errors.New("errortracker.Init: Environment is required when DSN is set")
	}
	if opts.SampleRate <= 0 {
		opts.SampleRate = 1.0
	}
	serverName := opts.ServerName
	if serverName == "" {
		// os.Hostname can fail on heavily-locked-down containers
		// (CAP_SYS_ADMIN dropped, no /etc/hostname). We don't
		// propagate the error — a missing server_name is a small loss
		// compared to losing the entire error pipeline.
		if h, err := os.Hostname(); err == nil {
			serverName = h
		}
	}

	clientOpts := sentry.ClientOptions{
		Dsn:         dsn,
		Environment: opts.Environment,
		Release:     opts.Release,
		ServerName:  serverName,
		SampleRate:  opts.SampleRate,
		Debug:       opts.Debug,
		// AttachStacktrace gives us a stacktrace on Capture(err) calls
		// even when err is a plain errors.New — the cost is small
		// (a runtime.Caller walk) and the value is large (one-click
		// triage in the Sentry UI). Default is false in sentry-go;
		// we deliberately flip it on.
		AttachStacktrace: true,
	}

	initFn := opts.initFunc
	if initFn == nil {
		initFn = sentry.Init
	}
	if err := initFn(clientOpts); err != nil {
		return nil, nil, fmt.Errorf("errortracker.Init: sentry.Init: %w", err)
	}

	opts.Logger.Info("errortracker: enabled",
		slog.String("environment", opts.Environment),
		slog.String("release", opts.Release),
		slog.String("server_name", serverName),
		slog.Float64("sample_rate", opts.SampleRate),
	)

	t := &Tracker{
		enabled:      true,
		flushTimeout: opts.FlushTimeout,
		logger:       opts.Logger,
	}
	return t, t.shutdown, nil
}

// Shutdown is the function shape returned by Init. The caller hands it
// to the shutdown orchestrator so the in-flight event queue flushes
// before the binary exits. Idempotent.
type Shutdown func(context.Context) error

func noopShutdown(_ context.Context) error { return nil }

// shutdown flushes the sentry-go transport with the configured
// timeout. The ctx parameter is honored: if it's already canceled, we
// run a best-effort Flush against the original budget but log the
// truncation so operators see that we shed events.
func (t *Tracker) shutdown(ctx context.Context) error {
	if !t.enabled {
		return nil
	}
	budget := t.flushTimeout
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining > 0 && remaining < budget {
			budget = remaining
		}
	}
	ok := sentry.Flush(budget)
	if !ok {
		t.logger.Warn("errortracker: flush timed out; some events may not have reached the ingestion endpoint",
			slog.Duration("budget", budget),
		)
		// We deliberately don't return an error: a slow Sentry must
		// not fail the drain. The warning is sufficient to surface
		// the issue.
	}
	return nil
}

// Capture reports err to the configured tracker. Returns immediately
// on the no-op tracker or when err is nil.
//
// ctx may carry plugin context via WithPluginSlug; the tag is stamped
// on the event before send so dashboards group by plugin.
//
// Returns the Sentry event ID (empty when disabled or on send
// failure) so callers can correlate with the captured event in a
// user-facing error message.
func (t *Tracker) Capture(ctx context.Context, err error) string {
	if t == nil || !t.enabled || err == nil {
		return ""
	}
	hub := hubFromContext(ctx)
	id := hub.CaptureException(err)
	if id == nil {
		return ""
	}
	return string(*id)
}

// CaptureMessage reports a string message — useful for "this should
// never happen but didn't return an error" branches. Same semantics as
// Capture.
func (t *Tracker) CaptureMessage(ctx context.Context, msg string) string {
	if t == nil || !t.enabled || msg == "" {
		return ""
	}
	hub := hubFromContext(ctx)
	id := hub.CaptureMessage(msg)
	if id == nil {
		return ""
	}
	return string(*id)
}

// Recover is intended for deferred panic-recovery: pass it through
// `defer t.Recover(ctx, recover())`. Captures the panic value as a
// Sentry event without re-panicking. The handler/worker layer's own
// recovery middleware still owns the response handling — this is
// purely for surfacing the panic to the error tracker.
func (t *Tracker) Recover(ctx context.Context, r any) string {
	if t == nil || !t.enabled || r == nil {
		return ""
	}
	hub := hubFromContext(ctx)
	id := hub.RecoverWithContext(ctx, r)
	if id == nil {
		return ""
	}
	return string(*id)
}

// pluginSlugKey is the context key used by WithPluginSlug. Unexported
// to prevent collisions with any other context value defined under
// the same string — the standard pattern for context keys.
type pluginSlugKey struct{}

// WithPluginSlug returns a context whose Capture/CaptureMessage/Recover
// events will be tagged with gonext.plugin.slug=slug. Call this at the
// entry point of every plugin handler — the host's plugin lifecycle
// middleware threads it on every dispatch, so end-of-chain Capture
// calls inherit the slug automatically.
//
// Passing an empty slug returns ctx unchanged. The fall-through case
// matters: a plugin call where the slug wasn't propagated should NOT
// retag with empty (which would create a "no plugin" tag) — it should
// inherit the parent context's tag, if any.
func WithPluginSlug(ctx context.Context, slug string) context.Context {
	if slug == "" {
		return ctx
	}
	return context.WithValue(ctx, pluginSlugKey{}, slug)
}

// PluginSlugFromContext returns the slug attached by WithPluginSlug,
// or "" when none. Exported so middleware that builds a hub manually
// (e.g. an integration test) can read the value.
func PluginSlugFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, ok := ctx.Value(pluginSlugKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// hubFromContext returns a sentry.Hub with the plugin slug applied as
// a tag (when present). We clone the current hub rather than mutating
// it — the current hub is process-global, and a slug-tagged scope
// must not leak across requests.
//
// Callers that don't carry plugin context get the bare CurrentHub
// which uses the same global scope as before. The branch keeps the
// hot path (no-plugin) allocation-free.
func hubFromContext(ctx context.Context) *sentry.Hub {
	slug := PluginSlugFromContext(ctx)
	if slug == "" {
		return sentry.CurrentHub()
	}
	hub := sentry.CurrentHub().Clone()
	hub.Scope().SetTag(TagPluginSlug, slug)
	return hub
}
