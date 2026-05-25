package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/i18n"
)

// recordingAuditEmitter is a minimal AuditEmitter that records every
// EmitPluginEvent call into an in-memory slice. Tests use it to assert
// on the audit emissions the observability host functions trigger.
type recordingAuditEmitter struct {
	mu     sync.Mutex
	events []recordedAuditEvent
}

type recordedAuditEvent struct {
	Slug      string
	EventType string
	Severity  string
	Metadata  map[string]any
}

func (r *recordingAuditEmitter) EmitPluginEvent(_ context.Context, slug, eventType, severity string, metadata map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedAuditEvent{
		Slug:      slug,
		EventType: eventType,
		Severity:  severity,
		Metadata:  metadata,
	})
	return nil
}

func (r *recordingAuditEmitter) Events() []recordedAuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedAuditEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestObsFor_DefaultsAreNonNil checks the lazy-init contract: a fresh
// Runtime that's never been configured for observability still
// returns a usable observability bundle (NoopTranslator, no audit).
func TestObsFor_DefaultsAreNonNil(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	o := obsFor(rt)
	if o.translator == nil {
		t.Error("translator should default to NoopTranslator")
	}
	if _, ok := o.translator.(i18n.NoopTranslator); !ok {
		t.Errorf("default translator: got %T, want NoopTranslator", o.translator)
	}
	if o.auditEmitter != nil {
		t.Errorf("auditEmitter should default to nil, got %T", o.auditEmitter)
	}
}

// TestUseObservability_InstallsAndExposesSinks verifies the builder
// API installs every sink and the accessors read them back. This is
// the primary wiring smoke test — if any of these fields fail to
// flow through, every host-function test downstream would mask the
// root cause.
func TestUseObservability_InstallsAndExposesSinks(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tr := i18n.NewMapTranslator(map[string]map[string]string{
		"fr": {"hello": "bonjour"},
	})
	audit := &recordingAuditEmitter{}

	rt.UseObservability().
		WithTranslator(tr).
		WithAuditEmitter(audit)

	if rt.Translator() != tr {
		t.Errorf("Translator: got %v, want injected translator", rt.Translator())
	}
	if rt.AuditEmitter() != audit {
		t.Errorf("AuditEmitter: not the injected instance")
	}
}

// TestObservePluginTrap_EmitsAuditAndLog covers the structured
// trap-event path: when a plugin traps, the runtime should emit a
// `plugin.trap` audit row at warning severity and log at error
// level. The Prometheus counter bump joins this path in #181.
func TestObservePluginTrap_EmitsAuditAndLog(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	audit := &recordingAuditEmitter{}
	rt.UseObservability().WithAuditEmitter(audit)

	rt.ObservePluginTrap(context.Background(), TrapEvent{
		Slug:       "test-plugin",
		InstanceID: "instance-1",
		Reason:     "division by zero",
		Fuel:       42,
		Stack:      "frame[0]: foo\nframe[1]: bar",
	})

	events := audit.Events()
	if len(events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(events))
	}
	e := events[0]
	if e.Slug != "test-plugin" {
		t.Errorf("slug: got %q, want %q", e.Slug, "test-plugin")
	}
	if e.EventType != "plugin.trap" {
		t.Errorf("event type: got %q, want %q", e.EventType, "plugin.trap")
	}
	if e.Severity != "warning" {
		t.Errorf("severity: got %q, want %q", e.Severity, "warning")
	}
	if e.Metadata["reason"] != "division by zero" {
		t.Errorf("reason: got %v", e.Metadata["reason"])
	}
	if e.Metadata["instance_id"] != "instance-1" {
		t.Errorf("instance_id: got %v", e.Metadata["instance_id"])
	}
	if e.Metadata["fuel_remaining"] != 42.0 {
		t.Errorf("fuel_remaining: got %v", e.Metadata["fuel_remaining"])
	}
	if e.Metadata["stack"] != "frame[0]: foo\nframe[1]: bar" {
		t.Errorf("stack: got %v", e.Metadata["stack"])
	}
}

// TestObservePluginTrap_EmptySlug_NoOp asserts the API contract: a
// trap event with an empty slug is ignored. This guards against
// accidental nil-pointer-style attribution that would surface as
// orphan audit rows.
func TestObservePluginTrap_EmptySlug_NoOp(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	audit := &recordingAuditEmitter{}
	rt.UseObservability().WithAuditEmitter(audit)

	rt.ObservePluginTrap(context.Background(), TrapEvent{
		Slug:   "",
		Reason: "ignored",
	})

	if events := audit.Events(); len(events) != 0 {
		t.Errorf("expected 0 audit events, got %d", len(events))
	}
}

// TestObservePluginTrap_NilAuditEmitter_NoPanic guards the no-audit
// path: the trap logger still fires, but the absent emitter doesn't
// panic.
func TestObservePluginTrap_NilAuditEmitter_NoPanic(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	// No UseObservability call → auditEmitter is nil.
	rt.ObservePluginTrap(context.Background(), TrapEvent{
		Slug:   "test-plugin",
		Reason: "no audit configured",
	})
}
