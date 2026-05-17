package email

import (
	"context"
	"sync"
	"testing"
)

func TestNoopSender_BasicSend(t *testing.T) {
	s := NewNoopSender()
	if _, ok := s.Last(); ok {
		t.Fatal("Last on fresh sender should be (zero, false)")
	}
	if s.Count() != 0 {
		t.Fatalf("Count on fresh sender: got %d", s.Count())
	}

	msg := Message{To: "a@b.test", Subject: "x", TextBody: "y"}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, ok := s.Last()
	if !ok {
		t.Fatal("Last returned (zero, false) after Send")
	}
	if got.To != msg.To {
		t.Errorf("Last.To: got %q want %q", got.To, msg.To)
	}
	if s.Count() != 1 {
		t.Errorf("Count: got %d want 1", s.Count())
	}
}

func TestNoopSender_RejectsInvalid(t *testing.T) {
	s := NewNoopSender()
	if err := s.Send(context.Background(), Message{}); err == nil {
		t.Fatal("expected validation error")
	}
	if s.Count() != 0 {
		t.Errorf("Count after rejected send: got %d want 0", s.Count())
	}
}

func TestNoopSender_CapturedOrdering(t *testing.T) {
	s := NewNoopSender()
	for _, name := range []string{"a", "b", "c"} {
		if err := s.Send(context.Background(), Message{
			To: name + "@x.test", Subject: name, TextBody: name,
		}); err != nil {
			t.Fatalf("Send %s: %v", name, err)
		}
	}
	captured := s.Captured()
	if len(captured) != 3 {
		t.Fatalf("len(Captured): got %d want 3", len(captured))
	}
	for i, want := range []string{"a", "b", "c"} {
		if captured[i].Subject != want {
			t.Errorf("captured[%d].Subject: got %q want %q", i, captured[i].Subject, want)
		}
	}
}

func TestNoopSender_Reset(t *testing.T) {
	s := NewNoopSender()
	_ = s.Send(context.Background(), Message{To: "a@b.test", Subject: "x", TextBody: "y"})
	s.Reset()
	if s.Count() != 0 {
		t.Errorf("Count after Reset: got %d", s.Count())
	}
	if got := s.Captured(); len(got) != 0 {
		t.Errorf("Captured after Reset: got %v", got)
	}
}

func TestNoopSender_ConcurrentSafe(t *testing.T) {
	s := NewNoopSender()
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = s.Send(context.Background(), Message{
				To: "a@b.test", Subject: "x", TextBody: "y",
			})
		}()
	}
	wg.Wait()
	if s.Count() != n {
		t.Errorf("Count after %d concurrent sends: got %d", n, s.Count())
	}
}
