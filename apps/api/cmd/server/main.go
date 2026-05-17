// Command server is the GoNext HTTP API server.
//
// It loads configuration from the environment, sets up structured logging,
// builds the HTTP router, applies the middleware chain, and serves until
// interrupted by SIGINT/SIGTERM (the standard container lifecycle) or
// until the parent context is canceled.
//
// Exit codes:
//
//	0 — clean shutdown after Run completed without error.
//	1 — configuration or startup error before serving began.
//	2 — server error during run (port conflict, drain timeout, etc.).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
)

const serviceName = "api"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(2)
	}
}

// run is main() with a returned error, so it's testable and unit-tests
// don't need to swap os.Exit.
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	bi := buildinfo.Get(serviceName)
	logger, err := log.Setup(os.Stdout, log.Options{
		Service:   serviceName,
		Version:   bi.Version,
		Commit:    bi.Commit,
		Level:     parseLogLevel(cfg.Log.Level),
		Format:    log.Format(cfg.Log.Format),
		AddSource: cfg.Log.AddSource,
		Redact:    true,
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	logger.Info("starting",
		"env", string(cfg.Env),
		"addr", cfg.Server.Addr,
		"go_version", bi.GoVersion,
	)

	pool, err := db.New(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer pool.Close()

	mux := buildRouter(cfg)

	srv, err := httpx.New(httpx.Options{
		Config:  cfg.Server,
		Log:     logger,
		Handler: mux,
		Middlewares: []httpx.Middleware{
			httpx.Recovery(logger),
			httpx.RequestID(),
			httpx.Logger(logger, "/healthz", "/readyz"),
		},
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	return srv.Run(ctx)
}

// buildRouter assembles the HTTP route table. Subsequent issues mount
// real routes here; for now we serve a single root endpoint that returns
// the binary's identity, as required by issue #2's AC.
func buildRouter(_ *config.Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		bi := buildinfo.Get(serviceName)
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    "gonext",
			"version": bi.Version,
			"commit":  bi.Commit,
		})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// parseLogLevel maps the config string to slog.Level. Unknown values
// fall back to INFO; log.OptionsFromEnv applies the same rule, but
// since main.go uses log.Setup directly (with explicit Options) we
// repeat the mapping here.
func parseLogLevel(s string) slog.Level {
	switch s {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
