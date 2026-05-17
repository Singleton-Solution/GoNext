// Package model holds the Go types that back GraphQL scalars and any
// hand-written models bound from gqlgen.yml. Generated gqlgen model
// structs land in models_gen.go in this same package.
//
// Keep this file small and dependency-free: it is imported by both the
// generated execution code and the hand-written resolvers, so heavy
// transitive imports here will leak into both.
package model
