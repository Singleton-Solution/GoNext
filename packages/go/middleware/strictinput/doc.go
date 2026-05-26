// Package strictinput is the request-shape gatekeeper described in
// issue #161. It enforces three rules on POST/PATCH/PUT/DELETE bodies
// when GONEXT_STRICT_INPUT=1 is set in the environment:
//
//  1. JSON bodies must parse with DisallowUnknownFields — an extra
//     field that the route's payload struct doesn't know about is a
//     400, not a "silently dropped" surprise. The REST handlers
//     already enforce this per-endpoint; this middleware is the
//     belt-and-braces layer in front of routes that haven't been
//     converted yet.
//
//  2. GraphQL requests have shape budgets: top-level body keys are
//     constrained to {query,variables,operationName,extensions} —
//     anything else (a forgotten "debug": true, a probe key from a
//     compromised SDK) is a 400. Variables and extensions are
//     bounded depth + key count so a hostile client can't slip past
//     query-cost analysis by burying the cost in a 100-level deep
//     extensions object.
//
//  3. Both rules are no-ops when GONEXT_STRICT_INPUT is unset or
//     empty. The default posture is permissive so the gate can land
//     incrementally; an operator flips it on once the API surface
//     has been audited.
//
// What this middleware does NOT do:
//
//   - It does not validate REST payloads against an OpenAPI schema
//     beyond shape (the "extra fields" check IS the shape gate; field-
//     level validation belongs in the handler's existing per-route
//     validation). Once openapi-validate has a Go-side runtime, that
//     layer plugs in here.
//   - It does not impose request-body size limits — that's the job of
//     http.MaxBytesReader at the per-handler level, where the limit
//     reflects the route's payload shape.
//
// Wiring example:
//
//	mux := http.NewServeMux()
//	gateway := strictinput.Middleware(strictinput.Config{
//	    Enabled: os.Getenv("GONEXT_STRICT_INPUT") == "1",
//	})
//	apiHandler := gateway(mux)
//
// Routes that already do their own DisallowUnknownFields decode pay
// no cost when the middleware re-validates because the second decode
// pass is cheap (the body has already been buffered).
package strictinput
