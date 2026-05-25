package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestBuilderDefaults(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.APIVersion != "gonext.io/v1" {
		t.Errorf("apiVersion: got %q, want gonext.io/v1", m.APIVersion)
	}
	if m.Entry != "plugin.wasm" {
		t.Errorf("entry: got %q, want plugin.wasm", m.Entry)
	}
	if m.Name != "hello" {
		t.Errorf("name: got %q, want hello", m.Name)
	}
	if m.Version != "0.1.0" {
		t.Errorf("version: got %q, want 0.1.0", m.Version)
	}
}

func TestManifestBuilderEmptyNameRejected(t *testing.T) {
	_, err := NewManifest("", "1.0.0").Build()
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifestBuilderEmptyVersionRejected(t *testing.T) {
	_, err := NewManifest("hello", "").Build()
	if err == nil {
		t.Fatal("expected error for empty version")
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManifestBuilderCapabilitiesDedup(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").
		WithCapability("kv.write").
		WithCapability("audit.emit").
		WithCapability("kv.write"). // dupe
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(m.Capabilities) != 2 {
		t.Errorf("expected 2 caps after dedup, got %d: %v", len(m.Capabilities), m.Capabilities)
	}
}

func TestManifestBuilderCapabilitiesVariadic(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").
		WithCapabilities("kv.write", "audit.emit", "http.fetch").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(m.Capabilities) != 3 {
		t.Errorf("expected 3 caps, got %d", len(m.Capabilities))
	}
}

func TestManifestBuilderHooks(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").
		WithAction("posts.publish").
		WithAction("posts.publish"). // dupe
		WithFilter("the_content").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.Hooks == nil {
		t.Fatal("expected non-nil hooks")
	}
	if len(m.Hooks.Actions) != 1 || m.Hooks.Actions[0] != "posts.publish" {
		t.Errorf("actions: %v", m.Hooks.Actions)
	}
	if len(m.Hooks.Filters) != 1 || m.Hooks.Filters[0] != "the_content" {
		t.Errorf("filters: %v", m.Hooks.Filters)
	}
}

func TestManifestBuilderJSONShape(t *testing.T) {
	m := NewManifest("gonext-hello", "0.1.0").
		WithCapability("kv.write").
		WithCapability("audit.emit").
		WithAction("posts.publish").
		WithFilter("the_content").
		WithHostRequirement(">=0.1.0").
		MustBuild()
	buf, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// We don't validate against the host schema here (that test
	// lives in the host package), but we do check the fields we
	// expect are present.
	s := string(buf)
	required := []string{
		`"apiVersion":"gonext.io/v1"`,
		`"name":"gonext-hello"`,
		`"version":"0.1.0"`,
		`"entry":"plugin.wasm"`,
		`"kv.write"`,
		`"audit.emit"`,
		`"posts.publish"`,
		`"the_content"`,
		// json.Marshal HTML-escapes `>` as a unicode sequence in
		// strings; we check the escaped form because that's what's
		// on the wire. The host's decoder accepts both forms.
		"\"host\":\"\\u003e=0.1.0\"",
	}
	for _, want := range required {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output: %s", want, s)
		}
	}
}

func TestManifestBuilderWithEntry(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").WithEntry("custom.wasm").Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.Entry != "custom.wasm" {
		t.Errorf("entry: got %q, want custom.wasm", m.Entry)
	}
}

func TestManifestBuilderWithDependency(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").
		WithDependency("gn-seo", "^0.1.0").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(m.Depends) != 1 {
		t.Fatalf("depends length: got %d, want 1", len(m.Depends))
	}
	if m.Depends[0].Name != "gn-seo" || m.Depends[0].Version != "^0.1.0" {
		t.Errorf("depends entry: %+v", m.Depends[0])
	}
}

func TestManifestBuilderWithJob(t *testing.T) {
	m, err := NewManifest("hello", "0.1.0").
		WithJob("recompute-scores").
		WithJob("recompute-scores"). // dupe
		WithJob("notify-admins").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(m.Jobs) != 2 {
		t.Errorf("jobs after dedup: %v", m.Jobs)
	}
}

func TestManifestBuilderWithKVStorage(t *testing.T) {
	maxBytes := int64(1024 * 1024)
	maxKeys := 100
	m, err := NewManifest("hello", "0.1.0").
		WithKVStorage(&maxBytes, &maxKeys).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if m.Storage == nil || m.Storage.KV == nil {
		t.Fatal("storage.kv should be set")
	}
	if *m.Storage.KV.MaxBytes != maxBytes {
		t.Errorf("max_bytes: %d", *m.Storage.KV.MaxBytes)
	}
	if *m.Storage.KV.MaxKeys != maxKeys {
		t.Errorf("max_keys: %d", *m.Storage.KV.MaxKeys)
	}
}

func TestMustBuildPanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	NewManifest("", "0.1.0").MustBuild()
}
