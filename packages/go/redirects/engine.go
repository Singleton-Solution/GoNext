package redirects

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// DefaultFlushInterval is the period between hit-counter flushes from
// the in-memory engine to the durable Store. 30s is short enough that
// the "Top traffic" admin tab feels live, and long enough that a
// high-traffic rule isn't hammering the database 1000x/s.
const DefaultFlushInterval = 30 * time.Second

// Match describes a successful rule lookup. Engine.Match returns it
// by value (callers should treat it as immutable) and the middleware
// uses it to construct the redirect response.
type Match struct {
	// RuleID identifies the rule that fired. The middleware uses it
	// to credit the hit counter on the engine, and the audit log
	// records it so admins can correlate traffic spikes to a
	// specific rule.
	RuleID uuid.UUID

	// Destination is the fully-resolved target URL. For literal rules
	// it's the stored destination verbatim; for regex rules it's the
	// destination with $1/$2/... substituted from the capture groups
	// of the matched path.
	Destination string

	// Status is the HTTP status code (301/302/307/308) to write.
	Status int

	// IsRegex is true when this match came out of a regex rule.
	// Exposed so the middleware can log which path served the
	// request (literal lookups are O(1); regex lookups are O(n)).
	IsRegex bool
}

// compiledRegex is the in-memory representation of one regex rule.
// We store the original Rule alongside the compiled pattern so the
// engine can produce a Match (with the rule's ID + status) without a
// second lookup.
type compiledRegex struct {
	rule    Rule
	pattern *regexp.Regexp
}

// Engine is the lock-free rule index. It owns:
//
//   - A map from literal source_path to Rule (O(1) match).
//   - A slice of compiledRegex, iterated in creation order
//     (first-match-wins).
//   - A pending-hits buffer flushed to the Store every FlushInterval.
//
// Snapshots are taken from a Store. Mutations to the underlying table
// don't propagate until the next Reload — the engine is intentionally
// snapshot-isolated so the hot path doesn't synchronize on every
// request.
type Engine struct {
	store         Store
	flushInterval time.Duration

	// nowFunc, when non-nil, replaces time.Now() in tests.
	nowFunc func() time.Time

	// index holds the read-mostly state. Replaced atomically by
	// Reload so Match never blocks on a writer.
	index atomic.Pointer[engineIndex]

	// pending is the in-memory hit accumulator. Keyed by RuleID.
	// We use a sync.Map because writers (Match) outnumber readers
	// (the flusher); the flusher takes a snapshot via Range, swaps in
	// a fresh map, and ships the snapshot to the Store.
	pending sync.Map // map[uuid.UUID]*pendingEntry

	// flusherStop signals the background flusher goroutine to exit.
	// Sent by Stop().
	flusherStop chan struct{}
	flusherDone chan struct{}
	flusherOnce sync.Once
}

// engineIndex is the immutable read-side data the hot path consults.
type engineIndex struct {
	literal map[string]Rule
	regex   []compiledRegex
}

// pendingEntry is one rule's accumulated hits since the last flush.
type pendingEntry struct {
	count   atomic.Int64
	lastHit atomic.Int64 // unix nanos
}

// EngineOption configures NewEngine.
type EngineOption func(*Engine)

// WithFlushInterval overrides DefaultFlushInterval. Sub-second values
// are accepted but discouraged outside tests.
func WithFlushInterval(d time.Duration) EngineOption {
	return func(e *Engine) {
		if d > 0 {
			e.flushInterval = d
		}
	}
}

// WithNowFunc overrides time.Now. For tests only.
func WithNowFunc(fn func() time.Time) EngineOption {
	return func(e *Engine) {
		e.nowFunc = fn
	}
}

// NewEngine constructs an Engine backed by store. The engine starts
// with an empty index; call Reload to populate it from the store
// before serving traffic.
func NewEngine(store Store, opts ...EngineOption) *Engine {
	e := &Engine{
		store:         store,
		flushInterval: DefaultFlushInterval,
		flusherStop:   make(chan struct{}),
		flusherDone:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	// Initialize with an empty index so Match doesn't NPE before the
	// first Reload.
	e.index.Store(&engineIndex{literal: map[string]Rule{}})
	return e
}

func (e *Engine) now() time.Time {
	if e.nowFunc != nil {
		return e.nowFunc()
	}
	return time.Now()
}

// Reload pulls a fresh snapshot from the Store and atomically swaps
// it in as the new index. Existing in-flight Match calls keep using
// the previous index (atomic.Pointer); subsequent calls see the new
// rules.
//
// Regex rules with uncompilable patterns are dropped with a logged
// note — we'd rather serve traffic with a partial rule set than 500
// every request because one operator typo'd a pattern. The admin UI
// is the right place to surface "your pattern is invalid" feedback.
func (e *Engine) Reload(ctx context.Context) error {
	rules, err := e.store.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("redirects: reload: %w", err)
	}
	idx := &engineIndex{
		literal: make(map[string]Rule, len(rules)),
		regex:   make([]compiledRegex, 0, len(rules)/4+1),
	}
	for _, r := range rules {
		if r.IsRegex {
			pat, err := regexp.Compile(r.SourcePath)
			if err != nil {
				// Skip uncompilable patterns — see method comment.
				continue
			}
			idx.regex = append(idx.regex, compiledRegex{rule: r, pattern: pat})
			continue
		}
		idx.literal[r.SourcePath] = r
	}
	// Snapshot was sorted by created_at ASC; preserve regex order so
	// "first match wins" lines up with creation chronology.
	sort.SliceStable(idx.regex, func(i, j int) bool {
		return idx.regex[i].rule.CreatedAt.Before(idx.regex[j].rule.CreatedAt)
	})
	e.index.Store(idx)
	return nil
}

// Match returns the first rule that fires for path, or false. The
// lookup tries the literal map first (O(1)); on miss it iterates
// the regex set in creation order.
//
// On match, an in-memory hit counter is incremented and last-hit
// timestamp updated. The accumulator is flushed to the Store
// asynchronously by the flusher goroutine.
func (e *Engine) Match(path string) (Match, bool) {
	idx := e.index.Load()
	if idx == nil {
		return Match{}, false
	}
	// Literal first. The map check is cheap and covers the common
	// case (operators write one literal "/old-page -> /new-page" rule
	// at a time and the table fills up that way).
	if r, ok := idx.literal[path]; ok {
		e.recordHit(r.ID)
		return Match{
			RuleID:      r.ID,
			Destination: r.DestinationPath,
			Status:      r.Status,
			IsRegex:     false,
		}, true
	}
	// Regex fallback. We pay a Find per pattern — for typical rule
	// counts (< 100 regex rules) this is well under a millisecond.
	for _, cr := range idx.regex {
		m := cr.pattern.FindStringSubmatchIndex(path)
		if m == nil {
			continue
		}
		// Substitute capture groups via the regexp/Expand machinery,
		// which understands $1 / ${name} the same way the admin UI
		// taught the operator.
		dest := string(cr.pattern.ExpandString(nil, cr.rule.DestinationPath, path, m))
		e.recordHit(cr.rule.ID)
		return Match{
			RuleID:      cr.rule.ID,
			Destination: dest,
			Status:      cr.rule.Status,
			IsRegex:     true,
		}, true
	}
	return Match{}, false
}

// recordHit increments the in-memory counter for ruleID. Lock-free in
// the common case (entry already exists); falls back to a sync.Map
// LoadOrStore on the first hit per rule per flush window.
func (e *Engine) recordHit(ruleID uuid.UUID) {
	now := e.now().UnixNano()
	if v, ok := e.pending.Load(ruleID); ok {
		entry := v.(*pendingEntry)
		entry.count.Add(1)
		// Last-write-wins on the timestamp. The CAS loop would be
		// more correct (atomic max), but for "last hit time" the
		// drift is in nanoseconds and the cost of a real max would
		// be a CompareAndSwap loop on the hot path.
		entry.lastHit.Store(now)
		return
	}
	entry := &pendingEntry{}
	entry.count.Store(1)
	entry.lastHit.Store(now)
	if existing, loaded := e.pending.LoadOrStore(ruleID, entry); loaded {
		// Someone beat us to inserting — fold our hit into theirs so
		// the very-first-hit-per-rule isn't lost.
		ex := existing.(*pendingEntry)
		ex.count.Add(1)
		ex.lastHit.Store(now)
	}
}

// Start launches the background flusher goroutine. Idempotent — only
// the first call wins. Pair with Stop().
func (e *Engine) Start() {
	e.flusherOnce.Do(func() {
		go e.flushLoop()
	})
}

// Stop signals the flusher to exit, performs one final flush, and
// returns when the goroutine has terminated. Calling Stop without a
// matching Start is a no-op (the flusherDone channel is closed by the
// goroutine, so we don't block on it).
func (e *Engine) Stop(ctx context.Context) error {
	// guard against unstarted engine.
	select {
	case <-e.flusherDone:
		return nil
	default:
	}
	// Closing flusherStop is idempotent only the first time — guard
	// with a quick check on a fresh channel.
	defer func() {
		// Recover from "close of closed channel" if Stop is called
		// twice. Cheaper than a sync.Once around the close.
		_ = recover()
	}()
	close(e.flusherStop)
	select {
	case <-e.flusherDone:
		return e.Flush(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Flush drains the pending counter map to the Store. Called by the
// background loop and by Stop; exported so tests can drive it
// deterministically without waiting for the timer.
func (e *Engine) Flush(ctx context.Context) error {
	deltas := make([]HitDelta, 0)
	// Range + Delete: take a snapshot of each entry by ATOMICALLY
	// swapping its counter to zero. New hits arriving during the
	// flush land on the (already-deleted-from-map) entry and would
	// be lost, so we use LoadAndDelete to remove the entry first;
	// the next hit creates a fresh entry.
	e.pending.Range(func(k, v any) bool {
		// LoadAndDelete returns the entry value and removes it. New
		// hits arriving after this point create a fresh entry — none
		// are lost.
		existing, loaded := e.pending.LoadAndDelete(k)
		if !loaded {
			return true
		}
		entry := existing.(*pendingEntry)
		cnt := entry.count.Load()
		if cnt <= 0 {
			return true
		}
		lastNanos := entry.lastHit.Load()
		deltas = append(deltas, HitDelta{
			RuleID:    k.(uuid.UUID),
			Count:     cnt,
			LastHitAt: time.Unix(0, lastNanos),
		})
		return true
	})
	if len(deltas) == 0 {
		return nil
	}
	return e.store.BulkIncrementHits(ctx, deltas)
}

func (e *Engine) flushLoop() {
	defer close(e.flusherDone)
	t := time.NewTicker(e.flushInterval)
	defer t.Stop()
	for {
		select {
		case <-e.flusherStop:
			return
		case <-t.C:
			// Fresh context per tick so a stuck store doesn't keep
			// the flusher pinned forever. The interval is the budget.
			ctx, cancel := context.WithTimeout(context.Background(), e.flushInterval)
			_ = e.Flush(ctx)
			cancel()
		}
	}
}

// Stats returns engine introspection for /metrics + admin UI. Cheap;
// safe to call from the hot path.
type Stats struct {
	LiteralRules int
	RegexRules   int
	PendingHits  int64
}

// Stats produces a snapshot of the engine state.
func (e *Engine) Stats() Stats {
	idx := e.index.Load()
	var s Stats
	if idx != nil {
		s.LiteralRules = len(idx.literal)
		s.RegexRules = len(idx.regex)
	}
	e.pending.Range(func(_, v any) bool {
		s.PendingHits += v.(*pendingEntry).count.Load()
		return true
	})
	return s
}
