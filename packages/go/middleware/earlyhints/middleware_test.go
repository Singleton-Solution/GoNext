package earlyhints

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordedInterim captures a single 1xx interim response observed by
// the test client's httptrace.
type recordedInterim struct {
	code   int
	header textproto.MIMEHeader
}

// runServerWithMiddleware spins up an httptest.Server fronting the
// given handler wrapped by Middleware(provider, opts), then performs
// one GET request and returns:
//   - the captured 1xx interim responses,
//   - the final response,
//   - the final body bytes (already drained).
//
// The test does NOT verify HTTP/2 specifically; httptest.Server runs
// HTTP/1.1 with chunked transfer encoding by default, which is fully
// adequate to exercise 103 framing.
func runServerWithMiddleware(
	t *testing.T,
	provider HintsProvider,
	opts Options,
	handler http.Handler,
) ([]recordedInterim, *http.Response, []byte) {
	t.Helper()
	mw := Middleware(provider, opts)
	ts := httptest.NewServer(mw(handler))
	t.Cleanup(ts.Close)

	var interim []recordedInterim
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			// Copy the header map — net/http reuses it after the
			// callback returns.
			hcopy := make(textproto.MIMEHeader, len(header))
			for k, v := range header {
				hcopy[k] = append([]string(nil), v...)
			}
			interim = append(interim, recordedInterim{code: code, header: hcopy})
			return nil
		},
	}
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("client do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return interim, resp, body
}

// helloHandler is the canonical inner handler the tests wrap.
var helloHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("hello"))
})

func TestMiddleware_Emits103WhenProviderHasHints(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/": {
			{URL: "/static/style.css", As: "style", FetchPriority: "high"},
			{URL: "/static/app.js", As: "script"},
		},
	})

	interim, resp, body := runServerWithMiddleware(t, provider, Options{}, helloHandler)

	if len(interim) != 1 {
		t.Fatalf("want 1 interim response, got %d", len(interim))
	}
	if interim[0].code != http.StatusEarlyHints {
		t.Errorf("interim code: got %d want %d", interim[0].code, http.StatusEarlyHints)
	}
	links := interim[0].header["Link"]
	if len(links) != 2 {
		t.Fatalf("want 2 Link headers on 103, got %d: %v", len(links), links)
	}
	if !strings.Contains(links[0], "/static/style.css") || !strings.Contains(links[0], "rel=preload") {
		t.Errorf("first Link malformed: %q", links[0])
	}
	if !strings.Contains(links[0], "fetchpriority=high") {
		t.Errorf("expected fetchpriority=high in: %q", links[0])
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status: got %d want 200", resp.StatusCode)
	}
	if got := string(body); got != "hello" {
		t.Errorf("body: got %q want %q", got, "hello")
	}
	// Default Options.KeepHeadersOnFinal is false → final response
	// MUST NOT echo the Link preloads.
	if vs := resp.Header.Values("Link"); len(vs) != 0 {
		t.Errorf("final response should not echo Link headers, got: %v", vs)
	}
}

func TestMiddleware_NoHints_No103(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/other": {{URL: "/x", As: "style"}},
	})

	interim, resp, body := runServerWithMiddleware(t, provider, Options{}, helloHandler)

	if len(interim) != 0 {
		t.Errorf("want 0 interim responses, got %d", len(interim))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status: got %d want 200", resp.StatusCode)
	}
	if string(body) != "hello" {
		t.Errorf("body: got %q", string(body))
	}
}

func TestMiddleware_ProviderError_FallsThrough(t *testing.T) {
	errProvider := HintsProviderFunc(func(r *http.Request) ([]Hint, error) {
		return nil, errors.New("boom")
	})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	interim, resp, body := runServerWithMiddleware(t, errProvider, Options{Logger: logger}, helloHandler)

	if len(interim) != 0 {
		t.Errorf("want 0 interim responses on provider error, got %d", len(interim))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status: got %d want 200", resp.StatusCode)
	}
	if string(body) != "hello" {
		t.Errorf("body: got %q", string(body))
	}
	// At least one WARN line should mention the provider failure.
	if !strings.Contains(buf.String(), "provider failed") {
		t.Errorf("expected WARN log mentioning provider failure; got:\n%s", buf.String())
	}
}

func TestMiddleware_NilProvider_Passthrough(t *testing.T) {
	mw := Middleware(nil, Options{})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("inner handler should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
}

func TestMiddleware_HTTP10_Skips103(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/": {{URL: "/x.css", As: "style"}},
	})
	var sentEarlyHints atomic.Bool
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We can't observe via the client (httptest.Server speaks
		// HTTP/1.1 always); instead, probe the request shape that
		// the middleware uses for its gating decision.
		if r.ProtoAtLeast(1, 1) {
			t.Errorf("test request advertised HTTP/1.1; cannot validate 1.0 gate")
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(HintsProviderFunc(func(r *http.Request) ([]Hint, error) {
		sentEarlyHints.Store(true)
		hints, _ := provider.HintsFor(r)
		return hints, nil
	}), Options{})

	// Build a synthetic HTTP/1.0 request via httptest.NewRequest and
	// invoke the handler directly. r.Proto = "HTTP/1.0" + ProtoMajor=1
	// + ProtoMinor=0 → ProtoAtLeast(1,1) is false → middleware MUST
	// skip the provider entirely.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Proto = "HTTP/1.0"
	req.ProtoMajor = 1
	req.ProtoMinor = 0

	rec := httptest.NewRecorder()
	mw(probe).ServeHTTP(rec, req)

	if sentEarlyHints.Load() {
		t.Error("middleware should not call provider for HTTP/1.0 requests")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("final status: got %d", rec.Code)
	}
}

func TestMiddleware_ConcurrentRequests(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/": {
			{URL: "/static/style.css", As: "style"},
			{URL: "/static/app.js", As: "script"},
		},
	})
	mw := Middleware(provider, Options{})
	ts := httptest.NewServer(mw(helloHandler))
	defer ts.Close()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)

	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			var got1xx int32
			trace := &httptrace.ClientTrace{
				Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
					if code == http.StatusEarlyHints {
						atomic.AddInt32(&got1xx, 1)
					}
					return nil
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = httptrace.WithClientTrace(ctx, trace)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/", nil)
			resp, err := ts.Client().Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("status %d", resp.StatusCode)
				return
			}
			if string(body) != "hello" {
				errs <- fmt.Errorf("body %q", string(body))
				return
			}
			if atomic.LoadInt32(&got1xx) != 1 {
				errs <- fmt.Errorf("got %d interim responses; want 1", got1xx)
				return
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent request: %v", err)
	}
}

func TestMiddleware_Disabled_NoOp(t *testing.T) {
	// "Disabled" at the config layer means the middleware is not
	// added to the chain. We simulate that by NOT wrapping the
	// handler at all and verifying the test plumbing emits no 103.
	interim, resp, _ := runServerWithMiddleware(t, nil, Options{}, helloHandler)
	if len(interim) != 0 {
		t.Errorf("want 0 interim responses when provider is nil, got %d", len(interim))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status: got %d", resp.StatusCode)
	}
}

func TestMiddleware_BudgetCap_DropsExcessHints(t *testing.T) {
	// 5 hints, each ~50 bytes. Cap to ~80 bytes → only the first one
	// fits.
	provider := NewStaticProvider(map[string][]Hint{
		"/": {
			{URL: "/a.css", As: "style"},
			{URL: "/b.css", As: "style"},
			{URL: "/c.css", As: "style"},
			{URL: "/d.css", As: "style"},
			{URL: "/e.css", As: "style"},
		},
	})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	interim, _, _ := runServerWithMiddleware(t, provider, Options{
		Logger:         logger,
		MaxHeaderBytes: 60,
	}, helloHandler)

	if len(interim) != 1 {
		t.Fatalf("want 1 interim, got %d", len(interim))
	}
	links := interim[0].header["Link"]
	if len(links) >= 5 {
		t.Errorf("expected budget to drop hints; got all %d links", len(links))
	}
	if !strings.Contains(buf.String(), "dropped hints over budget") {
		t.Errorf("expected WARN about dropped hints; got:\n%s", buf.String())
	}
}

func TestMiddleware_KeepHeadersOnFinal(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/": {{URL: "/a.css", As: "style"}},
	})
	_, resp, _ := runServerWithMiddleware(t, provider, Options{
		KeepHeadersOnFinal: true,
	}, helloHandler)
	links := resp.Header.Values("Link")
	if len(links) == 0 {
		t.Error("KeepHeadersOnFinal=true should preserve Link headers on the 200")
	}
}

func TestMiddleware_MaxHints_DropsExcess(t *testing.T) {
	provider := NewStaticProvider(map[string][]Hint{
		"/": {
			{URL: "/a.css", As: "style"},
			{URL: "/b.css", As: "style"},
			{URL: "/c.css", As: "style"},
		},
	})
	interim, _, _ := runServerWithMiddleware(t, provider, Options{
		MaxHints: 2,
	}, helloHandler)
	if len(interim) != 1 {
		t.Fatalf("want 1 interim, got %d", len(interim))
	}
	if got := len(interim[0].header["Link"]); got != 2 {
		t.Errorf("MaxHints=2: got %d Link headers, want 2", got)
	}
}
