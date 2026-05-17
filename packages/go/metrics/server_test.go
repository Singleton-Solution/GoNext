package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistry_Handler_RespondsInPromFormat(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("gonext_test_handler_total", "h", "l")
	c.WithLabelValues("a").Inc()

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	// Prometheus exposition format starts with text/plain; version=...
	// Either text/plain (legacy) or the openmetrics content-type is acceptable.
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "openmetrics") {
		t.Errorf("Content-Type: got %q, want prometheus exposition format", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	bs := string(body)

	// Our counter should show up.
	if !strings.Contains(bs, "gonext_test_handler_total") {
		t.Errorf("body missing counter, got:\n%s", bs)
	}
	// HELP and TYPE lines are part of the exposition format.
	if !strings.Contains(bs, "# HELP gonext_test_handler_total") {
		t.Error("missing HELP line for counter")
	}
	if !strings.Contains(bs, "# TYPE gonext_test_handler_total counter") {
		t.Error("missing TYPE line for counter")
	}
	// Default collectors should also appear.
	if !strings.Contains(bs, "go_goroutines") {
		t.Error("missing go_goroutines (default collector)")
	}
}

func TestServeMetrics_BindsAndShutsDown(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("gonext_test_serve_total", "s").WithLabelValues()

	// :0 lets the OS pick a free port. The returned server's Addr field
	// is set to the bound address by ServeMetrics.
	srv, shutdown, err := r.ServeMetrics("127.0.0.1:0", quietLogger())
	if err != nil {
		t.Fatalf("ServeMetrics: %v", err)
	}
	if srv == nil {
		t.Fatal("server is nil")
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	if srv.Addr == "" {
		t.Fatal("server Addr is empty; expected bound address")
	}

	addr := srv.Addr

	// /healthz is light, used to confirm the listener is up.
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status: got %d, want 200", resp.StatusCode)
	}

	// /metrics should serve prom format and include our counter.
	resp2, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/metrics status: got %d, want 200", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	bs := string(body)
	if !strings.Contains(bs, "gonext_test_serve_total") {
		t.Error("/metrics body missing test counter")
	}
	if !strings.Contains(bs, "go_goroutines") {
		t.Error("/metrics body missing default collectors")
	}

	// Shutdown should be clean and not return an error.
	if err := shutdown(); err != nil {
		t.Errorf("shutdown: %v", err)
	}

	// After shutdown, follow-up requests should fail to connect.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if _, err := client.Get("http://" + addr + "/metrics"); err == nil {
		t.Error("expected GET after shutdown to fail")
	}
}

func TestServeMetrics_PortInUse_ReturnsError(t *testing.T) {
	r := NewRegistry()
	srv1, shutdown1, err := r.ServeMetrics("127.0.0.1:0", quietLogger())
	if err != nil {
		t.Fatalf("first ServeMetrics: %v", err)
	}
	defer shutdown1()

	addr := srv1.Addr
	if addr == "" {
		t.Fatal("first server has no bound address")
	}

	r2 := NewRegistry()
	_, _, err = r2.ServeMetrics(addr, quietLogger())
	if err == nil {
		t.Fatal("expected port-in-use error")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("error should mention 'listen', got: %v", err)
	}
}

func TestServeMetrics_NilLoggerErrors(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.ServeMetrics(":0", nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
	if !strings.Contains(err.Error(), "logger") {
		t.Errorf("error should mention 'logger', got: %v", err)
	}
}

func TestServeMetrics_EmptyAddrErrors(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.ServeMetrics("", quietLogger())
	if err == nil {
		t.Fatal("expected error for empty addr")
	}
	if !strings.Contains(err.Error(), "addr") {
		t.Errorf("error should mention 'addr', got: %v", err)
	}
}
