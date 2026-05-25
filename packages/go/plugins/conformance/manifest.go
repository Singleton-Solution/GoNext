package conformance

import (
	"encoding/json"
	"fmt"
)

// Manifest is the conformance suite's parsed view of the plugin
// manifest. We re-declare this (rather than importing the CLI's
// plugintest.Manifest) so the suite is usable from non-CLI
// contexts (tests, future server-side certification jobs).
//
// The field set is the union of what conformance scenarios read:
// slug, capabilities, hooks, jobs. We tolerate the legacy
// `apiVersion`/`entry`/`requires` keys that some early plugins
// (including examples/plugins/seo) use; both legacy and v1 layouts
// are mapped to the same internal representation.
type Manifest struct {
	// Slug is the plugin slug. The v1 manifest names it "slug";
	// legacy manifests put it in "name".
	Slug string

	// Name is the human-readable plugin name (display only).
	Name string

	// Version is the manifest's declared version string.
	Version string

	// Capabilities is the deduplicated set of capability tokens.
	// v1 manifests declare them as map keys; legacy ones use an
	// array — both decode here.
	Capabilities []string

	// Hooks are the registered hook names (across actions and
	// filters).
	Hooks []string

	// Jobs are the registered job names.
	Jobs []string

	// Raw is the original JSON bytes — preserved for any scenario
	// that needs to assert on a field this struct does not yet
	// expose. Marshaling back from Raw is NOT guaranteed to round-trip
	// (we don't preserve key order).
	Raw []byte
}

// ParseManifest decodes the bundle's manifest JSON into a [Manifest].
// It accepts BOTH the v1 schema (slug + capabilities map + nested
// hooks) and the legacy schema (name + capabilities array + flat
// hooks). Unknown top-level keys are ignored — manifests evolve and
// the suite shouldn't break on additions.
func ParseManifest(b []byte) (*Manifest, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("manifest: invalid JSON: %w", err)
	}
	m := &Manifest{Raw: append([]byte(nil), b...)}

	// Slug — v1 uses "slug", legacy uses "name".
	if v, ok := raw["slug"].(string); ok && v != "" {
		m.Slug = v
	} else if v, ok := raw["name"].(string); ok {
		m.Slug = v
	}
	if v, ok := raw["name"].(string); ok {
		m.Name = v
	}
	if v, ok := raw["version"].(string); ok {
		m.Version = v
	}

	// Capabilities — v1 is a map, legacy an array.
	switch caps := raw["capabilities"].(type) {
	case map[string]any:
		for k := range caps {
			m.Capabilities = append(m.Capabilities, k)
		}
	case []any:
		for _, v := range caps {
			if s, ok := v.(string); ok {
				m.Capabilities = append(m.Capabilities, s)
			}
		}
	}

	// Hooks — v1 nests actions+filters under "hooks"; legacy flat.
	switch hooks := raw["hooks"].(type) {
	case map[string]any:
		for _, key := range []string{"actions", "filters"} {
			switch list := hooks[key].(type) {
			case []any:
				for _, h := range list {
					switch t := h.(type) {
					case string:
						m.Hooks = append(m.Hooks, t)
					case map[string]any:
						if name, ok := t["name"].(string); ok {
							m.Hooks = append(m.Hooks, name)
						}
					}
				}
			}
		}
	case []any:
		for _, v := range hooks {
			if s, ok := v.(string); ok {
				m.Hooks = append(m.Hooks, s)
			}
		}
	}

	// Jobs — array of strings in both layouts.
	if jobs, ok := raw["jobs"].([]any); ok {
		for _, v := range jobs {
			if s, ok := v.(string); ok {
				m.Jobs = append(m.Jobs, s)
			}
		}
	}

	return m, nil
}

// HasCapability reports whether the manifest declares cap.
func (m *Manifest) HasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// HasHook reports whether the manifest registers the named hook.
func (m *Manifest) HasHook(name string) bool {
	for _, h := range m.Hooks {
		if h == name {
			return true
		}
	}
	return false
}
