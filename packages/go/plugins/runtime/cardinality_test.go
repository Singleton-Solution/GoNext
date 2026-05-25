package runtime

import (
	"testing"
)

func TestCardinalityDam_AdmitsBelowLimit(t *testing.T) {
	d := NewCardinalityDam(3)
	for i, v := range []string{"a", "b", "c"} {
		_, ok := d.Admit("plugin-x", "requests", map[string]string{"status": v})
		if !ok {
			t.Errorf("value %d (%q): expected admitted, got rejected", i, v)
		}
	}
}

func TestCardinalityDam_RejectsAtLimit(t *testing.T) {
	d := NewCardinalityDam(2)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})
	d.Admit("plugin-x", "requests", map[string]string{"status": "b"})

	tag, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "c"})
	if ok {
		t.Error("third value should be rejected at limit 2")
	}
	if tag != "status" {
		t.Errorf("overflowing tag: got %q, want %q", tag, "status")
	}
}

func TestCardinalityDam_KnownValueDoesntCount(t *testing.T) {
	d := NewCardinalityDam(2)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})
	d.Admit("plugin-x", "requests", map[string]string{"status": "b"})

	// Re-admitting an already-seen value is fine even at the limit.
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "a"}); !ok {
		t.Error("re-admitting known value should succeed")
	}
}

func TestCardinalityDam_PerPlugin(t *testing.T) {
	d := NewCardinalityDam(2)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})
	d.Admit("plugin-x", "requests", map[string]string{"status": "b"})

	// A different plugin starts with a fresh budget.
	if _, ok := d.Admit("plugin-y", "requests", map[string]string{"status": "c"}); !ok {
		t.Error("plugin-y first observation should succeed (separate budget)")
	}
}

func TestCardinalityDam_PerMetric(t *testing.T) {
	d := NewCardinalityDam(1)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})

	if _, ok := d.Admit("plugin-x", "errors", map[string]string{"status": "a"}); !ok {
		t.Error("different metric should have separate budget")
	}
}

func TestCardinalityDam_EmptyValueIgnored(t *testing.T) {
	d := NewCardinalityDam(1)
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "a"}); !ok {
		t.Fatal("first observation failed")
	}
	// An empty value doesn't count toward the budget.
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": ""}); !ok {
		t.Error("empty-value tag should be admitted regardless of budget")
	}
}

func TestCardinalityDam_DropDoesntMutate(t *testing.T) {
	d := NewCardinalityDam(2)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})
	d.Admit("plugin-x", "requests", map[string]string{"status": "b"})

	// First over-budget call rejects.
	_, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "c"})
	if ok {
		t.Fatal("expected rejection")
	}

	// The rejected observation should NOT have been recorded — a
	// retry with one of the original values must still succeed.
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "a"}); !ok {
		t.Error("known value should still admit after a rejection")
	}

	// And the dam shouldn't show 3 distinct values for status.
	snap := d.Snapshot()
	if got := snap["plugin-x"]["requests"]["status"]; got != 2 {
		t.Errorf("status count after rejection: got %d, want 2", got)
	}
}

func TestCardinalityDam_Forget(t *testing.T) {
	d := NewCardinalityDam(2)
	d.Admit("plugin-x", "requests", map[string]string{"status": "a"})
	d.Admit("plugin-x", "requests", map[string]string{"status": "b"})

	d.Forget("plugin-x")

	// Budget should be reset after Forget.
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "c"}); !ok {
		t.Error("after Forget, budget should be fresh")
	}
	if _, ok := d.Admit("plugin-x", "requests", map[string]string{"status": "d"}); !ok {
		t.Error("after Forget, budget should accept second value")
	}
}

func TestCardinalityDam_NilSafe(t *testing.T) {
	var d *CardinalityDam
	if _, ok := d.Admit("x", "y", nil); !ok {
		t.Error("nil dam should admit-all")
	}
	d.Forget("x")          // must not panic
	d.Snapshot()           // must not panic
	if d.Limit() != 0 {
		t.Errorf("nil dam Limit: got %d, want 0", d.Limit())
	}
}

func TestCardinalityDam_DefaultsWhenZeroLimit(t *testing.T) {
	d := NewCardinalityDam(0)
	if got := d.Limit(); got != DefaultCardinalityLimit {
		t.Errorf("zero-limit input: got %d, want default %d", got, DefaultCardinalityLimit)
	}
}

func TestRuntime_CardinalityDam_DefaultsToInstance(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	dam := rt.CardinalityDam()
	if dam == nil {
		t.Fatal("CardinalityDam should never be nil")
	}
	if dam.Limit() != DefaultCardinalityLimit {
		t.Errorf("default dam limit: got %d, want %d", dam.Limit(), DefaultCardinalityLimit)
	}
}

func TestRuntime_WithCardinalityDam_Custom(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	custom := NewCardinalityDam(50)
	rt.UseObservability().WithCardinalityDam(custom)

	if rt.CardinalityDam() != custom {
		t.Error("CardinalityDam: not the injected instance")
	}
}
