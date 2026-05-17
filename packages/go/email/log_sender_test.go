package email

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestLogSender_WritesStructuredLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := NewLogSender(logger)

	msg := Message{
		To:       "alice@example.com",
		From:     "noreply@chassis.test",
		ReplyTo:  "support@chassis.test",
		Subject:  "Verify your email",
		TextBody: "click http://chassis.test/verify?token=abc",
		HTMLBody: `<a href="http://chassis.test/verify?token=abc">verify</a>`,
		Tags:     map[string]string{"flow": "verify", "version": "v1"},
	}

	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Decode the JSON line so we don't get fooled by attribute ordering.
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("decode log line: %v\nraw: %s", err, buf.String())
	}

	wantFields := map[string]any{
		"to":            "alice@example.com",
		"from":          "noreply@chassis.test",
		"reply_to":      "support@chassis.test",
		"subject":       "Verify your email",
		"text_body":     "click http://chassis.test/verify?token=abc",
		"html_body":     `<a href="http://chassis.test/verify?token=abc">verify</a>`,
		"text_body_len": float64(len(msg.TextBody)),
		"html_body_len": float64(len(msg.HTMLBody)),
	}
	for k, v := range wantFields {
		got, ok := entry[k]
		if !ok {
			t.Errorf("missing log field %q", k)
			continue
		}
		if got != v {
			t.Errorf("log field %q: got %v want %v", k, got, v)
		}
	}

	tags, ok := entry["tags"].(map[string]any)
	if !ok {
		t.Fatalf("tags group missing or wrong shape; entry=%v", entry)
	}
	if tags["flow"] != "verify" || tags["version"] != "v1" {
		t.Errorf("tags: got %v", tags)
	}

	// Confirm INFO level is what was emitted.
	if level, _ := entry["level"].(string); !strings.EqualFold(level, "INFO") {
		t.Errorf("level: got %v want INFO", entry["level"])
	}
}

func TestLogSender_NilLoggerFallsBackToDefault(t *testing.T) {
	// Redirect slog.Default temporarily.
	old := slog.Default()
	defer slog.SetDefault(old)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	s := &LogSender{Logger: nil}
	err := s.Send(context.Background(), Message{
		To: "a@b.test", Subject: "x", TextBody: "y",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatalf("expected slog.Default to receive the line")
	}
}

func TestLogSender_RejectsInvalidMessage(t *testing.T) {
	s := NewLogSender(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	err := s.Send(context.Background(), Message{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
