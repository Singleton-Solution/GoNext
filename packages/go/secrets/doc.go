// Package secrets is the system secret store for GoNext.
//
// It defines a small Store interface — Get and MustGet — and ships three
// production-ready adapters plus a factory for picking one at runtime:
//
//   - EnvStore   reads from os.Getenv. Default for development.
//   - FileStore  reads from a directory of "key.txt" files. The shape
//     Docker Secrets and Kubernetes projected-volume mounts produce.
//   - NoopStore  returns ErrNotFound for every key. Useful in tests
//     when you want to assert a code path doesn't actually need
//     a secret backend.
//
// Two adapters are reserved as documented stubs and will land in their own
// PRs once the upstream SDKs are pinned: VaultStore (Vault KV v2 with rotation
// notifications) and AWSSMStore (AWS Secrets Manager with TTL polling).
// See docs/13-security-baseline.md §5.2 and §5.7 for the design rationale.
//
// # Usage
//
// At program start, open a store from config and verify every secret the
// process can't run without:
//
//	store, err := secrets.Open(cfg.Secrets.Backend) // e.g. "env:" or "file:/run/secrets"
//	if err != nil {
//	    return fmt.Errorf("secrets open: %w", err)
//	}
//	if err := secrets.Require(store, "DATABASE_URL", "GONEXT_AUTH_PEPPER"); err != nil {
//	    return err // aggregated; lists every missing key in one go
//	}
//
// Then hold the Store and pull values where they're consumed:
//
//	dsn, err := store.Get("DATABASE_URL")
//
// For values whose absence is a programmer bug (constants the binary
// shouldn't even start without — but you've already run Require), MustGet
// panics with a clear, redacted message:
//
//	pepper := store.MustGet("GONEXT_AUTH_PEPPER")
//
// # Redaction
//
// No method in this package ever puts a secret value in an error message,
// a panic message, or a log line. The marker "***" stands in if a placeholder
// is needed. Callers that wrap errors from this package should preserve that
// discipline; see docs/13-security-baseline.md §5.6 for the universal rules.
package secrets
