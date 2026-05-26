package fragment

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyPrefix is the Redis key prefix used for cached fragment payloads.
// Exported so subscribers (e.g. the block-render cache invalidation
// listener) can pattern-match without re-deriving the constant.
const KeyPrefix = "gnf:f:"

// TagVersionPrefix is the Redis key prefix used for per-tag version
// counters. INCRing a key under this prefix is what makes every
// fragment that captured the old version stale on its next Get.
const TagVersionPrefix = "gnf:tv:"

// DefaultTTL is the time-to-live applied when Set is called with a
// non-positive ttl. We don't let callers store fragments forever —
// even with tag invalidation, an orphaned fragment (one whose tags
// no one is watching anymore) would sit in Redis indefinitely. One
// hour is a balance between "long enough to be useful" and "short
// enough that a forgotten tag drains out on its own".
const DefaultTTL = 1 * time.Hour

// MaxTagsPerEntry caps how many tags one Set may attach to a single
// fragment. The cap is defensive: every tag costs a Redis round-trip
// on Set (to read its current version) and on Get (to check whether
// the captured version still matches). 32 is comfortably larger than
// any of the project's documented use cases (a render touches a
// handful of taxonomies and the site-settings tag) and small enough
// that a misuse — a caller that fans out per-row tags — fails loudly.
const MaxTagsPerEntry = 32

// MaxValueBytes caps the payload size accepted by Set. Beyond ~1 MiB
// the Redis network round-trip stops being interesting (the renderer
// would do better to stream the source data directly), and a single
// 100 MiB blob can pin a Redis instance. 1 MiB matches the documented
// HTTP response budget for the public web.
const MaxValueBytes = 1 << 20

// ErrValueTooLarge is returned by Set when the supplied value exceeds
// MaxValueBytes. The caller should fall back to a non-cached render.
var ErrValueTooLarge = errors.New("fragment: value exceeds MaxValueBytes")

// ErrTooManyTags is returned by Set when the supplied tag list
// exceeds MaxTagsPerEntry. The caller should rethink its tag shape:
// per-row tags are an anti-pattern in this cache.
var ErrTooManyTags = errors.New("fragment: tag count exceeds MaxTagsPerEntry")

// Cache is the byte-fragment cache surface. One Cache instance binds
// to one Redis client and one outbox-writer; callers share the same
// instance across goroutines (Redis client and pgxpool are both
// concurrency-safe).
type Cache struct {
	rdb       *redis.Client
	outbox    OutboxWriter
	logger    *slog.Logger
	keyPrefix string
	tvPrefix  string
}

// OutboxWriter is the dependency Purge uses to record a tag
// invalidation. The concrete implementation in production is a thin
// pgxpool-backed inserter into cache_invalidations (see Writer in
// this package); tests pass a fake that records calls in memory.
//
// Keeping this an interface (rather than depending on pgxpool here)
// means the fragment package compiles without a Postgres driver —
// useful for unit tests, and matches the layering rule in
// packages/go/cache/invalidator (the worker imports pgx, the cache
// itself does not need to).
type OutboxWriter interface {
	// WriteInvalidations appends one row per tag into the
	// cache_invalidations outbox. The slug is the namespace the
	// invalidator worker re-prefixes when it publishes; for
	// fragment cache traffic the slug is always "gnf" so a
	// subscriber can route on a stable namespace.
	//
	// Returning an error from WriteInvalidations causes Purge to
	// return that error — the row is the durable record of the
	// invalidation, so a failed write is a real failure (the cache
	// has not been purged) and not a "best effort".
	WriteInvalidations(ctx context.Context, slug string, tags []string) error
}

// Option configures a Cache at construction time.
type Option func(*Cache)

// WithLogger swaps the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Cache) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithKeyPrefix overrides the Redis namespace used for payload keys.
// Mostly useful for tests that want to share a Redis instance across
// suites without colliding.
func WithKeyPrefix(prefix string) Option {
	return func(c *Cache) {
		if prefix != "" {
			c.keyPrefix = prefix
		}
	}
}

// WithTagVersionPrefix overrides the Redis namespace used for tag
// version counters. Mirror of WithKeyPrefix; both must be passed
// together when isolating a test from production keyspace.
func WithTagVersionPrefix(prefix string) Option {
	return func(c *Cache) {
		if prefix != "" {
			c.tvPrefix = prefix
		}
	}
}

// New constructs a Cache.
//
// rdb is required; passing nil panics — a fragment cache without a
// Redis client would silently no-op every Set and miss every Get,
// and that is more dangerous than a startup crash.
//
// outbox may be nil. A nil outbox means Purge returns an error
// (the cache still serves Gets and Sets); this is the right shape
// for "read-only" embeddings (an integration test that wants the
// hit/miss behaviour without spinning up Postgres).
func New(rdb *redis.Client, outbox OutboxWriter, opts ...Option) *Cache {
	if rdb == nil {
		panic("fragment.New: redis client is required")
	}
	c := &Cache{
		rdb:       rdb,
		outbox:    outbox,
		logger:    slog.Default(),
		keyPrefix: KeyPrefix,
		tvPrefix:  TagVersionPrefix,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Get looks up a cached fragment by key and returns its bytes when
// every captured tag version still matches the live version.
//
// The (value, true, nil) return is a fresh hit. A (nil, false, nil)
// return is a clean miss — either the key was absent, the entry was
// malformed (treated as a miss, not an error), or one of the captured
// tags has been invalidated since the Set. A non-nil error means
// Redis itself failed; the caller should fall back to a non-cached
// render rather than treat it as a miss (so a brief Redis outage
// doesn't quietly bypass the cache during a stampede).
//
// The tags argument is the SAME list the caller would pass to Set —
// Get re-checks the live versions against the captured ones stored
// alongside the payload. Mismatched tag-set between Set and Get
// produces a miss (we cannot prove the captured payload was built
// from the same dependencies). This lets a caller defensively grow
// or shrink its tag set across deploys without a poisoning hazard.
func (c *Cache) Get(ctx context.Context, key string, tags []string) ([]byte, bool, error) {
	if key == "" {
		return nil, false, errors.New("fragment: Get: key is required")
	}
	raw, err := c.rdb.Get(ctx, c.keyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("fragment: Get %q: %w", key, err)
	}
	captured, value, ok := decodeEntry(raw)
	if !ok {
		// Malformed entries can happen if a server with an older
		// schema wrote a key we now don't understand. Treat as a
		// miss; the next Set will rewrite the value with a current
		// schema. We log at debug because the situation is benign
		// during deploys but worth seeing under a magnifier.
		c.logger.Debug("fragment: malformed cached entry, treating as miss",
			slog.String("key", key))
		return nil, false, nil
	}
	if len(captured) != len(tags) {
		// Tag-set drift: the writer believed the value depended on
		// N tags; we now think it depends on M. We cannot prove
		// freshness, so we treat the entry as stale.
		return nil, false, nil
	}
	if len(tags) == 0 {
		// No tags to validate — the value is fresh by construction.
		return value, true, nil
	}
	live, err := c.readTagVersions(ctx, tags)
	if err != nil {
		return nil, false, fmt.Errorf("fragment: read tag versions: %w", err)
	}
	for i := range tags {
		if captured[i] != live[i] {
			return nil, false, nil
		}
	}
	return value, true, nil
}

// Set stores value under key with the given tags and ttl. The set of
// captured tag versions is encoded into the stored entry so a later
// Get can compare against the live versions and detect invalidation.
//
// A zero or negative ttl is replaced with DefaultTTL — fragments are
// never stored indefinitely (see the package comment).
//
// Set is best-effort durable: a Redis error is returned to the
// caller; on success, the entry is visible to subsequent Gets within
// one round-trip. Set does NOT update the cache_invalidations outbox
// — that is exclusively a Purge concern.
func (c *Cache) Set(ctx context.Context, key string, value []byte, tags []string, ttl time.Duration) error {
	if key == "" {
		return errors.New("fragment: Set: key is required")
	}
	if len(value) > MaxValueBytes {
		return fmt.Errorf("%w: %d > %d", ErrValueTooLarge, len(value), MaxValueBytes)
	}
	if len(tags) > MaxTagsPerEntry {
		return fmt.Errorf("%w: %d > %d", ErrTooManyTags, len(tags), MaxTagsPerEntry)
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	versions, err := c.readTagVersions(ctx, tags)
	if err != nil {
		return fmt.Errorf("fragment: Set: read tag versions: %w", err)
	}
	encoded := encodeEntry(versions, value)
	if err := c.rdb.Set(ctx, c.keyPrefix+key, encoded, ttl).Err(); err != nil {
		return fmt.Errorf("fragment: Set %q: %w", key, err)
	}
	return nil
}

// Purge appends one row per tag into the cache_invalidations outbox.
// The invalidator worker drains the table, INCRs gnf:tv:<tag>, and
// publishes a pub/sub message. Subsequent Gets whose captured tag
// version disagrees will return a miss.
//
// Purge is the ONLY supported way to evict fragments. A caller that
// wants to drop a single fragment should give it a tag whose only
// member is that fragment ("block:<id>") and Purge by that tag.
//
// Purge returns an error when no outbox writer is configured: a
// silent no-op here would be a real-world data-corruption bug (the
// caller would believe the invalidation succeeded). The expected
// production wiring always supplies an OutboxWriter; tests that
// want the no-write behaviour pass a recording fake instead of nil.
func (c *Cache) Purge(ctx context.Context, tags []string) error {
	if c.outbox == nil {
		return errors.New("fragment: Purge: no outbox writer configured")
	}
	if len(tags) == 0 {
		return nil
	}
	// Filter empties and de-duplicate. An empty tag would invalidate
	// the "no tags" entry (which has no version to bump) and is the
	// kind of typo we want to drop quietly rather than amplify.
	seen := make(map[string]struct{}, len(tags))
	filtered := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		filtered = append(filtered, t)
	}
	if len(filtered) == 0 {
		return nil
	}
	return c.outbox.WriteInvalidations(ctx, "gnf", filtered)
}

// ApplyInvalidation increments the version counter for one tag. This
// is the receiver side of the pub/sub message the invalidator worker
// publishes — when a Cache is wired into a pub/sub subscriber loop,
// each message triggers one call here.
//
// Idempotent in the "downstream impact" sense: a duplicate INCR moves
// the counter forward by two instead of one, but every captured-
// version comparison still resolves to "not equal", so the cache
// surface behaviour is identical. This is what makes at-least-once
// delivery from the invalidator safe to consume.
func (c *Cache) ApplyInvalidation(ctx context.Context, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	if err := c.rdb.Incr(ctx, c.tvPrefix+tag).Err(); err != nil {
		return fmt.Errorf("fragment: ApplyInvalidation %q: %w", tag, err)
	}
	return nil
}

// readTagVersions returns the current version int64 for each tag in
// the input slice. Missing keys read as 0 (the lazy-initialised
// value); Redis' INCR creates the key on first write, so we don't
// pre-seed.
//
// Uses a single pipelined MGET to keep the round-trip count at one
// regardless of tag-set size.
func (c *Cache) readTagVersions(ctx context.Context, tags []string) ([]int64, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	keys := make([]string, len(tags))
	for i, t := range tags {
		keys[i] = c.tvPrefix + t
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(tags))
	for i, v := range vals {
		switch s := v.(type) {
		case nil:
			out[i] = 0
		case string:
			// go-redis returns numbers as strings out of MGET; we
			// parse manually instead of pulling strconv to keep
			// the hot path tight. The version counter is the
			// product of INCR and is always a base-10 integer
			// representable in int64.
			var n int64
			for j := 0; j < len(s); j++ {
				c := s[j]
				if c < '0' || c > '9' {
					// Corrupted counter: treat as 0 so a stale
					// payload re-syncs on next Set. We don't
					// short-circuit to error because that would
					// poison the whole cache on one bad key.
					n = 0
					break
				}
				n = n*10 + int64(c-'0')
			}
			out[i] = n
		default:
			out[i] = 0
		}
	}
	return out, nil
}

// encodeEntry packs the captured tag versions and the payload into a
// single byte slice. Layout:
//
//	[ uint32 LE: tag count N ]
//	[ N × int64 LE: captured tag versions ]
//	[ value bytes ]
//
// Little-endian was chosen for fast decode on x86 / arm64; the encode
// is one make + N+1 PutUint64 calls, and the decode is symmetric.
// Versioning the format is future-proofed via the first byte: a
// caller reading a malformed entry treats it as a cache miss (see
// decodeEntry), so a format bump only needs to ensure the new
// encoding's count word is different from any legal old one. We do
// not bother with an explicit format byte today because there is
// only one format.
func encodeEntry(versions []int64, value []byte) []byte {
	const headerSize = 4
	const versionSize = 8
	out := make([]byte, headerSize+versionSize*len(versions)+len(value))
	binary.LittleEndian.PutUint32(out[0:headerSize], uint32(len(versions)))
	for i, v := range versions {
		off := headerSize + i*versionSize
		binary.LittleEndian.PutUint64(out[off:off+versionSize], uint64(v))
	}
	copy(out[headerSize+versionSize*len(versions):], value)
	return out
}

// decodeEntry is the inverse of encodeEntry. Returns the captured
// versions, the payload, and ok=true when the layout is well-formed.
// Layout violations (truncated header, declared count exceeds the
// remaining bytes) return ok=false; callers treat that as a miss.
func decodeEntry(raw []byte) (versions []int64, value []byte, ok bool) {
	const headerSize = 4
	const versionSize = 8
	if len(raw) < headerSize {
		return nil, nil, false
	}
	n := binary.LittleEndian.Uint32(raw[0:headerSize])
	// Guard against a malicious / corrupted header claiming a huge
	// version count. MaxTagsPerEntry is the upper bound at write
	// time so anything above it is a layout error.
	if n > MaxTagsPerEntry {
		return nil, nil, false
	}
	need := headerSize + int(n)*versionSize
	if len(raw) < need {
		return nil, nil, false
	}
	versions = make([]int64, n)
	for i := uint32(0); i < n; i++ {
		off := headerSize + int(i)*versionSize
		versions[i] = int64(binary.LittleEndian.Uint64(raw[off : off+versionSize]))
	}
	value = raw[need:]
	return versions, value, true
}
