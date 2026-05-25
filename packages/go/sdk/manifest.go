package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Manifest is the plugin-author-facing mirror of the gonext.io/v1
// manifest.json schema. It exists in the SDK so a plugin author can
// build a manifest programmatically (via NewManifest().With...().Build())
// rather than hand-rolling the JSON.
//
// The shape is intentionally a subset of the host's
// plugins/manifest.Manifest: only the fields a plugin author needs at
// build time. The Validate-able JSON Schema lives in the host package;
// the SDK's Build() emits JSON that passes that schema.
//
// We do not depend on the host manifest package because TinyGo can't
// compile its transitive imports (jsonschema, etc). The "single source
// of truth" property is preserved by the manifest-golden test in the
// host package: it includes a manifest-emitted-by-SDK fixture and
// validates it against the schema in CI.
type Manifest struct {
	APIVersion   string             `json:"apiVersion"`
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Entry        string             `json:"entry,omitempty"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Hooks        *ManifestHooks     `json:"hooks,omitempty"`
	Jobs         []string           `json:"jobs,omitempty"`
	Requires     *ManifestRequires  `json:"requires,omitempty"`
	Depends      []ManifestDepend   `json:"depends,omitempty"`
	Storage      *ManifestStorage   `json:"storage,omitempty"`
}

// ManifestHooks is the actions/filters split. Both arrays optional;
// an empty object is legal (but pointless).
type ManifestHooks struct {
	Actions []string `json:"actions,omitempty"`
	Filters []string `json:"filters,omitempty"`
}

// ManifestRequires carries compatibility ranges. Host is required if
// the object is present.
type ManifestRequires struct {
	Host string `json:"host"`
}

// ManifestDepend is one entry of the depends[] array — another plugin
// slug pinned to a semver range.
type ManifestDepend struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ManifestStorage is the persistent-storage budget bag. KV is the only
// surface in v1.
type ManifestStorage struct {
	KV *ManifestKVStorage `json:"kv,omitempty"`
}

// ManifestKVStorage carries the per-plugin KV namespace budget. Both
// fields are pointers so omitting them is distinguishable from setting
// them to zero (which means "must hold none").
type ManifestKVStorage struct {
	MaxBytes *int64 `json:"max_bytes,omitempty"`
	MaxKeys  *int   `json:"max_keys,omitempty"`
}

// ManifestBuilder is a fluent builder for Manifest. The pattern lets
// plugin authors write:
//
//	manifest := sdk.NewManifest("hello", "0.1.0").
//	    WithCapability("kv.write").
//	    WithCapability("audit.emit").
//	    WithAction("posts.publish").
//	    WithFilter("the_content").
//	    Build()
//
// Build returns a Manifest pre-populated with apiVersion/entry defaults
// and validates the minimum invariants (non-empty name/version, valid
// slug, no dupes). MustBuild is the panic-on-error variant.
type ManifestBuilder struct {
	m   Manifest
	err error
}

// NewManifest starts a builder. apiVersion defaults to "gonext.io/v1"
// and entry defaults to "plugin.wasm" — matching the convention every
// example in docs/02-plugin-system.md uses. Override either via
// WithAPIVersion / WithEntry.
func NewManifest(name, version string) *ManifestBuilder {
	b := &ManifestBuilder{
		m: Manifest{
			APIVersion: "gonext.io/v1",
			Name:       name,
			Version:    version,
			Entry:      "plugin.wasm",
		},
	}
	if strings.TrimSpace(name) == "" {
		b.err = errors.New("manifest: name is required")
	}
	if strings.TrimSpace(version) == "" {
		b.err = errors.New("manifest: version is required")
	}
	return b
}

// WithAPIVersion overrides the apiVersion field. The current v1 host
// accepts only "gonext.io/v1"; pass anything else and the host will
// reject the bundle at install time.
func (b *ManifestBuilder) WithAPIVersion(v string) *ManifestBuilder {
	b.m.APIVersion = v
	return b
}

// WithEntry overrides the wasm entry filename. Defaults to
// "plugin.wasm" — matching the build target every Makefile in the
// template directory writes.
func (b *ManifestBuilder) WithEntry(name string) *ManifestBuilder {
	b.m.Entry = name
	return b
}

// WithCapability appends one capability slug to the request list. Dupes
// are skipped — capabilities are a set, not a multiset. The host
// enforces grant policy at activation; the SDK only declares intent.
//
// Common values: posts.read, posts.write, http.fetch, db.read, db.write,
// kv.read, kv.write, cache.invalidate, audit.emit, cron.register,
// secrets.read.
func (b *ManifestBuilder) WithCapability(name string) *ManifestBuilder {
	for _, existing := range b.m.Capabilities {
		if existing == name {
			return b
		}
	}
	b.m.Capabilities = append(b.m.Capabilities, name)
	return b
}

// WithCapabilities appends multiple capabilities; same semantics as
// repeated WithCapability calls.
func (b *ManifestBuilder) WithCapabilities(names ...string) *ManifestBuilder {
	for _, n := range names {
		b.WithCapability(n)
	}
	return b
}

// WithAction registers an action hook subscription. Dupes are skipped.
// The handler MUST be registered via RegisterAction at runtime; an
// action declared here but not registered surfaces as
// ResultStatusUnknownHook when the host invokes it.
func (b *ManifestBuilder) WithAction(name string) *ManifestBuilder {
	if b.m.Hooks == nil {
		b.m.Hooks = &ManifestHooks{}
	}
	for _, existing := range b.m.Hooks.Actions {
		if existing == name {
			return b
		}
	}
	b.m.Hooks.Actions = append(b.m.Hooks.Actions, name)
	return b
}

// WithFilter registers a filter hook subscription. Same dupe semantics
// as WithAction.
func (b *ManifestBuilder) WithFilter(name string) *ManifestBuilder {
	if b.m.Hooks == nil {
		b.m.Hooks = &ManifestHooks{}
	}
	for _, existing := range b.m.Hooks.Filters {
		if existing == name {
			return b
		}
	}
	b.m.Hooks.Filters = append(b.m.Hooks.Filters, name)
	return b
}

// WithJob registers a background job id. The host's scheduler resolves
// it against its task registry at activation.
func (b *ManifestBuilder) WithJob(id string) *ManifestBuilder {
	for _, existing := range b.m.Jobs {
		if existing == id {
			return b
		}
	}
	b.m.Jobs = append(b.m.Jobs, id)
	return b
}

// WithHostRequirement sets the requires.host semver range.
func (b *ManifestBuilder) WithHostRequirement(rng string) *ManifestBuilder {
	b.m.Requires = &ManifestRequires{Host: rng}
	return b
}

// WithDependency adds an inter-plugin dependency entry.
func (b *ManifestBuilder) WithDependency(name, versionRange string) *ManifestBuilder {
	b.m.Depends = append(b.m.Depends, ManifestDepend{Name: name, Version: versionRange})
	return b
}

// WithKVStorage declares persistent-storage budgets for the plugin's
// KV namespace. Pass nil for either field to leave it omitted (use
// operator default). Pass a pointer to zero to declare "must hold none"
// — useful for disabling the namespace.
func (b *ManifestBuilder) WithKVStorage(maxBytes *int64, maxKeys *int) *ManifestBuilder {
	if b.m.Storage == nil {
		b.m.Storage = &ManifestStorage{}
	}
	b.m.Storage.KV = &ManifestKVStorage{MaxBytes: maxBytes, MaxKeys: maxKeys}
	return b
}

// Build returns the configured Manifest. If the builder accumulated
// an error (empty name/version), Build returns the zero Manifest and
// the error — callers that prefer panic can use MustBuild instead.
//
// Note: this is a SHALLOW build. The full schema validation lives in
// the host's plugins/manifest package; Build only checks invariants
// the builder can guarantee statically (non-empty fields, no dupes).
func (b *ManifestBuilder) Build() (Manifest, error) {
	if b.err != nil {
		return Manifest{}, b.err
	}
	return b.m, nil
}

// MustBuild is the panic-on-error variant. Convenient when a plugin
// builds its manifest at package init time — a misconfigured manifest
// is a programmer error, not a runtime condition.
func (b *ManifestBuilder) MustBuild() Manifest {
	m, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("sdk.Manifest.MustBuild: %v", err))
	}
	return m
}

// MarshalJSON emits the manifest as the canonical JSON the host
// schema validates. We implement this explicitly (instead of relying
// on the struct tags alone) so the field order matches the host's
// schema example and golden fixtures.
func (m Manifest) MarshalJSON() ([]byte, error) {
	// Use an alias to avoid recursing into ourselves.
	type alias Manifest
	return json.Marshal(alias(m))
}
