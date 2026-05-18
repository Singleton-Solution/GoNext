package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Middleware returns an HTTP middleware that registers and updates the
// canonical http_* metric family against reg.
//
// reg may be nil; prometheus.DefaultRegisterer is used in that case.
// Most callers should pass *prometheus.Registry from
// metrics.Registry.Prometheus() so /metrics stays isolated from any
// global state.
//
// The returned middleware is safe for concurrent use. It registers
// each metric family exactly once per call to Middleware — calling
// Middleware twice against the same registerer will panic on duplicate
// registration, matching prometheus.MustRegister semantics.
//
// Wire it into the chain AFTER Recovery / RequestID / Logger:
//
//	httpx.Chain(handler,
//	    httpx.Recovery(logger),
//	    httpx.RequestID(),
//	    httpx.Logger(logger),
//	    metrics.Middleware(reg),
//	)
func Middleware(reg prometheus.Registerer) func(http.Handler) http.Handler {
	c := newCollectors(reg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := RouteLabel(r)
			method := r.Method

			inflight := c.inflight.WithLabelValues(method, route)
			inflight.Inc()
			defer inflight.Dec()

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			start := time.Now()
			defer func() {
				dur := time.Since(start).Seconds()
				c.requestDuration.WithLabelValues(method, route).Observe(dur)
				c.requestsTotal.WithLabelValues(method, route, strconv.Itoa(rw.status)).Inc()
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the response
// status code. Defaults to 200 so handlers that never call WriteHeader
// (e.g. ones that only call Write) record the status net/http would
// have sent on the wire.
//
// The wrapper is intentionally local to this package: httpx.responseWriter
// is a sibling, not exported, and the metrics middleware's needs (status
// only, no byte count) are narrower. Duplicating ~15 lines keeps the
// import graph honest.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *statusRecorder) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *statusRecorder) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		// Mark headers sent without overriding the default 200. This
		// mirrors net/http behavior where the first Write implicitly
		// commits a 200 OK.
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController
// (Hijack, Flush, Push, etc.) reaches through the wrapper. Required
// for SSE handlers, WebSocket upgrades, and anything else that needs
// the unwrapped writer.
func (rw *statusRecorder) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// Flush forwards to the underlying writer if it supports http.Flusher.
// Streaming handlers (SSE, NDJSON) rely on this; without it, type
// assertions to http.Flusher on the wrapped writer would fail and the
// handler would buffer forever.
func (rw *statusRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
