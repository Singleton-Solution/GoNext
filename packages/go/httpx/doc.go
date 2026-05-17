// Package httpx is the HTTP server chassis for GoNext binaries that serve
// HTTP traffic (currently apps/api).
//
// What's here:
//
//   - Server: wraps net/http.Server with config-driven timeouts, a
//     middleware chain, structured logging via packages/go/log, and
//     a Run(ctx) entry point that performs graceful shutdown when ctx
//     is canceled or a signal arrives.
//
//   - Middleware: RequestID, Logger, Recovery. Each is a standard
//     func(http.Handler) http.Handler — composable with any third-party
//     middleware that uses the same shape.
//
// The router is intentionally the standard library's http.ServeMux (Go
// 1.22+ method routing). Adopters who want chi, gorilla/mux, etc. can pass
// any http.Handler as the root handler.
//
// Typical wiring in cmd/server/main.go:
//
//	cfg, err := config.Load()
//	if err != nil { ... }
//	logger, err := log.Setup(os.Stdout, log.OptionsFromEnv("api"))
//	if err != nil { ... }
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /", rootHandler(cfg))
//
//	srv := httpx.New(httpx.Options{
//	    Config:  cfg.Server,
//	    Log:     logger,
//	    Handler: mux,
//	    Middlewares: []httpx.Middleware{
//	        httpx.Recovery(logger),
//	        httpx.RequestID(),
//	        httpx.Logger(logger),
//	    },
//	})
//
//	if err := srv.Run(ctx); err != nil { ... }
//
// See docs/05-admin-api.md §3 (REST API surface) and docs/13-security-baseline.md
// §2 (HTTP security headers) — header middleware is intentionally NOT in
// this package; it belongs in packages/go/middleware/security so the
// httpx chassis stays minimal.
package httpx
