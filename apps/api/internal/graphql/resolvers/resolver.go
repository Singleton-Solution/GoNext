package resolvers

import (
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Resolver is the gqlgen root resolver. It carries the dependencies
// the field resolvers need — repositories, the policy engine — so
// resolver methods are pure functions of (ctx, args, deps).
//
// This struct is the ONLY hand-written code in resolvers/ that
// gqlgen does not touch. The other resolver files (schema.resolvers.go)
// are regenerated when the schema changes; their method bodies are
// preserved by gqlgen's "comment-out the old, splice in the new"
// merge strategy.
//
// Resolvers MUST be safe to share across goroutines (gqlgen calls
// them concurrently for sibling fields). The fields below are
// read-only after construction; per-request state lives on the
// context (principal, dataloaders) — not on the resolver.
type Resolver struct {
	PostRepo PostRepo
	UserRepo UserRepo
	Policy   policy.Policy
}
