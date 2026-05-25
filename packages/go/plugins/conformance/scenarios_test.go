package conformance

import (
	"context"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/fakehost"
)

func TestCapabilitiesDeclared_AllKnown_Passes(t *testing.T) {
	m := &Manifest{
		Capabilities: []string{"posts.read", "posts.write", "kv"},
	}
	got := scenarioCapabilitiesDeclared().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusPass {
		t.Fatalf("status = %v, msg = %s", got.Status, got.Message)
	}
}

func TestCapabilitiesDeclared_Unknown_Fails(t *testing.T) {
	m := &Manifest{
		Capabilities: []string{"posts.read", "unicorn"},
	}
	got := scenarioCapabilitiesDeclared().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusFail {
		t.Fatalf("expected fail, got %v", got)
	}
}

func TestCapabilitiesMatchUsage_HooksWithoutCaps_Fails(t *testing.T) {
	m := &Manifest{
		Hooks:        []string{"the_content"},
		Capabilities: nil,
	}
	got := scenarioCapabilitiesMatchUsage().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusFail {
		t.Fatalf("expected fail, got %v", got)
	}
}

func TestCapabilitiesMatchUsage_SavePostWithoutWrite_Fails(t *testing.T) {
	m := &Manifest{
		Hooks:        []string{"save_post"},
		Capabilities: []string{"posts.read"},
	}
	got := scenarioCapabilitiesMatchUsage().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusFail {
		t.Fatalf("expected fail, got %v", got)
	}
}

func TestCapabilitiesMatchUsage_Good_Passes(t *testing.T) {
	m := &Manifest{
		Hooks:        []string{"save_post"},
		Capabilities: []string{"posts.write"},
	}
	got := scenarioCapabilitiesMatchUsage().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusPass {
		t.Fatalf("expected pass, got %v", got)
	}
}

func TestHooksVocabulary_EmptyHookName_Fails(t *testing.T) {
	m := &Manifest{Hooks: []string{""}}
	got := scenarioHooksVocabulary().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusFail {
		t.Fatalf("expected fail, got %v", got)
	}
}

func TestHooksVocabulary_CustomHook_Passes(t *testing.T) {
	m := &Manifest{Hooks: []string{"my_custom_hook"}}
	got := scenarioHooksVocabulary().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusPass {
		t.Fatalf("expected pass (custom is fine), got %v", got)
	}
}

func TestJobsNaming_Empty_Passes(t *testing.T) {
	got := scenarioJobsRegistered().Run(context.Background(), &Manifest{}, fakehost.New())
	if got.Status != StatusPass {
		t.Fatalf("expected pass for empty jobs, got %v", got)
	}
}

func TestJobsNaming_BadPrefix_Fails(t *testing.T) {
	m := &Manifest{
		Slug: "seo",
		Jobs: []string{"someothertool.run"},
	}
	got := scenarioJobsRegistered().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusFail {
		t.Fatalf("expected fail, got %v", got)
	}
}

func TestJobsNaming_LegacySlugPrefix_Passes(t *testing.T) {
	// Legacy seo example: name is "gonext-seo", jobs prefix with "seo."
	m := &Manifest{
		Slug: "gonext-seo",
		Name: "gonext-seo",
		Jobs: []string{"seo.recompute-scores"},
	}
	got := scenarioJobsRegistered().Run(context.Background(), m, fakehost.New())
	if got.Status != StatusPass {
		t.Fatalf("expected pass (legacy strip-gonext): %+v", got)
	}
}

func TestInitIdempotent_Passes(t *testing.T) {
	m := &Manifest{
		Slug: "seo",
		Jobs: []string{"seo.recompute"},
	}
	h := fakehost.New()
	got := scenarioInitTeardownIdempotent().Run(context.Background(), m, h)
	if got.Status != StatusPass {
		t.Fatalf("expected pass, got %+v", got)
	}
}

func TestFuelBudget_RecordsAsSkip(t *testing.T) {
	got := scenarioFuelBudget().Run(context.Background(), &Manifest{}, fakehost.New())
	if got.Status != StatusSkipped {
		t.Fatalf("expected skip until WASM runner wired: got %+v", got)
	}
}

func TestBuiltinScenarios_NonEmpty(t *testing.T) {
	if len(BuiltinScenarios()) == 0 {
		t.Fatal("BuiltinScenarios is empty")
	}
}
