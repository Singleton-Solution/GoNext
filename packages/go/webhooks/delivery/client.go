package delivery

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/safehttp"
)

// ClientConfig tunes the HTTP client used to deliver webhooks.
//
// Defaults are picked so that an operator who instantiates a Deliverer
// with zero configuration gets sensible production behaviour. Anything
// here is overridable for tests or specialized deployments.
type ClientConfig struct {
	// ConnectTimeout caps how long DialContext may take. Default 5s —
	// a slow subscriber on the connect leg means the subscriber's
	// infrastructure is broken, not just busy.
	ConnectTimeout time.Duration

	// RequestTimeout caps the entire request lifecycle from Do() entry
	// to body close. Default 30s. Subscribers that legitimately need
	// longer should chunk their processing into a 200 OK quick-ack and
	// background work, not block the deliverer.
	RequestTimeout time.Duration

	// TLSHandshakeTimeout caps the TLS handshake. Default 5s. Aligns
	// with ConnectTimeout philosophy.
	TLSHandshakeTimeout time.Duration

	// IdleConnTimeout caps how long idle keep-alive connections live.
	// Default 90s, matching the Go stdlib default.
	IdleConnTimeout time.Duration

	// MaxIdleConnsPerHost caps connection reuse per subscriber host.
	// Default 4 — webhooks are bursty per-subscription; we don't need
	// a connection pool the size of an API client.
	MaxIdleConnsPerHost int

	// MaxResponseBytes caps how much of the response body we will read
	// before treating the response as too large. Default 64KB. We
	// don't need the body (just the status) but draining a small body
	// preserves connection reuse; abusive subscribers can't burn our
	// memory by streaming gigabytes.
	MaxResponseBytes int64

	// DisableSSRFGuard turns off the per-dial SSRF check. The default
	// (zero value = guard enabled) rejects any subscriber URL whose
	// IP resolves into the private/loopback/link-local denylist, which
	// is the right posture for production. Tests that need to talk to
	// httptest.Server (which always binds to loopback) flip this to
	// true. Production wiring MUST leave it false.
	DisableSSRFGuard bool
}

// applyDefaults fills in zero fields with the production defaults
// documented above. The receiver is not mutated.
func (c ClientConfig) applyDefaults() ClientConfig {
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 30 * time.Second
	}
	if c.TLSHandshakeTimeout <= 0 {
		c.TLSHandshakeTimeout = 5 * time.Second
	}
	if c.IdleConnTimeout <= 0 {
		c.IdleConnTimeout = 90 * time.Second
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = 4
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = 64 * 1024
	}
	return c
}

// errRedirect is the sentinel CheckRedirect returns to short-circuit any
// 3xx response. We surface 3xx as a permanent failure rather than
// follow: a redirect to a different host would let a misconfigured
// subscriber re-emit our signed body somewhere unrelated. The signed
// body is intended for one URL; if the subscriber moved, they should
// update the subscription rather than chain via 3xx.
var errRedirect = errors.New("webhook delivery: redirects are not allowed")

// newHTTPClient builds a configured http.Client. The returned client is
// safe for concurrent use and reuses connections via a hardened
// transport. Tests can substitute the Transport (or pass an
// http.RoundTripper override) to point at an httptest.Server.
//
// The dialer wraps net.Dialer.DialContext with an SSRF guard backed by
// packages/go/safehttp: the destination address is checked against the
// public-IP denylist before the TCP connection is opened. A subscriber
// whose URL resolves to (say) 169.254.169.254 fails with ErrBlocked
// rather than reaching the cloud-metadata service. This complements the
// scheme allowlist on the subscription-creation path: defense in depth,
// because a subscriber that was valid at creation time could have its
// DNS pointed at a private IP later (DNS rebinding).
func newHTTPClient(cfg ClientConfig) *http.Client {
	cfg = cfg.applyDefaults()
	rawDialer := &net.Dialer{
		Timeout:   cfg.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}
	dial := rawDialer.DialContext
	if !cfg.DisableSSRFGuard {
		dial = ssrfGuardedDial(dial)
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		// TLSClientConfig left as zero — that means: use system roots,
		// verify hostname, enforce TLS. We never set InsecureSkipVerify
		// here. Operators who need to talk to a self-signed test
		// subscriber should configure their system trust store instead
		// of disabling verification globally.
	}
	return &http.Client{
		Transport: transport,
		Timeout:   cfg.RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// redirect cap = 0 (i.e., reject ANY redirect, including
			// the first hop). See errRedirect docs.
			_ = req
			_ = via
			return errRedirect
		},
	}
}

// ssrfGuardedDial wraps a DialContext function with an SSRF check on
// the resolved address. The wrapped function still uses the host:port
// it was given (so HTTP cares unchanged) but rejects the dial if the
// host resolves to a private/loopback/link-local IP.
//
// Implementation: net.SplitHostPort the address, run AssertHostPublic,
// then defer to the underlying dialer. The check runs synchronously
// before the TCP connect, so a blocked subscriber fails fast.
func ssrfGuardedDial(next func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			// Malformed addr — let the underlying dialer surface the
			// error; we'd rather not invent a parse-failure path.
			return next(ctx, network, addr)
		}
		if err := safehttp.AssertHostPublic(ctx, host); err != nil {
			return nil, err
		}
		return next(ctx, network, addr)
	}
}

// isRedirectError reports whether err is the redirect sentinel wrapped
// inside a *url.Error. The stdlib wraps CheckRedirect's return value, so
// errors.Is unwraps through to our sentinel.
func isRedirectError(err error) bool {
	return errors.Is(err, errRedirect)
}

// netClass classifies an error returned by http.Client.Do (or by reading
// the body) into a result Status. Returns one of StatusRetry,
// StatusDeadletter, or — for nil err — caller-handled-out-of-band
// (StatusSuccess is decided by the caller from the response code).
//
// Classification rules (see ../types.go sentinel errors for the
// underlying contract):
//
//   - context cancelled by caller (ctx done with non-deadline reason):
//     bubble up; the worker is shutting down. Returns StatusRetry so
//     the task isn't lost.
//   - context deadline exceeded: timeout, retry.
//   - net.Error.Timeout(): timeout, retry.
//   - redirect rejected: permanent — deadletter.
//   - everything else: transient (DNS, TCP reset, TLS handshake).
//
// The caller is expected to attach the right sentinel via the Result.
func netClass(ctx context.Context, err error) Status {
	if err == nil {
		return StatusSuccess
	}
	// Redirect = permanent.
	if isRedirectError(err) {
		return StatusDeadletter
	}
	// SSRF guard rejection = permanent. The subscriber URL points at a
	// private/loopback/link-local IP; no amount of retrying fixes that
	// and we don't want to keep DOSing the cloud-metadata service.
	if errors.Is(err, safehttp.ErrBlocked) {
		return StatusDeadletter
	}
	// Caller cancellation (not deadline): pass through as retry so the
	// task isn't archived during a graceful worker shutdown. Asynq
	// detects shutdown separately and won't actually re-enqueue here;
	// the worker context already plays into that.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return StatusRetry
		}
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return StatusRetry
		}
	}
	// Timeout: retry.
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return StatusRetry
	}
	// Everything else (refused, reset, DNS): retry.
	return StatusRetry
}
