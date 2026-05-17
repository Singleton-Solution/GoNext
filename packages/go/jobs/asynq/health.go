package asynq

import (
	"sync/atomic"
	"time"
)

// healthState tracks the freshness and verdict of the latest Asynq Redis
// ping. Stored as two atomics so Healthy() can read without locking,
// which matters because /readyz is hit on every K8s probe interval and
// any lock contention there shows up as a tail-latency artifact.
//
// We store the last-ping wall-clock time as a UnixNano int64 because
// time.Time is not atomic-friendly. The cost is a single time.Unix() in
// the rare error-reporting path.
type healthState struct {
	lastPingUnixNano atomic.Int64 // 0 == never pinged (boot grace)
	lastPingOK       atomic.Bool
	staleAfter       time.Duration
}

// newHealthState returns a health state configured for the given check
// interval. The staleness threshold is 2× the interval, so a single
// missed ping doesn't immediately flip the binary to unready (that
// would create a thundering-herd ready/unready oscillation if Redis is
// briefly stalled). Two intervals matches the Kubernetes default
// failureThreshold of 3 for readiness probes — we want our staleness
// detection to be slightly more sensitive than the probe itself.
func newHealthState(checkInterval time.Duration) *healthState {
	return &healthState{
		staleAfter: 2 * checkInterval,
	}
}

// record stamps the latest ping result. Called from Asynq's
// HealthCheckFunc hook on the cadence configured by HealthCheckInterval.
// err == nil means the ping succeeded.
func (h *healthState) record(err error) {
	h.lastPingUnixNano.Store(time.Now().UnixNano())
	h.lastPingOK.Store(err == nil)
}

// Healthy returns true when the most recent Asynq→Redis ping succeeded
// AND was observed within the staleness window. The two conditions are
// independent on purpose: a successful-but-old ping (Asynq goroutine
// stuck) and a fresh failure both flip readiness off.
//
// Returns true during the boot grace period (before any ping has been
// recorded) so the first readiness probe doesn't fail purely because
// Asynq hasn't completed its first 15s health check. We rely on the
// fact that boot-time Redis connectivity is verified separately by
// packages/go/redis.New before the server even starts.
func (h *healthState) Healthy() bool {
	last := h.lastPingUnixNano.Load()
	if last == 0 {
		return true // boot grace
	}
	age := time.Since(time.Unix(0, last))
	if age > h.staleAfter {
		return false
	}
	return h.lastPingOK.Load()
}

// LastPing returns the wall-clock time of the most recent ping
// observation and whether it succeeded. Returns (zero, false) before
// the first ping has been recorded. Surfaced for /readyz JSON bodies
// and operator debugging — Healthy() is the boolean readiness fast path.
func (h *healthState) LastPing() (time.Time, bool) {
	last := h.lastPingUnixNano.Load()
	if last == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, last), h.lastPingOK.Load()
}
