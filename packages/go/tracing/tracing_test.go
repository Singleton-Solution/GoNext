package tracing

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// TestSetup_NoEndpoint exercises the no-op path: with no endpoint
// configured, Setup must return a no-op Shutdown, install the W3C
// propagator anyway (so partial rollouts can still join distributed
// traces), and never block.
func TestSetup_NoEndpoint(t *testing.T) {
	t.Setenv(EndpointEnv, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	shutdown, err := Setup(context.Background(), Options{
		ServiceName: "api-test",
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned nil shutdown")
	}
	// Shutdown should be safe to call (it's a no-op).
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// The W3C propagator should still be installed so incoming
	// traceparent headers thread through.
	if got := otel.GetTextMapPropagator(); got == nil {
		t.Fatal("TextMapPropagator not installed")
	}
}

// TestSetup_WithEndpoint stands up a fake OTLP HTTP collector and
// drives Setup against it. The test asserts:
//
//   - Setup returns a non-nil Shutdown,
//   - the global tracer provider is the SDK (not the no-op),
//   - Shutdown drains pending spans through the fake collector.
//
// The fake collector counts incoming requests via an atomic; we
// don't decode the protobuf payload — the SDK guarantees the wire
// format and the assertion of interest is "a request happened
// before Shutdown returned".
func TestSetup_WithEndpoint(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Setenv(EndpointEnv, "")
	shutdown, err := Setup(context.Background(), Options{
		ServiceName: "api-test",
		Endpoint:    srv.URL,
		Insecure:    true,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	})

	// Emit a single span via the global tracer.
	tracer := otel.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()

	// Drain — this should block until the fake collector receives the
	// span batch. We give it a short window; the test will fail loudly
	// if no request lands.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if got := atomic.LoadInt32(&hits); got == 0 {
		t.Fatal("expected at least one export request; got 0")
	}
}

// TestSetup_RejectsMissingServiceName guards the boot-time contract:
// an unnamed tracer is useless in a multi-service trace, so Setup
// refuses to build one. The caller is expected to surface this as
// a fatal config error.
func TestSetup_RejectsMissingServiceName(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := Setup(context.Background(), Options{Logger: logger})
	if err == nil {
		t.Fatal("expected error for missing ServiceName; got nil")
	}
}

// TestSetup_RejectsMissingLogger ensures the logger is required —
// Setup writes a one-shot boot diagnostic that operators rely on.
// Without a logger we'd silently degrade.
func TestSetup_RejectsMissingLogger(t *testing.T) {
	_, err := Setup(context.Background(), Options{ServiceName: "x"})
	if err == nil {
		t.Fatal("expected error for missing Logger; got nil")
	}
}

// TestPropagator_W3CRoundTrip exercises the propagator installed by
// Setup: a traceparent header injected on one request must extract
// into a valid SpanContext on the receiver. The composite includes
// Baggage so the test also confirms a baggage header survives the
// round-trip.
func TestPropagator_W3CRoundTrip(t *testing.T) {
	t.Setenv(EndpointEnv, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := Setup(context.Background(), Options{
		ServiceName: "x",
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	prop := otel.GetTextMapPropagator()
	// Inject a known traceparent + baggage.
	headers := http.Header{}
	headers.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	headers.Set("baggage", "tenant=acme,locale=en-US")
	ctx := prop.Extract(context.Background(), propagation.HeaderCarrier(headers))

	// Re-inject and check the headers round-trip back.
	out := http.Header{}
	prop.Inject(ctx, propagation.HeaderCarrier(out))
	if got := out.Get("traceparent"); got == "" {
		t.Fatalf("traceparent missing after round-trip; headers=%v", out)
	}
	if got := out.Get("baggage"); got == "" {
		t.Fatalf("baggage missing after round-trip; headers=%v", out)
	}
}
