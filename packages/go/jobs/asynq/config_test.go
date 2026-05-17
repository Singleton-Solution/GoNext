package asynq

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// quietLogger returns a slog logger that discards all output. Tests
// that don't care about log lines use this; matches the convention in
// packages/go/redis and packages/go/shutdown.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestDefaultQueuesHasSeven locks the canonical queue topology in
// place: any change to the seven-queue split is a deliberate design
// review, and this test forces it through code review rather than
// letting it drift via a one-line edit.
func TestDefaultQueuesHasSeven(t *testing.T) {
	q := DefaultQueues()
	if got, want := len(q), 7; got != want {
		t.Fatalf("DefaultQueues: got %d queues, want %d", got, want)
	}
	want := map[string]int{
		"critical":  10,
		"webhook":   5,
		"email":     5,
		"media":     3,
		"migration": 2,
		"plugin":    2,
		"default":   1,
	}
	for name, weight := range want {
		if got := q[name]; got != weight {
			t.Errorf("DefaultQueues[%q] = %d, want %d", name, got, weight)
		}
	}
}

// TestDefaultQueuesReturnsCopy ensures callers can mutate the returned
// map without contaminating other invocations. A shared map would let
// one boot-time customization bleed into every Config in the same
// process — easy to miss in review, painful to debug.
func TestDefaultQueuesReturnsCopy(t *testing.T) {
	a := DefaultQueues()
	a["critical"] = 9999
	b := DefaultQueues()
	if b["critical"] != 10 {
		t.Fatalf("DefaultQueues returned shared map; got critical=%d after mutation", b["critical"])
	}
}

// TestConfigValidateAppliesDefaults exercises the contract that an
// empty Config (Logger only) produces the production-canonical
// configuration. New() relies on this; if defaults stop applying, the
// worker boots with Asynq's library defaults instead of our policy.
func TestConfigValidateAppliesDefaults(t *testing.T) {
	cfg := Config{Logger: quietLogger()}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(cfg.Queues) != 7 {
		t.Errorf("Queues: got %d entries, want 7 (default topology)", len(cfg.Queues))
	}
	if cfg.Concurrency != defaultConcurrency {
		t.Errorf("Concurrency: got %d, want %d", cfg.Concurrency, defaultConcurrency)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout: got %v, want %v", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
	if cfg.HealthCheckInterval != defaultHealthCheckInterval {
		t.Errorf("HealthCheckInterval: got %v, want %v", cfg.HealthCheckInterval, defaultHealthCheckInterval)
	}
}

// TestConfigValidatePreservesCustomQueues verifies that explicitly
// supplied Queues are respected — defaults only apply on the empty
// path. Without this, a test or operator override would silently fall
// back to the canonical topology.
func TestConfigValidatePreservesCustomQueues(t *testing.T) {
	custom := map[string]int{"only": 1}
	cfg := Config{Logger: quietLogger(), Queues: custom}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(cfg.Queues) != 1 || cfg.Queues["only"] != 1 {
		t.Fatalf("validate clobbered custom Queues: got %v", cfg.Queues)
	}
}

// TestConfigValidateRequiresLogger codifies that the chassis refuses
// to boot without a logger. The asynq adapter would nil-deref later
// in a hard-to-debug location; failing fast at config time keeps the
// failure mode obvious.
func TestConfigValidateRequiresLogger(t *testing.T) {
	cfg := Config{}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate: want error for missing Logger, got nil")
	}
	if !strings.Contains(err.Error(), "Logger") {
		t.Errorf("validate error should mention Logger; got %q", err.Error())
	}
}

// TestConfigValidateRejectsNonPositiveWeight prevents Asynq's
// silent-drop behavior for zero/negative queue weights: we'd rather
// fail boot than start the worker with a queue that never schedules.
func TestConfigValidateRejectsNonPositiveWeight(t *testing.T) {
	cases := []struct {
		name string
		qs   map[string]int
	}{
		{"zero", map[string]int{"q": 0}},
		{"negative", map[string]int{"q": -1}},
		{"empty-name", map[string]int{"": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Logger: quietLogger(), Queues: tc.qs}
			if err := cfg.validate(); err == nil {
				t.Fatal("validate: want error, got nil")
			}
		})
	}
}

// TestConfigCustomDurationsRespected verifies that operator-supplied
// timeouts survive validate. A regression here would cause every
// /readyz freshness override and every drain budget customization to
// silently revert to defaults.
func TestConfigCustomDurationsRespected(t *testing.T) {
	cfg := Config{
		Logger:              quietLogger(),
		Concurrency:         64,
		ShutdownTimeout:     7 * time.Second,
		HealthCheckInterval: 11 * time.Second,
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Concurrency != 64 {
		t.Errorf("Concurrency: got %d, want 64", cfg.Concurrency)
	}
	if cfg.ShutdownTimeout != 7*time.Second {
		t.Errorf("ShutdownTimeout: got %v, want 7s", cfg.ShutdownTimeout)
	}
	if cfg.HealthCheckInterval != 11*time.Second {
		t.Errorf("HealthCheckInterval: got %v, want 11s", cfg.HealthCheckInterval)
	}
}
