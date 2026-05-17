package capabilities

import (
	"context"
	"errors"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// TestCheckerWithRealEmitter is the integration smoke test. It wires
// a Checker against the real *audit.Emitter (backed by MemoryStore)
// rather than the recording fake, and asserts that the audit_log
// receives one capability.denied row with the right plugin slug,
// severity, and metadata. This is the test that catches drift between
// the auditEmitter interface and the real Emitter signature — if Emit
// ever grows a new required argument, this test stops compiling.
func TestCheckerWithRealEmitter(t *testing.T) {
	t.Parallel()
	store := audit.NewMemoryStore()
	root := audit.NewEmitter(store)
	pluginEmitter := root.WithPlugin("my-plugin")

	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "http.fetch", Sensitive: true})

	chk := NewChecker(reg, NewGrantSet(), WithAuditEmitter(pluginEmitter))

	err := chk.MustAllow(context.Background(), "http.fetch")
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("MustAllow: got %v, want ErrCapabilityDenied", err)
	}

	events, err := store.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit_log: got %d events, want 1", len(events))
	}
	evt := events[0]
	if evt.EventType != capabilityDeniedEvent {
		t.Errorf("EventType: got %q, want %q", evt.EventType, capabilityDeniedEvent)
	}
	if evt.ActorPluginSlug != "my-plugin" {
		t.Errorf("ActorPluginSlug: got %q, want %q", evt.ActorPluginSlug, "my-plugin")
	}
	if evt.Severity != audit.SeverityWarning {
		t.Errorf("Severity: got %q, want %q", evt.Severity, audit.SeverityWarning)
	}
	if evt.ResourceType != "capability" || evt.ResourceID != "http.fetch" {
		t.Errorf("target: got (%q,%q), want (\"capability\",\"http.fetch\")",
			evt.ResourceType, evt.ResourceID)
	}
	if got, _ := evt.Metadata["capability"].(string); got != "http.fetch" {
		t.Errorf("metadata[capability]: got %v, want \"http.fetch\"", evt.Metadata["capability"])
	}
}
