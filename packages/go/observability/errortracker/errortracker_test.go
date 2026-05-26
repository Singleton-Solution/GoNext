package errortracker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestInit_NoDSN_ReturnsNoopTracker verifies that the absence of a DSN
// (the common dev-loop and partial-rollout case) returns a disabled
// tracker whose Capture calls are no-ops and whose Shutdown closer
// returns nil immediately.
func TestInit_NoDSN_ReturnsNoopTracker(t *testing.T) {
	t.Setenv(DSNEnv, "")
	tracker, shutdown, err := Init(Options{
		Logger: discardLogger(),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tracker.enabled {
		t.Fatal("expected disabled tracker when DSN unset")
	}
	// Capture on a disabled tracker returns "" and doesn't panic.
	if id := tracker.Capture(context.Background(), errors.New("never reported")); id != "" {
		t.Errorf("disabled Capture returned id %q; want empty", id)
	}
	if id := tracker.CaptureMessage(context.Background(), "msg"); id != "" {
		t.Errorf("disabled CaptureMessage returned id %q; want empty", id)
	}
	if id := tracker.Recover(context.Background(), "oops"); id != "" {
		t.Errorf("disabled Recover returned id %q; want empty", id)
	}
	// Shutdown should return nil with no flush attempted.
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestInit_NilLogger_Errors documents the precondition.
func TestInit_NilLogger_Errors(t *testing.T) {
	_, _, err := Init(Options{})
	if err == nil {
		t.Fatal("expected error on nil logger")
	}
	if !strings.Contains(err.Error(), "Logger is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestInit_DSNSet_RequiresEnvironment ensures we don't silently submit
// events with no `environment` tag — that creates a triage nightmare
// in the dashboard once the team has multiple deployments.
func TestInit_DSNSet_RequiresEnvironment(t *testing.T) {
	_, _, err := Init(Options{
		DSN:    "https://public@example.test/1",
		Logger: discardLogger(),
	})
	if err == nil {
		t.Fatal("expected error on missing environment when DSN set")
	}
	if !strings.Contains(err.Error(), "Environment is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestInit_DSNSet_CallsInit verifies the happy path by injecting a fake
// init function. We assert on the composed ClientOptions to confirm
// every advertised default landed.
func TestInit_DSNSet_CallsInit(t *testing.T) {
	var captured sentry.ClientOptions
	tracker, shutdown, err := Init(Options{
		DSN:         "https://public@example.test/1",
		Environment: "test",
		Release:     "v0.0.1",
		ServerName:  "test-server",
		Logger:      discardLogger(),
		initFunc: func(o sentry.ClientOptions) error {
			captured = o
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !tracker.enabled {
		t.Fatal("expected enabled tracker when DSN set")
	}
	if captured.Dsn != "https://public@example.test/1" {
		t.Errorf("DSN: got %q", captured.Dsn)
	}
	if captured.Environment != "test" {
		t.Errorf("Environment: got %q", captured.Environment)
	}
	if captured.Release != "v0.0.1" {
		t.Errorf("Release: got %q", captured.Release)
	}
	if captured.ServerName != "test-server" {
		t.Errorf("ServerName: got %q", captured.ServerName)
	}
	if !captured.AttachStacktrace {
		t.Error("AttachStacktrace should default to true")
	}
	if captured.SampleRate != 1.0 {
		t.Errorf("SampleRate default: got %v want 1.0", captured.SampleRate)
	}
	if shutdown == nil {
		t.Fatal("shutdown closer nil")
	}
}

// TestInit_DSNSet_HonorsCustomSampleRate confirms the sample-rate knob
// flows through.
func TestInit_DSNSet_HonorsCustomSampleRate(t *testing.T) {
	var captured sentry.ClientOptions
	_, _, err := Init(Options{
		DSN:         "https://public@example.test/1",
		Environment: "test",
		SampleRate:  0.25,
		Logger:      discardLogger(),
		initFunc: func(o sentry.ClientOptions) error {
			captured = o
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if captured.SampleRate != 0.25 {
		t.Errorf("SampleRate: got %v want 0.25", captured.SampleRate)
	}
}

// TestInit_DSN_ReadFromEnv covers the operator-friendly path: the DSN
// is set via GONEXT_SENTRY_DSN and Init picks it up without a code
// change.
func TestInit_DSN_ReadFromEnv(t *testing.T) {
	t.Setenv(DSNEnv, "https://envpub@example.test/2")
	var captured sentry.ClientOptions
	tracker, _, err := Init(Options{
		Environment: "staging",
		Logger:      discardLogger(),
		initFunc: func(o sentry.ClientOptions) error {
			captured = o
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !tracker.enabled {
		t.Fatal("expected enabled tracker when env DSN set")
	}
	if captured.Dsn != "https://envpub@example.test/2" {
		t.Errorf("env DSN: got %q", captured.Dsn)
	}
}

// TestInit_DefaultsServerNameFromHostname checks the fallback path.
func TestInit_DefaultsServerNameFromHostname(t *testing.T) {
	var captured sentry.ClientOptions
	_, _, err := Init(Options{
		DSN:         "https://public@example.test/1",
		Environment: "test",
		Logger:      discardLogger(),
		initFunc: func(o sentry.ClientOptions) error {
			captured = o
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	// We don't assert the exact hostname (CI environments differ),
	// only that ServerName is non-empty when not specified.
	if captured.ServerName == "" {
		t.Error("ServerName should default to os.Hostname")
	}
}

// TestWithPluginSlug_AndRoundTrip exercises the context plumbing for
// plugin-aware grouping.
func TestWithPluginSlug_AndRoundTrip(t *testing.T) {
	ctx := WithPluginSlug(context.Background(), "acme-seo")
	if got := PluginSlugFromContext(ctx); got != "acme-seo" {
		t.Errorf("PluginSlugFromContext: got %q want acme-seo", got)
	}

	// Empty slug passes through unchanged.
	unchanged := WithPluginSlug(ctx, "")
	if got := PluginSlugFromContext(unchanged); got != "acme-seo" {
		t.Errorf("empty slug should preserve parent: got %q", got)
	}

	// No slug returns empty.
	if got := PluginSlugFromContext(context.Background()); got != "" {
		t.Errorf("missing slug: got %q want empty", got)
	}
	if got := PluginSlugFromContext(nil); got != "" { //nolint:staticcheck // explicit nil-ctx coverage
		t.Errorf("nil ctx: got %q want empty", got)
	}
}

// TestTracker_NilReceiverSafe documents that callers don't have to
// nil-check before invoking — the package's no-op posture extends to
// the zero-value tracker case.
func TestTracker_NilReceiverSafe(t *testing.T) {
	var tracker *Tracker
	tracker.Capture(context.Background(), errors.New("x"))
	tracker.CaptureMessage(context.Background(), "x")
	tracker.Recover(context.Background(), "x")
}

// TestHubFromContext_TagsScopeOnSluggedCtx verifies the integration
// between WithPluginSlug and the sentry-go scope. We can't reach into
// the scope's tags directly (sentry-go's Scope.Tags is private), but
// we can check that the returned hub is the cloned variant rather
// than the global one when a slug is present.
func TestHubFromContext_TagsScopeOnSluggedCtx(t *testing.T) {
	base := sentry.CurrentHub()
	noPlugin := hubFromContext(context.Background())
	if noPlugin != base {
		t.Error("no-plugin ctx should return CurrentHub directly (no clone)")
	}
	plugin := hubFromContext(WithPluginSlug(context.Background(), "acme-seo"))
	if plugin == base {
		t.Error("plugin ctx should return a cloned hub, not CurrentHub")
	}
}
