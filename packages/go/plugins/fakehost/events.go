package fakehost

import (
	"fmt"
	"time"
)

// Event is a single recorded host call. The fake host appends one Event
// per ABI invocation, in the order the plugin issued them. Scenarios can
// then assert on the slice — both shape (kind + count) and contents
// (Args).
//
// Event is intentionally untyped beyond a discriminator string and a
// typed Args payload. We could split into Event[T] generics, but tests
// would still need to switch on Kind to walk a heterogenous trace; the
// untyped Args is simpler.
type Event struct {
	// Kind is the canonical ABI function name (e.g. "gn_kv_set",
	// "gn_db_read"). Always equal to one of the EventKind* constants.
	Kind string `json:"kind"`

	// At is the deterministic clock value at the moment the call was
	// recorded. The fake host's [Host.Now] backs this.
	At time.Time `json:"at"`

	// Args carries call-specific arguments — e.g. {"key": "...",
	// "value": "..."} for gn_kv_set. The concrete shape per Kind is
	// documented on each Host method.
	Args map[string]any `json:"args,omitempty"`

	// Result, if non-nil, captures what the fake host returned to the
	// plugin (e.g. a kv.Get hit value). Useful for scenarios where the
	// test wants to assert that a recorded value was actually read
	// back.
	Result any `json:"result,omitempty"`
}

// EventKind* are the stable kind strings used by [Event.Kind]. They
// match the wazero export names in
// [packages/go/plugins/runtime/host_*.go] one-for-one so a scenario
// written against fakehost reads the same as the real host's trace.
const (
	EventLog            = "gn_log"
	EventTimeMS         = "gn_time_ms"
	EventPanic          = "gn_panic"
	EventHTTPFetch      = "gn_http_fetch"
	EventMediaRead      = "gn_media_read"
	EventUsersRead      = "gn_users_read"
	EventHTTPServe      = "http.serve"
	EventDBRead         = "gn_db_read"
	EventDBWrite        = "gn_db_write"
	EventKVGet          = "gn_kv_get"
	EventKVSet          = "gn_kv_set"
	EventKVDel          = "gn_kv_del"
	EventKVIncr         = "gn_kv_incr"
	EventCacheInval     = "gn_cache_invalidate"
	EventSecretsGet     = "gn_secrets_get"
	EventAuditEmit      = "gn_audit_emit"
	EventCronRegister   = "gn_cron_register"
	EventI18NTranslate  = "gn_i18n_translate"
	EventMetricObserve  = "gn_metric_observe"
	EventEventEmit      = "gn_event_emit"
	EventSpanEvent      = "gn_span_event"
	EventPostsRead      = "gn_posts_read"
	EventPostsWrite     = "gn_posts_write"
)

// String returns a single-line human summary of the event suitable for
// CI log output.
func (e Event) String() string {
	return fmt.Sprintf("%s args=%v", e.Kind, e.Args)
}
