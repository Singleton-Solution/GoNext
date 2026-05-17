package csp

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Counter is the dependency the ReportHandler uses to record violation
// counts. It mirrors the prometheus.CounterVec shape so callers can pass
// either a real Prometheus counter or a test double.
//
// The labels argument identifies the violation's bucket. The handler
// emits labels in a documented order (see ReportLabels) so callers can
// register a CounterVec with matching label names.
type Counter interface {
	// Inc increments the counter for the given label set by 1.
	Inc(labels ...string)
}

// ReportLabels lists the label names emitted by ReportHandler to its
// Counter, in the order they are passed to Inc. Callers wiring a
// Prometheus CounterVec should declare the same labels in the same
// order.
//
//	[0] effective_directive — e.g. "script-src-elem", or "" if unknown
//	[1] blocked_host        — host portion of blocked-uri, or "" if unknown
//	[2] disposition         — "enforce" or "report", from the report
//	[3] report_kind         — "legacy" (application/csp-report) or
//	                          "modern" (application/reports+json)
var ReportLabels = []string{
	"effective_directive",
	"blocked_host",
	"disposition",
	"report_kind",
}

// nopCounter discards every Inc call. Used as the default when callers
// do not supply their own Counter.
type nopCounter struct{}

func (nopCounter) Inc(_ ...string) {}

// ReportConfig configures ReportHandler. The zero value is valid: the
// handler logs to slog.Default(), discards counters, and rate-limits at
// 100 requests/minute/IP (the budget from
// docs/13-security-baseline.md §11).
type ReportConfig struct {
	// Logger receives the WARN-level structured log line for each
	// accepted violation. Defaults to slog.Default().
	Logger *slog.Logger

	// Counter records one Inc per accepted violation, labeled per
	// ReportLabels. Defaults to a no-op counter.
	Counter Counter

	// RateLimit is the maximum number of accepted reports per minute
	// per client IP. Defaults to 100 if zero or negative. Once the
	// budget is exhausted, further reports from the IP receive
	// HTTP 429 Too Many Requests until the next minute window.
	RateLimit int

	// Window is the rolling window for RateLimit. Defaults to one
	// minute if zero.
	Window time.Duration

	// MaxBodyBytes caps the request body size. Defaults to 64 KiB.
	// Larger bodies are truncated and the report is rejected with 413
	// Payload Too Large. The default matches typical browser report
	// sizes (~2-4 KiB) with generous headroom.
	MaxBodyBytes int64

	// ClientIP extracts the rate-limit key from a request. The default
	// reads the leftmost X-Forwarded-For entry (when present) and
	// falls back to net.SplitHostPort(r.RemoteAddr). Override to
	// integrate with a custom edge / trusted-proxy scheme.
	ClientIP func(*http.Request) string

	// Now is the time source for the rate-limit window. Defaults to
	// time.Now; override in tests for determinism.
	Now func() time.Time
}

// defaultClientIP extracts the client IP from r, preferring the
// leftmost X-Forwarded-For entry when present, and falling back to
// r.RemoteAddr.
//
// The function trusts X-Forwarded-For unconditionally — callers behind
// an untrusted proxy chain should override ClientIP and resolve XFF
// against their trusted-proxy list.
func defaultClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Leftmost entry is the original client per RFC 7239.
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			xff = xff[:comma]
		}
		xff = strings.TrimSpace(xff)
		if xff != "" {
			return xff
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ReportHandler returns an http.Handler that accepts CSP violation
// reports at /_/csp-report (the path is by convention; mount it on the
// path you advertise via report-uri / report-to).
//
// Request shape:
//
//   - Method: POST (other methods return 405).
//   - Content-Type:
//   - application/csp-report          — legacy single-report body
//     {"csp-report": { ... }}
//   - application/reports+json        — modern Reporting API body, an
//     array of report objects each with
//     a "type": "csp-violation" key.
//   - application/json               — accepted opportunistically;
//     decoder tries legacy first,
//     then modern array shape.
//   - Body: up to MaxBodyBytes (default 64 KiB).
//
// Responses:
//
//   - 204 No Content — report accepted and recorded.
//   - 400 Bad Request — malformed JSON, wrong shape, no recognized
//     reports inside.
//   - 405 Method Not Allowed — non-POST request.
//   - 413 Payload Too Large — body exceeded MaxBodyBytes.
//   - 415 Unsupported Media Type — content-type not in the list above.
//   - 429 Too Many Requests — per-IP rate limit exceeded.
//
// On acceptance, each parsed report is:
//
//   - logged at slog.Warn with structured fields (document_uri,
//     blocked_uri, violated_directive, effective_directive, …);
//   - counted via the injected Counter with labels per ReportLabels.
//
// Cardinality control: blocked_uri is reduced to host only before being
// emitted as a label (per docs/13 §3.5). Raw values are kept in the
// log line.
func ReportHandler(cfg ReportConfig) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Counter == nil {
		cfg.Counter = nopCounter{}
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 100
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 64 * 1024
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = defaultClientIP
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	limiter := newIPLimiter(cfg.RateLimit, cfg.Window, cfg.Now)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Identify the client first so the rate-limit decision is fast
		// and does not depend on parsing the body.
		ip := cfg.ClientIP(r)
		if !limiter.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Reject obviously-wrong content types early.
		ct := contentType(r.Header.Get("Content-Type"))
		kind, ok := classifyContentType(ct)
		if !ok {
			http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
			return
		}

		// Enforce body size before decoding. http.MaxBytesReader caps
		// the body and returns an error on the next Read; we surface
		// the cap explicitly as 413 below.
		r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		reports, parseKind, err := parseReportBody(body, kind)
		if err != nil || len(reports) == 0 {
			http.Error(w, "malformed csp report", http.StatusBadRequest)
			return
		}

		for _, rep := range reports {
			cfg.Logger.LogAttrs(r.Context(), slog.LevelWarn, "csp violation",
				slog.String("document_uri", rep.DocumentURI),
				slog.String("referrer", rep.Referrer),
				slog.String("blocked_uri", rep.BlockedURI),
				slog.String("violated_directive", rep.ViolatedDirective),
				slog.String("effective_directive", rep.EffectiveDirective),
				slog.String("original_policy_excerpt", excerpt(rep.OriginalPolicy, 256)),
				slog.String("disposition", rep.Disposition),
				slog.String("source_file", rep.SourceFile),
				slog.Int("line_number", rep.LineNumber),
				slog.Int("column_number", rep.ColumnNumber),
				slog.String("status_code", rep.StatusCode),
				slog.String("report_kind", parseKind),
				slog.String("remote_ip", ip),
			)
			cfg.Counter.Inc(
				rep.EffectiveDirective,
				hostOf(rep.BlockedURI),
				rep.Disposition,
				parseKind,
			)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// contentType returns just the media type portion of a Content-Type
// header value (no parameters, lowercased).
func contentType(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// classifyContentType maps a media-type string to the internal kind
// used by parseReportBody. Returns ok=false for unsupported types.
func classifyContentType(ct string) (string, bool) {
	switch ct {
	case "application/csp-report":
		return "legacy", true
	case "application/reports+json":
		return "modern", true
	case "application/json", "":
		// Some browsers / proxies send plain application/json; accept
		// and let the body shape decide. Empty content-type from a
		// minimal test fixture is also tolerated.
		return "auto", true
	}
	return "", false
}

// excerpt returns the first n runes of s plus an ellipsis when s
// exceeds n. Bytes-not-runes is used here because we only need an
// approximation for log cardinality control.
func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// hostOf returns the host portion of a (possibly relative or malformed)
// URL. Used to bucket the blocked-uri label without unbounded
// cardinality.
func hostOf(uri string) string {
	if uri == "" {
		return ""
	}
	// Strip scheme.
	if i := strings.Index(uri, "://"); i >= 0 {
		uri = uri[i+3:]
	}
	// Cut at first /, ?, # — anything path-like.
	for i := 0; i < len(uri); i++ {
		c := uri[i]
		if c == '/' || c == '?' || c == '#' {
			uri = uri[:i]
			break
		}
	}
	// Drop any userinfo prefix.
	if i := strings.IndexByte(uri, '@'); i >= 0 {
		uri = uri[i+1:]
	}
	// Drop port.
	if i := strings.LastIndexByte(uri, ':'); i >= 0 {
		// Only strip a port if everything after the colon is digits.
		port := uri[i+1:]
		allDigits := port != ""
		for _, c := range port {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			uri = uri[:i]
		}
	}
	return uri
}

// ipLimiter is a fixed-window per-IP rate limiter. Keeps counts in a
// map keyed by IP; expired windows are reset on the next call from the
// same IP, so the map size grows with active IPs rather than total
// IPs seen.
//
// The implementation is deliberately simple: CSP reports are a low-rate
// stream (one violation per page load at most) and the window is one
// minute, so contention is negligible.
type ipLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	state map[string]ipBucket
}

type ipBucket struct {
	resetAt time.Time
	count   int
}

func newIPLimiter(limit int, window time.Duration, now func() time.Time) *ipLimiter {
	return &ipLimiter{
		limit:  limit,
		window: window,
		now:    now,
		state:  make(map[string]ipBucket),
	}
}

// allow returns true if the IP is under its limit (and increments the
// count). Otherwise it returns false without incrementing.
//
// Pruning policy: when a bucket's resetAt is in the past we reset it
// in-place. We additionally GC unrelated stale buckets opportunistically
// every ~128 calls, capping memory growth in pathological scenarios.
func (l *ipLimiter) allow(ip string) bool {
	if ip == "" {
		// Refuse to apply per-IP limits when we cannot identify the IP.
		// This is intentionally conservative: an unidentified client
		// might be a misconfigured edge or a localhost test; we let
		// the requests through rather than create a global denial of
		// service via one shared bucket.
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.state[ip]
	if !ok || !now.Before(b.resetAt) {
		b = ipBucket{resetAt: now.Add(l.window), count: 0}
	}
	if b.count >= l.limit {
		l.state[ip] = b
		return false
	}
	b.count++
	l.state[ip] = b

	// Opportunistic GC. Cheap and bounded; runs roughly once per 128
	// calls to keep the map from accumulating long-dead buckets when
	// the caller's IP distribution churns.
	if len(l.state) > 1024 && now.UnixNano()&0x7f == 0 {
		for k, v := range l.state {
			if !now.Before(v.resetAt) {
				delete(l.state, k)
			}
		}
	}
	return true
}

// jsonDecode is a small wrapper that returns ErrUnexpectedEOF as a
// "malformed" error rather than letting it bubble up untyped.
func jsonDecode(data []byte, out any) error {
	if len(data) == 0 {
		return errMalformed
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

var errMalformed = errors.New("malformed report body")
