package capabilities

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// recordingEmitter is a minimal auditEmitter fake. It records every
// Emit call so the audit-emission tests can assert on them without
// wiring up a real audit.Store. Safe for concurrent use.
type recordingEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
	err    error // optional: if non-nil, Emit returns it without recording
}

type recordedEvent struct {
	eventType string
	severity  audit.Severity
	target    string
	targetID  string
	metadata  map[string]any
}

func (r *recordingEmitter) Emit(_ context.Context, eventType string, opts ...audit.EmitOption) error {
	if r.err != nil {
		return r.err
	}
	// Build a synthetic audit.Event and apply the opts so we can read
	// out severity / target / metadata exactly as a real emitter
	// would set them. This keeps the test honest about the wire
	// shape rather than special-casing each opt by name.
	evt := audit.Event{EventType: eventType}
	for _, opt := range opts {
		opt(&evt)
	}
	r.mu.Lock()
	r.events = append(r.events, recordedEvent{
		eventType: eventType,
		severity:  evt.Severity,
		target:    evt.ResourceType,
		targetID:  evt.ResourceID,
		metadata:  evt.Metadata,
	})
	r.mu.Unlock()
	return nil
}

func (r *recordingEmitter) recorded() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestChecker_AllowedHonorsGrants is the happy path: a cap that is
// registered and granted is Allowed; one that is missing is not.
func TestChecker_AllowedHonorsGrants(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "posts.read"})
	_ = reg.Register(CapabilityDef{ID: "posts.write"})

	chk := NewChecker(reg, NewGrantSet("posts.read"))

	if !chk.Allowed("posts.read") {
		t.Error("Allowed(posts.read): granted cap denied")
	}
	if chk.Allowed("posts.write") {
		t.Error("Allowed(posts.write): ungranted cap allowed")
	}
}

// TestChecker_AllowedUnregisteredReturnsFalse covers the "defense in
// depth" branch: a phantom cap ID in the grant set must not bypass
// the registry. This is the test that catches a corrupted manifest
// import.
func TestChecker_AllowedUnregisteredReturnsFalse(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// Note: "phantom" is granted but NOT registered.
	chk := NewChecker(reg, NewGrantSet("phantom"))

	if chk.Allowed("phantom") {
		t.Error("Allowed(unregistered): expected false despite grant")
	}
}

// TestChecker_MustAllowReturnsErrorOnDenial pins the error contract.
// The returned error must satisfy errors.Is(err, ErrCapabilityDenied)
// so callers can switch on the failure category, AND it must allow
// matching specific cap IDs via errors.Is(err, Denied("posts.write"))
// so a curious admin tool can react to one denial in particular.
func TestChecker_MustAllowReturnsErrorOnDenial(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "posts.write"})

	chk := NewChecker(reg, NewGrantSet()) // empty grants

	err := chk.MustAllow(context.Background(), "posts.write")
	if err == nil {
		t.Fatal("MustAllow: expected denial, got nil")
	}
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Errorf("MustAllow error: not errors.Is ErrCapabilityDenied: %v", err)
	}
	if !errors.Is(err, Denied("posts.write")) {
		t.Errorf("MustAllow error: not errors.Is Denied(\"posts.write\"): %v", err)
	}
	// Specific-denial match must be exclusive: a different cap ID
	// must NOT match.
	if errors.Is(err, Denied("email.send")) {
		t.Error("MustAllow error: spuriously matched Denied(\"email.send\")")
	}
}

// TestChecker_MustAllowAllowsGranted confirms the positive return
// path: a granted cap returns nil with no error and no audit event.
func TestChecker_MustAllowAllowsGranted(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "kv.read"})

	em := &recordingEmitter{}
	chk := NewChecker(reg, NewGrantSet("kv.read"), WithAuditEmitter(em))

	if err := chk.MustAllow(context.Background(), "kv.read"); err != nil {
		t.Fatalf("MustAllow(granted): unexpected error %v", err)
	}
	if got := len(em.recorded()); got != 0 {
		t.Errorf("granted MustAllow emitted %d audit events, want 0", got)
	}
}

// TestChecker_MustAllowEmitsAuditOnDenial verifies the audit hook
// fires exactly once per denial, with the right event type, severity,
// and metadata. This is the headline of the whole package: a plugin
// reaching for a cap it doesn't hold leaves a trail.
func TestChecker_MustAllowEmitsAuditOnDenial(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "http.fetch", Sensitive: true})

	em := &recordingEmitter{}
	chk := NewChecker(reg, NewGrantSet(), WithAuditEmitter(em))

	_ = chk.MustAllow(context.Background(), "http.fetch")

	got := em.recorded()
	if len(got) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(got))
	}
	evt := got[0]
	if evt.eventType != capabilityDeniedEvent {
		t.Errorf("eventType: got %q, want %q", evt.eventType, capabilityDeniedEvent)
	}
	if evt.severity != audit.SeverityWarning {
		t.Errorf("severity: got %q, want %q", evt.severity, audit.SeverityWarning)
	}
	if evt.target != "capability" || evt.targetID != "http.fetch" {
		t.Errorf("target: got (%q,%q), want (\"capability\",\"http.fetch\")",
			evt.target, evt.targetID)
	}
	if got, ok := evt.metadata["capability"].(string); !ok || got != "http.fetch" {
		t.Errorf("metadata[capability]: got %v, want \"http.fetch\"", evt.metadata["capability"])
	}
}

// TestChecker_NoAuditWithoutEmitter ensures the nil-emitter path is
// safe and silent. Production code always installs an emitter, but
// unit tests routinely skip it.
func TestChecker_NoAuditWithoutEmitter(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "posts.write"})

	chk := NewChecker(reg, NewGrantSet()) // no WithAuditEmitter
	// Just verifying this doesn't panic / nil-deref.
	if err := chk.MustAllow(context.Background(), "posts.write"); err == nil {
		t.Error("MustAllow: expected denial")
	}
}

// TestChecker_AuditEmitFailureSwallowed pins the documented best-effort
// audit contract. If the emitter returns an error, MustAllow still
// returns the denial as its primary result and does NOT propagate the
// audit error — the operational signal is the denial itself.
func TestChecker_AuditEmitFailureSwallowed(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "email.send"})

	em := &recordingEmitter{err: errors.New("siem unavailable")}
	chk := NewChecker(reg, NewGrantSet(), WithAuditEmitter(em))

	err := chk.MustAllow(context.Background(), "email.send")
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("expected ErrCapabilityDenied, got %v", err)
	}
	// The denial cause must be the cap denial, not the audit error.
	// errors.Unwrap walks our wrapper, not the audit error's chain.
	if errors.Is(err, em.err) {
		t.Error("denial error chain spuriously contains the audit error")
	}
}

// TestNewChecker_NilRegistryPanics anchors the documented constructor
// contract: nil registry is a wiring bug, not a recoverable runtime
// condition.
func TestNewChecker_NilRegistryPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewChecker(nil registry): expected panic")
		}
	}()
	_ = NewChecker(nil, NewGrantSet())
}

// TestNewChecker_NilGrantSetIsEmpty verifies the "deny everything"
// default when no grant set is passed. A plugin instantiated before
// its grants were resolved must be denied access to everything, not
// silently allowed.
func TestNewChecker_NilGrantSetIsEmpty(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Register(CapabilityDef{ID: "posts.read"})

	chk := NewChecker(reg, nil)
	if chk.Allowed("posts.read") {
		t.Error("nil GrantSet should deny everything")
	}
}

// TestGrantSet_HasAndIDs sanity-checks the GrantSet helpers. Trivial
// but stops a regression from breaking the audit-metadata path that
// renders granted-cap lists.
func TestGrantSet_HasAndIDs(t *testing.T) {
	t.Parallel()
	g := NewGrantSet("b", "a", "c", "a") // duplicate folded
	if !g.Has("a") || !g.Has("b") || !g.Has("c") {
		t.Error("Has: missing expected member")
	}
	if g.Has("d") {
		t.Error("Has: false positive")
	}
	ids := g.IDs()
	want := []string{"a", "b", "c"}
	if len(ids) != len(want) {
		t.Fatalf("IDs: got %v, want %v", ids, want)
	}
	for i, id := range ids {
		if id != want[i] {
			t.Errorf("IDs[%d]: got %q, want %q", i, id, want[i])
		}
	}
}

// TestChecker_Granted exposes the sorted snapshot of granted caps.
// The admin UI reads this to render the install-confirmation screen.
func TestChecker_Granted(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	for _, id := range []string{"a", "b", "c"} {
		_ = reg.Register(CapabilityDef{ID: id})
	}
	chk := NewChecker(reg, NewGrantSet("c", "a", "b"))
	got := chk.Granted()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("Granted: got %v, want %v", got, want)
	}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("Granted[%d]: got %q, want %q", i, id, want[i])
		}
	}
}

// TestChecker_ConcurrentAllowed is a race-detector workout for the
// hot path. The WASM runtime will call Allowed concurrently from many
// goroutines (one per host-call) — the RWMutex must not race.
func TestChecker_ConcurrentAllowed(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	for _, id := range []string{"a", "b", "c"} {
		_ = reg.Register(CapabilityDef{ID: id})
	}
	chk := NewChecker(reg, NewGrantSet("a", "b"))

	const N = 128
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = chk.Allowed("a")
				_ = chk.Allowed("b")
				_ = chk.Allowed("c")
				_ = chk.Allowed("d")
			}
		}()
	}
	wg.Wait()
}

// TestDeniedError_IDExposed pins the helper that lets callers extract
// the cap ID from a denial without parsing the message string.
func TestDeniedError_IDExposed(t *testing.T) {
	t.Parallel()
	err := Denied("posts.write")
	var de *deniedError
	if !errors.As(err, &de) {
		t.Fatal("Denied: result is not a *deniedError")
	}
	if de.ID() != "posts.write" {
		t.Errorf("ID: got %q, want %q", de.ID(), "posts.write")
	}
}
