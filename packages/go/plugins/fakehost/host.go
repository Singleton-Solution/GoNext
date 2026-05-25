package fakehost

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// fixedEpoch is the deterministic clock the fake host returns from
// [Host.Now] when the caller has not overridden it. We pick a value far
// enough in the past that nothing in the scenarios can accidentally
// match wall-clock time, but recent enough that durations remain
// physically reasonable.
var fixedEpoch = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

// ErrNotFound is returned by [Host.KVGet] when the key is unbound. It
// mirrors the dataResultNotFound sentinel the real host returns to the
// guest. Tests assert via [errors.Is].
var ErrNotFound = errors.New("fakehost: key not found")

// ErrDenied is returned by any method whose capability has been
// disabled via [Host.DisableCapability]. The error wraps the cap name
// so test assertions can compare the failed cap.
var ErrDenied = errors.New("fakehost: capability denied")

// ErrQuota is returned by [Host.KVSet] when the configured KV byte
// budget would be exceeded. Same semantics as dataResultQuota.
var ErrQuota = errors.New("fakehost: quota exceeded")

// Host is the in-memory fake host. Construct one with [New], then call
// methods on it from your scenario. Every call appends a recorded
// [Event] to the call trace exposed by [Host.Events].
//
// Host is safe for concurrent use. Internal state is mu-guarded;
// scenarios that issue host calls from multiple goroutines see a
// consistent recorded order (the order Lock was acquired).
type Host struct {
	// slug identifies the plugin this host is bound to. Used in
	// audit-event metadata and quota accounting. Optional — the
	// zero value is fine for tests that don't care.
	slug string

	mu sync.Mutex

	// now is the deterministic clock. Overridden via [Host.SetNow].
	// Never nil — New seeds it with fixedEpoch.
	now time.Time

	// events is the recorded call trace, ordered by insertion. New
	// returns an empty slice; ResetEvents truncates it.
	events []Event

	// kv is the in-memory KV store. Keyed by the plugin's
	// post-prefix key (i.e. what the plugin actually passes; the
	// real host prepends "plugin:<slug>:" internally).
	kv map[string][]byte

	// secrets are the secret values exposed via [Host.SecretsGet].
	// Pre-seed via [Host.SetSecret]. Missing secret returns
	// ErrNotFound.
	secrets map[string]string

	// httpResponses are scripted HTTP responses keyed by request
	// URL. If a plugin issues [Host.HTTPFetch] for a URL not in
	// this map, the fake host returns ErrNotFound — most scenarios
	// want explicit allow-listing of allowed outbound calls.
	httpResponses map[string]HTTPResponse

	// posts is the in-memory post store. Keyed by post ID. Used by
	// PostsRead and PostsWrite.
	posts map[int64]map[string]any

	// users is the in-memory user store. Keyed by user ID.
	users map[int64]map[string]any

	// media is the in-memory media store. Keyed by media ID.
	media map[int64]map[string]any

	// denied is the set of capability names that should fail with
	// ErrDenied when invoked. Defaults to empty (everything is
	// granted); flip via [Host.DisableCapability].
	denied map[string]struct{}

	// kvQuotaBytes caps the total bytes stored across all keys.
	// Zero means unlimited. Set via [Host.SetKVQuota].
	kvQuotaBytes int64

	// nextID is the per-Host counter used to mint synthetic IDs
	// for newly-written posts/users/media. We do not depend on
	// uuid here — deterministic small ints make assertions easier.
	nextID int64
}

// HTTPResponse is the scripted reply for a [Host.HTTPFetch] call. The
// fake host returns the same shape regardless of the request method —
// scenarios are responsible for setting up enough fixtures to cover
// the methods their plugin issues.
type HTTPResponse struct {
	// Status is the HTTP status code returned to the plugin.
	Status int `json:"status"`

	// Headers are the response headers (canonical case). Optional;
	// missing means no headers were set.
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the response body bytes.
	Body []byte `json:"body,omitempty"`
}

// Option configures a [Host] at construction. We use the functional
// options pattern (rather than a Config struct) so future knobs
// (separate clocks per ABI, scripted clock-tick callbacks) can land
// without breaking the call site.
type Option func(*Host)

// WithSlug sets the plugin slug used for audit metadata and quota
// keys. Most scenarios don't care; tests that exercise per-plugin
// quotas should pin one.
func WithSlug(slug string) Option {
	return func(h *Host) { h.slug = slug }
}

// WithClock sets the initial deterministic clock value. If omitted,
// the host starts at [fixedEpoch] (2025-01-01 UTC).
func WithClock(t time.Time) Option {
	return func(h *Host) { h.now = t }
}

// New returns a fake host ready for use. Pass [Option] values for
// non-default behaviour; the zero-option form is fine for almost all
// scenarios.
func New(opts ...Option) *Host {
	h := &Host{
		now:           fixedEpoch,
		kv:            map[string][]byte{},
		secrets:       map[string]string{},
		httpResponses: map[string]HTTPResponse{},
		posts:         map[int64]map[string]any{},
		users:         map[int64]map[string]any{},
		media:         map[int64]map[string]any{},
		denied:        map[string]struct{}{},
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Events returns a copy of the recorded call trace. Returning a copy
// (rather than the underlying slice) lets the caller mutate freely
// without disturbing further calls.
func (h *Host) Events() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Event, len(h.events))
	copy(out, h.events)
	return out
}

// EventsOf returns the recorded events whose Kind matches one of the
// given names. Convenient for assertions like
// `assert.Len(host.EventsOf(EventKVSet), 1)`.
func (h *Host) EventsOf(kinds ...string) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	want := map[string]struct{}{}
	for _, k := range kinds {
		want[k] = struct{}{}
	}
	out := make([]Event, 0, len(h.events))
	for _, e := range h.events {
		if _, ok := want[e.Kind]; ok {
			out = append(out, e)
		}
	}
	return out
}

// ResetEvents clears the recorded trace. KV / secrets / scripted
// HTTP responses are preserved — only the trace is wiped. Useful for
// tests that want to assert "from this point on, the plugin made
// exactly N calls".
func (h *Host) ResetEvents() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = h.events[:0]
}

// Now returns the deterministic clock value. The fake host's
// gn_time_ms equivalent ([Host.TimeMS]) is derived from this.
func (h *Host) Now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

// SetNow overrides the deterministic clock. Useful for testing cron
// schedules, TTL behaviour, audit ordering, etc.
func (h *Host) SetNow(t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = t
}

// Advance moves the deterministic clock forward by d. Returns the
// new clock value. Equivalent to SetNow(Now().Add(d)) but spelled out
// so test code reads cleanly.
func (h *Host) Advance(d time.Duration) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
	return h.now
}

// DisableCapability marks cap as denied. Subsequent host calls that
// require this capability return ErrDenied (the fake host's
// equivalent of dataResultDenied) and record the failed attempt as a
// normal Event so the trace shows what the plugin tried.
func (h *Host) DisableCapability(cap string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.denied[cap] = struct{}{}
}

// EnableCapability flips a previously-disabled capability back on.
// Idempotent — calling it on a cap that was never disabled is fine.
func (h *Host) EnableCapability(cap string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.denied, cap)
}

// SetKVQuota installs a byte budget for KV writes. Zero means
// unlimited. Setting a smaller quota than the currently-used bytes
// does not evict anything — only future writes that would push past
// the new ceiling are rejected with ErrQuota.
func (h *Host) SetKVQuota(bytes int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kvQuotaBytes = bytes
}

// SetSecret pre-seeds a value for [Host.SecretsGet] lookup. The
// fake host returns the seeded value verbatim; there is no
// encryption or KMS-roundtrip simulation.
func (h *Host) SetSecret(name, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.secrets[name] = value
}

// SetHTTPResponse scripts a reply for the given URL. The fake host
// matches URLs verbatim (no glob / regex). Set a response per URL
// the plugin is expected to fetch.
func (h *Host) SetHTTPResponse(url string, resp HTTPResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.httpResponses[url] = resp
}

// SeedPost adds (or replaces) a post in the in-memory store. Returns
// the post ID. If id == 0 a fresh ID is minted; otherwise id is used
// verbatim (allowing scenarios to write posts with stable IDs they
// reference in fixtures).
func (h *Host) SeedPost(id int64, fields map[string]any) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == 0 {
		h.nextID++
		id = h.nextID
	}
	h.posts[id] = cloneFields(fields)
	return id
}

// SeedUser adds a user. Same contract as SeedPost.
func (h *Host) SeedUser(id int64, fields map[string]any) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == 0 {
		h.nextID++
		id = h.nextID
	}
	h.users[id] = cloneFields(fields)
	return id
}

// SeedMedia adds a media entry. Same contract as SeedPost.
func (h *Host) SeedMedia(id int64, fields map[string]any) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == 0 {
		h.nextID++
		id = h.nextID
	}
	h.media[id] = cloneFields(fields)
	return id
}

// recordLocked appends an Event under the assumption the caller
// already holds h.mu. Splitting the unlocked helper out keeps the
// per-method bodies one-shot and avoids re-locking inside
// composite ops (e.g. KVIncr).
func (h *Host) recordLocked(kind string, args map[string]any, result any) {
	h.events = append(h.events, Event{
		Kind:   kind,
		At:     h.now,
		Args:   cloneFields(args),
		Result: result,
	})
}

// requireCapLocked verifies the given cap has not been disabled.
// Records the denial as an event so trace assertions still see
// what the plugin tried. Caller must hold h.mu.
func (h *Host) requireCapLocked(kind, cap string, args map[string]any) error {
	if _, ok := h.denied[cap]; ok {
		h.recordLocked(kind, args, map[string]any{"denied_cap": cap})
		return fmt.Errorf("%w: %s", ErrDenied, cap)
	}
	return nil
}

// cloneFields returns a shallow copy of the input map. Recording an
// Event with the live arg map would let later mutations leak into
// the recorded trace; cloning is cheaper than reasoning about
// aliasing in every call site.
func cloneFields(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// String returns a multi-line summary of the recorded trace. Used by
// the conformance runner when dumping a failed scenario.
func (h *Host) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("fakehost(slug=%q events=%d, kv=%d entries)\n",
		h.slug, len(h.events), len(h.kv)))
	for i, e := range h.events {
		sb.WriteString(fmt.Sprintf("  [%d] %s @ %s args=%v\n",
			i, e.Kind, e.At.Format(time.RFC3339Nano), e.Args))
	}
	return sb.String()
}

// kvBytesUsedLocked returns the sum of value byte-lengths across all
// KV entries. Caller must hold h.mu. Cheap because the fake KV is
// in-memory and rarely exceeds a few dozen entries in tests.
func (h *Host) kvBytesUsedLocked() int64 {
	var n int64
	for _, v := range h.kv {
		n += int64(len(v))
	}
	return n
}
