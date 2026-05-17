package secrets

// This file documents the secret-store adapters that are reserved but
// not yet implemented. They live here so that Open can return an explicit
// "scheme is reserved" error for them (rather than a generic "unknown
// scheme"), and so that the design is visible to anyone reading the
// package without having to chase down the security baseline doc.
//
// When these land, each becomes its own file (vault.go, aws_sm.go) with
// a concrete type implementing Store, and the corresponding case in
// Open is filled in. Tests should follow the table-driven shape used by
// env_test.go / file_test.go / noop_test.go.
//
// # VaultStore (HashiCorp Vault KV v2)
//
// Reads secrets from a Vault KV v2 mount. Authentication is typically
// AppRole or Kubernetes auth. The adapter should:
//
//   - Fetch the value at <mount>/data/<key>, returning the "data.data"
//     field as the secret value.
//   - Map a 404 from Vault to ErrNotFound, redacting the underlying
//     error body so a misconfigured policy doesn't leak token TTLs.
//   - Support live rotation via Vault's KV subscription / event stream.
//     The Watch hook is intentionally not part of the Store interface in
//     this PR — wiring rotation through the codebase is a separate
//     concern (it touches the pepper cache, the OAuth client cache, and
//     the session signing key rotation runbook).
//
// Spec format reserved by Open: vault://host/path-to-mount.
//
// # AWSSMStore (AWS Secrets Manager)
//
// Reads secrets from AWS Secrets Manager via GetSecretValue. The adapter
// should:
//
//   - Pin the SDK version in packages/go/go.mod (deferred to the
//     implementation PR so this skeleton stays dependency-free).
//   - Cache values with a configurable TTL (default 60s) — Secrets
//     Manager bills per API call, and the request path can pull the
//     same secret thousands of times per second.
//   - Map ResourceNotFoundException to ErrNotFound. Other API errors
//     surface wrapped, with the AWS request ID retained for support.
//   - Treat both SecretString and SecretBinary inputs; binary values
//     are returned base64-encoded for transport over the string-typed
//     Store interface. A future GetBinary method can return raw bytes;
//     defining it now is premature.
//
// Spec format reserved by Open: aws-sm://region (or aws-sm:region for
// path-less forms).
