// Package graphql is the GoNext API's GraphQL surface. It owns the
// schema (schema.graphql), the gqlgen-generated execution code
// (generated/), the hand-written resolvers (resolvers/), and the
// HTTP handler that wires it all together.
//
// The handler responsibilities are intentionally narrow:
//
//   - Mount at /api/graphql.
//   - Attach per-request DataLoaders so the resolver fan-out
//     (Post.author etc.) coalesces into batched repo calls.
//   - Run the cost analyzer as an OperationMiddleware, rejecting
//     pathological queries before they reach the resolvers.
//   - Map sentinel resolver errors (errUnauthorized, errForbidden) to
//     GraphQL error extension codes so clients can branch.
//
// What the handler does NOT do:
//
//   - Auth. The standard session middleware (packages/go/middleware/auth)
//     runs upstream and attaches the policy.Principal; the handler
//     simply reads it via FromContext.
//   - Rate limiting. The shared API rate-limit middleware handles it;
//     GraphQL operations count against the same buckets as REST per
//     docs/05-admin-api.md §3.5.
//   - Persisted queries. That lands in a follow-up (the persistent
//     store needs a Redis schema and an admin UI; out of scope for
//     #83).
package graphql

import (
	"context"
	"errors"
	"net/http"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	gqlerror "github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/cost"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/dataloader"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/generated"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/resolvers"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bundle the handler needs to wire resolvers.
// The HTTP layer composes it once at boot and reuses it across
// requests — every field must therefore be safe to share across
// goroutines.
type Deps struct {
	// PostRepo is the persistence-layer interface for posts. In
	// production this is a pgx-backed implementation; tests pass a
	// fake.
	PostRepo resolvers.PostRepo

	// UserRepo is the persistence-layer interface for users. Used
	// directly by some resolvers and indirectly via the dataloader.
	UserRepo resolvers.UserRepo

	// Policy is the capability checker the resolvers call into.
	// Typically *policy.BasicPolicy in P0; the DB-backed policy is a
	// drop-in replacement when it lands.
	Policy policy.Policy

	// Cost configures the query cost budgets. Zero values fall back
	// to cost.DefaultAnonymousBudget / cost.DefaultAuthenticatedBudget.
	Cost cost.Config

	// EnableIntrospection toggles GraphQL schema introspection.
	// Default off — production servers should not advertise their
	// schema to anonymous clients (docs/05-admin-api.md §3.2).
	// Operators flip it on in dev via env, or per-request when an
	// admin token is present.
	EnableIntrospection bool
}

// Handler returns the http.Handler that serves the GraphQL endpoint.
//
// Mounting:
//
//	mux.Handle("POST /api/graphql", graphql.Handler(deps))
//	mux.Handle("GET /api/graphql", graphql.Handler(deps))  // for introspection / GET-style persisted queries
//
// The handler accepts both GET and POST per the GraphQL-over-HTTP
// spec. GET is only used for read-only operations (the gqlgen
// transport package enforces this).
func Handler(deps Deps) http.Handler {
	r := &resolvers.Resolver{
		PostRepo: deps.PostRepo,
		UserRepo: deps.UserRepo,
		Policy:   deps.Policy,
	}
	es := generated.NewExecutableSchema(generated.Config{Resolvers: r})

	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	// Note: we deliberately do NOT add transport.MultipartForm here.
	// File uploads go through the REST surface; mixing them into
	// GraphQL doubles the validation surface for no real win.

	// Query plan cache. 1000 entries is sized to cover the persisted
	// query set + a generous margin for ad-hoc queries during
	// development.
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	if deps.EnableIntrospection {
		srv.Use(extension.Introspection{})
	}

	// Wire the cost analyzer as an OperationContextMutator.
	// gqlgen runs OperationMiddleware AFTER parse + validate and
	// BEFORE execution, which is exactly the point we want to bail
	// out cheaply on a pathological query.
	srv.AroundOperations(costMiddleware(deps.Cost))

	// Map resolver auth errors onto GraphQL error extensions. The
	// resolvers return typed *gqlAuthError values via the
	// errUnauthorized / errForbidden sentinels; this hook walks
	// them and stamps the extension code so clients can branch on
	// errors[*].extensions.code.
	srv.SetErrorPresenter(authErrorPresenter)

	// The handler is the leaf — auth attachment is the upstream
	// middleware's job (see packages/go/middleware/auth). We just
	// add the per-request dataloaders here.
	return loadersMiddleware(deps)(srv)
}

// loadersMiddleware attaches a fresh Loaders bundle to every request.
// MUST be per-request: the loader caches results across a single
// request to coalesce fan-out, but caching across requests would mix
// data between users.
func loadersMiddleware(deps Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loaders := dataloader.New(func(ctx context.Context, ids []string) ([]*dataloader.UserRow, error) {
				rows, err := deps.UserRepo.ByIDs(ctx, ids)
				if err != nil {
					return nil, err
				}
				out := make([]*dataloader.UserRow, len(rows))
				for i, row := range rows {
					if row == nil {
						out[i] = nil
						continue
					}
					out[i] = &dataloader.UserRow{
						ID:          row.ID,
						Handle:      row.Handle,
						DisplayName: row.DisplayName,
						Email:       row.Email,
						CreatedAt:   row.CreatedAt,
					}
				}
				return out, nil
			})
			ctx := dataloader.Attach(r.Context(), loaders)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// costMiddleware returns a gqlgen OperationContextMutator that
// computes the cost of the parsed operation and rejects it if it
// exceeds the budget. The budget is selected based on the request
// principal — anonymous and authenticated clients get different
// budgets per docs/05-admin-api.md §3.2.
func costMiddleware(cfg cost.Config) graphql.OperationMiddleware {
	anonBudget, authBudget := cfg.Resolve()
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		op := graphql.GetOperationContext(ctx)
		if op == nil || op.Doc == nil {
			return next(ctx)
		}
		// Pick budget by principal. Anonymous = no UserID.
		budget := anonBudget
		if p, ok := policy.FromContext(ctx); ok && p.UserID != "" {
			budget = authBudget
		}
		// Score each operation in the document (typically one).
		// We stop on the first that exceeds budget — partial-success
		// rejection of a multi-op document is more confusing than
		// "the document is too expensive."
		for _, opDef := range op.Doc.Operations {
			if _, err := cost.Analyze(opDef, nil, budget); err != nil {
				return graphql.OneShot(graphql.ErrorResponse(ctx, "%s", err.Error()))
			}
		}
		return next(ctx)
	}
}

// authErrorPresenter is the gqlgen error presenter that promotes
// typed auth errors into a GraphQL error extension. It uses
// errors.As so wrapped errors are still recognised; if the error
// is not a known auth error, we fall through to gqlgen's default.
func authErrorPresenter(ctx context.Context, err error) *gqlerror.Error {
	gErr := graphql.DefaultErrorPresenter(ctx, err)
	var ae *resolvers.GQLAuthErrorPub
	if errors.As(err, &ae) {
		if gErr.Extensions == nil {
			gErr.Extensions = map[string]any{}
		}
		gErr.Extensions["code"] = ae.Code
	}
	return gErr
}
