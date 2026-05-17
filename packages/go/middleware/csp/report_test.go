package csp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingCounter captures Inc calls so tests can verify the labeled
// metric output produced by ReportHandler.
type recordingCounter struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *recordingCounter) Inc(labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string(nil), labels...))
}

func (c *recordingCounter) Calls() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.calls))
	for i, c := range c.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// postReport sends a CSP report to the handler with the given
// content-type. Returns the response recorder for assertion.
func postReport(t *testing.T, h http.Handler, body, contentType, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_/csp-report", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// legacyReportBody returns a syntactically valid application/csp-report
// body suitable for piping into the handler. Keeping a single fixture
// here keeps the tests focused on behavior.
const legacyReportBody = `{
  "csp-report": {
    "document-uri":        "https://example.com/page",
    "referrer":            "",
    "blocked-uri":         "https://evil.example/script.js",
    "violated-directive":  "script-src-elem",
    "effective-directive": "script-src-elem",
    "original-policy":     "default-src 'self'; script-src 'self'",
    "disposition":         "enforce",
    "source-file":         "https://example.com/page",
    "line-number":         42,
    "column-number":       17,
    "status-code":         200
  }
}`

// modernReportBody returns a Reporting API JSON array containing a
// single csp-violation entry with camelCase keys.
const modernReportBody = `[
  {
    "age": 0,
    "type": "csp-violation",
    "url":  "https://example.com/page",
    "body": {
      "documentURL":        "https://example.com/page",
      "blockedURL":         "https://evil.example/script.js",
      "violatedDirective":  "script-src-elem",
      "effectiveDirective": "script-src-elem",
      "originalPolicy":     "default-src 'self'",
      "disposition":        "enforce",
      "sourceFile":         "https://example.com/page",
      "lineNumber":         42,
      "columnNumber":       17,
      "statusCode":         200
    }
  }
]`

// TestReportHandler_AcceptsLegacyBody exercises the
// application/csp-report path: 204 No Content, one counter increment,
// one warn log.
func TestReportHandler_AcceptsLegacyBody(t *testing.T) {
	counter := &recordingCounter{}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h := ReportHandler(ReportConfig{Logger: logger, Counter: counter})

	rec := postReport(t, h, legacyReportBody, "application/csp-report", "1.2.3.4:9999")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204\nbody: %s", rec.Code, rec.Body.String())
	}
	calls := counter.Calls()
	if len(calls) != 1 {
		t.Fatalf("counter calls: got %d, want 1", len(calls))
	}
	if calls[0][0] != "script-src-elem" || calls[0][1] != "evil.example" || calls[0][2] != "enforce" || calls[0][3] != "legacy" {
		t.Errorf("counter labels wrong: %v", calls[0])
	}
	if !strings.Contains(logBuf.String(), "csp violation") {
		t.Errorf("expected log line, got:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "evil.example") {
		t.Errorf("expected blocked_uri in log, got:\n%s", logBuf.String())
	}
}

// TestReportHandler_AcceptsModernBody exercises the
// application/reports+json path with the documented array shape.
func TestReportHandler_AcceptsModernBody(t *testing.T) {
	counter := &recordingCounter{}
	h := ReportHandler(ReportConfig{Counter: counter, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	rec := postReport(t, h, modernReportBody, "application/reports+json", "1.2.3.4:9999")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204\nbody: %s", rec.Code, rec.Body.String())
	}
	calls := counter.Calls()
	if len(calls) != 1 {
		t.Fatalf("counter calls: got %d, want 1", len(calls))
	}
	if calls[0][3] != "modern" {
		t.Errorf("expected modern kind label, got %v", calls[0])
	}
}

// TestReportHandler_AcceptsApplicationJSONAuto verifies the
// application/json escape hatch: the body shape decides between legacy
// and modern.
func TestReportHandler_AcceptsApplicationJSONAuto(t *testing.T) {
	h := ReportHandler(ReportConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	rec := postReport(t, h, legacyReportBody, "application/json", "1.2.3.4:1000")
	if rec.Code != http.StatusNoContent {
		t.Errorf("legacy-via-json status: got %d, want 204", rec.Code)
	}

	rec = postReport(t, h, modernReportBody, "application/json", "1.2.3.4:1001")
	if rec.Code != http.StatusNoContent {
		t.Errorf("modern-via-json status: got %d, want 204", rec.Code)
	}
}

// TestReportHandler_RejectsMalformed checks the 400 path for a few
// representative malformed bodies.
func TestReportHandler_RejectsMalformed(t *testing.T) {
	h := ReportHandler(ReportConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	cases := []struct {
		name string
		body string
		ct   string
	}{
		{"empty body", "", "application/csp-report"},
		{"not json", "not json", "application/csp-report"},
		{"missing csp-report wrapper", `{"foo":"bar"}`, "application/csp-report"},
		{"empty csp-report", `{"csp-report":{}}`, "application/csp-report"},
		{"empty modern array", `[]`, "application/reports+json"},
		{"modern entry without csp-violation type", `[{"type":"deprecation","body":{"id":"x"}}]`, "application/reports+json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postReport(t, h, tc.body, tc.ct, "9.9.9.9:1234")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestReportHandler_RejectsUnsupportedMethod verifies non-POST is 405.
func TestReportHandler_RejectsUnsupportedMethod(t *testing.T) {
	h := ReportHandler(ReportConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest(http.MethodGet, "/_/csp-report", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("Allow header missing")
	}
}

// TestReportHandler_RejectsUnsupportedMediaType verifies the 415 path.
func TestReportHandler_RejectsUnsupportedMediaType(t *testing.T) {
	h := ReportHandler(ReportConfig{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	rec := postReport(t, h, legacyReportBody, "text/plain", "9.9.9.9:1234")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status: got %d, want 415", rec.Code)
	}
}

// TestReportHandler_PayloadTooLarge enforces MaxBodyBytes.
func TestReportHandler_PayloadTooLarge(t *testing.T) {
	h := ReportHandler(ReportConfig{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: 16,
	})
	rec := postReport(t, h, legacyReportBody, "application/csp-report", "9.9.9.9:1234")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want 413", rec.Code)
	}
}

// TestReportHandler_RateLimitDrops verifies the per-IP rate limiter
// returns 429 once the budget is exhausted, and resumes after the
// window rolls over.
func TestReportHandler_RateLimitDrops(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := &atomic.Int64{}
	clock.Store(now.UnixNano())
	nowFn := func() time.Time {
		return time.Unix(0, clock.Load())
	}
	h := ReportHandler(ReportConfig{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: 3,
		Window:    time.Minute,
		Now:       nowFn,
	})

	for i := 0; i < 3; i++ {
		rec := postReport(t, h, legacyReportBody, "application/csp-report", "5.5.5.5:1111")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("iter %d: got %d, want 204", i, rec.Code)
		}
	}
	rec := postReport(t, h, legacyReportBody, "application/csp-report", "5.5.5.5:1111")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("post-limit: got %d, want 429", rec.Code)
	}

	// Another IP must still be allowed.
	rec = postReport(t, h, legacyReportBody, "application/csp-report", "6.6.6.6:2222")
	if rec.Code != http.StatusNoContent {
		t.Errorf("other IP got blocked: %d", rec.Code)
	}

	// Advance past the window — the original IP must be allowed again.
	clock.Store(now.Add(61 * time.Second).UnixNano())
	rec = postReport(t, h, legacyReportBody, "application/csp-report", "5.5.5.5:1111")
	if rec.Code != http.StatusNoContent {
		t.Errorf("post-reset: got %d, want 204", rec.Code)
	}
}

// TestReportHandler_RespectsXForwardedFor verifies the client-IP
// extractor's leftmost-XFF behavior, which the rate limiter then keys
// off of.
func TestReportHandler_RespectsXForwardedFor(t *testing.T) {
	h := ReportHandler(ReportConfig{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		RateLimit: 1,
	})

	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodPost, "/_/csp-report", strings.NewReader(legacyReportBody))
		req.Header.Set("Content-Type", "application/csp-report")
		req.RemoteAddr = "127.0.0.1:0"
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if c := send("10.0.0.1, 10.0.0.2"); c != http.StatusNoContent {
		t.Errorf("first call: got %d", c)
	}
	if c := send("10.0.0.1, 10.0.0.3"); c != http.StatusTooManyRequests {
		t.Errorf("same leftmost-XFF should hit limit: got %d", c)
	}
	if c := send("10.0.0.99"); c != http.StatusNoContent {
		t.Errorf("different leftmost-XFF should pass: got %d", c)
	}
}

// TestReportHandler_CustomClientIPHonored exercises the ClientIP hook.
func TestReportHandler_CustomClientIPHonored(t *testing.T) {
	var observed string
	h := ReportHandler(ReportConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClientIP: func(r *http.Request) string {
			observed = r.Header.Get("X-Custom-Client")
			return observed
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/_/csp-report", strings.NewReader(legacyReportBody))
	req.Header.Set("Content-Type", "application/csp-report")
	req.Header.Set("X-Custom-Client", "tenant-A")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rec.Code)
	}
	if observed != "tenant-A" {
		t.Errorf("ClientIP hook not called as expected; observed=%q", observed)
	}
}

// TestReportHandler_DefaultsApply verifies the zero-value ReportConfig
// path: nil logger, nil counter, zero limits.
func TestReportHandler_DefaultsApply(t *testing.T) {
	// Replacing slog.Default keeps the default-Logger branch isolated
	// without leaking test output to other tests' loggers.
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	h := ReportHandler(ReportConfig{})
	rec := postReport(t, h, legacyReportBody, "application/csp-report", "5.4.3.2:1")
	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
}

// TestReportHandler_BodySizeBoundary exercises the boundary case where
// the body is exactly MaxBodyBytes. The body must succeed (off-by-one
// regressions in MaxBytesReader have happened before).
func TestReportHandler_BodySizeBoundary(t *testing.T) {
	h := ReportHandler(ReportConfig{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBodyBytes: int64(len(legacyReportBody)),
	})
	rec := postReport(t, h, legacyReportBody, "application/csp-report", "1.1.1.1:1")
	if rec.Code != http.StatusNoContent {
		t.Errorf("body == max: got %d, want 204", rec.Code)
	}
}

// TestReportHandler_AcceptsMultipleReportsInModernBody verifies the
// handler accepts an array containing several csp-violation reports
// plus unrelated report types (which must be silently skipped).
func TestReportHandler_AcceptsMultipleReportsInModernBody(t *testing.T) {
	body := `[
		{"type":"csp-violation","body":{"documentURL":"https://e/1","violatedDirective":"script-src","blockedURL":"https://x/a"}},
		{"type":"deprecation","body":{"id":"some-deprecation"}},
		{"type":"csp-violation","body":{"documentURL":"https://e/2","violatedDirective":"style-src","blockedURL":"https://x/b"}}
	]`
	counter := &recordingCounter{}
	h := ReportHandler(ReportConfig{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Counter: counter,
	})

	rec := postReport(t, h, body, "application/reports+json", "1.1.1.1:1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204\nbody: %s", rec.Code, rec.Body.String())
	}
	if got := len(counter.Calls()); got != 2 {
		t.Errorf("expected 2 csp-violation increments (deprecation entry skipped), got %d", got)
	}
}

// TestReportHandler_DropsEmptyIPRequestsThrough verifies that when the
// ClientIP hook returns "" we let the request through rather than
// piling all unidentified clients into one shared bucket.
func TestReportHandler_DropsEmptyIPRequestsThrough(t *testing.T) {
	h := ReportHandler(ReportConfig{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ClientIP:  func(r *http.Request) string { return "" },
		RateLimit: 1,
	})
	for i := 0; i < 5; i++ {
		rec := postReport(t, h, legacyReportBody, "application/csp-report", "10.0.0.1:1")
		if rec.Code != http.StatusNoContent {
			t.Errorf("iter %d unexpected status: %d", i, rec.Code)
		}
	}
}

// TestHostOf_HandlesEdgeCases pins the cardinality-control helper.
// "data:..." is a degenerate input — there's no real host, so we keep
// the URI prefix up to the first separator. The label still has bounded
// cardinality (one bucket per blocked-uri scheme), which is what we need.
func TestHostOf_HandlesEdgeCases(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"https://example.com":              "example.com",
		"https://example.com:8443":         "example.com",
		"https://user:pw@example.com/x":    "example.com",
		"https://example.com/foo?a=b#frag": "example.com",
		"data:image/png;base64,abc":        "data:image",
		"inline":                           "inline",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestExcerptHelper covers the cheap truncation helper used in log
// emission. Tiny but ensures coverage doesn't dip due to defensive
// branches.
func TestExcerptHelper(t *testing.T) {
	if got := excerpt("short", 10); got != "short" {
		t.Errorf("no-truncate path: %q", got)
	}
	if got := excerpt("xxxxxxxxxxxxxxxxxxxxxxxx", 4); got != "xxxx..." {
		t.Errorf("truncate path: %q", got)
	}
}

// TestParseReportBody_RejectsUnknownKind exercises the parser's
// fallthrough branch for an unrecognized kindHint value.
func TestParseReportBody_RejectsUnknownKind(t *testing.T) {
	if _, _, err := parseReportBody([]byte(legacyReportBody), "unknown"); err == nil {
		t.Errorf("unknown kind should error")
	}
}

// TestParseReportBody_AutoTriesFallback exercises the heuristic where
// an object body that fails the legacy decode is retried as modern.
func TestParseReportBody_AutoTriesFallback(t *testing.T) {
	body := `{"type":"csp-violation","body":{"documentURL":"https://e","violatedDirective":"script-src","blockedURL":"https://b"}}`
	reports, kind, err := parseReportBody([]byte(body), "auto")
	if err != nil {
		t.Fatalf("auto fallback error: %v", err)
	}
	if kind != "modern" {
		t.Errorf("expected modern, got %s", kind)
	}
	if len(reports) != 1 {
		t.Errorf("expected 1 report, got %d", len(reports))
	}
}

// TestDecodeIntField_StringForm verifies the helper that handles
// browsers (notably Firefox) which emit numeric fields as quoted
// strings.
func TestDecodeIntField_StringForm(t *testing.T) {
	got := decodeIntField(json.RawMessage(`"42"`))
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	if decodeIntField(nil) != 0 {
		t.Errorf("nil should yield 0")
	}
	if decodeIntField(json.RawMessage(`"not-a-number"`)) != 0 {
		t.Errorf("non-numeric string should yield 0")
	}
}

// TestDecodeStringOrIntField_Both verifies the helper accepts both
// JSON string and JSON number forms for status-code.
func TestDecodeStringOrIntField_Both(t *testing.T) {
	if got := decodeStringOrIntField(json.RawMessage(`"403"`)); got != "403" {
		t.Errorf("string form: %q", got)
	}
	if got := decodeStringOrIntField(json.RawMessage(`200`)); got != "200" {
		t.Errorf("number form: %q", got)
	}
	if got := decodeStringOrIntField(nil); got != "" {
		t.Errorf("nil form: %q", got)
	}
}

// TestNopCounter_NeverPanics covers the nop counter used as the
// default when ReportConfig.Counter is nil.
func TestNopCounter_NeverPanics(t *testing.T) {
	var n nopCounter
	n.Inc()
	n.Inc("a", "b")
}

// TestContentType_StripsParameters verifies the content-type parser
// strips charset etc. before the classifier match.
func TestContentType_StripsParameters(t *testing.T) {
	if got := contentType("application/csp-report; charset=utf-8"); got != "application/csp-report" {
		t.Errorf("got %q", got)
	}
	if got := contentType("  application/JSON ;x=y "); got != "application/json" {
		t.Errorf("got %q", got)
	}
}

// TestDefaultClientIP_FallbacksOK exercises the fallback paths of
// defaultClientIP — well-formed RemoteAddr without host:port, leftmost
// XFF stripping.
func TestDefaultClientIP_FallbacksOK(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "notavalidaddress"
	if got := defaultClientIP(r); got != "notavalidaddress" {
		t.Errorf("malformed RemoteAddr fallback: %q", got)
	}

	r = httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "  ,  ")
	if got := defaultClientIP(r); got != "10.0.0.1" {
		t.Errorf("XFF-with-blanks fallback: %q", got)
	}
}

// TestDecodeLegacyBody_HandlesMissingEffectiveDirective verifies the
// mirroring of violated-directive → effective-directive when the
// browser only sets the former.
func TestDecodeLegacyBody_HandlesMissingEffectiveDirective(t *testing.T) {
	body := `{"csp-report":{"document-uri":"https://x","blocked-uri":"https://y","violated-directive":"img-src"}}`
	reports, _, err := parseReportBody([]byte(body), "legacy")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 1 || reports[0].EffectiveDirective != "img-src" {
		t.Errorf("effective-directive mirror failed: %+v", reports)
	}
}

// TestDecodeModernBody_SingleObject verifies the parser accepts a
// single-object body (some intermediaries strip the array wrapper).
func TestDecodeModernBody_SingleObject(t *testing.T) {
	body := `{"type":"csp-violation","body":{"documentURL":"https://x","violatedDirective":"img-src","blockedURL":"https://y"}}`
	reports, kind, err := parseReportBody([]byte(body), "modern")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if kind != "modern" || len(reports) != 1 {
		t.Errorf("kind=%s reports=%+v", kind, reports)
	}
}

// TestDecodeModernBody_EmptyArrayErrors guarantees the modern decoder
// surfaces the "no reports" case as an error.
func TestDecodeModernBody_EmptyArrayErrors(t *testing.T) {
	if _, err := decodeModernBody([]byte("[]")); err == nil {
		t.Errorf("empty array should error")
	}
}

// TestDecodeLegacyBody_RawNumericNonInt covers the decodeIntField path
// where the raw bytes are technically present but unparseable.
func TestDecodeLegacyBody_RawNumericNonInt(t *testing.T) {
	body := `{"csp-report":{"document-uri":"https://x","blocked-uri":"https://y","violated-directive":"img-src","line-number":"abc","status-code":""}}`
	reports, _, err := parseReportBody([]byte(body), "legacy")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reports[0].LineNumber != 0 {
		t.Errorf("expected 0 for unparseable line-number, got %d", reports[0].LineNumber)
	}
}

// TestIPLimiter_OpportunisticGC pokes the opportunistic-GC branch to
// prove it doesn't deadlock and that the limiter still works after a
// GC sweep.
func TestIPLimiter_OpportunisticGC(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newIPLimiter(10, time.Second, func() time.Time { return now })
	for i := 0; i < 2000; i++ {
		l.allow(fmt.Sprintf("ip-%d", i))
	}
	// Advance the clock so all buckets are due for GC.
	now = now.Add(time.Hour)
	// One more call must trip the GC branch occasionally; loop until
	// the bucket count actually drops.
	for i := 0; i < 2000; i++ {
		l.allow(fmt.Sprintf("fresh-%d", i))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.state) == 0 {
		t.Errorf("limiter should still hold at least the fresh buckets")
	}
}
