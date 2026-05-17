package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Limiter is the contract every rate-limit backend implements.
//
// Allow consults the bucket associated with key and consumes one token
// if available. The returned bool is true iff the request should proceed.
// When false, retryAfter is the caller's hint for how long to wait
// before trying again (used to populate the Retry-After header in
// HTTP responses). retryAfter is always non-negative.
//
// Errors are reserved for backend failures (network drop to Redis,
// context cancellation). A caller that receives an error should fail
// open or fail closed per its security posture — see middleware.go for
// the default (fail open with a logged warning, on the principle that
// rate-limit availability shouldn't be a single point of failure).
type Limiter interface {
	Allow(ctx context.Context, key string) (allowed bool, retryAfter time.Duration, err error)
}

// Policy describes a single token bucket's parameters.
//
// Capacity is the maximum number of tokens the bucket can hold; this is
// the largest burst a caller can make in a short window before being
// throttled to the steady-state rate.
//
// RefillRate is the steady-state rate in tokens per second. For
// "60 requests per minute" set RefillRate = 1.0 (60/60). For
// "5 attempts per 15 minutes" set RefillRate = 5.0/900 ≈ 0.00555.
//
// Prefix namespaces keys in shared stores (Redis). MemoryLimiter ignores
// Prefix because each limiter owns its own map.
type Policy struct {
	Capacity   int
	RefillRate float64 // tokens per second
	Prefix     string
}

// validate returns an error if the policy is malformed.
func (p Policy) validate() error {
	if p.Capacity <= 0 {
		return fmt.Errorf("ratelimit.Policy: Capacity must be > 0, got %d", p.Capacity)
	}
	if p.RefillRate <= 0 {
		return fmt.Errorf("ratelimit.Policy: RefillRate must be > 0, got %f", p.RefillRate)
	}
	return nil
}

// timePerToken is the steady-state inter-token interval. Used to compute
// Retry-After when the bucket is empty.
func (p Policy) timePerToken() time.Duration {
	return time.Duration(float64(time.Second) / p.RefillRate)
}

// ErrPolicyInvalid is returned by constructors when Policy validation
// fails. Wrapped with %w so callers can errors.Is against it.
var ErrPolicyInvalid = errors.New("ratelimit: invalid policy")
