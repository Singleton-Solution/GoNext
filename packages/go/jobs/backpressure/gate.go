package backpressure

import (
	"fmt"
)

// DepthSource is the minimal contract the Gate consumes. *Monitor
// satisfies it via Depth; tests can pass a function-adapter to drive
// the decision directly. Keeping the dependency surface this small
// keeps Gate.Allow a pure-ish function: no goroutines, no I/O, easy
// to fuzz.
type DepthSource interface {
	Depth(queue string) int
}

// Gate makes the shed decision for an (queue, priority) pair given
// thresholds and the latest depth from a DepthSource. The struct is
// immutable after NewGate; concurrent Allow calls are race-clean by
// construction (no internal state mutates).
type Gate struct {
	// source supplies current pending depth per queue. Required.
	source DepthSource

	// byQueue is the lookup table built from the registered
	// Thresholds. Populated once in NewGate; read-only thereafter.
	byQueue map[string]Threshold
}

// NewGate validates the thresholds and returns a ready-to-use Gate.
// Returns ErrInvalidThreshold (wrapped with the offending queue name)
// if any Threshold violates an invariant. Duplicate queue names are
// rejected — the second occurrence would silently shadow the first,
// which is the kind of operator-error we want to fail loudly at boot.
func NewGate(source DepthSource, thresholds []Threshold) (*Gate, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: depth source is nil", ErrInvalidThreshold)
	}
	byQueue := make(map[string]Threshold, len(thresholds))
	for _, t := range thresholds {
		if t.Queue == "" {
			return nil, fmt.Errorf("%w: empty queue name", ErrInvalidThreshold)
		}
		if t.SoftLimit <= 0 {
			return nil, fmt.Errorf("%w: queue %q SoftLimit must be > 0 (got %d)",
				ErrInvalidThreshold, t.Queue, t.SoftLimit)
		}
		if t.HardLimit < t.SoftLimit {
			return nil, fmt.Errorf("%w: queue %q HardLimit %d < SoftLimit %d",
				ErrInvalidThreshold, t.Queue, t.HardLimit, t.SoftLimit)
		}
		if _, dup := byQueue[t.Queue]; dup {
			return nil, fmt.Errorf("%w: queue %q registered twice",
				ErrInvalidThreshold, t.Queue)
		}
		byQueue[t.Queue] = t
	}
	return &Gate{source: source, byQueue: byQueue}, nil
}

// Allow returns nil if a task with the given priority may be enqueued
// onto queue right now, or ErrShed otherwise.
//
// Decision table:
//
//	priority == Critical                 → always nil (never shed)
//	depth < SoftLimit                    → nil (every priority passes)
//	depth < HardLimit AND priority ≥ Important → nil
//	otherwise                            → ErrShed
//
// Unknown queues (not present in the configured thresholds) always
// pass. The contract is "configure backpressure for the queues you
// want protected"; unconfigured queues fail-open so adding a new
// queue doesn't require updating the gate's wiring atomically.
//
// Safe for concurrent use.
func (g *Gate) Allow(queue string, priority Priority) error {
	if g == nil {
		return nil
	}
	// Critical always passes regardless of depth. The Gate never sheds
	// security-critical traffic; this short-circuits before the lookup
	// so a misconfigured threshold can't accidentally block 2FA.
	if priority >= Critical {
		return nil
	}
	th, ok := g.byQueue[queue]
	if !ok {
		return nil
	}
	depth := g.source.Depth(queue)
	if depth < th.SoftLimit {
		return nil
	}
	if depth < th.HardLimit && priority >= Important {
		return nil
	}
	return fmt.Errorf("%w: queue=%q depth=%d soft=%d hard=%d priority=%s",
		ErrShed, queue, depth, th.SoftLimit, th.HardLimit, priority)
}

// Threshold returns the registered threshold for queue and whether
// the queue was configured. Surfaced for diagnostic endpoints and
// tests; the production fast-path goes through Allow.
func (g *Gate) Threshold(queue string) (Threshold, bool) {
	if g == nil {
		return Threshold{}, false
	}
	t, ok := g.byQueue[queue]
	return t, ok
}
