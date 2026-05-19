package email

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// failingSender is a Sender that returns a configured error from
// Send. Used to exercise the failure path of AuditSender without
// touching network code.
type failingSender struct {
	err error
}

func (f *failingSender) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	return f.err
}

func TestAuditSender_NilInnerRejected(t *testing.T) {
	store := audit.NewMemoryStore()
	emitter := audit.NewEmitter(store)
	if _, err := NewAuditSender(nil, emitter, nil); err == nil {
		t.Fatal("expected error for nil inner sender")
	}
}

func TestAuditSender_EmitsOnSuccess(t *testing.T) {
	store := audit.NewMemoryStore()
	emitter := audit.NewEmitter(store)
	inner := NewNoopSender()
	w, err := NewAuditSender(inner, emitter, slog.Default())
	if err != nil {
		t.Fatalf("NewAuditSender: %v", err)
	}

	msg := Message{
		To: "alice@example.com", Subject: "Hi", TextBody: "body",
		Tags: map[string]string{
			"flow":     "auth.verify.email",
			"template": "verify-email",
		},
	}
	if err := w.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if inner.Count() != 1 {
		t.Errorf("inner Send count: got %d want 1", inner.Count())
	}

	events, err := store.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != "email.sent" {
		t.Errorf("event type: got %q want email.sent", ev.EventType)
	}
	if ev.Severity != audit.SeverityInfo {
		t.Errorf("severity: got %q want info", ev.Severity)
	}
	if to, _ := ev.Metadata["to"].(string); to != "a***@example.com" {
		t.Errorf("masked recipient: got %q want a***@example.com", to)
	}
	if tmpl, _ := ev.Metadata["template"].(string); tmpl != "verify-email" {
		t.Errorf("template metadata: got %q", tmpl)
	}
	if flow, _ := ev.Metadata["flow"].(string); flow != "auth.verify.email" {
		t.Errorf("flow metadata: got %q", flow)
	}
}

func TestAuditSender_EmitsOnFailure(t *testing.T) {
	store := audit.NewMemoryStore()
	emitter := audit.NewEmitter(store)
	sendErr := errors.New("smtp: connection refused")
	w, err := NewAuditSender(&failingSender{err: sendErr}, emitter, slog.Default())
	if err != nil {
		t.Fatalf("NewAuditSender: %v", err)
	}

	msg := Message{To: "bob@example.com", Subject: "Hi", TextBody: "body"}
	gotErr := w.Send(context.Background(), msg)
	if !errors.Is(gotErr, sendErr) {
		t.Errorf("expected wrapped sendErr, got %v", gotErr)
	}

	events, _ := store.List(context.Background(), audit.Filter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != "email.failed" {
		t.Errorf("event type: got %q want email.failed", ev.EventType)
	}
	if ev.Severity != audit.SeverityWarning {
		t.Errorf("severity: got %q want warning", ev.Severity)
	}
	errMsg, _ := ev.Metadata["error"].(string)
	if !strings.Contains(errMsg, "connection refused") {
		t.Errorf("error metadata missing detail: %q", errMsg)
	}
}

func TestAuditSender_NilEmitter_PassThrough(t *testing.T) {
	inner := NewNoopSender()
	w, err := NewAuditSender(inner, nil, slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("NewAuditSender: %v", err)
	}
	msg := Message{To: "x@y.test", Subject: "S", TextBody: "B"}
	if err := w.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if inner.Count() != 1 {
		t.Errorf("inner Send count: got %d want 1", inner.Count())
	}
}

func TestMaskEmailForAudit(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"alice@example.com", "a***@example.com"},
		{"x@example.com", "*@example.com"},
		{"", ""},
		{"no-at-sign", "no-at-sign"},
	}
	for _, c := range cases {
		if got := maskEmailForAudit(c.in); got != c.want {
			t.Errorf("maskEmailForAudit(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
