package email

import (
	"context"
	"sync"
)

// NoopSender is the unit-test [Sender] that drops every message and
// returns nil. It satisfies the [Sender] interface so handlers can be
// exercised without standing up a real SMTP server.
//
// The sender tracks the last-sent and total-sent messages so tests can
// assert "Send was called" without wiring an external observer. Access
// is concurrency-safe.
//
// Use [LogSender] for the dev-only "see the rendered email in logs"
// case; NoopSender is strictly the test scaffolding.
type NoopSender struct {
	mu       sync.Mutex
	last     Message
	count    int
	captured []Message
}

// NewNoopSender returns a NoopSender ready for use. The zero value is
// also usable but a constructor keeps the API consistent with the
// other sender types.
func NewNoopSender() *NoopSender {
	return &NoopSender{}
}

// Send validates the message, increments the call counter, captures
// the message, and returns nil. Send is safe for concurrent use.
func (s *NoopSender) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = msg
	s.count++
	s.captured = append(s.captured, msg)
	return nil
}

// Last returns the most recently sent message and whether any
// message has been sent at all.
func (s *NoopSender) Last() (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return Message{}, false
	}
	return s.last, true
}

// Count returns how many times Send has been called successfully.
func (s *NoopSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// Captured returns a copy of every message Send accepted, in send
// order. Useful for tests that need to assert exact sequence.
func (s *NoopSender) Captured() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.captured))
	copy(out, s.captured)
	return out
}

// Reset clears the captured history and the count. The zero value
// after Reset is identical to a freshly constructed NoopSender.
func (s *NoopSender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = Message{}
	s.count = 0
	s.captured = nil
}

// Compile-time check that *NoopSender satisfies Sender.
var _ Sender = (*NoopSender)(nil)
