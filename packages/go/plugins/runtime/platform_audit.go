package runtime

// platform_audit.go — per-plugin audit emitter sink underpinning
// gn_audit_emit (#183). The wiring into the WASM ABI lives in
// host_platform.go; this file owns the slug-prefix gate and the
// per-plugin rate limiter.
//
// Design rules from the issue:
//
//   * Severity is capped at SeverityInfo (a plugin can't elevate to
//     warning/critical — those tiers exist for host-emitted events).
//   * Event name MUST start with the plugin's slug after the platform
//     "plugin." prefix the host adds. Plugins are namespaced; this is
//     where we enforce it.
//   * Per-plugin rate limit (default 100/min) prevents a runaway
//     plugin from chewing through the audit table. Rejected calls
//     return ErrAuditRateLimited; the audit row is NOT written.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrAuditSlugPrefix is returned by AuditSink.Emit when the event
// name does not start with the plugin's slug after the platform
// "plugin." prefix.
var ErrAuditSlugPrefix = errors.New("runtime: audit event must start with plugin slug")

// ErrAuditEmpty is returned when the event name (after slug-prefix
// stripping) is empty. Empty events would still pass the storage
// CHECK but are useless and almost always a guest bug.
var ErrAuditEmpty = errors.New("runtime: audit event name is empty")

// ErrAuditRateLimited is returned when the plugin has exceeded its
// per-plugin token-bucket allowance. We reject (not silently drop)
// so the plugin author sees the rejection and can fix the
// emit-in-tight-loop bug.
var ErrAuditRateLimited = errors.New("runtime: audit rate limit exceeded")

// AuditEmitterFunc is declared in host_platform.go — the platform
// shares one interface for both the per-plugin guest-facing sink and
// the platform-internal "plugin.<slug>.platform.*" emitter. See that
// file for the type declaration.

// DefaultAuditRatePerMinute is the per-plugin emission cap used when
// AuditSinkConfig.PerPluginPerMinute is zero. 100/min ≈ 1.67/s
// sustained — enough for legitimate event chains (publish + index +
// notify per save) and small enough that a runaway loop trips the
// limit before damaging the audit table.
const DefaultAuditRatePerMinute = 100

// AuditSinkConfig tunes the AuditSink.
//
// PerPluginPerMinute is the steady-state allowance in events per
// minute, applied as a token bucket. Zero means
// DefaultAuditRatePerMinute.
//
// Burst is the bucket capacity — how many events a quiet plugin can
// fire back-to-back before the per-minute drip rate becomes binding.
// Zero means PerPluginPerMinute. Larger values accommodate
// legitimate spikes; smaller values throttle harder during bursts.
//
// NowFunc replaces time.Now for tests so a rate-limit test doesn't
// have to sleep through a real minute.
type AuditSinkConfig struct {
	PerPluginPerMinute int
	Burst              int
	NowFunc            func() time.Time
}

// AuditSink is the per-plugin audit emitter exposed to WASM through
// gn_audit_emit. It wraps an underlying audit.Emitter with two
// host-side policies:
//
//  1. Slug-prefix enforcement: the event name MUST start with the
//     plugin's slug (possibly after a redundant "plugin." prefix).
//     Plugins emitting events under another plugin's namespace would
//     defeat the audit_log.actor_label trust model.
//
//  2. Per-plugin rate limit: a token bucket caps the per-minute
//     emission rate. Rejected calls return ErrAuditRateLimited
//     rather than silently dropping.
//
// On any rejection the audit row is NOT written.
//
// AuditSink is goroutine-safe; the bucket map is guarded by a mutex.
type AuditSink struct {
	emitter AuditEmitterFunc
	cfg     AuditSinkConfig

	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

// NewAuditSink wraps emitter with the slug-prefix gate and rate
// limiter. emitter is required (nil panics — this is boot-time
// wiring).
func NewAuditSink(emitter AuditEmitterFunc, cfg AuditSinkConfig) *AuditSink {
	if emitter == nil {
		panic("runtime: NewAuditSink: emitter is required")
	}
	if cfg.PerPluginPerMinute <= 0 {
		cfg.PerPluginPerMinute = DefaultAuditRatePerMinute
	}
	if cfg.Burst <= 0 {
		cfg.Burst = cfg.PerPluginPerMinute
	}
	if cfg.NowFunc == nil {
		cfg.NowFunc = time.Now
	}
	return &AuditSink{
		emitter: emitter,
		cfg:     cfg,
		buckets: map[string]*tokenBucket{},
	}
}

// Emit records a single audit event from the plugin identified by
// pluginSlug. eventName is the guest-supplied name; metadata is the
// guest-supplied JSON-decoded map.
//
// Slug-prefix rule:
//
//   * The expected guest-side name is "<slug>.<noun>.<verb>" (e.g.
//     "seo.sitemap.regen").
//   * The host transforms this to "plugin.<slug>.<noun>.<verb>" before
//     handing it to the audit store — the "plugin." prefix is the
//     contract documented in docs/06-auth-permissions.md §14.6.
//   * A plugin attempting to emit "other-plugin.foo.bar" is rejected
//     with ErrAuditSlugPrefix.
//   * A redundant leading "plugin." is stripped before the slug
//     check (some plugin authors mirror the host convention out of
//     caution).
//
// Severity is implicitly SeverityInfo: the runtime caps plugin-
// emitted severity at info per the issue #183 rules so a plugin
// can't elevate noise to the page-the-operator tier.
func (s *AuditSink) Emit(ctx context.Context, pluginSlug, eventName string, metadata map[string]any) error {
	if pluginSlug == "" {
		// The runtime fills this from the trusted module identity, so
		// an empty slug means the runtime forgot to thread context.
		// Surface as a host bug rather than a guest bug.
		return errors.New("runtime: audit emit: pluginSlug is required")
	}
	if eventName == "" {
		return ErrAuditEmpty
	}

	stripped := strings.TrimPrefix(eventName, "plugin.")
	// "<slug>" or "<slug>.<rest>" — both legal. "<slug>foo" or
	// "other-plugin.x" — both rejected. The dot boundary check is
	// the safety against namespace bleed.
	if stripped != pluginSlug && !strings.HasPrefix(stripped, pluginSlug+".") {
		return fmt.Errorf("%w: %q (slug=%q)", ErrAuditSlugPrefix, eventName, pluginSlug)
	}

	// Rate-limit before incurring the store I/O. Rejected calls do
	// not consume a token (the bucket is reservation-based — Take
	// only withdraws on success).
	if !s.takeToken(pluginSlug) {
		return ErrAuditRateLimited
	}

	final := "plugin." + stripped
	if err := s.emitter.Emit(ctx, pluginSlug, final, metadata); err != nil {
		// Return the raw error so the runtime can decide whether to
		// trap or log-and-continue. The token has already been
		// withdrawn — we deliberately do NOT refund on emitter
		// failure, because a flapping store would otherwise let a
		// runaway plugin bypass the limiter while still spending
		// store cycles.
		return fmt.Errorf("runtime: audit emit: %w", err)
	}
	return nil
}

// takeToken pulls one token from the named plugin's bucket. Returns
// true if the token was taken (call allowed) or false if the bucket
// was empty.
//
// The bucket is created lazily on first use — most plugins never
// emit audit events, so pre-creating one per plugin would waste
// memory.
func (s *AuditSink) takeToken(pluginSlug string) bool {
	s.mu.Lock()
	b, ok := s.buckets[pluginSlug]
	if !ok {
		b = newTokenBucket(s.cfg.PerPluginPerMinute, s.cfg.Burst, s.cfg.NowFunc)
		s.buckets[pluginSlug] = b
	}
	s.mu.Unlock()
	// Bucket math runs outside the map lock so the lock holds for a
	// constant time regardless of how expensive a test-injected
	// now() is.
	return b.take()
}

// tokenBucket is a minimal token bucket with steady-state refill in
// events-per-minute. Kept private to runtime — the only consumer is
// AuditSink and exposing it would grow the package surface for no
// caller benefit.
//
// State:
//
//   * tokens — current bucket fill (float so partial refill between
//     calls accumulates correctly).
//   * cap    — bucket capacity (the burst limit).
//   * rate   — tokens added per second (PerPluginPerMinute / 60).
//   * last   — moment we last refilled.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	cap    float64
	rate   float64
	last   time.Time
	now    func() time.Time
}

// newTokenBucket constructs a bucket with the given per-minute rate
// and burst capacity. The bucket starts full so a fresh plugin can
// emit up to `burst` events without waiting for refill.
func newTokenBucket(perMinute, burst int, now func() time.Time) *tokenBucket {
	if now == nil {
		now = time.Now
	}
	return &tokenBucket{
		tokens: float64(burst),
		cap:    float64(burst),
		rate:   float64(perMinute) / 60.0,
		last:   now(),
		now:    now,
	}
}

// take attempts to withdraw one token. Returns true on success;
// false if the bucket is empty (no refund needed because nothing was
// taken).
//
// Refill happens lazily: we add (elapsed * rate) tokens, clamped at
// cap, before checking the >= 1 threshold. Float math is fine at
// this scale; rounding errors don't change semantics over realistic
// time windows.
func (b *tokenBucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.cap {
			b.tokens = b.cap
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}
