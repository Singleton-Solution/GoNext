package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// Middleware wraps an http.Handler. The outer-most middleware in a chain
// is the first to run on a request (and the last to run on the response,
// e.g. for recovery / logging). Middleware composes via the standard
// onion-skin pattern; no special framework is needed.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares around h, applying them so the FIRST element
// of mws is the OUTERMOST wrapper. This matches the natural reading order:
//
//	httpx.Chain(handler, Recovery, RequestID, Logger)
//
// means: when a request comes in, Recovery runs first (catches any panic
// inside), then RequestID assigns the ID and stores it on the context,
// then Logger starts its timer, then the actual handler runs.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Options configure a Server. Handler is the root handler (often a
// *http.ServeMux). Middlewares are applied in order (outermost first).
type Options struct {
	Config      config.ServerConfig
	Log         *slog.Logger
	Handler     http.Handler
	Middlewares []Middleware
}

// Server runs an HTTP listener with graceful shutdown.
//
// Lifecycle:
//  1. New() validates options and builds the http.Server.
//  2. Run(ctx) starts Listen + Serve in a goroutine.
//  3. Run blocks until any of: ctx is canceled, SIGINT/SIGTERM arrives,
//     or Serve returns an error.
//  4. On shutdown trigger, http.Server.Shutdown is called with a budget
//     equal to cfg.ShutdownTimeout. New connections are refused; in-flight
//     requests get the drain window to complete.
//  5. Run returns nil on clean shutdown or the underlying error.
type Server struct {
	cfg     config.ServerConfig
	log     *slog.Logger
	handler http.Handler
	srv     *http.Server

	// readyOnce guards the readiness channel: published exactly once when
	// the listener is bound, so tests can wait for the bind before issuing
	// requests without polling.
	readyMu   sync.Mutex
	readyCh   chan struct{}
	boundAddr string
}

// New builds a Server. Returns an error if Options is invalid (nil
// handler, missing log, zero address).
func New(opts Options) (*Server, error) {
	if opts.Handler == nil {
		return nil, errors.New("httpx.New: Handler is required")
	}
	if opts.Log == nil {
		return nil, errors.New("httpx.New: Log is required")
	}
	if opts.Config.Addr == "" {
		return nil, errors.New("httpx.New: Config.Addr is required")
	}

	handler := Chain(opts.Handler, opts.Middlewares...)

	srv := &http.Server{
		Addr:              opts.Config.Addr,
		Handler:           handler,
		ReadHeaderTimeout: opts.Config.ReadHeaderTimeout,
		ReadTimeout:       opts.Config.ReadTimeout,
		WriteTimeout:      opts.Config.WriteTimeout,
		IdleTimeout:       opts.Config.IdleTimeout,
		MaxHeaderBytes:    opts.Config.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(opts.Log.Handler(), slog.LevelError),
	}

	return &Server{
		cfg:     opts.Config,
		log:     opts.Log,
		handler: handler,
		srv:     srv,
		readyCh: make(chan struct{}),
	}, nil
}

// Run starts the server and blocks until shutdown.
//
// Shutdown is triggered by any of:
//   - ctx being canceled by the caller
//   - SIGINT (Ctrl-C) or SIGTERM (kubectl, docker stop) being received
//   - the listener encountering an unrecoverable error
//
// On shutdown, http.Server.Shutdown drains in-flight requests within the
// ShutdownTimeout budget. If the budget expires, hung connections are
// closed forcibly and Run returns the timeout error.
//
// Run is safe to call once per Server instance. Calling it twice is
// undefined.
func (s *Server) Run(ctx context.Context) error {
	// Bind the listener up front so we know which port we're on (handy when
	// Addr was ":0" for tests) and so the readiness signal fires after a
	// real bind.
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("httpx.Server.Run: listen %q: %w", s.srv.Addr, err)
	}
	s.readyMu.Lock()
	s.boundAddr = ln.Addr().String()
	close(s.readyCh)
	s.readyMu.Unlock()

	s.log.Info("http server listening",
		slog.String("addr", s.boundAddr),
		slog.Duration("read_timeout", s.cfg.ReadTimeout),
		slog.Duration("write_timeout", s.cfg.WriteTimeout),
		slog.Duration("shutdown_timeout", s.cfg.ShutdownTimeout),
	)

	// signalCtx fires when SIGINT/SIGTERM arrives. Cleanup stops the signal
	// handler regardless of how we exit (signal received, parent ctx canceled,
	// or Serve errors).
	signalCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// Run Serve in a goroutine so we can race it against signalCtx.
	serveErr := make(chan error, 1)
	go func() {
		err := s.srv.Serve(ln)
		// http.ErrServerClosed is the expected error after Shutdown is
		// called; treat it as a clean exit.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		// Serve returned (probably an error). No need to shutdown — it's
		// already done.
		return err
	case <-signalCtx.Done():
		// Either the caller's ctx was canceled or a signal arrived. Drain.
		reason := "context canceled"
		if errors.Is(signalCtx.Err(), context.Canceled) && ctx.Err() == nil {
			reason = "signal received"
		}
		s.log.Info("http server shutting down", slog.String("reason", reason))
	}

	// Detached shutdown context so we honor ShutdownTimeout even if the
	// parent ctx is already canceled.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		// Drain budget exceeded; remaining connections are forcibly closed.
		s.log.Warn("http server shutdown exceeded budget",
			slog.Duration("budget", s.cfg.ShutdownTimeout),
			slog.String("err", err.Error()),
		)
		return fmt.Errorf("httpx.Server.Run: shutdown: %w", err)
	}

	// Drain the serve goroutine; it should have returned by now via
	// ErrServerClosed (translated to nil above).
	if err := <-serveErr; err != nil {
		return err
	}
	s.log.Info("http server stopped")
	return nil
}

// Ready returns a channel that closes once the listener is bound.
// Tests use this to wait for the server to be ready without polling.
// In production, Run logs "http server listening" at the same point.
func (s *Server) Ready() <-chan struct{} {
	return s.readyCh
}

// Shutdown gracefully drains the underlying http.Server using the
// supplied ctx as the deadline. This is the integration point for
// the shutdown orchestrator (packages/go/shutdown), which prefers to
// own the signal handling and budget itself.
//
// When the orchestrator drives shutdown, callers typically wire it
// like so in main():
//
//	go func() { _ = srv.Run(serverCtx) }()
//	orch.Register("http.server", srv.Shutdown)
//
// where serverCtx is canceled by the orchestrator just before it
// invokes srv.Shutdown. That ordering — cancel Run's context to break
// its own signal wait, then Shutdown for the actual drain — keeps the
// existing single-binary behavior working and lets the orchestrator
// take over when the binary has more than HTTP to drain.
//
// Shutdown forwards to http.Server.Shutdown verbatim and never blocks
// past ctx's deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Addr returns the bound listener address. Only meaningful after Ready
// fires (before then, returns the configured address which may have a
// port of 0).
func (s *Server) Addr() string {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	if s.boundAddr != "" {
		return s.boundAddr
	}
	return s.cfg.Addr
}

// drainTimeout returns the shutdown timeout, falling back to 30s if the
// caller misconfigured a zero value. Defensive: a zero shutdown timeout
// would skip the drain entirely, which is rarely what you want.
func drainTimeout(cfg config.ServerConfig) time.Duration {
	if cfg.ShutdownTimeout <= 0 {
		return 30 * time.Second
	}
	return cfg.ShutdownTimeout
}
