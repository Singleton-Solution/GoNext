package depends

import (
	"fmt"
	"sort"
)

// Registry is the resolver's view onto the plugin world. Given a slug
// it returns the (read-only) record and an ok flag; ok=false means the
// plugin isn't installed at all.
//
// Production callers wire this to lifecycle.Storage; tests pass an
// in-memory fake. The function-value shape (rather than an interface)
// keeps the call site trivial and avoids dragging the lifecycle
// package into the depends package's dependency closure.
type Registry func(name string) (*PluginRecord, bool)

// Resolver checks a list of dependencies against a Registry view and
// returns a structured Report.
//
// Resolver is stateless except for the Registry function. Concurrent
// Check calls on a single Resolver are safe; the Registry implementation
// is responsible for its own internal locking. (lifecycle.MemoryStorage
// and lifecycle.PostgresStorage both already lock their reads.)
type Resolver struct {
	// Registry maps a slug to its current plugin record. Required.
	// Resolver.Check panics on a nil Registry — that's a wiring bug
	// that should fail at boot rather than at the first Activate.
	Registry Registry
}

// Check resolves the given dependency list against the registry and
// returns a Report. Returns a non-nil Report even on success (with all
// slices empty) so callers can range without nil checks.
//
// The returned error is reserved for malformed inputs (bad version
// range syntax). Soft failures — missing, inactive, incompatible —
// surface through the Report fields. Callers decide how to convert
// those into the typed activation errors; the Gate does that mapping.
//
// The deps argument is treated as read-only. Duplicate entries are
// allowed (manifest.Validate de-duplicates via uniqueItems, but
// programmatic callers may pass dupes); the resolver tolerates them.
func (r *Resolver) Check(deps []Dependency) (*Report, error) {
	if r == nil || r.Registry == nil {
		// Programmer error, not a runtime one. We surface it as an
		// error rather than a panic so a test that constructs a
		// Resolver{} by mistake gets a useful message.
		return nil, fmt.Errorf("depends.Resolver: Registry is required")
	}
	out := &Report{}
	if len(deps) == 0 {
		return out, nil
	}

	// Iterate in input order. Stable output makes the resulting
	// error message deterministic for both humans and tests.
	for _, d := range deps {
		if d.Name == "" {
			return nil, fmt.Errorf("depends: empty dependency name")
		}
		if d.VersionRange == "" {
			return nil, fmt.Errorf("depends: %q: empty version range", d.Name)
		}

		rec, ok := r.Registry(d.Name)
		if !ok {
			out.Missing = append(out.Missing, d.Name)
			continue
		}
		if !rec.Active {
			out.Inactive = append(out.Inactive, d.Name)
			continue
		}
		matched, err := matchRange(rec.Version, d.VersionRange)
		if err != nil {
			// A malformed range is treated as an incompatibility
			// rather than a hard error so the operator gets a
			// structured failure surface — the schema would have
			// caught this at install time, but the resolver is the
			// last line of defence in case a manifest bypasses
			// validation.
			out.Incompatible = append(out.Incompatible, Mismatch{
				Name: d.Name,
				Got:  rec.Version,
				Want: d.VersionRange,
			})
			continue
		}
		if !matched {
			out.Incompatible = append(out.Incompatible, Mismatch{
				Name: d.Name,
				Got:  rec.Version,
				Want: d.VersionRange,
			})
		}
	}

	// Sort the buckets for deterministic output. Test assertions and
	// log lines depend on the order being stable across runs.
	sort.Strings(out.Missing)
	sort.Strings(out.Inactive)
	sort.Slice(out.Incompatible, func(i, j int) bool {
		return out.Incompatible[i].Name < out.Incompatible[j].Name
	})
	return out, nil
}
