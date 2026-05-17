package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestFromContext_NoLogger_ReturnsDefault(t *testing.T) {
	got := FromContext(context.Background())
	if got == nil {
		t.Fatal("FromContext returned nil")
	}
	if got != slog.Default() {
		t.Errorf("FromContext on bare context: expected slog.Default()")
	}
}

func TestFromContext_NilContext_ReturnsDefault(t *testing.T) {
	got := FromContext(nil) //nolint:staticcheck // intentional: API must tolerate nil
	if got != slog.Default() {
		t.Errorf("FromContext(nil): expected slog.Default(), got %v", got)
	}
}

func TestWithLogger_RoundTrip(t *testing.T) {
	custom := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ctx := WithLogger(context.Background(), custom)
	got := FromContext(ctx)
	if got != custom {
		t.Errorf("WithLogger/FromContext: expected the same logger back")
	}
}

func TestWithLogger_NilLogger_PassesThrough(t *testing.T) {
	ctx := WithLogger(context.Background(), nil)
	if got := FromContext(ctx); got != slog.Default() {
		t.Errorf("WithLogger(ctx, nil) should leave ctx unchanged, got non-default logger")
	}
}

func TestWithRequest_StampsFields(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := WithLogger(context.Background(), base)

	ctx = WithRequest(ctx, RequestFields{
		TraceID:   "trace-1",
		RequestID: "req-1",
		UserID:    "u-1",
	})

	FromContext(ctx).Info("hi")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k, want := range map[string]string{
		"trace_id":   "trace-1",
		"request_id": "req-1",
		"user_id":    "u-1",
	} {
		if got[k] != want {
			t.Errorf("field %q: got %v, want %q", k, got[k], want)
		}
	}
	if _, present := got["span_id"]; present {
		t.Errorf("empty SpanID should not appear in output")
	}
	if _, present := got["plugin_slug"]; present {
		t.Errorf("empty PluginSlug should not appear in output")
	}
}

func TestWithRequest_EmptyFields_NoOp(t *testing.T) {
	base := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	ctx := WithLogger(context.Background(), base)

	got := WithRequest(ctx, RequestFields{})
	if FromContext(got) != base {
		t.Errorf("empty RequestFields should not derive a new logger")
	}
}

func TestRequestFields_toAttrs_StableOrder(t *testing.T) {
	// Order matters for log output stability and snapshot tests.
	rf := RequestFields{
		TraceID:    "t",
		SpanID:     "s",
		RequestID:  "r",
		UserID:     "u",
		PluginSlug: "p",
		TenantID:   "tn",
	}
	attrs := rf.toAttrs()
	want := []string{"trace_id", "span_id", "request_id", "user_id", "plugin_slug", "tenant_id"}
	if len(attrs) != len(want) {
		t.Fatalf("len=%d want=%d", len(attrs), len(want))
	}
	for i, w := range want {
		if attrs[i].Key != w {
			t.Errorf("attrs[%d]: got %q, want %q", i, attrs[i].Key, w)
		}
	}
}
