// Package earlyhints emits HTTP/1.1 + HTTP/2 "103 Early Hints" responses
// carrying Link: rel=preload headers before the real 200 response body is
// generated. Browsers that support 103 (Chrome 103+, Firefox 110+, Safari
// 17+) begin fetching those assets while the server is still rendering
// the response, typically shaving 50-200ms off Largest Contentful Paint
// on theme-rendered pages.
//
// The middleware is request-scoped: a HintsProvider inspects the
// *http.Request and returns the asset list (Hint values) for that route.
// The middleware turns each Hint into a Link header value and flushes the
// 103 status via http.ResponseController.EnableEarlyHints +
// WriteEarlyHints (Go 1.21+). The inner handler then runs unchanged; its
// final 200 (or other status) is delivered as usual.
//
// Failure-mode policy:
//
//   - Provider returns 0 hints → no 103 is sent. The inner handler runs
//     immediately. No allocations beyond the provider call.
//   - Provider returns an error → middleware logs at WARN and falls
//     through cleanly. Early Hints are a performance optimization and
//     MUST NOT break the request when the source of truth (e.g. the
//     theme provider's CSS lookup) fails.
//   - Request is HTTP/1.0 (no support for interim responses) → middleware
//     skips the 103 entirely. RFC 8297 §2 forbids sending interim
//     responses to HTTP/1.0 clients.
//   - The underlying ResponseWriter does not support WriteEarlyHints
//     (rare; only happens behind exotic test recorders) → the middleware
//     skips the 103. This is silent on purpose: WriteEarlyHints failing
//     is not actionable for the operator and we already logged when the
//     handler was wired.
//
// Wiring example (apps/api/cmd/server/main.go):
//
//	mux := buildRouter(...)
//	provider := earlyhints.NewStaticProvider(map[string][]earlyhints.Hint{
//	    "/": {{URL: "/static/style.css", As: "style"}},
//	})
//	srv, _ := httpx.New(httpx.Options{
//	    Handler: mux,
//	    Middlewares: []httpx.Middleware{
//	        httpx.Recovery(logger),
//	        earlyhints.Middleware(provider, earlyhints.Options{Logger: logger}),
//	        httpx.RequestID(),
//	        httpx.Logger(logger),
//	    },
//	})
//
// Note the ordering: earlyhints sits AFTER Recovery (so a panicking
// provider does not crash the server) but BEFORE RequestID/Logger. We
// want the 103 flushed as early as possible — the log lines and request
// IDs are about the final 200, not the interim 103.
//
// Disabling: callers should gate the entire middleware on the
// Config.PerformanceEarlyHints toggle. When the toggle is false, do not
// add Middleware to the chain at all. The middleware itself does not
// re-check the toggle on every request.
package earlyhints
