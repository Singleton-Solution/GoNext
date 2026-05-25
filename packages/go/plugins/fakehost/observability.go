package fakehost

import (
	"strings"
)

// Observability ABI surface (gn_log, gn_time_ms, gn_i18n_translate,
// gn_metric_observe, gn_event_emit, gn_span_event).
//
// These calls do not have capability gates in the real host either
// (logs, metrics and spans are unconditionally available to every
// plugin), so the methods skip the requireCapLocked plumbing.

// Log records a log line at the given level. Levels match the real
// host's mapping (0=debug, 1=info, 2=warn, 3=error). Returns the
// number of recorded log events so the caller can assert without
// reading Events().
func (h *Host) Log(level int32, msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"level": level, "msg": msg}
	h.recordLocked(EventLog, args, nil)
	return len(h.events)
}

// TimeMS returns the current deterministic clock as Unix
// milliseconds. The trace records the returned value as the Result
// field — most scenarios don't care, but it makes time-related
// assertions trivial.
func (h *Host) TimeMS() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	ms := h.now.UnixMilli()
	h.recordLocked(EventTimeMS, nil, ms)
	return ms
}

// I18NTranslate returns the input key wrapped in a `[t:locale:KEY]`
// envelope. The fake host does not maintain a translation catalogue
// — scenarios that care about the translated text should script
// the host (a future SetTranslation helper) or inspect the recorded
// event.
func (h *Host) I18NTranslate(key, locale string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := "[t:" + locale + ":" + key + "]"
	args := map[string]any{"key": key, "locale": locale}
	h.recordLocked(EventI18NTranslate, args, out)
	return out
}

// MetricObserve records a metric observation. The fake host does
// not maintain a Prometheus registry — assertions are made against
// the recorded events.
func (h *Host) MetricObserve(name string, value float64, tags map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"name":  name,
		"value": value,
		"tags":  cloneStringMap(tags),
	}
	h.recordLocked(EventMetricObserve, args, nil)
}

// EventEmit records a domain event the plugin published on the host
// event bus.
func (h *Host) EventEmit(eventName string, payload map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"event":   eventName,
		"payload": cloneFields(payload),
	}
	h.recordLocked(EventEventEmit, args, nil)
}

// SpanEvent records an OpenTelemetry span event on the current
// span. Real host attaches attributes to the parent span; fakehost
// just records.
func (h *Host) SpanEvent(name string, attrs map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"name":  name,
		"attrs": cloneFields(attrs),
	}
	h.recordLocked(EventSpanEvent, args, nil)
}

// Panic records a fatal-trap event the plugin raised through
// gn_panic. Unlike the real host this does NOT actually abort
// execution — the test continues — but the recorded event makes it
// trivial to assert "the plugin panicked with reason X".
func (h *Host) Panic(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"reason": strings.TrimSpace(reason)}
	h.recordLocked(EventPanic, args, nil)
}
