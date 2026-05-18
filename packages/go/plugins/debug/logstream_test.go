package debug

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLogHub_FanoutInOrder is the pub/sub contract check: N events
// published in order arrive at the subscriber in the same order. A
// regression here would reorder dev-tool output non-deterministically.
func TestLogHub_FanoutInOrder(t *testing.T) {
	hub := NewLogHub(16)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := hub.Subscribe(ctx, "blog")

	const N = 8
	for i := 0; i < N; i++ {
		hub.Publish(LogEvent{
			Timestamp: time.Now(),
			Plugin:    "blog",
			Level:     "info",
			Message:   fmt.Sprintf("msg-%d", i),
		})
	}

	for i := 0; i < N; i++ {
		select {
		case evt := <-sub:
			if want := fmt.Sprintf("msg-%d", i); evt.Message != want {
				t.Fatalf("msg[%d]: got %q want %q", i, evt.Message, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for msg-%d", i)
		}
	}
}

// TestLogHub_PluginFilter verifies that a subscriber for "blog" does
// NOT receive events from "shop". Cross-plugin leakage would expose
// one author's logs to another.
func TestLogHub_PluginFilter(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	blogSub := hub.Subscribe(ctx, "blog")
	hub.Publish(LogEvent{Plugin: "shop", Message: "hidden"})
	hub.Publish(LogEvent{Plugin: "blog", Message: "visible"})

	select {
	case evt := <-blogSub:
		if evt.Message != "visible" {
			t.Errorf("first message: got %q want visible", evt.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	select {
	case evt := <-blogSub:
		t.Fatalf("unexpected second event: %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// success: nothing else delivered
	}
}

// TestLogHub_AllPluginsSubscriber checks the "" filter path: a
// subscriber asking for all plugins should receive events from every
// origin. Daemon-wide log sinks rely on this.
func TestLogHub_AllPluginsSubscriber(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	all := hub.Subscribe(ctx, "")
	hub.Publish(LogEvent{Plugin: "shop", Message: "a"})
	hub.Publish(LogEvent{Plugin: "blog", Message: "b"})

	gotPlugins := []string{}
	for i := 0; i < 2; i++ {
		select {
		case evt := <-all:
			gotPlugins = append(gotPlugins, evt.Plugin)
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}
	if !contains(gotPlugins, "shop") || !contains(gotPlugins, "blog") {
		t.Errorf("got plugins %v, want both shop and blog", gotPlugins)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestLogHub_CtxCancelEvicts is the cleanup contract: cancelling the
// subscriber's context closes the channel and removes the entry from
// the hub. A leak here would slowly grow the subscriber set across a
// long dev session.
func TestLogHub_CtxCancelEvicts(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	sub := hub.Subscribe(ctx, "blog")
	cancel()

	// The cleanup goroutine should close the channel shortly.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-sub:
			if !ok {
				goto done
			}
		case <-deadline:
			t.Fatal("channel not closed after ctx cancel")
		}
	}
done:

	hub.subsMu.RLock()
	got := len(hub.subs)
	hub.subsMu.RUnlock()
	if got != 0 {
		t.Errorf("hub.subs after evict: got %d entries, want 0", got)
	}
}

// TestLogHub_NoBlockOnSlowSubscriber is the back-pressure contract.
// Publishers MUST NOT block on a full subscriber buffer; instead the
// subscriber is marked lagged and the next opportunity emits a warning
// event. This protects every plugin's gn_log from a single slow
// subscriber.
func TestLogHub_NoBlockOnSlowSubscriber(t *testing.T) {
	hub := NewLogHub(2)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe but don't drain — buffer fills after 2 publishes.
	sub := hub.Subscribe(ctx, "")

	const N = 50
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for i := 0; i < N; i++ {
			hub.Publish(LogEvent{Plugin: "blog", Message: "x"})
		}
	}()

	select {
	case <-doneCh:
		// success — publishers returned despite a stuck subscriber
	case <-time.After(time.Second):
		t.Fatal("publishers blocked on slow subscriber")
	}

	// Drain at least one event to confirm subsequent publishes can
	// resume after the buffer drains.
	select {
	case <-sub:
	case <-time.After(time.Second):
		t.Fatal("no events available")
	}
}

// TestLogHub_LaggedWarnEmitted confirms a subscriber whose buffer
// overflowed eventually receives the synthetic "lagged" event so the
// operator knows messages were dropped. The exact interleaving of the
// warning vs. subsequent events depends on buffer size; with a buf=2
// and one drain in between, the next publish has room for the warn.
func TestLogHub_LaggedWarnEmitted(t *testing.T) {
	hub := NewLogHub(2)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := hub.Subscribe(ctx, "blog")

	// Fill, then overflow.
	hub.Publish(LogEvent{Plugin: "blog", Message: "a"})
	hub.Publish(LogEvent{Plugin: "blog", Message: "b"})
	hub.Publish(LogEvent{Plugin: "blog", Message: "drop-me"}) // overflow → lagged

	// Drain both buffered events.
	if evt := <-sub; evt.Message != "a" {
		t.Fatalf("first: got %q", evt.Message)
	}
	if evt := <-sub; evt.Message != "b" {
		t.Fatalf("second: got %q", evt.Message)
	}
	// Next publish: buffer empty, lagged=true → warn pushed, then "c"
	// pushed. We should see warn followed by "c".
	hub.Publish(LogEvent{Plugin: "blog", Message: "c"})

	got1 := <-sub
	got2 := <-sub
	if !(got1.Level == "warn" && strings.Contains(got1.Message, "lagged")) {
		t.Errorf("first arrival: got %+v, want lagged warn", got1)
	}
	if got2.Message != "c" {
		t.Errorf("second arrival: got %q, want c", got2.Message)
	}
}

// TestLogHub_AsPublisher verifies the runtime.LogPublisher adapter
// converts (module, level, message) tuples into LogEvent values
// correctly. The level mapping must follow the LevelName table — a
// regression would mis-tag info logs as info, but warns as info too.
func TestLogHub_AsPublisher(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := hub.Subscribe(ctx, "")

	p := hub.AsPublisher()
	p.Publish("blog", 2, "hello")

	select {
	case evt := <-sub:
		if evt.Plugin != "blog" || evt.Level != "warn" || evt.Message != "hello" {
			t.Errorf("got %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

// TestExtractPlugin covers the URL-routing helper. Edge cases include
// trailing slashes, missing prefix, and embedded slashes (which we
// reject since the plugin name is a single segment).
func TestExtractPlugin(t *testing.T) {
	cases := map[string]string{
		"/_/plugins/dev/logs/blog":    "blog",
		"/_/plugins/dev/logs/blog/":   "blog",
		"/_/plugins/dev/logs/":        "",
		"/_/plugins/dev/logs":         "",
		"/_/plugins/dev/logs/a/b":     "",
		"/_/plugins/dev/install/blog": "",
	}
	for in, want := range cases {
		if got := extractPlugin(in); got != want {
			t.Errorf("extractPlugin(%q): got %q want %q", in, got, want)
		}
	}
}

// TestLogStreamHandler_RoundTrip is the end-to-end test: spin up a
// real httptest server, perform a WebSocket handshake from the
// outside, publish events on the hub, and verify the bytes arrive on
// the client socket in order. We do the framing by hand so we don't
// pull in a websocket library just for this test.
func TestLogStreamHandler_RoundTrip(t *testing.T) {
	hub := NewLogHub(16)
	defer hub.Close()

	mux := http.NewServeMux()
	mux.Handle("/_/plugins/dev/logs/", LogStreamHandler(hub))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn := dialTestWS(t, srv.URL+"/_/plugins/dev/logs/blog")
	defer conn.Close()
	br := bufio.NewReader(conn)
	// Drain the rest of the handshake response if any leftover bytes.
	_ = br

	// Publish 5 events and read them back.
	const N = 5
	for i := 0; i < N; i++ {
		hub.Publish(LogEvent{
			Timestamp: time.Now().UTC(),
			Plugin:    "blog",
			Level:     "info",
			Message:   fmt.Sprintf("hello-%d", i),
		})
	}

	for i := 0; i < N; i++ {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		opcode, payload, err := readFrameTest(br)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if opcode != 0x1 {
			t.Fatalf("opcode %d: got %x want 0x1 (text)", i, opcode)
		}
		var evt LogEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		want := fmt.Sprintf("hello-%d", i)
		if evt.Message != want {
			t.Errorf("msg[%d]: got %q want %q", i, evt.Message, want)
		}
	}
}

// TestLogStreamHandler_RejectsBadPath confirms the handler returns a
// 400 (not a panic, not a stuck connection) when the URL doesn't carry
// a plugin name.
func TestLogStreamHandler_RejectsBadPath(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()
	srv := httptest.NewServer(LogStreamHandler(hub))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/wrong/path")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", resp.StatusCode)
	}
}

// TestLogStreamHandler_DisconnectCleansUp confirms that closing the
// client TCP connection evicts the subscription from the hub. This
// protects long-running dev hosts from accumulating dead subscribers.
func TestLogStreamHandler_DisconnectCleansUp(t *testing.T) {
	hub := NewLogHub(4)
	defer hub.Close()
	srv := httptest.NewServer(LogStreamHandler(hub))
	defer srv.Close()

	conn := dialTestWS(t, srv.URL+"/_/plugins/dev/logs/blog")

	// Wait briefly for the server to register the subscription.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hub.subsMu.RLock()
		n := len(hub.subs)
		hub.subsMu.RUnlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_ = conn.Close()

	// And wait briefly for the cleanup to fire.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.subsMu.RLock()
		n := len(hub.subs)
		hub.subsMu.RUnlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subscription not cleaned up after client disconnect")
}

// dialTestWS performs the minimal client handshake for httptest URLs.
// Returns the underlying TCP connection so the test can read framed
// bytes directly.
func dialTestWS(t *testing.T, urlStr string) net.Conn {
	t.Helper()
	hostport, path := parseHTTPURL(t, urlStr)
	conn, err := net.Dial("tcp", hostport)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	key := randomTestKey(t)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + hostport + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status: %d", resp.StatusCode)
	}
	// Return a wrapper that surfaces bytes the bufio.Reader already
	// pre-fetched, then falls through to the raw conn.
	return &bufferedConn{conn: conn, buf: br}
}

func parseHTTPURL(t *testing.T, s string) (host, path string) {
	t.Helper()
	// "http://127.0.0.1:NNNN/path"
	const prefix = "http://"
	if !strings.HasPrefix(s, prefix) {
		t.Fatalf("not an http URL: %q", s)
	}
	rest := s[len(prefix):]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return rest, "/"
	}
	return rest[:slash], rest[slash:]
}

func randomTestKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// bufferedConn wraps a net.Conn so reads first drain the bufio.Reader
// that consumed the response headers, then fall through to the
// underlying connection.
type bufferedConn struct {
	conn net.Conn
	buf  *bufio.Reader

	closeOnce sync.Once
	closed    atomic.Bool
}

func (c *bufferedConn) Read(p []byte) (int, error)  { return c.buf.Read(p) }
func (c *bufferedConn) Write(p []byte) (int, error) { return c.conn.Write(p) }
func (c *bufferedConn) Close() error {
	c.closeOnce.Do(func() { c.closed.Store(true) })
	return c.conn.Close()
}
func (c *bufferedConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *bufferedConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *bufferedConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *bufferedConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *bufferedConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// readFrameTest mirrors the inbound-frame parser used by both the
// server side (drainClientFrames in logstream.go) and the CLI tailer.
// Duplicated here so the test file remains self-contained.
func readFrameTest(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext)
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return opcode, payload, nil
}

// TestLevelName covers the public level-translation helper. The runtime
// passes int32 levels straight through to LogPublisher, so a bad
// mapping here silently misroutes events.
func TestLevelName(t *testing.T) {
	cases := map[int32]string{
		0:    "debug",
		1:    "info",
		2:    "warn",
		3:    "error",
		99:   "info", // unknown defaults to info
		-1:   "info",
	}
	for in, want := range cases {
		if got := LevelName(in); got != want {
			t.Errorf("LevelName(%d): got %q want %q", in, got, want)
		}
	}
}

// TestFormatLine confirms the canonical text format the dev tools
// produce. A format drift would break grep-based log analysis.
func TestFormatLine(t *testing.T) {
	ts := time.Date(2026, 5, 17, 12, 34, 56, int(789*time.Millisecond/time.Nanosecond), time.UTC)
	out := FormatLine(LogEvent{
		Timestamp: ts,
		Plugin:    "blog",
		Level:     "info",
		Message:   "hello",
	})
	want := "[12:34:56.789] INFO  blog: hello"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
