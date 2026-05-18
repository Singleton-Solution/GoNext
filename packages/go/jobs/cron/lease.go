package cron

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrEmptyOwner is returned by NewLease when the Owner string is
// empty. The Owner is the compare-and-swap identity used by Renew
// and Release — without it a stale leader's Release call could wipe
// a fresh leader's key, so we refuse to construct a lease that lacks
// one.
var ErrEmptyOwner = errors.New("cron: lease owner is required")

// ErrEmptyKey is returned by NewLease when the Key string is empty.
// The lease key is shared across all replicas; an empty key would
// either silently collide with whatever else uses the empty-key
// convention or be rejected by Redis.
var ErrEmptyKey = errors.New("cron: lease key is required")

// ErrInvalidTTL is returned by NewLease when TTL is non-positive.
// A zero or negative TTL would make Acquire expire instantly, which
// is a configuration bug we'd rather catch at construction than at
// the first acquire attempt.
var ErrInvalidTTL = errors.New("cron: lease TTL must be positive")

// ErrNotLeader is returned by Renew and Release when the lease key
// either does not exist (already expired) or is held by a different
// Owner. Callers should treat both cases the same way: the lease is
// not ours, stop firing, drop back to the idle poll.
var ErrNotLeader = errors.New("cron: lease is not held by this owner")

// renewScript extends a lease's TTL only if the current value matches
// the expected Owner. Without the compare leg, a stale process whose
// Acquire-side TTL has elapsed (and whose key was then claimed by a
// new leader) could push the new leader's expiry out — a classic
// timing-attack on naive leases.
//
// KEYS[1]   lease key
// ARGV[1]   expected owner
// ARGV[2]   new TTL in milliseconds
// Returns 1 if the renewal happened, 0 if the key was missing or
// owned by someone else.
var renewScript = redis.NewScript(`
local cur = redis.call("GET", KEYS[1])
if not cur then
	return 0
end
if cur ~= ARGV[1] then
	return 0
end
redis.call("PEXPIRE", KEYS[1], tonumber(ARGV[2]))
return 1
`)

// releaseScript deletes a lease only if its current value matches the
// expected Owner. Same compare-leg motivation as renewScript: a
// stale process must not be able to wipe a fresh leader's key.
//
// KEYS[1]   lease key
// ARGV[1]   expected owner
// Returns 1 if the delete happened, 0 otherwise.
var releaseScript = redis.NewScript(`
local cur = redis.call("GET", KEYS[1])
if not cur then
	return 0
end
if cur ~= ARGV[1] then
	return 0
end
redis.call("DEL", KEYS[1])
return 1
`)

// Lease is a Redis-backed mutex with an Owner identity and a TTL.
//
// One Lease value corresponds to one logical leadership claim — the
// cron scheduler uses exactly one per process, keyed by a constant
// like "gonext:cron:leader". The Owner string distinguishes processes
// (typically a hostname-plus-PID or a K8s pod UID); the TTL bounds
// the missed-tick window after a sudden leader death.
//
// Methods are safe for concurrent use, but the typical wiring has one
// goroutine renewing in a loop and one goroutine reading IsHeld via
// the scheduler's state — there's no need for the caller to add their
// own mutex.
//
// Implementation note: Acquire is SET NX PX (an atomic Redis-side
// "set if not exists with TTL"). Renew and Release are Lua scripts
// because they need to compare-and-swap against the Owner value,
// which can't be done with a plain go-redis call without a TOCTOU
// window between GET and EXPIRE/DEL.
type Lease struct {
	rdb   *redis.Client
	key   string
	owner string
	ttl   time.Duration
}

// NewLease constructs a Lease against rdb. Returns an error if any of
// the required arguments are missing or invalid:
//
//   - ErrEmptyKey if key is empty.
//   - ErrEmptyOwner if owner is empty.
//   - ErrInvalidTTL if ttl is non-positive.
//
// rdb is not checked for nil here — the first Acquire call will
// surface that as a nil dereference. The caller is expected to wire
// a live client from packages/go/redis (which has already pinged).
func NewLease(rdb *redis.Client, key, owner string, ttl time.Duration) (*Lease, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if owner == "" {
		return nil, ErrEmptyOwner
	}
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	return &Lease{
		rdb:   rdb,
		key:   key,
		owner: owner,
		ttl:   ttl,
	}, nil
}

// Key returns the Redis key this lease guards. Useful for logging
// and for tests; the field is otherwise read-only.
func (l *Lease) Key() string { return l.key }

// Owner returns the Owner identity this lease asserts when it holds
// the key. Two leases with different Owner strings cannot Renew or
// Release each other's claims.
func (l *Lease) Owner() string { return l.owner }

// TTL returns the configured Redis TTL applied to the key on each
// Acquire and Renew. The scheduler typically renews every TTL/3 so a
// single dropped renewal doesn't lose leadership.
func (l *Lease) TTL() time.Duration { return l.ttl }

// Acquire attempts to claim the lease. Returns (true, nil) if this
// call wrote the key — the caller now holds leadership for TTL.
// Returns (false, nil) if the key is already held (by us or by
// someone else, distinguishable only by a subsequent GET); the
// caller should back off and retry later.
//
// Implementation: SET key owner NX PX <ttl-millis>. Redis's NX
// semantics mean the SET succeeds only if the key did not exist. We
// use millisecond-resolution PX rather than EX so a sub-second TTL
// (used in tests) doesn't round to zero.
//
// Returns a non-nil error only on transport / Redis errors. A "key
// already held" outcome is a regular `false` result, not an error —
// it's the normal case for non-leaders.
func (l *Lease) Acquire(ctx context.Context) (bool, error) {
	pxMs := l.ttl.Milliseconds()
	if pxMs < 1 {
		pxMs = 1
	}
	ok, err := l.rdb.SetNX(ctx, l.key, l.owner, time.Duration(pxMs)*time.Millisecond).Result()
	if err != nil {
		return false, fmt.Errorf("cron: lease acquire: %w", err)
	}
	return ok, nil
}

// Renew extends the lease's TTL only if the key is still owned by
// this Lease's Owner. Returns ErrNotLeader if the key is gone (we
// expired) or is held by a different owner (someone took over). On
// transport errors, returns the wrapped error.
//
// The scheduler is expected to call Renew at TTL/3 cadence. At that
// rate, losing leadership requires three consecutive failed renewals
// in a row — a single network blip doesn't unseat the leader.
func (l *Lease) Renew(ctx context.Context) error {
	pxMs := l.ttl.Milliseconds()
	if pxMs < 1 {
		pxMs = 1
	}
	res, err := renewScript.Run(ctx, l.rdb,
		[]string{l.key}, l.owner, pxMs,
	).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("cron: lease renew: %w", err)
	}
	if res != 1 {
		return ErrNotLeader
	}
	return nil
}

// Release deletes the lease key only if it is still owned by this
// Lease's Owner. Returns ErrNotLeader if the key is gone or held by
// someone else; nil otherwise. On transport errors, returns the
// wrapped error.
//
// Calling Release on a lease we never Acquired (or whose TTL we let
// elapse) returns ErrNotLeader — Release is therefore safe to call
// unconditionally on shutdown without first checking IsHeld.
func (l *Lease) Release(ctx context.Context) error {
	res, err := releaseScript.Run(ctx, l.rdb,
		[]string{l.key}, l.owner,
	).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("cron: lease release: %w", err)
	}
	if res != 1 {
		return ErrNotLeader
	}
	return nil
}

// CurrentOwner returns the current value of the lease key. Returns
// ("", redis.Nil) if the key is absent. Intended for diagnostics and
// for the cron_lease_holder Prometheus gauge surfaced by the
// scheduler — production code MUST NOT use this value to decide
// whether to renew (use the result of Acquire/Renew instead).
func (l *Lease) CurrentOwner(ctx context.Context) (string, error) {
	return l.rdb.Get(ctx, l.key).Result()
}
