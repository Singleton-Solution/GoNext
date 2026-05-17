// Package healthz provides the liveness and readiness HTTP endpoints
// for the API server.
//
// Two endpoints, two distinct probes — they do different things and
// confusing them causes outages:
//
//   - Liveness (/healthz) answers "is this process up?" It never
//     depends on anything external. A liveness failure means restart
//     the container. If liveness depends on the database, a brief
//     DB blip cascades into a full pod restart loop.
//
//   - Readiness (/readyz) answers "should this process receive
//     traffic?" It checks every external dependency the server
//     actually needs to serve requests — DB, Redis, etc. A
//     readiness failure means take the pod out of rotation;
//     it does NOT mean restart.
//
// The split mirrors Kubernetes' livenessProbe vs readinessProbe and
// is the canonical pattern for any container that talks to backing
// stores.
//
// Liveness response shape:
//
//	200 OK
//	{"status":"alive","service":"api","version":"<bi.Version>"}
//
// Readiness response shape (all checks pass):
//
//	200 OK
//	{"status":"ready","checks":{"db":"ok","redis":"ok"},"duration_ms":N}
//
// Readiness response shape (any check fails):
//
//	503 Service Unavailable
//	{"status":"not_ready","checks":{"db":"err: connection refused","redis":"ok"},"duration_ms":N}
//
// Checks run concurrently with a per-check timeout (default 2s) so
// one slow backend doesn't drag the whole probe past Kubernetes'
// default 1s probe timeout. The aggregate duration is reported in
// the response for observability.
package healthz
