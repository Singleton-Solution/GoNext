package plugintest

import (
	"encoding/json"
	"strings"
	"testing"
)

// minimalManifest is the smallest manifest [ValidateManifest] should accept.
// Tests mutate copies of it to verify each failure mode in isolation.
const minimalManifest = `{
  "$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",
  "slug": "gn-seo",
  "name": "GN SEO",
  "version": "1.0.0",
  "abi_version": 1,
  "license": "GPL-3.0-or-later",
  "server": { "wasm": "server/plugin.wasm" }
}`

func TestParseManifest_HappyPath(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Slug != "gn-seo" {
		t.Errorf("slug = %q; want %q", m.Slug, "gn-seo")
	}
	if m.ABIVersion != 1 {
		t.Errorf("abi_version = %d; want 1", m.ABIVersion)
	}
	if m.Server.WASM != "server/plugin.wasm" {
		t.Errorf("server.wasm = %q; want %q", m.Server.WASM, "server/plugin.wasm")
	}
}

func TestParseManifest_InvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte("not json"))
	if err == nil {
		t.Fatal("ParseManifest accepted non-JSON input")
	}
}

func TestValidateManifest(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Manifest)
		wantSub string // substring expected in at least one problem; "" = no problems
	}{
		{
			name:    "valid",
			mutate:  func(_ *Manifest) {},
			wantSub: "",
		},
		{
			name:    "missing $schema",
			mutate:  func(m *Manifest) { m.Schema = "" },
			wantSub: "$schema",
		},
		{
			name:    "wrong $schema",
			mutate:  func(m *Manifest) { m.Schema = "https://example.com/old.json" },
			wantSub: "want",
		},
		{
			name:    "missing slug",
			mutate:  func(m *Manifest) { m.Slug = "" },
			wantSub: `"slug"`,
		},
		{
			name:    "slug too short",
			mutate:  func(m *Manifest) { m.Slug = "ab" },
			wantSub: "does not match",
		},
		{
			name:    "slug with uppercase",
			mutate:  func(m *Manifest) { m.Slug = "GN-SEO" },
			wantSub: "does not match",
		},
		{
			name:    "missing name",
			mutate:  func(m *Manifest) { m.Name = "" },
			wantSub: `"name"`,
		},
		{
			name:    "missing version",
			mutate:  func(m *Manifest) { m.Version = "" },
			wantSub: `"version"`,
		},
		{
			name:    "missing license",
			mutate:  func(m *Manifest) { m.License = "" },
			wantSub: `"license"`,
		},
		{
			name:    "missing abi_version",
			mutate:  func(m *Manifest) { m.ABIVersion = 0 },
			wantSub: "abi_version",
		},
		{
			name:    "unsupported abi_version",
			mutate:  func(m *Manifest) { m.ABIVersion = 999 },
			wantSub: "not in supported set",
		},
		{
			name:    "missing server.wasm",
			mutate:  func(m *Manifest) { m.Server.WASM = "" },
			wantSub: "server.wasm",
		},
		{
			name: "unknown capability",
			mutate: func(m *Manifest) {
				m.Capabilities = map[string]json.RawMessage{"telepathy": json.RawMessage(`true`)}
			},
			wantSub: "unknown capability",
		},
		{
			name: "hook missing handler",
			mutate: func(m *Manifest) {
				m.Hooks.Actions = []HookEntry{{Name: "post.published"}}
			},
			wantSub: "missing handler",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(minimalManifest))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			tc.mutate(m)
			problems := ValidateManifest(m)
			if tc.wantSub == "" {
				if len(problems) != 0 {
					t.Fatalf("expected no problems; got %v", problems)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("expected at least one problem matching %q; got none", tc.wantSub)
			}
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.wantSub) {
				t.Fatalf("expected a problem matching %q; got:\n%s", tc.wantSub, joined)
			}
		})
	}
}

func TestValidateManifest_NilManifest(t *testing.T) {
	problems := ValidateManifest(nil)
	if len(problems) == 0 {
		t.Fatal("expected at least one problem for nil manifest")
	}
}

func TestIsKnownCapability(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"db", true},
		{"kv", true},
		{"http.serve", true},
		{"cache.invalidate", true},
		{"telepathy", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsKnownCapability(tc.name); got != tc.want {
				t.Errorf("IsKnownCapability(%q) = %v; want %v", tc.name, got, tc.want)
			}
		})
	}
}
