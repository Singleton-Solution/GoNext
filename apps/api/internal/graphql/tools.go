//go:build tools

// Package graphql tools build tag: this file exists ONLY so that
// `go mod tidy` keeps the gqlgen toolchain dependencies in
// apps/api/go.mod. Without it, `go mod tidy` would strip them — they
// are only used at code-generation time, not by the runtime — and
// `go generate` would fail in CI.
//
// The build tag `tools` ensures this file is never compiled into any
// real binary; it just acts as an import declaration for the module
// graph.
package graphql

import (
	// Pulls the gqlgen executable's API package into the module graph.
	// We never call into it at runtime — the runtime uses the
	// generated/ output instead — but importing it pins the version.
	_ "github.com/99designs/gqlgen/api"
	_ "github.com/99designs/gqlgen/codegen/config"
)
