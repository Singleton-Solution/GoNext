package delivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Deliverer runs a single webhook delivery attempt.
//
// One Deliverer is constructed at worker startup and shared across all
// task invocations: the underlying http.Client is safe for concurrent
// use, and Deliverer holds no per-attempt state.
//
// The asynq handler is a thin wrapper (in apps/worker or in the broader
// webhooks package) that unmarshals the queued payload, fetches the
// subscription from the control-plane store, and calls Deliver. The
// returned Result tells the wrapper whether to return nil, an error
// (asynq retries per its schedule), or asynq.SkipRetry. See README in
// docs/12-jobs-cron.md §14 for that wiring.
type Deliverer struct {
	httpClient        *http.Client
	clock             func() time.Time
	deliveryIDFactory func() string
	scheduler         RetryScheduler
	secrets           SecretResolver
	idempotency       IdempotencyStore
	deadletter        *deadletterPipeline
	maxAttempts       int
	allowedSchemes    map[string]struct{}
	idempotencyTTL    time.Duration
	userAgent         string
}

// Option configures a Deliverer. Options compose; later options win.
type Option func(*Deliverer)

// WithHTTPClient overrides the default hardened http.Client. The
// supplied client's CheckRedirect SHOULD reject redirects (the default
// client does); the deliverer does not enforce this on caller-supplied
// clients. Used by tests to point at an httptest.Server, and by
// operators who want to add a custom RoundTripper for metrics.
func WithHTTPClient(c *http.Client) Option { return func(d *Deliverer) { d.httpClient = c } }

// WithClientConfig overrides defaults for the built-in http.Client. If
// WithHTTPClient is also set, that wins (this option is ignored).
func WithClientConfig(cfg ClientConfig) Option {
	return func(d *Deliverer) {
		if d.httpClient == nil {
			d.httpClient = newHTTPClient(cfg)
		}
	}
}

// WithScheduler overrides the retry scheduler. Default: NewSchedule(nil)
// with full jitter, which produces the schedule in DefaultRetrySchedule.
func WithScheduler(s RetryScheduler) Option { return func(d *Deliverer) { d.scheduler = s } }

// WithSecretResolver installs the secret store. Required for any non-
// test wiring; without it Deliver returns ErrInvalidSubscription because
// it can't sign.
func WithSecretResolver(r SecretResolver) Option { return func(d *Deliverer) { d.secrets = r } }

// WithIdempotency installs the idempotency store. Optional — see the
// IdempotencyStore docstring on the consequence of leaving it nil. Also
// configures the TTL applied to fresh claims (defaults to 7 days per
// doc 12 §7.2).
func WithIdempotency(s IdempotencyStore, ttl time.Duration) Option {
	return func(d *Deliverer) {
		d.idempotency = s
		if ttl > 0 {
			d.idempotencyTTL = ttl
		}
	}
}

// WithAuditRecorder installs the dead-letter audit pipeline. Optional in
// tests; required in production so an operator can see why a
// subscription went dark.
func WithAuditRecorder(a AuditRecorder) Option {
	return func(d *Deliverer) {
		if d.deadletter == nil {
			d.deadletter = &deadletterPipeline{}
		}
		d.deadletter.audit = a
	}
}

// WithSubscriptions installs the subscription store used by the
// dead-letter pipeline to mark subscriptions degraded.
func WithSubscriptions(s Subscriptions) Option {
	return func(d *Deliverer) {
		if d.deadletter == nil {
			d.deadletter = &deadletterPipeline{}
		}
		d.deadletter.subscriptions = s
	}
}

// WithClock installs a clock for the signature timestamp + retry
// scheduler. Default: time.Now. Used by tests to pin the timestamp so
// signature vectors are reproducible.
func WithClock(fn func() time.Time) Option {
	return func(d *Deliverer) {
		d.clock = fn
		if d.deadletter != nil {
			d.deadletter.now = fn
		}
	}
}

// WithDeliveryIDFactory installs a generator for the per-attempt
// delivery ID. Default: random 16-byte hex. Tests pin to a counter for
// stable assertion against the X-GoNext-Delivery-Id header.
func WithDeliveryIDFactory(fn func() string) Option {
	return func(d *Deliverer) { d.deliveryIDFactory = fn }
}

// WithMaxAttempts overrides the max attempts before dead-letter. Default
// MaxDeliveryAttempts (7). Bounded to len(schedule)+1; passing a higher
// value is silently capped.
func WithMaxAttempts(n int) Option { return func(d *Deliverer) { d.maxAttempts = n } }

// WithAllowedSchemes overrides the URL-scheme allowlist. Default:
// {"https"}. In development a caller can opt into {"http", "https"};
// production wiring should leave the default.
func WithAllowedSchemes(schemes ...string) Option {
	return func(d *Deliverer) {
		d.allowedSchemes = make(map[string]struct{}, len(schemes))
		for _, s := range schemes {
			d.allowedSchemes[s] = struct{}{}
		}
	}
}

// WithUserAgent overrides the User-Agent header. Default:
// "GoNext-Webhooks/1 (+https://gonext.example/docs/webhooks)". Subscribers
// can use the suffix to look up our debugging docs.
func WithUserAgent(ua string) Option {
	return func(d *Deliverer) { d.userAgent = ua }
}

// New builds a Deliverer with sensible defaults. Pass options to wire in
// the production resolver, audit, subscriptions, etc.
//
// A zero-option Deliverer is just-about-usable for tests: the
// SecretResolver, AuditRecorder, and Subscriptions are nil, so Deliver
// will return ErrInvalidSubscription (no secret can be resolved). Tests
// always need at least WithSecretResolver to drive the signing path.
func New(opts ...Option) *Deliverer {
	d := &Deliverer{
		clock:             time.Now,
		deliveryIDFactory: defaultDeliveryID,
		scheduler:         NewSchedule(nil),
		maxAttempts:       MaxDeliveryAttempts,
		allowedSchemes:    map[string]struct{}{"https": {}},
		idempotencyTTL:    7 * 24 * time.Hour,
		userAgent:         "GoNext-Webhooks/1",
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.httpClient == nil {
		d.httpClient = newHTTPClient(ClientConfig{})
	}
	// Cap maxAttempts to what the scheduler can actually serve. If the
	// scheduler is custom (e.g. ConstantSchedule), this is a no-op
	// because ConstantSchedule never exhausts; otherwise the cap
	// prevents an "off-by-one" deadletter that never fires.
	if s, ok := d.scheduler.(*Schedule); ok {
		if d.maxAttempts > len(s.delays)+1 {
			d.maxAttempts = len(s.delays) + 1
		}
	}
	return d
}

// Deliver runs one HTTP attempt against sub.URL with the supplied
// payload. It signs the body, sets the standard X-GoNext-* headers,
// applies the configured timeouts, and returns a structured Result.
//
// Deliver does NOT mutate p.Attempt — the caller (or the
// worker-task wrapper) is responsible for incrementing the attempt
// counter before re-enqueueing. Deliver only reads p.Attempt to set the
// X-GoNext-Attempt header and to ask the scheduler for the next delay
// on retry.
//
// On a permanent failure or schedule exhaustion, Deliver runs the
// dead-letter pipeline before returning. The Result carries the
// classification so the worker can return SkipRetry for permanent
// failures or a transient error for retryable ones.
func (d *Deliverer) Deliver(ctx context.Context, p Payload, sub Subscription) Result {
	if err := validatePayload(p); err != nil {
		return Result{
			Status:  StatusDeadletter,
			Attempt: max(1, p.Attempt),
			Err:     err,
		}
	}
	if err := d.validateSubscription(sub); err != nil {
		return Result{
			Status:  StatusDeadletter,
			Attempt: max(1, p.Attempt),
			Err:     err,
		}
	}
	attempt := p.Attempt
	if attempt < 1 {
		attempt = 1
	}

	// Idempotency claim — if we've already delivered this
	// (sub, event) pair, short-circuit. Treat a claim store error as
	// transient (retry): the alternative is double-delivery, which is
	// far worse for subscribers than one extra retry.
	if d.idempotency != nil {
		key := idempotencyKey(sub.ID, p.EventID)
		claimed, err := d.idempotency.Claim(ctx, key, d.idempotencyTTL)
		if err != nil {
			delay, exhausted := d.scheduler.NextDelay(attempt)
			res := Result{
				Status:    StatusRetry,
				Attempt:   attempt,
				NextDelay: delay,
				Err:       fmt.Errorf("idempotency claim: %w", errors.Join(err, ErrTransient)),
			}
			if exhausted || attempt >= d.maxAttempts {
				return d.exhaust(ctx, sub, p, res)
			}
			return res
		}
		if !claimed {
			// Already delivered — success-as-noop.
			return Result{
				Status:  StatusSuccess,
				Attempt: attempt,
			}
		}
	}

	secret, err := d.secrets.Resolve(ctx, sub.SecretID)
	if err != nil {
		// Secret resolution failure: transient (the secret store is
		// likely down) — retry. The very-unlikely-permanent case
		// (secret ID doesn't exist) would also retry until exhausted
		// and then dead-letter; that's the right behaviour, because we
		// can't tell "doesn't exist" from "store down" without a typed
		// error from the resolver, and a misconfigured subscription
		// SHOULD age out of the queue.
		delay, exhausted := d.scheduler.NextDelay(attempt)
		res := Result{
			Status:    StatusRetry,
			Attempt:   attempt,
			NextDelay: delay,
			Err:       fmt.Errorf("resolve secret: %w", errors.Join(err, ErrTransient)),
		}
		if exhausted || attempt >= d.maxAttempts {
			return d.exhaust(ctx, sub, p, res)
		}
		return res
	}

	// Build the request.
	req, err := d.buildRequest(ctx, p, sub, secret, attempt)
	if err != nil {
		// Building the request failed at a stage that shouldn't be
		// retried — e.g. http.NewRequestWithContext rejected an
		// invalid URL after our pre-validation slipped one through.
		// Treat as deadletter; permanent.
		return Result{
			Status:  StatusDeadletter,
			Attempt: attempt,
			Err:     fmt.Errorf("build request: %w", errors.Join(err, ErrPermanent)),
		}
	}

	// Do the request.
	start := d.clock()
	resp, doErr := d.httpClient.Do(req)
	latency := d.clock().Sub(start)
	if doErr != nil {
		status := netClass(ctx, doErr)
		if status == StatusDeadletter {
			res := Result{
				Status:  StatusDeadletter,
				Attempt: attempt,
				Latency: latency,
				Err:     fmt.Errorf("http: %w", errors.Join(doErr, ErrPermanent)),
			}
			reason := ReasonRedirectRejected // currently the only deadletter from netClass
			_ = d.deadletter.trigger(ctx, sub, p, res, reason)
			return res
		}
		// Transient. Schedule a retry or exhaust.
		delay, exhausted := d.scheduler.NextDelay(attempt)
		res := Result{
			Status:    StatusRetry,
			Attempt:   attempt,
			Latency:   latency,
			NextDelay: delay,
			Err:       fmt.Errorf("http: %w", errors.Join(doErr, ErrTransient)),
		}
		if exhausted || attempt >= d.maxAttempts {
			return d.exhaust(ctx, sub, p, res)
		}
		return res
	}
	// Drain (bounded) and close so the connection can be reused.
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, getMaxResponseBytes(d)))

	status, reason := classifyHTTPStatus(resp.StatusCode)
	switch status {
	case StatusSuccess:
		return Result{
			Status:     StatusSuccess,
			HTTPStatus: resp.StatusCode,
			Attempt:    attempt,
			Latency:    latency,
		}
	case StatusDeadletter:
		res := Result{
			Status:     StatusDeadletter,
			HTTPStatus: resp.StatusCode,
			Attempt:    attempt,
			Latency:    latency,
			Err:        fmt.Errorf("http status %d: %w", resp.StatusCode, ErrPermanent),
		}
		_ = d.deadletter.trigger(ctx, sub, p, res, reason)
		return res
	default:
		// Retry. Honor Retry-After if present and the schedule's value
		// is shorter; honor schedule if longer.
		delay, exhausted := d.scheduler.NextDelay(attempt)
		ra := parseRetryAfter(resp.Header.Get("Retry-After"), d.clock())
		if ra > delay {
			delay = ra
		}
		res := Result{
			Status:     StatusRetry,
			HTTPStatus: resp.StatusCode,
			Attempt:    attempt,
			Latency:    latency,
			NextDelay:  delay,
			Err:        fmt.Errorf("http status %d: %w", resp.StatusCode, ErrTransient),
		}
		if exhausted || attempt >= d.maxAttempts {
			return d.exhaust(ctx, sub, p, res)
		}
		return res
	}
}

// exhaust converts a Retry-class Result into a Deadletter once the
// schedule has been used up. Runs the deadletter pipeline as a side
// effect. The returned Result's Err is rewrapped with ErrExhausted so
// callers can branch on classification.
func (d *Deliverer) exhaust(ctx context.Context, sub Subscription, p Payload, last Result) Result {
	last.Status = StatusDeadletter
	last.NextDelay = 0
	if last.Err != nil {
		last.Err = fmt.Errorf("%w: %w", ErrExhausted, last.Err)
	} else {
		last.Err = ErrExhausted
	}
	_ = d.deadletter.trigger(ctx, sub, p, last, ReasonScheduleExhausted)
	return last
}

// buildRequest constructs an http.Request with the signed body, the
// reserved X-GoNext-* headers, and the user-supplied custom headers.
// Custom headers cannot override reserved ones (the iteration order
// applies user headers first, then overwrites with reserved values).
func (d *Deliverer) buildRequest(ctx context.Context, p Payload, sub Subscription, secret []byte, attempt int) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(p.Body))
	if err != nil {
		return nil, err
	}
	now := d.clock()
	deliveryID := d.deliveryIDFactory()
	// User-supplied custom headers go first.
	for k, v := range p.Headers {
		if _, reserved := reservedHeaders[http.CanonicalHeaderKey(k)]; reserved {
			continue
		}
		req.Header.Set(k, v)
	}
	// Reserved headers — always last, never overridden.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", d.userAgent)
	req.Header.Set(SignatureHeader, Sign(secret, now, p.Body))
	req.Header.Set(TimestampHeader, fmt.Sprintf("%d", now.Unix()))
	req.Header.Set(EventIDHeader, p.EventID)
	req.Header.Set(DeliveryIDHeader, deliveryID)
	req.Header.Set(AttemptHeader, fmt.Sprintf("%d", attempt))
	if p.EventType != "" {
		req.Header.Set(EventTypeHeader, p.EventType)
	}
	req.Header.Set(SubscriptionIDHeader, sub.ID)
	// Explicit Content-Length (bytes.Reader supports Len so the stdlib
	// would have set this anyway; making it explicit aids debugging
	// of proxies that reject chunked POSTs).
	req.ContentLength = int64(len(p.Body))
	return req, nil
}

// validateSubscription checks the URL scheme allowlist + structural
// invariants. We do this before any I/O so a misconfigured subscription
// produces an immediate deadletter rather than burning the retry
// schedule.
func (d *Deliverer) validateSubscription(s Subscription) error {
	if s.ID == "" {
		return fmt.Errorf("%w: ID is empty", ErrInvalidSubscription)
	}
	if s.SecretID == "" {
		return fmt.Errorf("%w: SecretID is empty", ErrInvalidSubscription)
	}
	if s.URL == "" {
		return fmt.Errorf("%w: URL is empty", ErrInvalidSubscription)
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: URL not parseable", ErrInvalidSubscription)
	}
	if _, ok := d.allowedSchemes[u.Scheme]; !ok {
		return fmt.Errorf("%w: scheme %q not allowed", ErrInvalidSubscription, u.Scheme)
	}
	if d.secrets == nil {
		return fmt.Errorf("%w: no SecretResolver wired", ErrInvalidSubscription)
	}
	return nil
}

func validatePayload(p Payload) error {
	if p.SubscriptionID == "" {
		return fmt.Errorf("%w: SubscriptionID is empty", ErrInvalidPayload)
	}
	if p.EventID == "" {
		return fmt.Errorf("%w: EventID is empty", ErrInvalidPayload)
	}
	if len(p.Body) == 0 {
		return fmt.Errorf("%w: Body is empty", ErrInvalidPayload)
	}
	return nil
}

func idempotencyKey(subscriptionID, eventID string) string {
	return "webhook.deliver:" + subscriptionID + ":" + eventID
}

func getMaxResponseBytes(d *Deliverer) int64 {
	// 64KB matches ClientConfig default; if a caller passed a custom
	// http.Client we don't have the cfg to inspect, so use the default.
	_ = d
	return 64 * 1024
}

func defaultDeliveryID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
