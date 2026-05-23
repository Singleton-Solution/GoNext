package setup

import (
	"context"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// NewMemoryLimiter constructs a process-local Limiter from the supplied
// policy. It is the default wiring main.go uses on a single-instance
// deployment; multi-replica installs should swap in a RedisLimiter (the
// shared bucket prevents an attacker from amortizing their attempts
// across pods).
//
// The returned Limiter shares the policy.Capacity initial budget with
// the same Refill rate so a fresh install starts with a full burst —
// the operator gets five honest attempts before any throttling kicks in.
func NewMemoryLimiter(p RateLimitPolicy) (Limiter, error) {
	rl, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   p.Capacity,
		RefillRate: p.RefillRate,
		Prefix:     "setup",
	})
	if err != nil {
		return nil, err
	}
	return adapter{rl: rl}, nil
}

// adapter bridges ratelimit.Limiter (the shared backend) into the
// narrower Limiter interface this package exposes. It is unexported on
// purpose: callers outside this package should construct a Limiter via
// NewMemoryLimiter or NewRedisLimiter, not by writing the adapter
// themselves.
type adapter struct {
	rl ratelimit.Limiter
}

func (a adapter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	return a.rl.Allow(ctx, key)
}
