package plugin

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBuildLogsURL covers the http→ws rewrite and path composition. A
// misformed URL here would point the tailer at the wrong endpoint.
func TestBuildLogsURL(t *testing.T) {
	cases := []struct {
		host, plugin, want string
		wantErr            bool
	}{
		{"http://localhost:8080", "blog", "ws://localhost:8080/_/plugins/dev/logs/blog", false},
		{"https://example.com", "shop", "wss://example.com/_/plugins/dev/logs/shop", false},
		{"http://localhost:8080/", "blog", "ws://localhost:8080/_/plugins/dev/logs/blog", false},
		{"ws://h", "p", "ws://h/_/plugins/dev/logs/p", false},
		{"ftp://x", "p", "", true},
		{"http://localhost", "", "", true},
	}
	for _, tc := range cases {
		got, err := buildLogsURL(tc.host, tc.plugin)
		if (err != nil) != tc.wantErr {
			t.Errorf("buildLogsURL(%q,%q): err=%v wantErr=%v", tc.host, tc.plugin, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("buildLogsURL(%q,%q): got %q want %q", tc.host, tc.plugin, got, tc.want)
		}
	}
}

// TestLevelColor exercises the ANSI color mapping. We don't pin the
// exact escape sequences (terminal compatibility might force a tweak)
// but every named level should map to a non-empty string and unknown
// levels should produce empty output.
func TestLevelColor(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if levelColor(level) == "" {
			t.Errorf("levelColor(%q): expected non-empty", level)
		}
	}
	if levelColor("trace") != "" {
		t.Errorf("levelColor(trace): expected empty for unknown")
	}
}

// TestRenderLogLine exercises the formatting path end-to-end. Output
// must contain the formatted timestamp, plugin, level, and message
// surrounded by the expected ANSI markers.
func TestRenderLogLine(t *testing.T) {
	evt := logEventLite{
		Timestamp: time.Date(2026, 5, 17, 12, 34, 56, int(123*time.Millisecond/time.Nanosecond), time.UTC),
		Plugin:    "blog",
		Level:     "warn",
		Message:   "low disk space",
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	renderLogLine(&buf, payload)
	out := buf.String()
	for _, want := range []string{"12:34:56.123", "WARN", "blog", "low disk space"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderLogLine_MalformedFallsBack confirms that a non-JSON payload
// is still printed verbatim rather than dropped — operators should
// always see SOMETHING when the wire format drifts.
func TestRenderLogLine_MalformedFallsBack(t *testing.T) {
	var buf bytes.Buffer
	renderLogLine(&buf, []byte("not json"))
	if !strings.Contains(buf.String(), "not json") {
		t.Errorf("malformed fallback: got %q", buf.String())
	}
}

// TestWsLogTailer_RoundTrip is the integration test for the CLI tailer.
// It stands up an httptest server that does the WebSocket handshake
// and emits 5 events as text frames, then verifies the tailer prints
// each one. The test mirrors what the dev host's LogStreamHandler will
// do once both pieces are wired.
func TestWsLogTailer_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "not a ws upgrade", http.StatusBadRequest)
			return
		}
		key := r.Header.Get("Sec-WebSocket-Key")
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		accept := acceptKey(key)
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
		_, _ = rw.WriteString(resp)
		_ = rw.Flush()

		// Send 5 text frames.
		for i := 0; i < 5; i++ {
			payload := fmt.Sprintf(`{"ts":"2026-05-17T12:34:56Z","plugin":"blog","level":"info","msg":"hello-%d"}`, i)
			frame := buildTextFrame([]byte(payload))
			if _, err := conn.Write(frame); err != nil {
				return
			}
		}
		// Hold the connection open until the test client disconnects.
		_, _ = io.Copy(io.Discard, conn)
	}))
	defer srv.Close()

	var (
		buf bytes.Buffer
		mu  sync.Mutex
	)
	out := &lockedBuf{b: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tailer := wsLogTailer{}
	err := make(chan error, 1)
	go func() {
		err <- tailer.Tail(ctx, srv.URL, "blog", out)
	}()

	// Spin until we've seen all 5 lines or the deadline fires.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := buf.String()
		mu.Unlock()
		if strings.Count(s, "hello-") >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-err

	mu.Lock()
	out2 := buf.String()
	mu.Unlock()
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf("hello-%d", i)
		if !strings.Contains(out2, want) {
			t.Errorf("missing %q in output:\n%s", want, out2)
		}
	}
}

// TestStartLogTail_NoLogsFlag confirms the tail goroutine is NOT
// spawned when --logs is unset, even if a Tailer is wired. A spurious
// connection would fail the dev loop with "connection refused"
// every time the operator runs without the flag.
func TestStartLogTail_NoLogsFlag(t *testing.T) {
	called := false
	opts := devOptions{
		Logs:   false,
		Tailer: stubTailer{onCall: func() { called = true }},
		Now:    time.Now,
	}
	done := startLogTail(context.Background(), opts, io.Discard, io.Discard)
	if done != nil {
		t.Error("startLogTail returned non-nil done channel when Logs=false")
	}
	if called {
		t.Error("Tailer.Tail was called when Logs=false")
	}
}

type stubTailer struct {
	onCall func()
}

func (s stubTailer) Tail(ctx context.Context, host, plugin string, out io.Writer) error {
	if s.onCall != nil {
		s.onCall()
	}
	<-ctx.Done()
	return nil
}

func buildTextFrame(payload []byte) []byte {
	const (
		opcodeText = 0x1
		finBit     = 0x80
	)
	header := []byte{finBit | opcodeText}
	n := len(payload)
	switch {
	case n <= 125:
		header = append(header, byte(n))
	case n <= 0xFFFF:
		ext := []byte{0, 0}
		binary.BigEndian.PutUint16(ext, uint16(n))
		header = append(header, 126)
		header = append(header, ext...)
	default:
		ext := []byte{0, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint64(ext, uint64(n))
		header = append(header, 127)
		header = append(header, ext...)
	}
	return append(header, payload...)
}

func acceptKey(key string) string {
	const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(guid))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type lockedBuf struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

