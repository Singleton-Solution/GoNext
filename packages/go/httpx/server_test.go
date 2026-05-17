package httpx

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// quietLogger returns a slog.Logger that discards everything. Used by
// tests that don't care about log output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// testConfig returns a ServerConfig with very short timeouts so tests
// don't hang waiting for shutdown drain. Addr is :0 so the OS picks a
// free port.
func testConfig() config.ServerConfig {
	return config.ServerConfig{
		Addr:              ":0",
		ReadHeaderTimeout: 1 * time.Second,
		ReadTimeout:       1 * time.Second,
		WriteTimeout:      1 * time.Second,
		IdleTimeout:       1 * time.Second,
		ShutdownTimeout:   2 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func TestNew_ValidatesOptions(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "missing handler",
			opts: Options{Config: testConfig(), Log: quietLogger()},
			want: "Handler",
		},
		{
			name: "missing log",
			opts: Options{Config: testConfig(), Handler: http.NewServeMux()},
			want: "Log",
		},
		{
			name: "missing addr",
			opts: Options{Log: quietLogger(), Handler: http.NewServeMux()},
			want: "Addr",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.opts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err: got %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestServer_StartsAndServes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	srv, err := New(Options{
		Config:  testConfig(),
		Log:     quietLogger(),
		Handler: mux,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for bind.
	select {
	case <-srv.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready in 2s")
	}

	// Make a request.
	resp, err := http.Get("http://" + srv.Addr() + "/ping")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Errorf("body: got %q, want pong", body)
	}

	// Trigger shutdown.
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of ctx cancel")
	}
}

func TestServer_GracefulShutdown_DrainsInFlight(t *testing.T) {
	// A request that takes longer than the shutdown trigger but less than
	// the shutdown budget should complete cleanly.
	handlerDone := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		close(handlerDone)
	})

	cfg := testConfig()
	cfg.WriteTimeout = 3 * time.Second // headroom for the slow handler
	cfg.ShutdownTimeout = 2 * time.Second

	srv, err := New(Options{
		Config:  cfg,
		Log:     quietLogger(),
		Handler: mux,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	<-srv.Ready()

	// Start the slow request in a goroutine.
	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/slow")
		if err != nil {
			respCh <- -1
			return
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		respCh <- resp.StatusCode
	}()

	// Give the handler ~50ms to start, then trigger shutdown.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case status := <-respCh:
		if status != http.StatusOK {
			t.Errorf("in-flight request status: got %d, want 200", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request did not complete during drain")
	}

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Error("handler did not complete")
	}

	if err := <-runErr; err != nil {
		t.Errorf("Run: %v", err)
	}
}

func TestServer_PortConflict_Errors(t *testing.T) {
	// First server takes a port.
	cfg := testConfig()
	srv1, err := New(Options{
		Config:  cfg,
		Log:     quietLogger(),
		Handler: http.NewServeMux(),
	})
	if err != nil {
		t.Fatalf("New 1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv1.Run(ctx)
	<-srv1.Ready()

	// Second server tries the same port.
	cfg.Addr = srv1.Addr()
	srv2, err := New(Options{
		Config:  cfg,
		Log:     quietLogger(),
		Handler: http.NewServeMux(),
	})
	if err != nil {
		t.Fatalf("New 2: %v", err)
	}

	err = srv2.Run(context.Background())
	if err == nil {
		t.Error("expected port-in-use error")
	} else if !strings.Contains(err.Error(), "listen") {
		t.Errorf("error should mention 'listen', got: %v", err)
	}
}

func TestServer_MiddlewareApplied(t *testing.T) {
	// Use a marker middleware that adds a header so we can verify the chain
	// actually ran around the handler.
	marker := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Marker", "applied")
			next.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv, err := New(Options{
		Config:      testConfig(),
		Log:         quietLogger(),
		Handler:     mux,
		Middlewares: []Middleware{marker, RequestID()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx)
	<-srv.Ready()

	resp, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Test-Marker") != "applied" {
		t.Error("marker middleware did not run")
	}
	if resp.Header.Get(HeaderRequestID) == "" {
		t.Error("RequestID middleware did not run")
	}
}

func TestChain_AppliesInOrder(t *testing.T) {
	// Outer middleware should see the inner one's modifications on the way
	// OUT, not on the way in.
	var order []string

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "outer-in")
			next.ServeHTTP(w, r)
			order = append(order, "outer-out")
		})
	}
	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "inner-in")
			next.ServeHTTP(w, r)
			order = append(order, "inner-out")
		})
	}
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "core")
	})

	h := Chain(core, outer, inner)
	h.ServeHTTP(nil, &http.Request{})

	want := []string{"outer-in", "inner-in", "core", "inner-out", "outer-out"}
	if len(order) != len(want) {
		t.Fatalf("order: got %v, want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("[%d]: got %q, want %q", i, order[i], w)
		}
	}
}

func TestDrainTimeout_DefaultsWhenZero(t *testing.T) {
	if got := drainTimeout(config.ServerConfig{ShutdownTimeout: 0}); got != 30*time.Second {
		t.Errorf("zero ShutdownTimeout: got %v, want 30s default", got)
	}
	if got := drainTimeout(config.ServerConfig{ShutdownTimeout: -5 * time.Second}); got != 30*time.Second {
		t.Errorf("negative ShutdownTimeout: got %v, want 30s default", got)
	}
	if got := drainTimeout(config.ServerConfig{ShutdownTimeout: 10 * time.Second}); got != 10*time.Second {
		t.Errorf("positive ShutdownTimeout: got %v, want passthrough", got)
	}
}

// Silence the "imported but not used" complaint for bytes if no test
// happens to need it after refactors. (Currently uses it implicitly via
// log/slog handlers in subtests.)
var _ = bytes.NewBufferString
