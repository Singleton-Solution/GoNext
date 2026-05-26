package revalidate_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/webhooks/revalidate"
)

type stubHTTP struct {
	lastReq    *http.Request
	resp       *http.Response
	err        error
	callCount  int
	respBodies []string
}

func (s *stubHTTP) Do(req *http.Request) (*http.Response, error) {
	s.callCount++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		// Tests reuse the stub; rewrap a fresh Body so close-on-defer
		// in the client doesn't ruin the next call.
		if len(s.respBodies) > 0 {
			s.resp.Body = io.NopCloser(strings.NewReader(s.respBodies[0]))
			s.respBodies = s.respBodies[1:]
		} else {
			s.resp.Body = io.NopCloser(&bytes.Buffer{})
		}
		return s.resp, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(&bytes.Buffer{})}, nil
}

func TestClient_NoopWhenDisabled(t *testing.T) {
	c := revalidate.New("", "secret")
	if err := c.Notify(context.Background(), "/posts/x"); err != nil {
		t.Fatalf("disabled client should noop, got %v", err)
	}
	if c.Enabled() {
		t.Fatalf("expected Enabled()=false")
	}

	c2 := revalidate.New("https://example.com", "")
	if err := c2.Notify(context.Background(), "/posts/x"); err != nil {
		t.Fatalf("no-secret should noop, got %v", err)
	}
}

func TestClient_NoopOnEmptyPath(t *testing.T) {
	stub := &stubHTTP{}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))
	if err := c.Notify(context.Background(), ""); err != nil {
		t.Fatalf("empty path should noop, got %v", err)
	}
	if stub.callCount != 0 {
		t.Fatalf("expected no HTTP calls, got %d", stub.callCount)
	}
}

func TestClient_NotifyBuildsURL(t *testing.T) {
	stub := &stubHTTP{
		resp: &http.Response{StatusCode: http.StatusOK},
	}
	c := revalidate.New("https://example.com/", "topsecret", revalidate.WithHTTPClient(stub))

	if err := c.Notify(context.Background(), "/posts/hello-world"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if stub.callCount != 1 {
		t.Fatalf("expected 1 call, got %d", stub.callCount)
	}

	u := stub.lastReq.URL
	if u.Path != "/api/revalidate" {
		t.Fatalf("path: got %q, want /api/revalidate", u.Path)
	}
	q := u.Query()
	if q.Get("path") != "/posts/hello-world" {
		t.Fatalf("path query: got %q", q.Get("path"))
	}
	if q.Get("secret") != "topsecret" {
		t.Fatalf("secret query: got %q", q.Get("secret"))
	}
	if stub.lastReq.Method != http.MethodPost {
		t.Fatalf("method: got %q, want POST", stub.lastReq.Method)
	}
}

func TestClient_BaseURLTrailingSlashTrimmed(t *testing.T) {
	stub := &stubHTTP{
		resp: &http.Response{StatusCode: http.StatusOK},
	}
	c := revalidate.New("https://example.com/", "topsecret", revalidate.WithHTTPClient(stub))
	_ = c.Notify(context.Background(), "/")
	if !strings.HasPrefix(stub.lastReq.URL.String(), "https://example.com/api/revalidate?") {
		t.Fatalf("URL: got %q", stub.lastReq.URL.String())
	}
}

func TestClient_HTTPError(t *testing.T) {
	stub := &stubHTTP{err: errors.New("dial: no route to host")}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))

	err := c.Notify(context.Background(), "/x")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("error: %v", err)
	}
}

func TestClient_Non2xxIsUpstream(t *testing.T) {
	stub := &stubHTTP{
		resp: &http.Response{StatusCode: http.StatusUnauthorized},
	}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))

	err := c.Notify(context.Background(), "/x")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, revalidate.ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestClient_NotifyMany(t *testing.T) {
	stub := &stubHTTP{
		resp: &http.Response{StatusCode: http.StatusOK},
	}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))

	if err := c.NotifyMany(context.Background(), []string{"/", "/posts/x", ""}); err != nil {
		t.Fatalf("NotifyMany: %v", err)
	}
	if stub.callCount != 2 {
		t.Fatalf("expected 2 calls (empty skipped), got %d", stub.callCount)
	}
}

func TestClient_NotifyManyAggregatesErrors(t *testing.T) {
	stub := &stubHTTP{err: errors.New("transport down")}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))

	err := c.NotifyMany(context.Background(), []string{"/a", "/b"})
	if err == nil {
		t.Fatalf("expected error")
	}
	// errors.Join wraps both; check string contains both URLs' worth.
	if !strings.Contains(err.Error(), "transport down") {
		t.Fatalf("err: %v", err)
	}
}

func TestClient_NotifyManyDisabledNoop(t *testing.T) {
	c := revalidate.New("", "")
	if err := c.NotifyMany(context.Background(), []string{"/a", "/b"}); err != nil {
		t.Fatalf("disabled client should noop, got %v", err)
	}
}

func TestClient_PathWithSpecialChars(t *testing.T) {
	stub := &stubHTTP{
		resp: &http.Response{StatusCode: http.StatusOK},
	}
	c := revalidate.New("https://example.com", "topsecret", revalidate.WithHTTPClient(stub))

	if err := c.Notify(context.Background(), "/posts/hello world & friends"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	// Should be percent-encoded.
	parsed, _ := url.Parse(stub.lastReq.URL.String())
	if got := parsed.Query().Get("path"); got != "/posts/hello world & friends" {
		t.Fatalf("decoded path: %q", got)
	}
}
