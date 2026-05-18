package schemas

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
)

// builtinFS embeds the canonical schema JSON files shipped with the
// package. They live next to this source file so a future maintainer can
// edit them with regular editor tooling — and so the same files can be
// shared with the TypeScript host (packages/ts/hooks-schemas) via a
// build-time sync. Keeping the files in their own subdir (schemas/)
// rather than alongside the .go sources makes the //go:embed glob
// straightforward and the directory readable.
//
//go:embed schemas/*.json
var builtinFS embed.FS

// builtinDir is the embed.FS subdirectory the JSON files live in.
const builtinDir = "schemas"

// BuiltinSchemas reads every JSON file from the embedded schemas/
// directory and returns name -> raw schema bytes. The hook name is the
// file's base name without the .json suffix.
//
// Used by [BuiltinRegistry] to populate a registry; also exported so the
// docs generator and the TS-side codegen can iterate the canonical set
// without re-parsing the directory layout.
func BuiltinSchemas() map[string][]byte {
	entries, err := fs.ReadDir(builtinFS, builtinDir)
	if err != nil {
		// embed.FS errors here are programmer bugs — the //go:embed
		// directive guarantees the directory exists at build time.
		// Surfacing the error rather than panicking keeps tests
		// deterministic without obscuring the underlying cause.
		panic(fmt.Errorf("schemas: read embedded builtins: %w", err))
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := fs.ReadFile(builtinFS, path.Join(builtinDir, name))
		if err != nil {
			panic(fmt.Errorf("schemas: read embedded %s: %w", name, err))
		}
		hook := strings.TrimSuffix(name, ".json")
		out[hook] = raw
	}
	return out
}

// builtinRegistry is the lazily-initialised, process-wide registry that
// owns the built-in schemas. We build it once because compiling 20
// schemas runs the JSON parser 20 times — cheap, but no reason to
// re-do it per-call.
var (
	builtinOnce sync.Once
	builtinReg  *Registry
)

// BuiltinRegistry returns a [Registry] pre-populated with the WP-compat
// hook schemas embedded in this package. The same *Registry pointer is
// returned on every call — callers should treat it as a shared, read-only
// reference. If a host needs an editable registry that starts from the
// built-ins, see [BuiltinRegistryCopy].
func BuiltinRegistry() *Registry {
	builtinOnce.Do(func() {
		builtinReg = NewRegistry()
		for hook, raw := range BuiltinSchemas() {
			builtinReg.MustRegister(hook, raw)
		}
	})
	return builtinReg
}

// BuiltinRegistryCopy returns a fresh [Registry] populated with the
// built-in schemas. Use this when the host wants to register additional
// plugin-specific schemas on top of the WP-compat baseline without
// mutating the process-wide singleton (which would surface as test
// pollution).
func BuiltinRegistryCopy() *Registry {
	r := NewRegistry()
	for hook, raw := range BuiltinSchemas() {
		r.MustRegister(hook, raw)
	}
	return r
}
