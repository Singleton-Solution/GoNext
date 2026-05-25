package fakehost

import (
	"errors"
	"testing"
	"time"
)

func TestNew_Defaults(t *testing.T) {
	h := New()
	if got := h.Now(); !got.Equal(fixedEpoch) {
		t.Fatalf("New clock = %v, want %v", got, fixedEpoch)
	}
	if got := h.Events(); len(got) != 0 {
		t.Fatalf("New events len = %d, want 0", len(got))
	}
}

func TestWithSlug_AndClock(t *testing.T) {
	want := time.Date(2030, time.March, 4, 12, 0, 0, 0, time.UTC)
	h := New(WithSlug("seo"), WithClock(want))
	if got := h.Now(); !got.Equal(want) {
		t.Fatalf("clock = %v, want %v", got, want)
	}
	if h.slug != "seo" {
		t.Fatalf("slug = %q, want seo", h.slug)
	}
}

func TestAdvance(t *testing.T) {
	h := New()
	start := h.Now()
	got := h.Advance(7 * time.Second)
	if want := start.Add(7 * time.Second); !got.Equal(want) {
		t.Fatalf("Advance = %v, want %v", got, want)
	}
}

func TestKV_RoundTrip(t *testing.T) {
	h := New()
	if err := h.KVSet("a", []byte("hello")); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	got, err := h.KVGet("a")
	if err != nil {
		t.Fatalf("KVGet: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("KVGet = %q, want hello", got)
	}
	if err := h.KVDel("a"); err != nil {
		t.Fatalf("KVDel: %v", err)
	}
	_, err = h.KVGet("a")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("KVGet after del: %v, want ErrNotFound", err)
	}
}

func TestKV_Quota(t *testing.T) {
	h := New()
	h.SetKVQuota(8)
	if err := h.KVSet("a", []byte("xxxxxxxx")); err != nil { // exactly 8 bytes
		t.Fatalf("KVSet 8 bytes: %v", err)
	}
	if err := h.KVSet("b", []byte("y")); !errors.Is(err, ErrQuota) {
		t.Fatalf("KVSet over quota: %v, want ErrQuota", err)
	}
	// Overwriting an existing key with a smaller value should succeed.
	if err := h.KVSet("a", []byte("x")); err != nil {
		t.Fatalf("KVSet shrinking: %v", err)
	}
	if err := h.KVSet("b", []byte("yz")); err != nil {
		t.Fatalf("KVSet after shrink: %v", err)
	}
}

func TestKV_CapDenied(t *testing.T) {
	h := New()
	h.DisableCapability("kv")
	if err := h.KVSet("a", []byte("x")); !errors.Is(err, ErrDenied) {
		t.Fatalf("KVSet denied: %v", err)
	}
	if got := h.EventsOf(EventKVSet); len(got) != 1 {
		t.Fatalf("denied attempt should record: got %d events", len(got))
	}
}

func TestKVIncr(t *testing.T) {
	h := New()
	got, err := h.KVIncr("counter", 5)
	if err != nil {
		t.Fatalf("KVIncr fresh: %v", err)
	}
	if got != 5 {
		t.Fatalf("KVIncr fresh = %d, want 5", got)
	}
	got, _ = h.KVIncr("counter", -2)
	if got != 3 {
		t.Fatalf("KVIncr second = %d, want 3", got)
	}
}

func TestKVIncr_NonInt(t *testing.T) {
	h := New()
	_ = h.KVSet("a", []byte("not-a-number"))
	if _, err := h.KVIncr("a", 1); err == nil {
		t.Fatalf("KVIncr non-int: nil error, want failure")
	}
}

func TestDB_ReadWrite_Posts(t *testing.T) {
	h := New()
	id, err := h.DBWrite("posts", map[string]any{"title": "Hello"})
	if err != nil {
		t.Fatalf("DBWrite: %v", err)
	}
	if id == 0 {
		t.Fatalf("DBWrite returned zero ID")
	}
	rows, err := h.DBRead("posts", "select *")
	if err != nil {
		t.Fatalf("DBRead: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("DBRead rows = %d, want 1", len(rows))
	}
	if rows[0]["title"] != "Hello" {
		t.Fatalf("row[title] = %v, want Hello", rows[0]["title"])
	}
}

func TestDB_UnknownRelation(t *testing.T) {
	h := New()
	if _, err := h.DBRead("widgets", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DBRead unknown: %v", err)
	}
}

func TestHTTPFetch_Scripted(t *testing.T) {
	h := New()
	h.SetHTTPResponse("https://api.example/x", HTTPResponse{
		Status: 200,
		Body:   []byte("{}"),
	})
	got, err := h.HTTPFetch("GET", "https://api.example/x", nil, nil)
	if err != nil {
		t.Fatalf("HTTPFetch: %v", err)
	}
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
}

func TestHTTPFetch_NoFixture(t *testing.T) {
	h := New()
	if _, err := h.HTTPFetch("GET", "https://no.example", nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("HTTPFetch unmatched: %v", err)
	}
}

func TestSecrets_Hide_FromTrace(t *testing.T) {
	h := New()
	h.SetSecret("api_key", "supersecret")
	got, err := h.SecretsGet("api_key")
	if err != nil {
		t.Fatalf("SecretsGet: %v", err)
	}
	if got != "supersecret" {
		t.Fatalf("SecretsGet = %q", got)
	}
	for _, e := range h.EventsOf(EventSecretsGet) {
		// We must never record the raw value in Args/Result. The
		// recorded Result carries only {"hit": true}; the Args carry
		// only the name.
		if e.Args["value"] != nil {
			t.Fatalf("trace leaked secret value: %v", e)
		}
		if v, ok := e.Result.(map[string]any); ok {
			if _, isThere := v["value"]; isThere {
				t.Fatalf("trace leaked secret value via result: %v", v)
			}
		}
	}
}

func TestAuditEmit_RecordsTypeAndMetadata(t *testing.T) {
	h := New(WithSlug("seo"))
	if err := h.AuditEmit("plugin.activated", map[string]any{"version": "0.1.0"}); err != nil {
		t.Fatalf("AuditEmit: %v", err)
	}
	got := h.EventsOf(EventAuditEmit)
	if len(got) != 1 {
		t.Fatalf("audit events = %d", len(got))
	}
	if got[0].Args["event_type"] != "plugin.activated" {
		t.Fatalf("event_type = %v", got[0].Args["event_type"])
	}
	if got[0].Args["slug"] != "seo" {
		t.Fatalf("slug = %v", got[0].Args["slug"])
	}
}

func TestCronRegister(t *testing.T) {
	h := New()
	if err := h.CronRegister("recompute", "0 * * * *", "handle"); err != nil {
		t.Fatalf("CronRegister: %v", err)
	}
	got := h.EventsOf(EventCronRegister)
	if len(got) != 1 || got[0].Args["spec"] != "0 * * * *" {
		t.Fatalf("unexpected cron event: %+v", got)
	}
}

func TestLog_AndTime(t *testing.T) {
	h := New()
	h.Log(1, "hello")
	ms := h.TimeMS()
	if ms != fixedEpoch.UnixMilli() {
		t.Fatalf("TimeMS = %d, want %d", ms, fixedEpoch.UnixMilli())
	}
	if len(h.Events()) != 2 {
		t.Fatalf("expected 2 events, got %d", len(h.Events()))
	}
}

func TestI18N_DeterministicEnvelope(t *testing.T) {
	h := New()
	got := h.I18NTranslate("welcome", "fr")
	if got != "[t:fr:welcome]" {
		t.Fatalf("I18NTranslate = %q", got)
	}
}

func TestMetric_And_Event_And_Span(t *testing.T) {
	h := New()
	h.MetricObserve("requests", 1, map[string]string{"route": "/x"})
	h.EventEmit("post.saved", map[string]any{"id": 7})
	h.SpanEvent("step", map[string]any{"kind": "render"})
	if len(h.Events()) != 3 {
		t.Fatalf("expected 3 events, got %d", len(h.Events()))
	}
}

func TestResetEvents(t *testing.T) {
	h := New()
	h.Log(0, "a")
	h.Log(0, "b")
	if got := len(h.Events()); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	h.ResetEvents()
	if got := len(h.Events()); got != 0 {
		t.Fatalf("after reset = %d", got)
	}
}

func TestPostsRead_Write(t *testing.T) {
	h := New()
	id, err := h.PostsWrite(map[string]any{"title": "X"})
	if err != nil {
		t.Fatalf("PostsWrite: %v", err)
	}
	row, err := h.PostsRead(id)
	if err != nil {
		t.Fatalf("PostsRead: %v", err)
	}
	if row["title"] != "X" {
		t.Fatalf("title = %v", row["title"])
	}
}

func TestHTTPServe_Recorded(t *testing.T) {
	h := New()
	if err := h.HTTPServe("GET", "/webhook", "onWebhook"); err != nil {
		t.Fatalf("HTTPServe: %v", err)
	}
	got := h.EventsOf(EventHTTPServe)
	if len(got) != 1 || got[0].Args["path"] != "/webhook" {
		t.Fatalf("unexpected http.serve event: %+v", got)
	}
}

func TestCacheInvalidate(t *testing.T) {
	h := New()
	if err := h.CacheInvalidate([]string{"posts", "search"}); err != nil {
		t.Fatalf("CacheInvalidate: %v", err)
	}
	got := h.EventsOf(EventCacheInval)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	tags, ok := got[0].Args["tags"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "posts" {
		t.Fatalf("tags = %v", got[0].Args["tags"])
	}
}

func TestMediaRead_AndUsersRead(t *testing.T) {
	h := New()
	mid := h.SeedMedia(0, map[string]any{"url": "/m.png"})
	uid := h.SeedUser(0, map[string]any{"email": "x@y"})
	if _, err := h.MediaRead(mid); err != nil {
		t.Fatalf("MediaRead: %v", err)
	}
	if _, err := h.UsersRead(uid); err != nil {
		t.Fatalf("UsersRead: %v", err)
	}
}

func TestConcurrent_Safe(t *testing.T) {
	h := New()
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			h.Log(0, "concurrent")
			h.KVSet("k", []byte("v"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Both events fire per goroutine; we asserted no race. We also
	// observe a stable post-state.
	if got, _ := h.KVGet("k"); string(got) != "v" {
		t.Fatalf("after concurrent: %q", got)
	}
}
