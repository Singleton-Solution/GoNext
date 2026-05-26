package fragment

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// recordingOutbox is a test-only OutboxWriter that captures the
// (slug, tags) tuples handed to WriteInvalidations. The fragment
// package compiles without Postgres so we never hit the real outbox
// in unit tests — the worker integration test in invalidator/
// covers the SQL side.
type recordingOutbox struct {
	calls []recordedCall
	err   error
}

type recordedCall struct {
	slug string
	tags []string
}

func (r *recordingOutbox) WriteInvalidations(_ context.Context, slug string, tags []string) error {
	r.calls = append(r.calls, recordedCall{slug: slug, tags: append([]string(nil), tags...)})
	return r.err
}

// newTestCache spins up a miniredis-backed Cache + recording outbox
// for one test, registering t.Cleanup to release both. Returning the
// outbox lets the test inspect what Purge wrote.
func newTestCache(t *testing.T) (*Cache, *recordingOutbox) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	out := &recordingOutbox{}
	return New(rdb, out), out
}

// TestSetGet_HitAndMiss covers the most common path: a Set followed
// by a Get returns the value (hit), and a Get of an unwritten key
// returns false (miss).
func TestSetGet_HitAndMiss(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k1", []byte("hello"), []string{"tag-a"}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, hit, err := c.Get(ctx, "k1", []string{"tag-a"})
	if err != nil || !hit || !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("Get hit: got=%q hit=%v err=%v", got, hit, err)
	}

	if _, hit, err := c.Get(ctx, "absent", []string{"tag-a"}); err != nil || hit {
		t.Fatalf("Get miss: hit=%v err=%v", hit, err)
	}
}

// TestApplyInvalidation_DropsCachedEntry walks the whole invalidation
// loop: Set, invalidate-the-tag (the same INCR the worker will do),
// re-Get → miss. This is the core contract — invalidation is what
// makes the cache useful.
func TestApplyInvalidation_DropsCachedEntry(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(t)
	ctx := context.Background()

	tags := []string{"posts:42"}
	if err := c.Set(ctx, "render:hero", []byte("HTML"), tags, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, hit, _ := c.Get(ctx, "render:hero", tags); !hit {
		t.Fatal("expected hit before invalidation")
	}

	if err := c.ApplyInvalidation(ctx, "posts:42"); err != nil {
		t.Fatalf("ApplyInvalidation: %v", err)
	}
	if _, hit, _ := c.Get(ctx, "render:hero", tags); hit {
		t.Fatal("expected miss after invalidation")
	}
}

// TestPurge_WritesOutbox confirms Purge routes through the outbox
// writer rather than calling INCR directly. The invalidator worker is
// what actually fans the INCR out (so a multi-process deployment
// stays consistent); Purge must NOT short-circuit.
func TestPurge_WritesOutbox(t *testing.T) {
	t.Parallel()
	c, out := newTestCache(t)
	ctx := context.Background()

	if err := c.Purge(ctx, []string{"posts:42", "sitemap", "", "posts:42"}); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if len(out.calls) != 1 {
		t.Fatalf("expected 1 outbox call, got %d", len(out.calls))
	}
	got := out.calls[0]
	if got.slug != "gnf" {
		t.Errorf("slug: got %q, want %q", got.slug, "gnf")
	}
	if len(got.tags) != 2 || got.tags[0] != "posts:42" || got.tags[1] != "sitemap" {
		t.Errorf("tags: got %v, want [posts:42 sitemap] (de-duped, empties stripped)", got.tags)
	}
}

// TestPurge_NilOutboxReturnsError asserts that constructing a Cache
// without an outbox is allowed (for read-only tests) but Purge fails
// loudly rather than silently no-op. Silent no-op would be a real
// data-consistency bug.
func TestPurge_NilOutboxReturnsError(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c := New(rdb, nil)

	if err := c.Purge(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected error from Purge with nil outbox")
	}
}

// TestSet_Bounds covers the two size guards: value-too-large and
// tag-count-too-large. Both must surface a typed error so callers can
// react (e.g. fall back to a non-cached render).
func TestSet_Bounds(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(t)
	ctx := context.Background()

	tooBig := make([]byte, MaxValueBytes+1)
	if err := c.Set(ctx, "k", tooBig, nil, time.Minute); !errors.Is(err, ErrValueTooLarge) {
		t.Errorf("oversized value: got %v, want ErrValueTooLarge", err)
	}

	tooManyTags := make([]string, MaxTagsPerEntry+1)
	for i := range tooManyTags {
		tooManyTags[i] = "t"
	}
	if err := c.Set(ctx, "k", []byte("x"), tooManyTags, time.Minute); !errors.Is(err, ErrTooManyTags) {
		t.Errorf("too many tags: got %v, want ErrTooManyTags", err)
	}
}

// TestGet_TagSetDriftIsMiss makes sure a Get that supplies a
// different tag-set length than the original Set is treated as a
// miss. A caller who grew its tag set across deploys would otherwise
// see a stale payload validated against an obsolete tag list.
func TestGet_TagSetDriftIsMiss(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("v"), []string{"a"}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, hit, _ := c.Get(ctx, "k", []string{"a", "b"}); hit {
		t.Fatal("expected miss after tag-set drift")
	}
}

// TestEncodeDecode_RoundTrip pins the on-wire layout. A future format
// bump must keep this test green or change it deliberately.
func TestEncodeDecode_RoundTrip(t *testing.T) {
	t.Parallel()
	versions := []int64{0, 1, 9_223_372_036_854_775_807}
	value := []byte("rendered HTML")
	enc := encodeEntry(versions, value)
	gotV, gotVal, ok := decodeEntry(enc)
	if !ok {
		t.Fatal("decodeEntry: ok=false")
	}
	if !bytes.Equal(gotVal, value) {
		t.Errorf("value: got %q want %q", gotVal, value)
	}
	if len(gotV) != len(versions) {
		t.Fatalf("versions: got %d entries want %d", len(gotV), len(versions))
	}
	for i := range versions {
		if gotV[i] != versions[i] {
			t.Errorf("versions[%d]: got %d want %d", i, gotV[i], versions[i])
		}
	}
}

// TestDecode_Malformed treats truncated headers and over-claimed tag
// counts as misses rather than errors. Same shape as the live-system
// behaviour described in the Get docstring.
func TestDecode_Malformed(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		nil,
		{0x00, 0x00},                                                                 // truncated header
		{0xFF, 0xFF, 0xFF, 0x7F},                                                     // declared tag count beyond MaxTagsPerEntry
		{0x05, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0}, // claims 5 versions, body shorter
	}
	for i, raw := range cases {
		if _, _, ok := decodeEntry(raw); ok {
			t.Errorf("case %d: expected ok=false", i)
		}
	}
}
