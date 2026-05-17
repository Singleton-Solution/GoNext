package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedTime returns a clock function pinned at the given moment. Used
// in tests so X-GoNext-Timestamp and the signature are reproducible.
func fixedTime(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

// secret returns a SecretResolver that resolves any ID to the same key.
func secret(key string) SecretResolver {
	return SecretResolverFunc(func(context.Context, string) ([]byte, error) {
		return []byte(key), nil
	})
}

// fakeIdempotency is a thread-safe in-memory store mirroring the
// Redis SET NX EX semantics. Claim returns true on the first call for a
// key and false on every subsequent call (TTL is ignored — tests don't
// care).
type fakeIdempotency struct {
	mu   sync.Mutex
	seen map[string]struct{}
	// failErr forces every Claim call to return this error.
	failErr error
}

func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{seen: map[string]struct{}{}}
}

func (f *fakeIdempotency) Claim(_ context.Context, key string, _ time.Duration) (bool, error) {
	if f.failErr != nil {
		return false, f.failErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seen[key]; ok {
		return false, nil
	}
	f.seen[key] = struct{}{}
	return true, nil
}

// captureSub records every MarkDegraded call.
type captureSub struct {
	mu    sync.Mutex
	calls []struct{ id, reason string }
}

func (c *captureSub) MarkDegraded(_ context.Context, id, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, struct{ id, reason string }{id, reason})
	return nil
}

func (c *captureSub) snapshot() []struct{ id, reason string } {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]struct{ id, reason string }, len(c.calls))
	copy(out, c.calls)
	return out
}

type captureAudit struct {
	mu    sync.Mutex
	calls []DeadletterEvent
}

func (c *captureAudit) RecordDeadletter(_ context.Context, e DeadletterEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, e)
	return nil
}

func (c *captureAudit) snapshot() []DeadletterEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]DeadletterEvent, len(c.calls))
	copy(out, c.calls)
	return out
}

// newTestDeliverer builds a Deliverer wired up for a unit test against
// an httptest.Server. It uses a deterministic clock, a counting delivery
// ID, no jitter, a fresh idempotency store, and capture sinks for the
// dead-letter pipeline.
func newTestDeliverer(t *testing.T, srv *httptest.Server, key string) (*Deliverer, *captureAudit, *captureSub, *fakeIdempotency) {
	t.Helper()
	ca := &captureAudit{}
	cs := &captureSub{}
	idem := newFakeIdempotency()
	var idCounter atomic.Int64
	d := New(
		WithHTTPClient(srv.Client()),
		WithSecretResolver(secret(key)),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithIdempotency(idem, time.Hour),
		WithAuditRecorder(ca),
		WithSubscriptions(cs),
		WithClock(fixedTime(time.Unix(1_700_000_000, 0))),
		WithDeliveryIDFactory(func() string {
			return fmt.Sprintf("dlv-%d", idCounter.Add(1))
		}),
		WithAllowedSchemes("http", "https"), // httptest is http://
	)
	return d, ca, cs, idem
}

// TestDeliver_Success2xxNoRetry: a 2xx response yields one attempt and
// no retry. The dead-letter pipeline is not invoked.
func TestDeliver_Success2xxNoRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	d, ca, cs, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s_1", EventID: "e_1", Body: []byte(`{"x":1}`), Attempt: 1,
	}, Subscription{ID: "s_1", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusSuccess {
		t.Fatalf("Status = %v, want Success; err=%v", res.Status, res.Err)
	}
	if res.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus = %d", res.HTTPStatus)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
	if len(ca.snapshot()) != 0 || len(cs.snapshot()) != 0 {
		t.Fatalf("deadletter pipeline must not fire on success")
	}
}

// TestDeliver_Sets_Headers_And_Signature verifies the request shape
// matches the wire-format docs: signed body, all reserved headers, and
// a constant Event-Id across attempts.
func TestDeliver_SetsHeadersAndSignature(t *testing.T) {
	type captured struct {
		method     string
		path       string
		signature  string
		eventID    string
		deliveryID string
		ts         string
		eventType  string
		subID      string
		attempt    string
		ua         string
		ct         string
		body       []byte
		custom     string
	}
	var got captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = captured{
			method:     r.Method,
			path:       r.URL.Path,
			signature:  r.Header.Get(SignatureHeader),
			eventID:    r.Header.Get(EventIDHeader),
			deliveryID: r.Header.Get(DeliveryIDHeader),
			ts:         r.Header.Get(TimestampHeader),
			eventType:  r.Header.Get(EventTypeHeader),
			subID:      r.Header.Get(SubscriptionIDHeader),
			attempt:    r.Header.Get(AttemptHeader),
			ua:         r.Header.Get("User-Agent"),
			ct:         r.Header.Get("Content-Type"),
			body:       body,
			custom:     r.Header.Get("X-Tenant"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	d, _, _, _ := newTestDeliverer(t, srv, "the-key")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "sub_42",
		EventID:        "evt_abc",
		EventType:      "post.published",
		Body:           []byte(`{"id":1}`),
		Attempt:        3,
		Headers: map[string]string{
			"X-Tenant":      "acme",
			"Content-Type": "text/plain", // must be ignored — reserved
		},
	}, Subscription{ID: "sub_42", URL: srv.URL + "/hook", SecretID: "ref"})

	if res.Status != StatusSuccess {
		t.Fatalf("Status = %v; err=%v", res.Status, res.Err)
	}
	if got.method != "POST" {
		t.Fatalf("method = %q, want POST", got.method)
	}
	if got.path != "/hook" {
		t.Fatalf("path = %q", got.path)
	}
	if got.eventID != "evt_abc" || got.subID != "sub_42" {
		t.Fatalf("event/sub headers wrong: %+v", got)
	}
	if got.eventType != "post.published" {
		t.Fatalf("event type header = %q", got.eventType)
	}
	if got.deliveryID != "dlv-1" {
		t.Fatalf("delivery id = %q, want dlv-1", got.deliveryID)
	}
	if got.attempt != "3" {
		t.Fatalf("attempt header = %q", got.attempt)
	}
	if got.ct != "application/json" {
		t.Fatalf("Content-Type = %q, custom override should not win", got.ct)
	}
	if got.custom != "acme" {
		t.Fatalf("custom header X-Tenant lost: %q", got.custom)
	}
	if got.ts != "1700000000" {
		t.Fatalf("ts header = %q", got.ts)
	}
	if !strings.HasPrefix(got.ua, "GoNext-Webhooks/") {
		t.Fatalf("UA = %q", got.ua)
	}

	// Cross-check: the signature must verify against the body the
	// server saw, with the test secret and clock.
	err := Verify([]byte("the-key"), got.body, got.signature, time.Unix(1_700_000_000, 0), 0)
	if err != nil {
		t.Fatalf("server-side signature verify: %v", err)
	}
}

// TestDeliver_5xxRetried: a 500 response yields StatusRetry with the
// expected NextDelay from the schedule, no dead-letter (yet).
func TestDeliver_5xxRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d, ca, cs, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_5xx", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusRetry {
		t.Fatalf("Status = %v, want Retry; err=%v", res.Status, res.Err)
	}
	if res.HTTPStatus != 500 {
		t.Fatalf("HTTPStatus = %d", res.HTTPStatus)
	}
	if res.NextDelay != 30*time.Second {
		t.Fatalf("NextDelay = %v, want 30s (first slot)", res.NextDelay)
	}
	if !errors.Is(res.Err, ErrTransient) {
		t.Fatalf("Err = %v, want wrap of ErrTransient", res.Err)
	}
	if len(ca.snapshot()) != 0 || len(cs.snapshot()) != 0 {
		t.Fatal("transient retry must not fire deadletter")
	}
}

// TestDeliver_4xxImmediateDeadletter: a 400 sends straight to
// dead-letter without burning the retry schedule.
func TestDeliver_4xxImmediateDeadletter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	d, ca, cs, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_400", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusDeadletter {
		t.Fatalf("Status = %v, want Deadletter; err=%v", res.Status, res.Err)
	}
	if res.HTTPStatus != 400 {
		t.Fatalf("HTTPStatus = %d", res.HTTPStatus)
	}
	if !errors.Is(res.Err, ErrPermanent) {
		t.Fatalf("Err = %v, want wrap of ErrPermanent", res.Err)
	}
	audits := ca.snapshot()
	subs := cs.snapshot()
	if len(audits) != 1 || audits[0].Reason != ReasonPermanent4xx {
		t.Fatalf("audit calls: %+v", audits)
	}
	if len(subs) != 1 || subs[0].reason != ReasonPermanent4xx {
		t.Fatalf("subs calls: %+v", subs)
	}
}

// TestDeliver_408_429AreRetried: 408 Request Timeout and 429 Too Many
// Requests are explicitly retried (per docs/12-jobs-cron.md §14.4).
func TestDeliver_408And429Retried(t *testing.T) {
	for _, code := range []int{408, 429} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()
			d, _, _, _ := newTestDeliverer(t, srv, "k")
			res := d.Deliver(context.Background(), Payload{
				SubscriptionID: "s", EventID: "e_" + strconv.Itoa(code), Body: []byte("{}"), Attempt: 1,
			}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})
			if res.Status != StatusRetry {
				t.Fatalf("status %d should retry, got %v", code, res.Status)
			}
		})
	}
}

// TestDeliver_410GoneDeadlettersWithURLGoneReason. Per the doc 12 §14.4
// contract, a 410 is "the subscriber's URL is dead, do not try this URL
// again".
func TestDeliver_410GoneDeadlettersURLGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	d, ca, cs, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_410", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusDeadletter {
		t.Fatalf("status = %v", res.Status)
	}
	audits := ca.snapshot()
	if len(audits) != 1 || audits[0].Reason != ReasonURLGone {
		t.Fatalf("expected url_gone audit, got %+v", audits)
	}
	if cs.snapshot()[0].reason != ReasonURLGone {
		t.Fatalf("expected url_gone sub call")
	}
}

// TestDeliver_TimeoutRetried: a subscriber that holds the request past
// the client timeout produces a Retry classification.
func TestDeliver_TimeoutRetried(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(hold)

	// A custom client with a tiny timeout — same hardened defaults
	// otherwise.
	c := newHTTPClient(ClientConfig{RequestTimeout: 50 * time.Millisecond, ConnectTimeout: 50 * time.Millisecond})
	d := New(
		WithHTTPClient(c),
		WithSecretResolver(secret("k")),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithClock(fixedTime(time.Unix(1, 0))),
		WithDeliveryIDFactory(func() string { return "dlv-x" }),
		WithAllowedSchemes("http", "https"),
	)
	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_t", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusRetry {
		t.Fatalf("Status = %v, want Retry; err=%v", res.Status, res.Err)
	}
	if !errors.Is(res.Err, ErrTransient) {
		t.Fatalf("Err = %v, want ErrTransient", res.Err)
	}
}

// TestDeliver_NetworkErrorRetried: a server that closes the connection
// mid-handshake (we point at a closed port) produces a retry.
func TestDeliver_NetworkErrorRetried(t *testing.T) {
	// Bind a listener, get the port, close it. Nothing answers there.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	d := New(
		WithSecretResolver(secret("k")),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithClock(fixedTime(time.Unix(1, 0))),
		WithDeliveryIDFactory(func() string { return "dlv-x" }),
		WithClientConfig(ClientConfig{ConnectTimeout: 200 * time.Millisecond, RequestTimeout: 500 * time.Millisecond}),
		WithAllowedSchemes("http", "https"),
	)
	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_net", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: "http://" + addr, SecretID: "ref"})

	if res.Status != StatusRetry {
		t.Fatalf("Status = %v, want Retry; err=%v", res.Status, res.Err)
	}
	if res.HTTPStatus != 0 {
		t.Fatalf("HTTPStatus = %d, want 0 on transport failure", res.HTTPStatus)
	}
}

// TestDeliver_IdempotencyPreventsDoubleDelivery: a re-enqueued task
// (same SubscriptionID+EventID) is a no-op — the underlying HTTP
// handler is not hit a second time.
func TestDeliver_IdempotencyPreventsDoubleDelivery(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	d, _, _, _ := newTestDeliverer(t, srv, "k")

	p := Payload{SubscriptionID: "s", EventID: "e_once", Body: []byte("{}"), Attempt: 1}
	sub := Subscription{ID: "s", URL: srv.URL, SecretID: "ref"}

	res1 := d.Deliver(context.Background(), p, sub)
	if res1.Status != StatusSuccess {
		t.Fatalf("first delivery: %v", res1.Status)
	}
	res2 := d.Deliver(context.Background(), p, sub)
	if res2.Status != StatusSuccess {
		t.Fatalf("second delivery should be success-as-noop, got %v", res2.Status)
	}
	if hits.Load() != 1 {
		t.Fatalf("handler hit %d times, want 1", hits.Load())
	}
}

// TestDeliver_ScheduleExhaustionTriggersDeadletter exercises the
// "after 6 failures, mark subscription degraded" requirement from
// issue #266. We drive Deliver with attempt=7 (i.e. the 7th attempt,
// past the 6-element retry schedule) on a 5xx response, and assert the
// deadletter pipeline ran with ReasonScheduleExhausted.
func TestDeliver_ScheduleExhaustionTriggersDeadletter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	d, ca, cs, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_exh", Body: []byte("{}"), Attempt: MaxDeliveryAttempts,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusDeadletter {
		t.Fatalf("attempt %d 5xx must deadletter, got %v", MaxDeliveryAttempts, res.Status)
	}
	if !errors.Is(res.Err, ErrExhausted) {
		t.Fatalf("Err = %v, want wrap of ErrExhausted", res.Err)
	}
	audits := ca.snapshot()
	subs := cs.snapshot()
	if len(audits) != 1 || audits[0].Reason != ReasonScheduleExhausted {
		t.Fatalf("audit: %+v", audits)
	}
	if len(subs) != 1 || subs[0].reason != ReasonScheduleExhausted {
		t.Fatalf("subs: %+v", subs)
	}
	if audits[0].SubscriptionID != "s" || audits[0].EventID != "e_exh" || audits[0].LastStatus != 502 {
		t.Fatalf("audit fields wrong: %+v", audits[0])
	}
}

// TestDeliver_RejectsRedirect: a 301 with a Location header is a
// permanent deadletter — the deliverer refuses to follow. We use the
// production http.Client (which carries our CheckRedirect) but swap
// its transport with the test server's so the connection is local.
func TestDeliver_RejectsRedirect(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	client := newHTTPClient(ClientConfig{})
	// Use the test server's transport so we don't actually hit DNS.
	client.Transport = srv.Client().Transport

	ca := &captureAudit{}
	cs := &captureSub{}
	d := New(
		WithHTTPClient(client),
		WithSecretResolver(secret("k")),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithAuditRecorder(ca),
		WithSubscriptions(cs),
		WithClock(fixedTime(time.Unix(1, 0))),
		WithDeliveryIDFactory(func() string { return "dlv-x" }),
		WithAllowedSchemes("http", "https"),
	)

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_redir", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusDeadletter {
		t.Fatalf("redirect should deadletter, got %v; err=%v", res.Status, res.Err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1 (no follow)", hits.Load())
	}
	if got := ca.snapshot(); len(got) == 0 || got[0].Reason != ReasonRedirectRejected {
		t.Fatalf("audit not recorded with redirect reason: %+v", got)
	}
}

// TestDeliver_RetryAfterHonoredWhenLongerThanSchedule. When a 429
// includes Retry-After larger than the next schedule slot, we wait the
// longer.
func TestDeliver_RetryAfterHonored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	d, _, _, _ := newTestDeliverer(t, srv, "k")

	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e_429", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusRetry {
		t.Fatalf("status = %v", res.Status)
	}
	// Slot 1 = 30s; Retry-After said 120s; we take the larger.
	if res.NextDelay != 120*time.Second {
		t.Fatalf("NextDelay = %v, want 120s (Retry-After wins)", res.NextDelay)
	}
}

// TestDeliver_InvalidPayloadIsDeadletter: missing required fields go
// straight to deadletter — never retry a poison payload.
func TestDeliver_InvalidPayload(t *testing.T) {
	d := New(
		WithSecretResolver(secret("k")),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithAllowedSchemes("http", "https"),
	)
	cases := []Payload{
		{SubscriptionID: "", EventID: "e", Body: []byte("{}")},
		{SubscriptionID: "s", EventID: "", Body: []byte("{}")},
		{SubscriptionID: "s", EventID: "e", Body: nil},
	}
	for i, p := range cases {
		res := d.Deliver(context.Background(), p, Subscription{ID: "s", URL: "https://x", SecretID: "ref"})
		if res.Status != StatusDeadletter {
			t.Fatalf("case %d: %v", i, res.Status)
		}
		if !errors.Is(res.Err, ErrInvalidPayload) {
			t.Fatalf("case %d: %v", i, res.Err)
		}
	}
}

// TestDeliver_DisallowedScheme rejects http:// when only https is in
// the allowlist. Production default.
func TestDeliver_DisallowedScheme(t *testing.T) {
	d := New(
		WithSecretResolver(secret("k")),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		// Don't override allowedSchemes — default is {"https"}.
	)
	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: "http://example.test", SecretID: "ref"})

	if res.Status != StatusDeadletter {
		t.Fatalf("http URL should deadletter with default schemes, got %v", res.Status)
	}
	if !errors.Is(res.Err, ErrInvalidSubscription) {
		t.Fatalf("Err = %v, want ErrInvalidSubscription", res.Err)
	}
}

// TestDeliver_IdempotencyClaimErrorIsRetry: if the idem store is down,
// we'd rather risk a retry than double-deliver.
func TestDeliver_IdempotencyClaimErrorIsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	idem := newFakeIdempotency()
	idem.failErr = errors.New("redis down")
	d := New(
		WithHTTPClient(srv.Client()),
		WithSecretResolver(secret("k")),
		WithIdempotency(idem, time.Hour),
		WithScheduler(NewSchedule(nil).WithoutJitter()),
		WithClock(fixedTime(time.Unix(1, 0))),
		WithDeliveryIDFactory(func() string { return "dlv-x" }),
		WithAllowedSchemes("http", "https"),
	)
	res := d.Deliver(context.Background(), Payload{
		SubscriptionID: "s", EventID: "e", Body: []byte("{}"), Attempt: 1,
	}, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})

	if res.Status != StatusRetry {
		t.Fatalf("idem store err should yield retry, got %v", res.Status)
	}
	if !errors.Is(res.Err, ErrTransient) {
		t.Fatalf("Err = %v, want ErrTransient", res.Err)
	}
}

// TestDeliver_DeadletterFields verifies the audit row carries the
// fields the admin UI needs to render the failure view.
func TestDeliver_DeadletterFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	d, ca, _, _ := newTestDeliverer(t, srv, "k")

	_ = d.Deliver(context.Background(), Payload{
		SubscriptionID: "sub_fields", EventID: "evt_fields", EventType: "user.created",
		Body: []byte("{}"), Attempt: 4,
	}, Subscription{ID: "sub_fields", URL: srv.URL + "/h", SecretID: "ref"})

	audits := ca.snapshot()
	if len(audits) != 1 {
		t.Fatalf("len audits = %d", len(audits))
	}
	a := audits[0]
	if a.SubscriptionID != "sub_fields" || a.EventID != "evt_fields" || a.EventType != "user.created" {
		t.Fatalf("audit identity wrong: %+v", a)
	}
	if a.URL != srv.URL+"/h" {
		t.Fatalf("audit URL = %q", a.URL)
	}
	if a.Attempts != 4 || a.LastStatus != 403 {
		t.Fatalf("audit counters wrong: attempts=%d status=%d", a.Attempts, a.LastStatus)
	}
	if a.OccurredAt.IsZero() {
		t.Fatalf("OccurredAt unset")
	}
}

// TestDeliver_ConcurrentSafe: many goroutines hitting Deliver against
// the same Deliverer + httptest.Server must produce one HTTP hit per
// unique event ID.
func TestDeliver_ConcurrentSafe(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	d, _, _, _ := newTestDeliverer(t, srv, "k")

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			p := Payload{
				SubscriptionID: "s",
				EventID:        fmt.Sprintf("e_%d", i),
				Body:           []byte("{}"),
				Attempt:        1,
			}
			res := d.Deliver(context.Background(), p, Subscription{ID: "s", URL: srv.URL, SecretID: "ref"})
			if res.Status != StatusSuccess {
				t.Errorf("goroutine %d: %v", i, res.Status)
			}
		}(i)
	}
	wg.Wait()
	if got := hits.Load(); got != N {
		t.Fatalf("hits = %d, want %d", got, N)
	}
}

// TestNew_DefaultUsesProductionSchedule is a doc-as-test: a Deliverer
// built with no options uses DefaultRetrySchedule.
func TestNew_DefaultUsesProductionSchedule(t *testing.T) {
	d := New()
	s, ok := d.scheduler.(*Schedule)
	if !ok {
		t.Fatalf("default scheduler not *Schedule: %T", d.scheduler)
	}
	got := s.Delays()
	if len(got) != len(DefaultRetrySchedule) {
		t.Fatalf("len = %d, want %d", len(got), len(DefaultRetrySchedule))
	}
	for i, v := range got {
		if v != DefaultRetrySchedule[i] {
			t.Fatalf("slot %d: %v != %v", i, v, DefaultRetrySchedule[i])
		}
	}
}
