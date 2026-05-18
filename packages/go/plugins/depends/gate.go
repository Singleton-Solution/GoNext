package depends

import (
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// Gate is the public entry point the lifecycle Manager calls before
// flipping a plugin to Active.
//
// It owns the Resolver and serialises check + activate decisions with
// a sync.Mutex so a concurrent activation of two interdependent
// plugins can't see an inconsistent registry view in between Resolver
// runs. The mutex is sufficient because the lock is brief: the gate
// only reads the registry, never blocks on I/O.
//
// Construct one Gate per process at boot. The zero value is not
// usable — use NewGate so the Resolver is wired up correctly.
type Gate struct {
	mu       sync.Mutex
	resolver *Resolver
}

// NewGate returns a Gate whose AllowActivate consults registry. A nil
// registry is a wiring bug; NewGate panics in that case so the failure
// surfaces at boot.
func NewGate(registry Registry) *Gate {
	if registry == nil {
		panic("depends.NewGate: registry is required")
	}
	return &Gate{resolver: &Resolver{Registry: registry}}
}

// AllowActivate evaluates m's depends[] against the registry and
// returns nil iff every dependency is installed, Active, and version-
// compatible. On any failure it returns a *DependencyError whose Kind
// matches the first failing category, in order:
//
//  1. ErrMissingDependency (highest priority: nothing else matters if
//     a dep isn't installed)
//  2. ErrInactiveDependency
//  3. ErrVersionMismatch
//
// The ordering means an operator who installs a missing plugin sees
// the next layer of problems on the retry rather than chasing
// transient version errors against a record that doesn't exist.
//
// A nil manifest is treated as "no dependencies" and passes through.
// Same for a manifest with an empty Depends slice: the gate is a
// strict no-op in that case. This matters because every existing
// plugin in the registry pre-dates this field and must still
// activate.
func (g *Gate) AllowActivate(m *manifest.Manifest) error {
	if g == nil || m == nil || len(m.Depends) == 0 {
		return nil
	}
	deps := FromManifest(m.Depends)

	// Serialise the check so two concurrent AllowActivate calls
	// observe the same registry snapshot. The lock is held for the
	// duration of the Resolver.Check call only — no I/O happens
	// inside it.
	g.mu.Lock()
	report, err := g.resolver.Check(deps)
	g.mu.Unlock()
	if err != nil {
		return err
	}
	if report.OK() {
		return nil
	}

	plugin := m.Name
	// Apply the priority ordering documented above.
	if len(report.Missing) > 0 {
		return &DependencyError{
			Plugin: plugin,
			Kind:   ErrMissingDependency,
			Slugs:  append([]string(nil), report.Missing...),
		}
	}
	if len(report.Inactive) > 0 {
		return &DependencyError{
			Plugin: plugin,
			Kind:   ErrInactiveDependency,
			Slugs:  append([]string(nil), report.Inactive...),
		}
	}
	slugs := make([]string, len(report.Incompatible))
	for i, m := range report.Incompatible {
		slugs[i] = m.Name
	}
	return &DependencyError{
		Plugin:     plugin,
		Kind:       ErrVersionMismatch,
		Slugs:      slugs,
		Mismatches: append([]Mismatch(nil), report.Incompatible...),
	}
}

// FromManifest converts the manifest's typed Dependency slice into
// the resolver's shape. Exported so external callers (tests, CLI
// linters) can reuse the conversion without re-implementing it.
func FromManifest(in []manifest.Dependency) []Dependency {
	if len(in) == 0 {
		return nil
	}
	out := make([]Dependency, len(in))
	for i, d := range in {
		out[i] = Dependency{Name: d.Name, VersionRange: d.Version}
	}
	return out
}
