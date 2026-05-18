package schemas

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/hooks/wpcompat"
	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
)

// ----------------------------------------------------------------------
// Registry: Register + Validate happy and unhappy paths
// ----------------------------------------------------------------------

func TestRegistry_RegisterValidate_HappyPath(t *testing.T) {
	r := NewRegistry()
	schema := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"required": ["id"],
		"properties": { "id": {"type": "string"} }
	}`)
	if err := r.Register("plg.demo.payload", schema); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Has("plg.demo.payload") {
		t.Fatalf("Has after Register should be true")
	}
	// Well-formed payload passes.
	if err := r.ValidatePayload("plg.demo.payload", map[string]any{"id": "x"}); err != nil {
		t.Fatalf("well-formed payload: %v", err)
	}
}

func TestRegistry_ValidatePayload_RejectsMalformed(t *testing.T) {
	r := NewRegistry()
	schema := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"required": ["id"],
		"properties": { "id": {"type": "string"} }
	}`)
	if err := r.Register("plg.demo.payload", schema); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Missing the required "id" key.
	err := r.ValidatePayload("plg.demo.payload", map[string]any{"name": "x"})
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("error should be ErrInvalidPayload: got %v", err)
	}
	// Wrong scalar type.
	err = r.ValidatePayload("plg.demo.payload", map[string]any{"id": 42})
	if err == nil {
		t.Fatalf("expected validation error for wrong type, got nil")
	}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("error should be ErrInvalidPayload: got %v", err)
	}
}

func TestRegistry_ValidatePayload_LooseUnregisteredPasses(t *testing.T) {
	r := NewRegistry()
	// No schemas registered — loose validation returns nil.
	if err := r.ValidatePayload("unknown.hook", "anything"); err != nil {
		t.Fatalf("loose unregistered should pass: %v", err)
	}
}

func TestRegistry_ValidateStrict_UnregisteredRejected(t *testing.T) {
	r := NewRegistry()
	err := r.ValidateStrict("unknown.hook", "x")
	if err == nil {
		t.Fatalf("strict unregistered should fail")
	}
	if !errors.Is(err, ErrUnregisteredHook) {
		t.Errorf("error should be ErrUnregisteredHook: got %v", err)
	}
}

func TestRegistry_Register_RejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	schema := []byte(`{"type": "string"}`)
	if err := r.Register("dup", schema); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("dup", schema)
	if err == nil {
		t.Fatalf("duplicate Register should fail")
	}
	if !errors.Is(err, ErrSchemaAlreadyRegistered) {
		t.Errorf("error should be ErrSchemaAlreadyRegistered: got %v", err)
	}
}

func TestRegistry_Register_RejectsWrongDialect(t *testing.T) {
	r := NewRegistry()
	schema := []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "string"
	}`)
	err := r.Register("bad", schema)
	if err == nil {
		t.Fatalf("draft-07 schema should be rejected")
	}
	if !errors.Is(err, jsonschemautil.ErrUnsupportedDialect) {
		t.Errorf("error should wrap ErrUnsupportedDialect: got %v", err)
	}
}

func TestRegistry_Register_RejectsEmptyName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", []byte(`{"type":"string"}`)); err == nil {
		t.Fatalf("empty hook name should be rejected")
	}
}

// ----------------------------------------------------------------------
// Race: concurrent Register + Validate
// ----------------------------------------------------------------------

func TestRegistry_Concurrent_RegisterAndValidate(t *testing.T) {
	r := NewRegistry()
	schema := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "string"
	}`)
	const goroutines = 16
	const perGoroutine = 50

	var wg sync.WaitGroup
	// Pre-register one stable hook so the validators have something to
	// hit in addition to the racing registrations.
	if err := r.Register("stable", schema); err != nil {
		t.Fatalf("seed Register: %v", err)
	}

	// Writers: each goroutine registers its own batch of hook names.
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				name := buildHookName(g, i)
				if err := r.Register(name, schema); err != nil {
					t.Errorf("Register(%s): %v", name, err)
					return
				}
			}
		}()
	}
	// Readers: continuously validate the stable hook + try unregistered.
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := r.ValidatePayload("stable", "value"); err != nil {
					t.Errorf("ValidatePayload(stable): %v", err)
					return
				}
				// Loose mode: unknown returns nil — proves there's no
				// torn read on the schema pointer.
				if err := r.ValidatePayload("never.registered", 123); err != nil {
					t.Errorf("loose unknown: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// Sanity: the right number of names are registered after the race.
	got := len(r.Names())
	want := goroutines*perGoroutine + 1 // +1 for "stable"
	if got != want {
		t.Errorf("Names count: got %d want %d", got, want)
	}
}

func buildHookName(g, i int) string {
	var sb strings.Builder
	sb.WriteString("g")
	writeInt(&sb, g)
	sb.WriteString(".i")
	writeInt(&sb, i)
	return sb.String()
}

func writeInt(sb *strings.Builder, n int) {
	if n == 0 {
		sb.WriteByte('0')
		return
	}
	if n < 0 {
		sb.WriteByte('-')
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	sb.Write(buf[pos:])
}

// ----------------------------------------------------------------------
// Built-in schemas: cover every WP-compat alias
// ----------------------------------------------------------------------

func TestBuiltinRegistry_CoversEveryWPAlias(t *testing.T) {
	r := BuiltinRegistry()
	missing := []string{}
	for wpName := range wpcompat.Aliases {
		if !r.Has(wpName) {
			missing = append(missing, wpName)
		}
	}
	if len(missing) > 0 {
		t.Errorf("built-in registry is missing schemas for: %v", missing)
	}
}

func TestBuiltinRegistry_TheContentAcceptsStringRejectsObject(t *testing.T) {
	r := BuiltinRegistry()
	if err := r.ValidatePayload("the_content", "Hello, world."); err != nil {
		t.Errorf("the_content (string) should validate: %v", err)
	}
	err := r.ValidatePayload("the_content", map[string]any{"v": 1})
	if err == nil {
		t.Errorf("the_content (object) should be rejected")
	}
}

func TestBuiltinRegistry_SavePostShape(t *testing.T) {
	r := BuiltinRegistry()
	// The WP-compat adapter produces this shape when forwarding
	// core.post.saved into save_post. Validating it should pass.
	good := wpcompat.WPPost{ID: "p1", Post: nil, Update: true}
	if err := r.ValidatePayload("save_post", good); err != nil {
		t.Errorf("save_post should accept WPPost: %v", err)
	}
	// Wrong field type — Update must be boolean.
	bad := map[string]any{"ID": "p1", "Update": "yes"}
	if err := r.ValidatePayload("save_post", bad); err == nil {
		t.Errorf("save_post should reject non-boolean Update")
	}
}

func TestBuiltinRegistry_UserRegisterShape(t *testing.T) {
	r := BuiltinRegistry()
	if err := r.ValidatePayload("user_register", wpcompat.WPUser{ID: "u1"}); err != nil {
		t.Errorf("user_register should accept WPUser: %v", err)
	}
	if err := r.ValidatePayload("user_register", map[string]any{}); err == nil {
		t.Errorf("user_register should reject missing ID")
	}
}

func TestBuiltinRegistry_CopyIsIndependent(t *testing.T) {
	a := BuiltinRegistryCopy()
	b := BuiltinRegistryCopy()
	// Different pointer values.
	if a == b {
		t.Errorf("BuiltinRegistryCopy should return fresh registries")
	}
	// Registering on one does not affect the other.
	schema := []byte(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "string"
	}`)
	if err := a.Register("plg.unique", schema); err != nil {
		t.Fatalf("Register on a: %v", err)
	}
	if b.Has("plg.unique") {
		t.Errorf("registry b should not see registrations on a")
	}
}

// ----------------------------------------------------------------------
// Enforcer: modes, IsContractError, integration with the bus
// ----------------------------------------------------------------------

func TestEnforcer_ModeStrict_UnregisteredRejected(t *testing.T) {
	enf := NewEnforcer(NewRegistry(), ModeStrict)
	if err := enf.Validate("unregistered", "x"); err == nil {
		t.Fatalf("strict mode should reject unregistered hooks")
	} else if !errors.Is(err, ErrUnregisteredHook) {
		t.Errorf("error should be ErrUnregisteredHook: got %v", err)
	}
}

func TestEnforcer_ModeLoose_UnregisteredAccepted(t *testing.T) {
	enf := NewEnforcer(NewRegistry(), ModeLoose)
	if err := enf.Validate("unregistered", "x"); err != nil {
		t.Errorf("loose mode should accept unregistered: %v", err)
	}
}

func TestEnforcer_NilReceiverIsNoop(t *testing.T) {
	var enf *Enforcer // nil pointer
	if err := enf.Validate("anything", "x"); err != nil {
		t.Errorf("nil enforcer.Validate should be no-op: %v", err)
	}
	if enf.Mode() != ModeLoose {
		t.Errorf("nil enforcer Mode should default to ModeLoose, got %s", enf.Mode())
	}
}

func TestEnforcer_WithMode_SharesRegistry(t *testing.T) {
	reg := BuiltinRegistryCopy()
	loose := NewEnforcer(reg, ModeLoose)
	strict := loose.WithMode(ModeStrict)
	if loose.Registry() != strict.Registry() {
		t.Errorf("WithMode should share the registry pointer")
	}
	if loose.Mode() != ModeLoose || strict.Mode() != ModeStrict {
		t.Errorf("WithMode should not mutate the source enforcer")
	}
}

func TestIsContractError(t *testing.T) {
	if !IsContractError(ErrInvalidPayload) {
		t.Errorf("IsContractError(ErrInvalidPayload) should be true")
	}
	if !IsContractError(ErrUnregisteredHook) {
		t.Errorf("IsContractError(ErrUnregisteredHook) should be true")
	}
	if IsContractError(errors.New("unrelated")) {
		t.Errorf("IsContractError(other) should be false")
	}
	if IsContractError(nil) {
		t.Errorf("IsContractError(nil) should be false")
	}
}

func TestEnforcer_Describe(t *testing.T) {
	var enf *Enforcer
	if got := enf.Describe(); got != "no schemas configured" {
		t.Errorf("nil Describe: got %q", got)
	}
	enf = NewEnforcer(BuiltinRegistryCopy(), ModeStrict)
	if got := enf.Describe(); !strings.Contains(got, "mode=strict") {
		t.Errorf("Describe should mention mode: got %q", got)
	}
}

// ----------------------------------------------------------------------
// Bus integration: WithSchemas validates Do and ApplyFilters
// ----------------------------------------------------------------------

func TestBus_WithSchemas_ValidatesApplyFilters(t *testing.T) {
	bus := hooks.NewBus().WithSchemas(NewEnforcer(BuiltinRegistry(), ModeLoose))
	// Well-formed string payload for the_content passes.
	ran := false
	bus.RegisterFilter("the_content", 10, func(ctx context.Context, v any, args ...any) (any, error) {
		ran = true
		return v, nil
	})
	got, err := bus.ApplyFilters(context.Background(), "the_content", "Hello.")
	if err != nil {
		t.Fatalf("ApplyFilters good: %v", err)
	}
	if got != "Hello." {
		t.Errorf("ApplyFilters got %v want \"Hello.\"", got)
	}
	if !ran {
		t.Errorf("filter handler should have run on well-formed payload")
	}
	// Malformed payload (int instead of string) is rejected before
	// any handler runs.
	ran = false
	_, err = bus.ApplyFilters(context.Background(), "the_content", 42)
	if err == nil {
		t.Fatalf("ApplyFilters with bad payload should fail")
	}
	if ran {
		t.Errorf("handler should NOT run on rejected payload")
	}
	if !IsContractError(err) {
		t.Errorf("error should be a contract error: %v", err)
	}
}

func TestBus_WithSchemas_ValidatesDo(t *testing.T) {
	bus := hooks.NewBus().WithSchemas(NewEnforcer(BuiltinRegistry(), ModeLoose))
	// init has zero-args contract; calling without args passes.
	if err := bus.Do(context.Background(), "init"); err != nil {
		t.Errorf("init (no args) should validate: %v", err)
	}
	// init with args fails.
	if err := bus.Do(context.Background(), "init", "extra"); err == nil {
		t.Errorf("init (with args) should be rejected")
	}
}

func TestBus_WithSchemas_StrictModeRejectsUnknownHook(t *testing.T) {
	bus := hooks.NewBus().WithSchemas(NewEnforcer(BuiltinRegistry(), ModeStrict))
	err := bus.Do(context.Background(), "plg.unknown.hook")
	if err == nil {
		t.Fatalf("strict mode should reject unregistered hook")
	}
	if !errors.Is(err, ErrUnregisteredHook) {
		t.Errorf("error should be ErrUnregisteredHook: got %v", err)
	}
}

func TestBus_WithSchemas_LooseModeAllowsUnknownHook(t *testing.T) {
	// Loose default: unregistered passes through.
	bus := hooks.NewBus().WithSchemas(NewEnforcer(BuiltinRegistry(), ModeLoose))
	if err := bus.Do(context.Background(), "plg.unknown.hook"); err != nil {
		t.Errorf("loose mode should pass unregistered: %v", err)
	}
}

func TestBus_WithSchemas_NilDisablesEnforcement(t *testing.T) {
	bus := hooks.NewBus().WithSchemas(NewEnforcer(BuiltinRegistry(), ModeStrict))
	// Disabling brings us back to "no validation".
	bus.WithSchemas(nil)
	if err := bus.Do(context.Background(), "plg.unknown.hook"); err != nil {
		t.Errorf("nil enforcer should disable validation: %v", err)
	}
}
