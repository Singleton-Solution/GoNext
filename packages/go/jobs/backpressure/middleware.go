package backpressure

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// Metric name. The gonext_backpressure_ prefix mirrors the gonext_jobs_
// family from jobs/asynq. Counter ends in _total per the Prometheus
// convention. This name is part of the project's observability
// contract — renaming it is a breaking change for dashboards and
// alerts.
const metricShedTotal = "gonext_backpressure_shed_total"

// Resolver extracts the queue and priority for an inbound request.
// Wired by the caller because the URL/header convention differs per
// endpoint (the webhook delivery endpoint reads X-Backpressure-Queue;
// internal enqueue endpoints encode it in the path; etc.). Returning
// an empty queue tells the middleware to admit the request without
// consulting the Gate — useful for diagnostic endpoints that share a
// mux prefix but aren't enqueue endpoints.
type Resolver func(*http.Request) (queue string, priority Priority)

// Middleware wraps an HTTP handler with the backpressure gate. On
// admission it calls through to next; on shed it responds 429 Too
// Many Requests, increments gonext_backpressure_shed_total, and logs
// at warn level. The struct holds its own Prometheus collector so
// each call site can wire its own registry; the typical wiring is one
// Middleware per /enqueue mux.
type Middleware struct {
	gate     *Gate
	resolver Resolver
	logger   *slog.Logger
	shed     *prometheus.CounterVec
}

// NewMiddleware constructs a Middleware. reg may be nil (the counter
// is built but not registered, so callers in tests can introspect it
// without contaminating the default registry). logger may be nil.
//
// resolver and gate are required; we'd rather panic at boot than nil-
// dereference inside the hot path. (Tests can use a stub resolver.)
func NewMiddleware(gate *Gate, resolver Resolver, logger *slog.Logger, reg prometheus.Registerer) *Middleware {
	if gate == nil {
		panic("backpressure.NewMiddleware: gate is required")
	}
	if resolver == nil {
		panic("backpressure.NewMiddleware: resolver is required")
	}
	shed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: metricShedTotal,
		Help: "Total number of enqueue requests shed by the backpressure gate, by queue and priority.",
	}, []string{"queue", "priority"})
	if reg != nil {
		reg.MustRegister(shed)
	}
	return &Middleware{
		gate:     gate,
		resolver: resolver,
		logger:   logger,
		shed:     shed,
	}
}

// Handler returns the http.Handler that fronts next with the gate.
// Returns next unchanged when the Middleware is nil so callers can
// wire conditional backpressure without branching at the call site.
func (mw *Middleware) Handler(next http.Handler) http.Handler {
	if mw == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queue, priority := mw.resolver(r)
		if queue == "" {
			next.ServeHTTP(w, r)
			return
		}
		if err := mw.gate.Allow(queue, priority); err != nil {
			mw.shed.WithLabelValues(queue, priority.String()).Inc()
			if mw.logger != nil {
				mw.logger.Warn("backpressure: shedding enqueue request",
					slog.String("queue", queue),
					slog.String("priority", priority.String()),
					slog.String("err", err.Error()),
				)
			}
			// Retry-After: 1 gives operationally sensible guidance to
			// well-behaved clients (back off, then retry). The value
			// is a hint, not a contract — clients that respect it
			// reduce thundering-herd; clients that don't are no
			// worse off than before.
			w.Header().Set("Retry-After", "1")
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IsShed reports whether err originated from the backpressure gate.
// Provided as a convenience so callers don't have to import the
// errors package alongside this one. errors.Is(err, ErrShed) is the
// canonical check; IsShed is the readable shorthand.
func IsShed(err error) bool {
	return errors.Is(err, ErrShed)
}
