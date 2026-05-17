package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gonext_log "github.com/Singleton-Solution/GoNext/packages/go/log"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	var seenID string
	h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = w.Header().Get(HeaderRequestID)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !validRequestID(seenID) {
		t.Errorf("generated request ID is invalid: %q", seenID)
	}
	if rec.Header().Get(HeaderRequestID) == "" {
		t.Error("response missing X-Request-Id header")
	}
}

func TestRequestID_HonorsValidIncoming(t *testing.T) {
	const incoming = "abcdef0123456789abcdef0123456789"
	var seenID string
	h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = w.Header().Get(HeaderRequestID)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, incoming)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seenID != incoming {
		t.Errorf("expected to honor incoming ID %q, got %q", incoming, seenID)
	}
}

func TestRequestID_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",                          // empty
		"short",                     // too short
		strings.Repeat("a", 65),     // too long
		"abc def 123 456 789 0",     // spaces
		"abcd\nefgh1234567890",      // newline (header smuggling)
		"abc;def;123;456;789;0123", // semicolons
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			var got string
			h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = w.Header().Get(HeaderRequestID)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(HeaderRequestID, bad)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got == bad {
				t.Errorf("malformed ID %q should have been replaced", bad)
			}
			if !validRequestID(got) {
				t.Errorf("replacement %q is not valid", got)
			}
		})
	}
}

func TestRequestID_AttachesToContextLogger(t *testing.T) {
	// The middleware should set the logger on r.Context() so handlers can
	// pull the request_id out via log.FromContext.
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	baseCtx := gonext_log.WithLogger(context.Background(), base)

	h := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gonext_log.FromContext(r.Context()).Info("inside")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(baseCtx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if rid, ok := got["request_id"].(string); !ok || rid == "" {
		t.Errorf("log line missing request_id: %v", got)
	}
}

func TestRecovery_RecoversFromPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	// Use the responseWriter wrapper so headersWritten works correctly.
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}
	h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if strings.Contains(string(body), "boom") {
		t.Errorf("response body leaked panic message: %q", body)
	}
	if !strings.Contains(buf.String(), "http handler panic") {
		t.Errorf("log line missing 'http handler panic': %s", buf.String())
	}
	if !strings.Contains(buf.String(), "stack") {
		t.Errorf("log line missing stack trace")
	}
}

func TestRecovery_PassesThroughNoPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	called := false
	h := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Error("inner handler not called")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status: got %d, want 418", rec.Code)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no log lines when no panic, got: %s", buf.String())
	}
}

func TestRecovery_PropagatesAbortHandler(t *testing.T) {
	// http.ErrAbortHandler is the documented signal for a deliberate abort
	// (e.g., used by Hijack failures). Recovery must re-panic so the http
	// server's own logic runs.
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	defer func() {
		rec := recover()
		if rec == nil {
			t.Error("expected ErrAbortHandler to propagate; recovered nothing")
		}
		if !errors.Is(rec.(error), http.ErrAbortHandler) {
			t.Errorf("expected ErrAbortHandler, got %v", rec)
		}
	}()
	h := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestLogger_EmitsRequestLine(t *testing.T) {
	var buf bytes.Buffer
	// Level=DEBUG so we capture 2xx responses (Logger emits 2xx at DEBUG).
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
		}),
		RequestID(),
		Logger(logger),
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	req.Header.Set("User-Agent", "test-ua/1.0")
	h.ServeHTTP(rec, req)

	// Find the "http request" line. Buffer may contain multiple lines; pick the right one.
	var line map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			continue
		}
		if m["msg"] == "http request" {
			line = m
			break
		}
	}
	if line == nil {
		t.Fatalf("no 'http request' log line emitted\nbuffer:\n%s", buf.String())
	}
	checks := map[string]any{
		"method":      "GET",
		"path":        "/foo",
		"status":      float64(200), // json numbers
		"bytes":       float64(5),
		"user_agent":  "test-ua/1.0",
	}
	for k, want := range checks {
		if line[k] != want {
			t.Errorf("field %q: got %v, want %v", k, line[k], want)
		}
	}
	if line["duration_ms"] == nil {
		t.Error("missing duration_ms")
	}
}

func TestLogger_SkipsConfiguredPaths(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Logger(logger, "/healthz")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if buf.Len() != 0 {
		t.Errorf("expected /healthz to be skipped, got log output: %s", buf.String())
	}
}

func TestLogger_LevelByStatus(t *testing.T) {
	cases := []struct {
		status    int
		wantLevel string
	}{
		{200, "DEBUG"},
		{404, "INFO"},
		{500, "WARN"},
		{503, "WARN"},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			var buf bytes.Buffer
			// Level=DEBUG so we capture every level.
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			h := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
			}))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, buf.String())
			}
			if line["level"] != c.wantLevel {
				t.Errorf("status %d: level got %v want %s", c.status, line["level"], c.wantLevel)
			}
		})
	}
}

func TestResponseWriter_CapturesStatusAndBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	_, _ = rw.Write([]byte("hello "))
	rw.WriteHeader(http.StatusCreated) // superfluous; should be ignored
	_, _ = rw.Write([]byte("world"))

	if rw.status != http.StatusOK { // implicit 200 from first Write
		t.Errorf("status: got %d, want 200", rw.status)
	}
	if rw.bytes != 11 {
		t.Errorf("bytes: got %d, want 11", rw.bytes)
	}
}

func TestValidRequestID(t *testing.T) {
	good := []string{
		"abcdef0123456789",
		strings.Repeat("a", 64),
		strings.Repeat("a", 16),
		"abc_def-123-456_abc",
	}
	bad := []string{
		"",
		"short",
		strings.Repeat("a", 65),
		"with space here xx",
		"\nnewline12345678901",
		"semi;colon123456789",
	}
	for _, s := range good {
		if !validRequestID(s) {
			t.Errorf("validRequestID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validRequestID(s) {
			t.Errorf("validRequestID(%q) = true, want false", s)
		}
	}
}
