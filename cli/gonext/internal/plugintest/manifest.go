package plugintest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// supportedABIVersions enumerates the host ABI versions this build of the
// CLI knows how to validate against. Mirrors the host-side advertised list
// from docs/02-plugin-system.md §2.2 ("abi_version" notes). Update when the
// host adds a new ABI version.
var supportedABIVersions = map[int]bool{
	1: true,
}

// slugPattern is the canonical slug grammar from docs/02-plugin-system.md
// §2.2 ("`^[a-z][a-z0-9-]{2,40}$`"). It is the plugin's namespace platform-wide.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,40}$`)

// schemaURI is the `$schema` value the manifest must declare. This is the
// JSON Schema 2020-12 pin from docs/02-plugin-system.md §7.7.
const schemaURI = "https://wpc.dev/schemas/plugin-manifest-v1.json"

// Manifest is the subset of the plugin manifest the contract checks read.
// It is intentionally narrow — the full schema is the responsibility of the
// JSON Schema validator referenced in [schemaURI]. This struct only covers
// fields the CLI needs to drive subsequent checks (layout, WASM path,
// capability vocabulary).
type Manifest struct {
	Schema      string `json:"$schema"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	ABIVersion  int    `json:"abi_version"`
	License     string `json:"license"`
	Description string `json:"description,omitempty"`

	Server struct {
		WASM string `json:"wasm"`
	} `json:"server"`

	Capabilities map[string]json.RawMessage `json:"capabilities,omitempty"`

	Hooks struct {
		Actions []HookEntry `json:"actions,omitempty"`
		Filters []HookEntry `json:"filters,omitempty"`
	} `json:"hooks"`

	AdminPages []AdminPage `json:"admin_pages,omitempty"`
}

// HookEntry is a single manifest-declared hook registration.
type HookEntry struct {
	Name     string `json:"name"`
	Priority int    `json:"priority"`
	Handler  string `json:"handler"`
}

// AdminPage is one entry in the manifest's `admin_pages` slot.
type AdminPage struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Capability string `json:"capability"`
	Entry      string `json:"entry"`
}

// ParseManifest decodes manifest JSON bytes into a [Manifest]. Returns a
// detailed error if the bytes are not valid JSON.
func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields() // strict — typos in field names are bugs
	if err := dec.Decode(&m); err != nil {
		// Fall back to a lenient parse to give a better diagnostic when the
		// only problem is an unknown field — strict mode collapses those
		// into a generic error.
		var lenient Manifest
		if jerr := json.Unmarshal(b, &lenient); jerr == nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
		return nil, fmt.Errorf("manifest: invalid JSON: %w", err)
	}
	return &m, nil
}

// ValidateManifest runs the structural checks that don't depend on the WASM
// host: required fields, slug grammar, supported abi_version, schema URI
// pin, and capability vocabulary. Returns a list of human-readable problems;
// empty means the manifest is valid.
func ValidateManifest(m *Manifest) []string {
	var problems []string

	if m == nil {
		return []string{"manifest is nil"}
	}

	if m.Schema == "" {
		problems = append(problems, `missing "$schema" — manifest must pin the v1 schema URI`)
	} else if m.Schema != schemaURI {
		problems = append(problems, fmt.Sprintf("`$schema` = %q; want %q", m.Schema, schemaURI))
	}

	if m.Slug == "" {
		problems = append(problems, `missing required field "slug"`)
	} else if !slugPattern.MatchString(m.Slug) {
		problems = append(problems, fmt.Sprintf("slug %q does not match %s", m.Slug, slugPattern.String()))
	}

	if m.Name == "" {
		problems = append(problems, `missing required field "name"`)
	}
	if m.Version == "" {
		problems = append(problems, `missing required field "version"`)
	}
	if m.License == "" {
		problems = append(problems, `missing required field "license"`)
	}

	if m.ABIVersion == 0 {
		problems = append(problems, `missing required field "abi_version"`)
	} else if !supportedABIVersions[m.ABIVersion] {
		problems = append(problems, fmt.Sprintf("abi_version %d not in supported set %v", m.ABIVersion, sortedKeys(supportedABIVersions)))
	}

	if m.Server.WASM == "" {
		problems = append(problems, `missing required field "server.wasm"`)
	}

	for cap := range m.Capabilities {
		if !IsKnownCapability(cap) {
			problems = append(problems, fmt.Sprintf("unknown capability %q (not in v1 vocabulary)", cap))
		}
	}

	for i, h := range m.Hooks.Actions {
		if h.Name == "" {
			problems = append(problems, fmt.Sprintf("hooks.actions[%d]: missing name", i))
		}
		if h.Handler == "" {
			problems = append(problems, fmt.Sprintf("hooks.actions[%d] (%s): missing handler", i, h.Name))
		}
	}
	for i, h := range m.Hooks.Filters {
		if h.Name == "" {
			problems = append(problems, fmt.Sprintf("hooks.filters[%d]: missing name", i))
		}
		if h.Handler == "" {
			problems = append(problems, fmt.Sprintf("hooks.filters[%d] (%s): missing handler", i, h.Name))
		}
	}

	return problems
}

// sortedKeys returns the integer keys of m sorted ascending — handy for
// stable error messages.
func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
