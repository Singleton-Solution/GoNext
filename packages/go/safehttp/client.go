package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default caps. These mirror the brief: 30s timeout, 3-redirect cap,
// 10 MiB body cap. Callers override via the options pattern.
const (
	// DefaultTimeout is the per-request deadline if WithTimeout was
	// not supplied. 30s is generous for any well-behaved upstream and
	// matches the value used by the plugin runtime's http.fetch ABI.
	DefaultTimeout = 30 * time.Second

	// DefaultMaxRedirects is the redirect-hop cap. 3 is enough to follow
	// the common "http -> https" and "/api -> /api/v2" patterns without
	// admitting a redirect loop.
	DefaultMaxRedirects = 3

	// DefaultMaxResponseBytes is the body-size cap. 10 MiB is generous
	// for an API response; anything bigger is almost always a streaming
	// endpoint the safehttp client isn't the right tool for.
	DefaultMaxResponseBytes int64 = 10 * 1024 * 1024
)

// Client is a hardened HTTP client. Construct via New(); the zero value
// is unusable (the SSRF guard requires a Resolver, even if it's the
// default — Client.Do panics on a nil Client).
//
// Concurrency: safe for concurrent use, like net/http.Client.
type Client struct {
	httpClient *http.Client

	allowlist        map[string]struct{}
	allowSubdomains  bool
	resolver         Resolver
	timeout          time.Duration
	maxRedirects     int
	maxResponseBytes int64
	allowedSchemes   map[string]struct{}
}

// Option configures a Client at construction. Options compose; later
// options win when they overlap (e.g. two WithTimeout calls — the last
// one takes effect).
type Option func(*Client)

// WithAllowlist sets the exact list of hostnames the client may reach.
// Hostnames are compared case-insensitively (DNS is case-insensitive).
// An empty allowlist refuses every request — that's the safe default;
// callers MUST supply at least one host.
//
// Subdomain handling: by default the match is exact. Use
// WithAllowlistSubdomains in addition to grant access to subdomains of
// the allowlisted hosts.
func WithAllowlist(hosts ...string) Option {
	return func(c *Client) {
		c.allowlist = make(map[string]struct{}, len(hosts))
		for _, h := range hosts {
			h = strings.ToLower(strings.TrimSpace(h))
			if h == "" {
				continue
			}
			c.allowlist[h] = struct{}{}
		}
	}
}

// WithAllowlistSubdomains, when set, treats the allowlist as a list of
// suffixes: a request to "api.example.com" is allowed when
// "example.com" is on the list. Use sparingly — exact-match is the
// safer default because a typo in the allowlist (e.g. omitting a
// component) can't accidentally grant a large slice of the DNS.
func WithAllowlistSubdomains() Option {
	return func(c *Client) { c.allowSubdomains = true }
}

// WithTimeout overrides the default request timeout. Pass 0 to disable
// (NOT recommended — callers should always supply a sensible cap).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// WithMaxRedirects overrides the redirect-hop cap. Pass 0 to disable
// redirects entirely; -1 is treated as "no limit" (also not
// recommended — a redirect loop will then hit the request timeout
// instead, which produces a less-clear error).
func WithMaxRedirects(n int) Option {
	return func(c *Client) { c.maxRedirects = n }
}

// WithMaxResponseBytes overrides the body-size cap. Pass 0 to disable
// (not recommended outside tests).
func WithMaxResponseBytes(n int64) Option {
	return func(c *Client) { c.maxResponseBytes = n }
}

// WithResolver overrides the DNS resolver used for SSRF checks. Tests
// substitute a fake resolver here so a hostname can be made to resolve
// to 127.0.0.1 (the SSRF guard would otherwise reject httptest.Server
// addresses, since httptest binds to loopback).
func WithResolver(r Resolver) Option {
	return func(c *Client) { c.resolver = r }
}

// WithDialContext overrides the net.Dialer's DialContext. Tests use
// this to pin connections to an httptest.Server's listener without
// going through real DNS+TCP.
func WithDialContext(f func(ctx context.Context, network, addr string) (net.Conn, error)) Option {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{}
		}
		t, ok := c.httpClient.Transport.(*http.Transport)
		if !ok {
			t = &http.Transport{}
			c.httpClient.Transport = t
		}
		t.DialContext = f
	}
}

// WithSchemes overrides the URL scheme allowlist. Default: {"https"}.
// Tests and dev tooling can opt into {"http", "https"} explicitly.
// Other schemes (file, gopher, ftp) are never allowed regardless of
// what's passed.
func WithSchemes(schemes ...string) Option {
	return func(c *Client) {
		c.allowedSchemes = make(map[string]struct{}, len(schemes))
		for _, s := range schemes {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "http" && s != "https" {
				continue
			}
			c.allowedSchemes[s] = struct{}{}
		}
	}
}

// WithHTTPClient replaces the underlying *http.Client. Use only when
// the default transport doesn't fit — for example, when a caller needs
// a custom RoundTripper for request-level tracing. The package will
// still install its own CheckRedirect on the supplied client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// New constructs a Client. At least one host must be on the allowlist;
// New returns an error otherwise (a no-allowlist client is useless —
// it refuses every request, and we'd rather catch that at boot than at
// the first call).
func New(opts ...Option) (*Client, error) {
	c := &Client{
		timeout:          DefaultTimeout,
		maxRedirects:     DefaultMaxRedirects,
		maxResponseBytes: DefaultMaxResponseBytes,
		allowedSchemes:   map[string]struct{}{"https": {}},
	}
	for _, opt := range opts {
		opt(c)
	}
	if len(c.allowlist) == 0 {
		return nil, errors.New("safehttp: allowlist is empty; refusing to construct a client that would block every request")
	}
	if c.resolver == nil {
		c.resolver = defaultResolver
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				// Defaults mirror net/http's DefaultTransport but with
				// a tighter dial deadline; the per-request timeout
				// still bounds everything end-to-end.
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}
	// Install the redirect cap + revalidation hook regardless of
	// whether the caller supplied their own *http.Client. The hook
	// re-runs the allowlist + SSRF guard at each hop.
	c.httpClient.CheckRedirect = c.checkRedirect
	if c.timeout > 0 {
		c.httpClient.Timeout = c.timeout
	}
	return c, nil
}

// Do issues req via the underlying client after running the URL
// through the allowlist + SSRF guard. The returned Response.Body is
// wrapped in a LimitReader bounded by MaxResponseBytes; readers that
// need to distinguish "body fit" from "body truncated" should compare
// the bytes read to the cap.
//
// On any pre-flight failure the function returns a wrapped ErrBlocked.
// On a successful pre-flight the underlying error is the *url.Error
// the stdlib returns from http.Client.Do.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c == nil {
		return nil, errors.New("safehttp: nil Client")
	}
	if err := c.preflight(ctx, req.URL); err != nil {
		return nil, err
	}
	// Bind the supplied ctx into the request so the caller's
	// cancellation propagates. The Client.Timeout still applies.
	req = req.WithContext(ctx)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if c.maxResponseBytes > 0 {
		resp.Body = wrapLimit(resp.Body, c.maxResponseBytes)
	}
	return resp, nil
}

// Get is a convenience for c.Do with method=GET. Returns the body
// limited to MaxResponseBytes.
func (c *Client) Get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, req)
}

// Post is a convenience for c.Do with method=POST. The body is sent
// as-is; the caller sets Content-Type via req if needed (or use
// PostWithContentType).
func (c *Client) Post(ctx context.Context, rawURL, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(ctx, req)
}

// Allows reports whether host would be permitted by the allowlist. Use
// from callers that want to fail fast before constructing a request
// (e.g. webhook registration).
func (c *Client) Allows(host string) bool {
	return c.allows(host)
}

// allows is the internal allowlist check. Pulled out for reuse by Do
// and CheckRedirect.
func (c *Client) allows(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if _, ok := c.allowlist[host]; ok {
		return true
	}
	if !c.allowSubdomains {
		return false
	}
	for h := range c.allowlist {
		// "api.example.com" matches "example.com" only if the suffix
		// is preceded by a '.', so "evilexample.com" doesn't match.
		if strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// preflight runs the scheme + allowlist + SSRF gauntlet on u. Returns
// a wrapped ErrBlocked on any failure.
func (c *Client) preflight(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: nil URL", ErrBlocked)
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := c.allowedSchemes[scheme]; !ok {
		return fmt.Errorf("%w: scheme %q not allowed", ErrBlocked, scheme)
	}
	host := u.Hostname()
	if !c.allows(host) {
		return fmt.Errorf("%w: host %q not in allowlist", ErrBlocked, host)
	}
	return assertHostPublic(ctx, c.resolver, host)
}

// checkRedirect is installed on the underlying http.Client. It enforces
// the redirect cap and re-validates each hop through preflight — that
// re-runs the allowlist + SSRF check, so a 302 to a private IP is
// rejected just like a direct request would have been.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if c.maxRedirects == 0 {
		return errors.New("safehttp: redirects are disabled")
	}
	if c.maxRedirects > 0 && len(via) >= c.maxRedirects {
		return fmt.Errorf("safehttp: redirect cap (%d) exceeded", c.maxRedirects)
	}
	return c.preflight(req.Context(), req.URL)
}

// limitReader wraps an io.ReadCloser with an io.LimitReader. We hold the
// original ReadCloser so Close still releases the underlying connection
// to the connection pool.
type limitReader struct {
	r io.Reader
	c io.Closer
}

func (l *limitReader) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitReader) Close() error               { return l.c.Close() }

// wrapLimit installs a LimitReader on rc bounded by max. The LimitReader
// returns io.EOF at the boundary; callers that need to distinguish
// "body fit" from "body truncated" should compare bytes read to max.
func wrapLimit(rc io.ReadCloser, max int64) io.ReadCloser {
	return &limitReader{r: io.LimitReader(rc, max), c: rc}
}
