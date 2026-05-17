package graphql

// This file holds the go:generate directive that drives the gqlgen
// codegen. Run `go generate ./internal/graphql/...` after editing
// schema.graphql to regenerate the executable schema and model
// structs in generated/ and model/. Hand-written resolver bodies
// (resolvers/*.resolvers.go) are preserved by gqlgen's merge
// strategy — gqlgen detects existing implementations and splices
// them back into the regenerated file.
//
// The directive runs the gqlgen binary via `go run`, which is the
// stdlib-blessed way to invoke a versioned tool: it uses the exact
// version pinned in apps/api/go.mod (kept in the module graph by
// tools.go). No global install, no `which gqlgen` ambiguity.

//go:generate go run github.com/99designs/gqlgen generate
