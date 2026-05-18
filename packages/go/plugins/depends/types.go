package depends

import (
	"errors"
	"fmt"
	"strings"
)

// Dependency is one entry of a plugin's depends[] array, flattened
// into the shape the resolver consumes.
//
// Name is the dependency's plugin slug. VersionRange is a semver
// range using the npm-style operator vocabulary documented in the
// package doc. Both fields are required; the manifest schema enforces
// that, and the resolver re-validates so callers that bypass the
// schema (tests, programmatic construction) fail loudly.
//
// This type is structurally identical to manifest.Dependency. We
// re-declare it here so the depends package can be imported without
// dragging in the JSON-Schema compiler that manifest depends on. The
// Gate exposes a helper that converts manifest.Dependency to this
// shape so the wiring stays trivial.
type Dependency struct {
	Name         string
	VersionRange string
}

// PluginRecord is the resolver's view of an installed plugin.
//
// It's the small read-only subset the dependency check needs:
//
//   - Slug to identify the row,
//   - Version to compare against a range,
//   - Active to detect plugins that are installed but not currently
//     running, and
//   - Depends so the topological sort can follow edges into pending
//     transitive activations.
//
// Lifecycle's Plugin row is the production source; the Registry
// adapter converts it to this shape. Tests construct PluginRecord
// values directly.
type PluginRecord struct {
	Slug    string
	Version string
	Active  bool
	Depends []Dependency
}

// Mismatch describes a dependency whose declared version sits outside
// the requested range. Name is the dependent's slug, Got is the
// installed version of the dependency, Want is the range the manifest
// requested.
type Mismatch struct {
	Name string
	Got  string
	Want string
}

// String renders the mismatch as "name@got, want want". Used in error
// messages so logs stay readable.
func (m Mismatch) String() string {
	return fmt.Sprintf("%s@%s (want %s)", m.Name, m.Got, m.Want)
}

// Report aggregates every problem the resolver found in one pass.
//
// Missing lists slugs the registry didn't know about at all.
// Inactive lists slugs that are installed but not in the Active
// state. Incompatible lists slugs whose installed version fell
// outside the requested range. A Report with all three slices empty
// means the dependency set resolved cleanly.
//
// The resolver always returns a non-nil Report so callers can range
// over the slices without nil-checking.
type Report struct {
	Missing      []string
	Inactive     []string
	Incompatible []Mismatch
}

// OK reports whether every dependency satisfied its constraint.
func (r *Report) OK() bool {
	if r == nil {
		return true
	}
	return len(r.Missing) == 0 && len(r.Inactive) == 0 && len(r.Incompatible) == 0
}

// ErrMissingDependency is returned by Gate.AllowActivate when one or
// more dependencies are not present in storage at all. The error wraps
// the offending slugs for structured display.
var ErrMissingDependency = errors.New("depends: missing dependency")

// ErrInactiveDependency is returned by Gate.AllowActivate when one or
// more dependencies are installed but not currently Active.
var ErrInactiveDependency = errors.New("depends: dependency not active")

// ErrVersionMismatch is returned by Gate.AllowActivate when one or
// more dependencies report a version outside the requested range.
var ErrVersionMismatch = errors.New("depends: dependency version mismatch")

// ErrCircularDependency is returned by the topological sort when the
// dependency graph contains a cycle. The error message lists the slugs
// participating in the cycle for operator triage.
var ErrCircularDependency = errors.New("depends: circular dependency")

// DependencyError is the structured error type AllowActivate returns
// when a dependency check fails. It satisfies errors.Is for each of
// the four typed sentinels above so callers can switch on the failure
// mode without parsing strings.
//
// Slugs carries the offending dependency names (or, for the version
// kind, the rendered "name@got want range" tuples). The admin UI
// reads this directly to render a structured failure list.
type DependencyError struct {
	// Plugin is the slug of the plugin whose Activate was refused.
	Plugin string
	// Kind selects which sentinel this error satisfies. One of
	// ErrMissingDependency, ErrInactiveDependency, ErrVersionMismatch,
	// ErrCircularDependency.
	Kind error
	// Slugs is the affected dependency names. Always populated.
	Slugs []string
	// Mismatches is populated only when Kind == ErrVersionMismatch.
	Mismatches []Mismatch
}

// Error renders the typed failure. Slug list is comma-joined to keep
// the line scannable in logs; structured renderers should walk Slugs
// directly.
func (e *DependencyError) Error() string {
	var what string
	switch {
	case errors.Is(e.Kind, ErrMissingDependency):
		what = "missing"
	case errors.Is(e.Kind, ErrInactiveDependency):
		what = "inactive"
	case errors.Is(e.Kind, ErrVersionMismatch):
		what = "version mismatch"
	case errors.Is(e.Kind, ErrCircularDependency):
		what = "circular dependency"
	default:
		what = "dependency failure"
	}
	detail := strings.Join(e.Slugs, ", ")
	if len(e.Mismatches) > 0 {
		parts := make([]string, len(e.Mismatches))
		for i, m := range e.Mismatches {
			parts[i] = m.String()
		}
		detail = strings.Join(parts, ", ")
	}
	if e.Plugin == "" {
		return fmt.Sprintf("depends: %s: %s", what, detail)
	}
	return fmt.Sprintf("depends: activate %q: %s: %s", e.Plugin, what, detail)
}

// Unwrap exposes Kind so errors.Is matches against the sentinels.
func (e *DependencyError) Unwrap() error { return e.Kind }
