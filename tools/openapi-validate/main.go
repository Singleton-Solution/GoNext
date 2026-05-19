// Command openapi-validate is the structural validator for
// apps/api/openapi/openapi.yaml (via the generated JSON mirror).
//
// It enforces the invariants the lint-openapi CI job cares about:
//
//   - The document parses as JSON.
//   - openapi field starts with "3.1".
//   - info, servers, paths, components are all present and non-empty.
//   - Every operationId is unique.
//   - Every $ref resolves to an existing JSON Pointer target.
//   - Every securitySchemes reference is defined.
//   - No orphan schemas (a schema referenced from a path lookup that
//     isn't in components is flagged).
//
// We intentionally avoid kin-openapi or similar third-party validators
// to keep the tool stdlib-only — it runs in CI without sync-ing the rest
// of the workspace's deps.
//
// Usage:
//
//	go run . [path/to/openapi.json]
//
// With no argument it loads apps/api/openapi/gonext.openapi.json relative
// to the workspace root. Exits 0 on success, 1 on validation failure (with
// every problem listed to stderr).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Singleton-Solution/GoNext/tools/openapi-validate/internal/validator"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [openapi.json]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	path := defaultSpecPath()
	if flag.NArg() > 0 {
		path = flag.Arg(0)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve %s: %v\n", path, err)
		os.Exit(1)
	}

	doc, err := validator.Load(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load %s: %v\n", abs, err)
		os.Exit(1)
	}

	issues := validator.Validate(doc)
	if len(issues) == 0 {
		fmt.Printf("OK: %s — %d paths, %d schemas\n", abs, len(doc.Paths), len(doc.Components.Schemas))
		return
	}
	fmt.Fprintf(os.Stderr, "FAIL: %s\n", abs)
	for _, iss := range issues {
		fmt.Fprintf(os.Stderr, "  - %s\n", iss)
	}
	os.Exit(1)
}

// defaultSpecPath returns the canonical spec location relative to the
// repository root. The validator is most often invoked from the repo
// root (`go run ./tools/openapi-validate`), but the build-from-tools-dir
// case is supported too: we walk up looking for go.work as the marker.
func defaultSpecPath() string {
	const rel = "apps/api/openapi/gonext.openapi.json"
	if _, err := os.Stat(rel); err == nil {
		return rel
	}
	// Walk up: tools/openapi-validate is two levels deep.
	if cwd, err := os.Getwd(); err == nil {
		for i := 0; i < 5; i++ {
			cwd = filepath.Dir(cwd)
			candidate := filepath.Join(cwd, rel)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return rel
}
