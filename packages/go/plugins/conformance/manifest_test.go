package conformance

import (
	"testing"
)

func TestParseManifest_V1Schema(t *testing.T) {
	in := []byte(`{
		"$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
		"slug": "gn-seo",
		"name": "SEO",
		"version": "1.0.0",
		"capabilities": {"kv": {}, "posts.read": {}},
		"hooks": {
			"actions": [{"name": "save_post", "handler": "onSave"}],
			"filters": [{"name": "the_content", "handler": "onContent"}]
		},
		"jobs": ["gn-seo.recompute"]
	}`)
	m, err := ParseManifest(in)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Slug != "gn-seo" {
		t.Fatalf("slug = %q", m.Slug)
	}
	if !m.HasCapability("kv") || !m.HasCapability("posts.read") {
		t.Fatalf("caps = %v", m.Capabilities)
	}
	if !m.HasHook("save_post") || !m.HasHook("the_content") {
		t.Fatalf("hooks = %v", m.Hooks)
	}
	if len(m.Jobs) != 1 || m.Jobs[0] != "gn-seo.recompute" {
		t.Fatalf("jobs = %v", m.Jobs)
	}
}

func TestParseManifest_LegacySchema(t *testing.T) {
	in := []byte(`{
		"apiVersion": "gonext.io/v1",
		"name": "gonext-seo",
		"version": "0.1.0",
		"capabilities": ["posts.read", "hooks.subscribe"],
		"hooks": {
			"filters": ["the_content"],
			"actions": ["wp_head", "save_post"]
		},
		"jobs": ["seo.recompute-scores"]
	}`)
	m, err := ParseManifest(in)
	if err != nil {
		t.Fatalf("ParseManifest legacy: %v", err)
	}
	if m.Slug != "gonext-seo" {
		t.Fatalf("slug = %q", m.Slug)
	}
	if !m.HasCapability("hooks.subscribe") {
		t.Fatalf("legacy cap not picked up: %v", m.Capabilities)
	}
	if !m.HasHook("the_content") || !m.HasHook("save_post") {
		t.Fatalf("legacy hooks = %v", m.Hooks)
	}
}

func TestParseManifest_BadJSON_Errors(t *testing.T) {
	if _, err := ParseManifest([]byte("not-json")); err == nil {
		t.Fatal("expected error on garbage input")
	}
}

func TestVocabulary_Known(t *testing.T) {
	for _, c := range []string{"kv", "db", "posts.read", "hooks.subscribe", "jobs.enqueue"} {
		if !IsKnownCapability(c) {
			t.Errorf("expected %q to be known", c)
		}
	}
	if IsKnownCapability("unicorn") {
		t.Error("unicorn should be unknown")
	}
}
