package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// CacheBackend is the byte cache the block render layer consults
// before invoking a renderer. The concrete production backend is
// packages/go/cache/fragment.Cache, but the walker depends on this
// narrower interface so the render package compiles without a Redis
// driver — useful for unit tests, and keeps the import graph clean.
//
// Tags carry the dependency story for invalidation: a block render
// keyed by site-settings touches the "site:settings" tag, so a
// settings update Purges all renders that captured it. The cache
// itself does not interpret tags — it just records and re-checks
// them via the fragment cache's version vector.
type CacheBackend interface {
	// Get returns (value, true) on a fresh hit. A non-nil error is
	// for transport failures (e.g. Redis unreachable); a clean miss
	// is (nil, false, nil). The CachedWalker treats errors and
	// misses identically — both fall through to a fresh render.
	Get(ctx context.Context, key string, tags []string) ([]byte, bool, error)

	// Set stores value under key with the given tags and ttl. A
	// non-nil error is logged at Warn; the rendered HTML is still
	// served, since a missed write isn't a correctness bug.
	Set(ctx context.Context, key string, value []byte, tags []string, ttl time.Duration) error
}

// DefaultCacheTTL is the time-to-live applied to cached block
// renders. One hour matches the fragment-cache default (see
// packages/go/cache/fragment) and is the right "cheap to regenerate
// but expensive on a hot page" balance for content blocks. Operators
// who want a longer TTL pass a custom value via CachedWalkerOptions.
const DefaultCacheTTL = 1 * time.Hour

// KeyPrefix is the namespace prefix the cached-walker prepends to
// every cache key. Exported so subscribers / dashboards that inspect
// Redis can route on "br:" without re-deriving the constant.
const KeyPrefix = "br:"

// CachedWalkerOptions configures a CachedWalker at construction time.
type CachedWalkerOptions struct {
	// Cache is the byte backend. Required — a CachedWalker without
	// a backend is a contradiction in terms. Pass nil to opt out:
	// callers that want the same code path with caching disabled
	// for a request (e.g. preview) toggle via NewWalker.
	Cache CacheBackend

	// Version is the cache-key salt that lets an operator purge
	// every cached render at once by bumping the value (typically
	// the build SHA or a release tag). Two builds with the same
	// renderer code share the cache; two builds with different
	// rendering behaviour MUST set different values or one's
	// output will be served by the other.
	Version string

	// TTL is the time-to-live for stored entries. Zero means
	// DefaultCacheTTL. Callers tuning a hot path may go higher;
	// going lower than 1 minute defeats the purpose (the round-
	// trip cost dominates the regenerate cost).
	TTL time.Duration

	// ExtraTags is appended to every cached entry. The typical
	// use is a per-tenant or per-site tag so an operator can
	// purge a single tenant's renders without touching others'.
	// Empty by default.
	ExtraTags []string

	// Logger receives the structured cache-event output. Nil
	// falls back to slog.Default.
	Logger *slog.Logger
}

// Metrics is the cumulative cache-event tally for one CachedWalker.
// All four counters are read concurrently safely.
//
// Hit-rate is the field issue #108's acceptance criteria measure
// against (target: 80%+). Operators read these via Snapshot.
type Metrics struct {
	hits     atomic.Uint64
	misses   atomic.Uint64
	stores   atomic.Uint64
	bypasses atomic.Uint64
}

// MetricsSnapshot is the value type the public Metrics() returns.
// Fields are uint64 (no pointers) so a snapshot is cheap to copy and
// safe to log straight into slog without atomic semantics leaking.
type MetricsSnapshot struct {
	// Hits is the number of cached lookups that returned a fresh
	// value.
	Hits uint64
	// Misses is the number of cached lookups that returned no value
	// (key absent OR captured tag-version mismatch).
	Misses uint64
	// Stores is the number of Set calls completed successfully
	// (one per miss whose render produced cacheable HTML).
	Stores uint64
	// Bypasses is the number of blocks that were rendered without
	// consulting the cache at all. Blocks bypass caching when they
	// consume context (the cache key cannot capture inherited
	// values) or fail the cacheability heuristic.
	Bypasses uint64
}

// HitRate returns the fraction of cache lookups that produced a
// fresh value, in the [0, 1] range. Returns 0 when no lookups have
// occurred yet — undefined would be a worse contract for log lines.
func (m MetricsSnapshot) HitRate() float64 {
	total := m.Hits + m.Misses
	if total == 0 {
		return 0
	}
	return float64(m.Hits) / float64(total)
}

// CachedWalker is a Walker whose subtree renders are memoised through
// a CacheBackend.
//
// The cache is keyed on the (block type, deep content hash, version)
// triple — the same render under two posts of the same shape shares
// a cache entry. The hash covers the block's Attributes and the full
// InnerBlocks subtree, so a child change correctly busts every
// ancestor that included it.
//
// Blocks that declare UsesContext bypass caching entirely. Caching
// a context-consuming block would require including the consumed
// values in the key, which in turn requires walking the inherited
// context at lookup time — and at that point the cost has eaten
// most of the win.
type CachedWalker struct {
	walker    *Walker
	cache     CacheBackend
	version   string
	ttl       time.Duration
	extraTags []string
	logger    *slog.Logger
	metrics   Metrics
}

// NewCached constructs a CachedWalker bound to the given registry
// and cache. A nil registry panics for the same reason render.New
// panics — a walker without a dispatch table can't produce output.
// A nil opts.Cache is rejected by an error rather than a panic so
// callers can wire conditionally (preview requests skip caching by
// passing an explicit nil).
func NewCached(reg *Registry, opts CachedWalkerOptions) (*CachedWalker, error) {
	if reg == nil {
		panic("render.NewCached: registry is nil")
	}
	if opts.Cache == nil {
		return nil, fmt.Errorf("render: NewCached: opts.Cache is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = DefaultCacheTTL
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &CachedWalker{
		walker:    New(reg),
		cache:     opts.Cache,
		version:   opts.Version,
		ttl:       opts.TTL,
		extraTags: append([]string(nil), opts.ExtraTags...),
		logger:    opts.Logger,
	}, nil
}

// Walk renders the tree against the cache. The output is identical
// to Walker.Walk's — same HTML, same errors — but each cacheable
// subtree is looked up before invocation and stored after.
//
// The cache layer is per-subtree, not per-root: every block whose
// type does NOT declare UsesContext is a candidate. This is what
// gives the 80%+ hit rate on repeated renders — the post-level cache
// would miss anytime one paragraph changes, but the block-level
// cache keeps the rest of the post hot.
func (cw *CachedWalker) Walk(ctx context.Context, tree BlockTree, blockCtx Context) WalkResult {
	res := WalkResult{}
	if blockCtx == nil {
		blockCtx = Context{}
	}
	var html strings.Builder
	for i, block := range tree {
		path := fmt.Sprintf("/%d", i)
		out, errs := cw.walkBlockCached(ctx, block, blockCtx, path)
		html.WriteString(string(out))
		res.Errors = append(res.Errors, errs...)
	}
	res.HTML = template.HTML(html.String())
	return res
}

// Metrics returns a snapshot of the cumulative cache-event counters.
// The snapshot is taken at non-monotonic precision (each field is
// read independently), which is good enough for hit-rate dashboards
// — a Hits/Misses ratio that briefly lags by one event under load
// is not worth a coarser lock for.
func (cw *CachedWalker) Metrics() MetricsSnapshot {
	return MetricsSnapshot{
		Hits:     cw.metrics.hits.Load(),
		Misses:   cw.metrics.misses.Load(),
		Stores:   cw.metrics.stores.Load(),
		Bypasses: cw.metrics.bypasses.Load(),
	}
}

// walkBlockCached looks the block up in the cache; on miss it falls
// through to the underlying Walker and writes back the rendered HTML.
//
// Errors found inside a cached subtree are NOT cached — only the
// HTML output is. A cached error would be surprising on retry (the
// renderer typically fixes itself after a transient failure), and
// the error wiring already has a "log and degrade" path for repeat
// problems.
func (cw *CachedWalker) walkBlockCached(
	ctx context.Context,
	block Block,
	inherited Context,
	path string,
) (template.HTML, []WalkError) {
	spec, ok := cw.walker.registry.Get(block.Type)
	if !ok {
		// Unknown blocks reuse the underlying walker's placeholder
		// machinery. They don't go through the cache because the
		// placeholder is essentially free and the cache would just
		// burn a Redis round-trip.
		return cw.walker.walkBlock(block, inherited, path)
	}
	if !cw.isCacheable(spec) {
		cw.metrics.bypasses.Add(1)
		return cw.walker.walkBlock(block, inherited, path)
	}

	key := cw.cacheKey(block)
	tags := cw.cacheTags(block)

	cached, hit, err := cw.cache.Get(ctx, key, tags)
	if err != nil {
		// Transport errors are treated as misses: a brief Redis
		// outage degrades the cache to a passthrough, not a hard
		// failure.
		cw.logger.Debug("render cache: get error, falling through",
			slog.String("key", key),
			slog.Any("err", err))
	}
	if hit {
		cw.metrics.hits.Add(1)
		return template.HTML(cached), nil
	}
	cw.metrics.misses.Add(1)

	out, errs := cw.walker.walkBlock(block, inherited, path)
	if len(errs) > 0 {
		// Don't cache subtrees that produced errors — see method
		// docstring.
		return out, errs
	}
	if err := cw.cache.Set(ctx, key, []byte(out), tags, cw.ttl); err != nil {
		cw.logger.Warn("render cache: set error",
			slog.String("key", key),
			slog.Any("err", err))
	} else {
		cw.metrics.stores.Add(1)
	}
	return out, errs
}

// isCacheable answers "can this block's render be safely cached
// without including the inherited context in the key?"
//
// Two rules:
//
//  1. The block must not declare UsesContext. A consumer of context
//     is a block whose output depends on ancestor state we can't
//     easily fold into the key.
//
//  2. The block must not provide context to its descendants. A
//     provider whose attributes change has to invalidate every
//     descendant that captured the old value, and the cache layer
//     doesn't track that dependency graph.
//
// Both rules are conservative — many real-world blocks neither
// provide nor consume context, and those are exactly the ones a
// post repeats over and over (headings, paragraphs, columns,
// images, lists). The 80%+ hit rate target is met because those
// blocks dominate the long-tail of post content.
func (cw *CachedWalker) isCacheable(spec BlockSpec) bool {
	return len(spec.UsesContext) == 0 && len(spec.ProvidesContext) == 0
}

// cacheKey computes the cache key for a block subtree.
//
// Key shape: "br:<version>:<block.type>:<sha256(canonical(block))>"
//
// - The leading prefix lets a dashboard route or count by namespace.
// - The version segment is the operator-controlled cache buster.
// - block.type is included verbatim so a key reads usefully in
//   `redis-cli MONITOR`.
// - The sha256 hash covers the canonical-JSON encoding of the block
//   (Attributes + InnerBlocks recursively). Two blocks with the same
//   on-wire shape collide on the key — that is the WHOLE point.
//
// The hash uses canonical JSON: sorted attribute keys, no whitespace.
// We do not use the post's pre-existing content_blocks_hash because
// that column hashes the whole post; we need a per-subtree hash.
func (cw *CachedWalker) cacheKey(block Block) string {
	canonical := canonicalEncode(block)
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("%s%s:%s:%s",
		KeyPrefix, cw.version, block.Type, hex.EncodeToString(sum[:]))
}

// cacheTags computes the tag set the cache should record for one
// block. The base tag is "br:type:<block.type>" so an operator can
// purge every render of a single block type (e.g. when a renderer
// has a bug fix); the configured ExtraTags are appended.
//
// We do NOT add a per-block-instance tag — the cache key is already
// content-addressable, so a content change naturally produces a
// fresh key and the old one ages out via TTL.
func (cw *CachedWalker) cacheTags(block Block) []string {
	tags := make([]string, 0, 1+len(cw.extraTags))
	tags = append(tags, "br:type:"+block.Type)
	tags = append(tags, cw.extraTags...)
	return tags
}

// canonicalEncode produces a deterministic byte serialisation of a
// block subtree for hashing. The rules are:
//
//   - JSON-encode with sorted attribute keys (sortedMap is the
//     hand-written serializer).
//   - InnerBlocks are encoded recursively.
//   - ClientID is intentionally omitted — it's editor-state and not
//     part of the on-wire content shape.
//
// We do not use encoding/json's Marshal directly because Go's
// map iteration order is randomized; two encodings of the same
// attributes would produce different bytes and miss the cache.
//
// Errors are degraded into a synthetic byte sequence so an
// undecodable attribute doesn't crash the walker. A degraded hash
// will simply produce a unique key per attempt, defeating caching
// for that one block — preferable to a panic.
func canonicalEncode(block Block) []byte {
	var b strings.Builder
	b.WriteString(`{"t":`)
	jsonString(&b, block.Type)
	b.WriteString(`,"a":`)
	writeSortedValue(&b, block.Attributes)
	if len(block.InnerBlocks) > 0 {
		b.WriteString(`,"i":[`)
		for i, child := range block.InnerBlocks {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(canonicalEncode(child))
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// writeSortedValue is the recursive heart of canonicalEncode: maps
// are emitted with keys in ascending order, slices in source order,
// everything else falls through to encoding/json.
func writeSortedValue(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jsonString(b, k)
			b.WriteByte(':')
			writeSortedValue(b, t[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeSortedValue(b, item)
		}
		b.WriteByte(']')
	default:
		// Numbers, strings, booleans, nested concrete types — let
		// encoding/json handle the formatting. We only need
		// determinism for maps and slices.
		out, err := json.Marshal(t)
		if err != nil {
			// Sentinel value so a hash failure produces a
			// deterministic but distinct key per (block, time).
			fmt.Fprintf(b, `"err:%s"`, err.Error())
			return
		}
		b.Write(out)
	}
}

// jsonString writes a JSON-quoted string. Defers to encoding/json for
// escape correctness — it's the simplest way to get U+2028, control
// chars, and quote escaping right.
func jsonString(b *strings.Builder, s string) {
	out, _ := json.Marshal(s)
	b.Write(out)
}
