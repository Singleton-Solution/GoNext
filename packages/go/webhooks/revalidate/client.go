package revalidate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds the HTTP request to the Next.js side. ISR
// revalidation is best-effort — a slow response means a stale cache
// page is served for a few seconds, which is preferable to blocking
// the REST POST that triggered the notify.
const DefaultTimeout = 5 * time.Second

// HTTPClient is the small surface Notify needs from net/http. The
// concrete type used in production is *http.Client; tests substitute a
// stub.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client posts revalidation requests to the apps/web ISR endpoint.
//
// A Client with an empty BaseURL OR Secret is "disabled" — Notify
// returns nil without making a request. This is the right shape for
// the chassis's "renderer optional" deployment: operators who run the
// API standalone (or behind a static host that doesn't speak Next.js
// ISR) don't have to wire a fake URL just to silence errors.
type Client struct {
	baseURL string
	secret  string
	http    HTTPClient
	logger  *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying HTTP client. Tests pass a
// stub; production code accepts the default (an *http.Client with
// DefaultTimeout).
func WithHTTPClient(c HTTPClient) Option {
	return func(cl *Client) {
		if c != nil {
			cl.http = c
		}
	}
}

// WithLogger swaps the structured logger. nil keeps slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(cl *Client) {
		if l != nil {
			cl.logger = l
		}
	}
}

// New returns a Client that will POST to baseURL/api/revalidate. Both
// baseURL and secret may be empty — the resulting Client's Notify is a
// no-op (returns nil), which is the production behavior when
// GONEXT_NEXT_REVALIDATE_URL or GONEXT_NEXT_REVALIDATE_SECRET is unset.
//
// baseURL is normalized: trailing slash trimmed (the client appends
// "/api/revalidate" verbatim, so a trailing slash would produce a
// double slash). A baseURL that doesn't parse as an absolute URL is
// kept as-is — the error surfaces on the first Notify call instead of
// at construction so a misconfigured chassis still boots (a noisy log
// line is preferred over a hard boot failure on a best-effort hook).
func New(baseURL, secret string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		http:    &http.Client{Timeout: DefaultTimeout},
		logger:  slog.Default(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Enabled reports whether the client will actually issue requests. A
// disabled client's Notify returns nil — useful for callers that want
// to skip computing the path argument when revalidation is off.
func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != "" && c.secret != ""
}

// Notify POSTs a revalidation request for the given path. Path SHOULD
// be the URL path that should be revalidated on the Next.js side —
// e.g. "/" for the homepage feed, "/posts/{slug}" for a single post
// page.
//
// The function is best-effort:
//
//   - Disabled client → returns nil silently.
//   - Empty path → returns nil silently (nothing to revalidate).
//   - HTTP transport error → returned to caller; the REST handler
//     logs and continues (a failed revalidation should not roll back
//     a successful publish).
//   - Non-2xx response → ErrUpstream with the status code included so
//     the caller can decide whether to retry.
//
// The secret is sent as a query parameter (not a header) because
// Next.js's ISR convention is `?secret=...` in the route handler.
// Sending it as a query string in a TLS-encrypted POST is fine — the
// concern with secrets-in-URLs is access log leakage, and we control
// both endpoints of this hop.
func (c *Client) Notify(ctx context.Context, path string) error {
	if !c.Enabled() {
		return nil
	}
	if path == "" {
		return nil
	}

	u, err := buildURL(c.baseURL, path, c.secret)
	if err != nil {
		return fmt.Errorf("revalidate: build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return fmt.Errorf("revalidate: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// The Next.js side reads X-GoNext-Source to differentiate
	// revalidation requests from other webhook traffic on the same
	// origin. Cheap to set, useful in logs.
	req.Header.Set("X-GoNext-Source", "rest")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("revalidate: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
}

// NotifyMany fires Notify for each non-empty path in paths. Errors are
// aggregated (errors.Join) so the caller sees all failures, not just
// the first. Empty / disabled paths are skipped silently.
//
// Used when a single publish event invalidates several routes — e.g.
// publishing a post should revalidate both "/posts/{slug}" and "/" so
// the homepage feed picks up the new entry.
func (c *Client) NotifyMany(ctx context.Context, paths []string) error {
	if !c.Enabled() {
		return nil
	}
	var errs []error
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := c.Notify(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// ErrUpstream is returned by Notify when the apps/web side answers
// with a non-2xx status. The error message includes the status code
// so callers can log it without a separate field; errors.Is(err,
// ErrUpstream) reports the category.
var ErrUpstream = errors.New("revalidate: upstream returned non-2xx")

// buildURL constructs the full Next.js ISR endpoint URL. We use
// url.Parse + url.Values rather than fmt.Sprintf so callers passing a
// path with special characters (a slug with a hash, an apostrophe)
// don't produce a malformed URL.
func buildURL(base, path, secret string) (string, error) {
	u, err := url.Parse(base + "/api/revalidate")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("path", path)
	q.Set("secret", secret)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
