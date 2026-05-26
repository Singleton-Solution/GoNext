// Package tracing wires the GoNext binary's OpenTelemetry tracer
// provider. The contract is:
//
//   - If the `GONEXT_OTLP_ENDPOINT` env var is unset (or empty), the
//     package returns a NOOP TracerProvider and the Shutdown closer is
//     a no-op. The rest of the codebase calls `otel.Tracer(...)` which
//     transparently falls through to the global no-op tracer, so the
//     binary's behavior is unchanged.
//
//   - If the endpoint is set, the package constructs an OTLP HTTP
//     exporter, wraps it in a batched SDK TracerProvider, sets the
//     global TextMap propagator to the W3C TraceContext+Baggage
//     composite (so incoming requests inherit a remote span context
//     and outgoing requests carry their span downstream), and returns
//     a Shutdown closer the caller registers with the shutdown
//     orchestrator.
//
// The package deliberately uses HTTP rather than gRPC for the
// exporter: the operator-facing knob is a single URL, no extra TLS or
// channel-tuning surface, and the binary already imports net/http
// transitively for every other client.
//
// Issue #186.
package tracing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// EndpointEnv is the env var read by [Setup]. Exported so the print-
// config dump can surface the current value alongside its sibling
// knobs.
const EndpointEnv = "GONEXT_OTLP_ENDPOINT"

// Options configures [Setup]. ServiceName / ServiceVersion become
// resource attributes on every emitted span; Logger receives one-shot
// boot lines ("tracing enabled, endpoint=...", "tracing disabled, no
// endpoint set").
type Options struct {
	// ServiceName is the value used for the OTel `service.name`
	// resource attribute. Required — an unnamed tracer is useless
	// when looking at a multi-service trace in the SIEM.
	ServiceName string

	// ServiceVersion populates `service.version`. Optional but
	// strongly recommended; set this from buildinfo.Version so the
	// trace pinpoints the binary that produced it.
	ServiceVersion string

	// Endpoint overrides EndpointEnv when non-empty. Tests pin it to
	// a httptest server URL; the production caller leaves it unset
	// and lets Setup read the env var directly.
	Endpoint string

	// Insecure disables TLS on the OTLP HTTP exporter. Required for
	// local development where the collector typically listens on
	// plain HTTP. The production caller leaves this false.
	Insecure bool

	// Logger receives the one-line boot diagnostics. Required.
	Logger *slog.Logger
}

// Shutdown is the function shape returned by [Setup]. The caller is
// expected to hand it to the shutdown orchestrator so the in-flight
// span batch is flushed before the binary exits. The function is
// idempotent — calling it twice is a no-op the second time.
type Shutdown func(context.Context) error

// noopShutdown is the closer returned when tracing was not enabled
// (no endpoint, or Setup failed before constructing a provider). It
// satisfies the [Shutdown] type so the caller can register it with
// the orchestrator unconditionally.
func noopShutdown(_ context.Context) error { return nil }

// Setup constructs the tracer provider, installs it globally, and
// returns a Shutdown closer.
//
// When o.Endpoint (or the env fallback) is empty, Setup returns a
// no-op Shutdown and logs that tracing is disabled. The rest of the
// codebase can call `otel.Tracer(...)` regardless; the global tracer
// provider is the SDK's no-op default in that case.
//
// When the endpoint is set, Setup wires:
//
//   - an OTLP HTTP exporter pointed at the endpoint;
//   - a BatchSpanProcessor with default tuning;
//   - a TracerProvider with the service-name resource;
//   - the global TextMap propagator set to the W3C composite
//     (TraceContext + Baggage) so incoming requests inherit a
//     remote trace and outgoing requests propagate it.
//
// On any exporter-construction failure, Setup returns the error
// untouched — the caller decides whether to bail (fatal) or carry on
// without tracing (warn + noop). main.go logs and falls through to
// the no-op path; tracing must never block the boot.
func Setup(ctx context.Context, o Options) (Shutdown, error) {
	if o.Logger == nil {
		return noopShutdown, errors.New("tracing.Setup: Logger is required")
	}
	if o.ServiceName == "" {
		return noopShutdown, errors.New("tracing.Setup: ServiceName is required")
	}

	endpoint := strings.TrimSpace(o.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv(EndpointEnv))
	}
	if endpoint == "" {
		// No endpoint configured — install the W3C propagator
		// anyway so the binary still threads incoming
		// `traceparent` headers through outgoing requests. The
		// trace IDs the binary stamps onto the headers are
		// random (the no-op tracer's contract) but downstream
		// services can still join them; this matches the
		// "headers always flow" rule that lets a partial
		// rollout of OTel work end-to-end.
		otel.SetTextMapPropagator(newPropagator())
		o.Logger.Info("tracing: disabled (no endpoint set)",
			slog.String("env", EndpointEnv))
		return noopShutdown, nil
	}

	// The exporter's option surface accepts a bare host:port; we
	// strip the scheme so an operator who pastes a full URL doesn't
	// get a confused-host error. The Insecure flag controls TLS;
	// schemes "http://" map to insecure, "https://" to TLS.
	insecure := o.Insecure
	stripped := endpoint
	switch {
	case strings.HasPrefix(endpoint, "http://"):
		stripped = strings.TrimPrefix(endpoint, "http://")
		insecure = true
	case strings.HasPrefix(endpoint, "https://"):
		stripped = strings.TrimPrefix(endpoint, "https://")
	}
	// The OTLP HTTP exporter wants endpoint = host[:port][/v1/traces].
	// We pass it stripped of scheme; the path is left at the
	// exporter's default ("/v1/traces"), which matches the OTLP
	// collector spec.
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(stripped),
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return noopShutdown, fmt.Errorf("tracing: build exporter: %w", err)
	}

	res, err := sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(o.ServiceName),
			semconv.ServiceVersion(o.ServiceVersion),
		),
	)
	if err != nil {
		// Resource merge errors are non-fatal (the SDK will fall
		// back to the default resource); but we DO want to log the
		// failure so an operator knows the service-name attribute
		// might be missing from spans.
		o.Logger.Warn("tracing: resource merge failed; using default resource",
			slog.Any("err", err))
		res = sdkresource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// Default batch timing is fine for production; we
			// pin it explicitly so an env-var bump to the
			// default doesn't change behavior under us.
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(newPropagator())

	o.Logger.Info("tracing: enabled",
		slog.String("endpoint", endpoint),
		slog.String("service", o.ServiceName),
		slog.Bool("insecure", insecure),
	)

	// The shutdown closer flushes the batched exporter and then
	// shuts down the tracer provider. Both Shutdown methods are
	// idempotent — calling twice is safe. We use a sync.Once-style
	// guard via a bool capture so we don't double-shutdown if the
	// orchestrator re-invokes us.
	var done bool
	return func(stopCtx context.Context) error {
		if done {
			return nil
		}
		done = true
		// Order matters: Shutdown the provider FIRST (which
		// drains the BSP) then the exporter. The provider's
		// Shutdown blocks until pending spans flush via the
		// exporter, so the exporter is implicitly drained too;
		// calling exporter.Shutdown afterwards is the documented
		// pattern and is idempotent.
		if err := tp.Shutdown(stopCtx); err != nil {
			// Wrap rather than swallow — the orchestrator
			// logs the per-step error and the boot summary
			// will surface it.
			return fmt.Errorf("tracing: provider shutdown: %w", err)
		}
		// Best-effort: the provider already drained the BSP via
		// the exporter, so this call is largely a belt-and-
		// suspenders flush. We don't propagate errors from this
		// second call — they almost always reduce to "already
		// shut down" and the orchestrator only needs one
		// authoritative status.
		_ = exporter.Shutdown(stopCtx)
		return nil
	}, nil
}

// newPropagator returns the W3C composite propagator we install
// globally. TraceContext carries traceparent + tracestate (the
// standard distributed-trace headers); Baggage carries operator-
// defined key=value pairs (typically tenant id, locale, etc.). The
// composite is the canonical choice for OTLP-emitting services.
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}
