// Package containers spins up real Postgres, Redis, and MinIO instances in
// Docker for integration tests, using testcontainers-go.
//
// The intent is to let package-level integration tests stop reaching for
// DSNs in environment variables ("set DATABASE_URL or skip") and instead
// declare what they need inline:
//
//	dsn := containers.Postgres(t)
//	url := containers.Redis(t)
//	ep, ak, sk := containers.MinIO(t)
//
// Each helper:
//
//   - Uses t.Helper() so failure lines point at the caller.
//   - Registers t.Cleanup to terminate the container when the test ends.
//   - Skips the test cleanly with t.Skip("docker not available") when no
//     Docker daemon is reachable, so suites stay green on machines that
//     can't run containers (laptops without Docker, restricted CI shards).
//   - Waits for the container to actually accept connections before
//     returning — callers can use the returned DSN/URL immediately.
//
// Options follow the functional-options pattern. The defaults pin image
// versions so test runs are reproducible across machines and over time:
//
//	dsn := containers.Postgres(t,
//	    containers.WithVersion("16-alpine"),
//	    containers.WithDB("orders_test"),
//	)
//
// For long iterative loops, set TESTCONTAINERS_REUSE_ENABLE=true in the
// environment and testcontainers-go will recycle containers across runs;
// the helpers here don't need to do anything special for that.
//
// See docs/11-testing-ci.md §3 for the broader testing-substrate plan.
package containers
