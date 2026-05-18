package debug

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LogEvent is one published log line from a plugin. Mirrors gn_log's
// parameters plus identification metadata so subscribers can route
// without parsing the message.
//
// JSON-encoded to the wire as a single line; field ordering and naming
// are stable contract — the dev CLI parses this.
type LogEvent struct {
	Timestamp time.Time `json:"ts"`
	Plugin    string    `json:"plugin"`
	Level     string    `json:"level"`
	Message   string    `json:"msg"`
}

// LogHub is the per-process pub/sub for plugin log events. Publishers
// (the runtime's hostGnLog) call Publish; subscribers (the WebSocket
// endpoint, future log-sink plugins) call Subscribe to obtain a
// receive-only channel that is closed when the subscriber's ctx is
// cancelled.
//
// One LogHub serves all plugins — the routing is done by Event.Plugin
// at subscribe time, not by maintaining separate channels per plugin.
// That keeps the publisher path O(N subscribers per plugin) without a
// global lookup map for the common case (most subscribers are per-
// plugin tails from the dev CLI; one global subscriber may exist for a
// daemon-wide stream).
type LogHub struct {
	// subsMu guards subs. The publisher path takes a read lock so N
	// concurrent publishes can fan out without contending; subscriber
	// (un)registration takes the write lock briefly.
	subsMu sync.RWMutex
	subs   map[*subscription]struct{}

	// bufSize is the per-subscriber channel buffer. Publishers never
	// block: when a subscriber's channel is full the event is dropped
	// for that subscriber and a synthetic "lagged" event is queued in
	// its place (if there's room) or just lost (if there isn't).
	bufSize int

	// closed is set after Close. Subsequent Publish calls become
	// no-ops, and Subscribe returns a pre-closed channel.
	closedMu sync.Mutex
	closed   bool
}

// subscription is the LogHub's internal handle for one subscriber.
// Holds the channel, the optional plugin filter, and the lagged flag.
type subscription struct {
	ch       chan LogEvent
	plugin   string // "" means all plugins
	laggedMu sync.Mutex
	lagged   bool
}

// NewLogHub returns a hub with the given per-subscriber buffer size.
// 256 is a sensible default for development streams; production-grade
// log fanout would use a much larger buffer or a different abstraction
// entirely (this hub is dev-loop scoped).
//
// bufSize < 1 panics — a zero buffer would deadlock the publisher.
func NewLogHub(bufSize int) *LogHub {
	if bufSize < 1 {
		panic("debug: NewLogHub: bufSize must be >= 1")
	}
	return &LogHub{
		subs:    make(map[*subscription]struct{}),
		bufSize: bufSize,
	}
}

// Subscribe returns a receive-only channel of events scoped to the
// given plugin name. Passing "" subscribes to events from all plugins.
// The channel is closed when ctx is cancelled or Close is called on
// the hub.
//
// Cancelling ctx is the ONLY supported unsubscribe path — there is no
// explicit Unsubscribe method. This forces every subscriber to own a
// lifecycle, which prevents the leaked-subscriber bug from creeping
// in.
func (h *LogHub) Subscribe(ctx context.Context, plugin string) <-chan LogEvent {
	out := make(chan LogEvent, h.bufSize)
	h.closedMu.Lock()
	if h.closed {
		h.closedMu.Unlock()
		close(out)
		return out
	}
	h.closedMu.Unlock()

	sub := &subscription{
		ch:     out,
		plugin: plugin,
	}

	h.subsMu.Lock()
	h.subs[sub] = struct{}{}
	h.subsMu.Unlock()

	// Cleanup goroutine: when ctx fires, evict the subscription and
	// close its channel exactly once. We start it eagerly so a
	// pre-cancelled ctx is honoured without the caller having to
	// drive Publish first.
	go func() {
		<-ctx.Done()
		h.evict(sub)
	}()

	return out
}

// evict removes sub from the hub and closes its channel. Idempotent:
// calling twice is safe (the second call sees the missing entry and
// no-ops). Both the ctx-cancellation goroutine and the Close path call
// this, so the idempotence is load-bearing.
func (h *LogHub) evict(sub *subscription) {
	h.subsMu.Lock()
	_, present := h.subs[sub]
	if present {
		delete(h.subs, sub)
	}
	h.subsMu.Unlock()
	if present {
		close(sub.ch)
	}
}

// Publish broadcasts evt to every matching subscriber. Never blocks:
// subscribers with full channels are marked "lagged" and a synthetic
// warning is queued for them on next opportunity. This is the
// contractual hot path called from inside the wazero host function;
// adding back-pressure here would let a slow subscriber stall every
// plugin's gn_log.
func (h *LogHub) Publish(evt LogEvent) {
	h.closedMu.Lock()
	if h.closed {
		h.closedMu.Unlock()
		return
	}
	h.closedMu.Unlock()

	h.subsMu.RLock()
	defer h.subsMu.RUnlock()

	for sub := range h.subs {
		if sub.plugin != "" && sub.plugin != evt.Plugin {
			continue
		}
		// If we previously marked the subscriber lagged, prepend the
		// warning before resuming normal delivery. We only emit one
		// "lagged" event per gap — once it's delivered, the flag
		// clears.
		sub.laggedMu.Lock()
		if sub.lagged {
			warn := LogEvent{
				Timestamp: time.Now().UTC(),
				Plugin:    evt.Plugin,
				Level:     "warn",
				Message:   "log stream lagged: previous events dropped",
			}
			select {
			case sub.ch <- warn:
				sub.lagged = false
			default:
				// Still full; leave lagged=true so we try again next
				// time. Drop both events for now.
				sub.laggedMu.Unlock()
				continue
			}
		}
		sub.laggedMu.Unlock()

		select {
		case sub.ch <- evt:
		default:
			// Buffer full: drop and mark lagged.
			sub.laggedMu.Lock()
			sub.lagged = true
			sub.laggedMu.Unlock()
		}
	}
}

// Close shuts the hub down. All subscribers are evicted and their
// channels closed. Subsequent Publish/Subscribe calls become no-ops.
// Idempotent.
func (h *LogHub) Close() {
	h.closedMu.Lock()
	if h.closed {
		h.closedMu.Unlock()
		return
	}
	h.closed = true
	h.closedMu.Unlock()

	h.subsMu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.subs = make(map[*subscription]struct{})
	h.subsMu.Unlock()

	for _, sub := range subs {
		close(sub.ch)
	}
}

// LogStreamHandler returns an http.Handler that upgrades HTTP/1.1
// requests to WebSocket connections and pipes events from hub to the
// client. The plugin name is taken from the URL using extractPlugin,
// which expects paths shaped like "/_/plugins/dev/logs/{plugin}".
//
// The handler's lifetime is bound to the request context: closing the
// HTTP server, the client disconnecting, or a write error all cause
// the underlying subscription to be evicted and the goroutines to
// unwind.
func LogStreamHandler(hub *LogHub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plugin := extractPlugin(r.URL.Path)
		if plugin == "" {
			http.Error(w, "missing plugin name in path", http.StatusBadRequest)
			return
		}

		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			// upgradeWebSocket writes its own error response on
			// handshake failures, so we don't double-respond.
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		events := hub.Subscribe(ctx, plugin)

		// Reader goroutine: drain client→server frames. A client may
		// only send pings or a close frame; either way we just need to
		// notice when the connection drops so cancel() fires.
		go func() {
			defer cancel()
			_ = drainClientFrames(conn)
		}()

		// Writer loop: forward events as text frames. JSON-encoding
		// per the package doc.
		enc := json.NewEncoder(io.Discard) // not used; kept for symmetry with future binary path
		_ = enc
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-events:
				if !ok {
					return
				}
				payload, err := json.Marshal(evt)
				if err != nil {
					// Marshal failures on our own types would be a
					// programming bug; bail out to avoid spinning.
					return
				}
				if err := writeTextFrame(conn, payload); err != nil {
					return
				}
			}
		}
	})
}

// extractPlugin pulls the plugin name out of a path like
// "/_/plugins/dev/logs/blog". Returns "" for anything else, so the
// handler can reject the request cleanly.
//
// We don't use net/http.ServeMux's pattern matching here because the
// hub is mounted under a fixed prefix by callers; passing the pattern
// in via a closure would force every caller to repeat the same
// boilerplate.
func extractPlugin(path string) string {
	const prefix = "/_/plugins/dev/logs/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	// Trim trailing slash if present, and reject anything with a slash
	// in the middle (we expect a single segment).
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.ContainsRune(rest, '/') {
		return ""
	}
	return rest
}

// upgradeWebSocket performs the RFC 6455 §4.2 server handshake. On
// success it returns the hijacked net.Conn wrapped in a struct that
// gives us access to the framed byte stream. On failure it writes the
// appropriate HTTP error and returns it.
//
// We support only the minimum subset: HTTP/1.1, Upgrade: websocket,
// Connection: Upgrade, Sec-WebSocket-Version: 13, and a Sec-WebSocket-
// Key. Origin checks are deliberately omitted — the endpoint lives on
// the dev host's bind address (localhost by default) and adding a CORS
// dance would just push the friction onto plugin authors.
func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "expected Upgrade: websocket", http.StatusBadRequest)
		return nil, errors.New("not a websocket upgrade")
	}
	if !headerContainsToken(r.Header.Get("Connection"), "upgrade") {
		http.Error(w, "expected Connection: upgrade", http.StatusBadRequest)
		return nil, errors.New("missing Connection: upgrade")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Header(w.Header()).Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusBadRequest)
		return nil, errors.New("bad version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("missing key")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return nil, errors.New("not hijackable")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	accept := computeAcceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.Writer.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Writer.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &wsConn{rwc: conn, rw: rw}, nil
}

// headerContainsToken is a comma-separated-list contains check, case
// insensitive. The Connection header may legally hold "Upgrade,
// keep-alive" or "keep-alive, Upgrade"; we accept any ordering.
func headerContainsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// computeAcceptKey is the RFC 6455 §4.2.2 derivation: base64(sha1(key
// || GUID)). Constant-time isn't required — the GUID is public.
func computeAcceptKey(key string) string {
	const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(guid))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wsConn is the minimal interface we need to read+write framed bytes
// after the hijack. It carries the buffered Reader (the only place
// where a few bytes might already be sitting from the request) and the
// raw net.Conn.
type wsConn struct {
	rwc io.ReadWriteCloser
	rw  interface {
		io.Reader
		io.Writer
	}
}

func (c *wsConn) Close() error { return c.rwc.Close() }

// writeTextFrame writes a single unfragmented RFC 6455 text frame with
// payload. Server-side frames are unmasked (RFC §5.1). For payloads
// longer than 125 bytes we use the 16-bit or 64-bit extended length
// fields per the spec.
//
// We write directly to the underlying io.Writer rather than the buffered
// rw.Writer so a frame becomes one syscall — a partially-flushed buffer
// between header and payload would corrupt the framing.
func writeTextFrame(c *wsConn, payload []byte) error {
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
		header = append(header, 126)
		header = append(header, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 127)
		header = append(header, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}
	if _, err := c.rwc.(io.Writer).Write(header); err != nil {
		return err
	}
	if _, err := c.rwc.(io.Writer).Write(payload); err != nil {
		return err
	}
	return nil
}

// drainClientFrames reads from the client until EOF or a close frame
// arrives. We do NOT process payload data — the client should never
// send anything except pings or close — but we still need to drain so
// the TCP connection survives until the client decides to bail. Any
// read error (including a normal close) terminates the loop.
func drainClientFrames(c *wsConn) error {
	r := c.rwc.(io.Reader)
	buf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		opcode := buf[0] & 0x0F
		masked := buf[1]&0x80 != 0
		length := uint64(buf[1] & 0x7F)

		switch length {
		case 126:
			ext := make([]byte, 2)
			if _, err := io.ReadFull(r, ext); err != nil {
				return err
			}
			length = uint64(binary.BigEndian.Uint16(ext))
		case 127:
			ext := make([]byte, 8)
			if _, err := io.ReadFull(r, ext); err != nil {
				return err
			}
			length = binary.BigEndian.Uint64(ext)
		}
		if masked {
			mask := make([]byte, 4)
			if _, err := io.ReadFull(r, mask); err != nil {
				return err
			}
		}
		if length > 0 {
			pl := make([]byte, length)
			if _, err := io.ReadFull(r, pl); err != nil {
				return err
			}
		}
		// Opcode 0x8 is "connection close". Bail cleanly.
		if opcode == 0x8 {
			return io.EOF
		}
	}
}

// runtimePublisher is the adapter that bridges the LogHub onto the
// runtime.LogPublisher contract. Using a separate type instead of
// putting the (module, level, message) method directly on LogHub
// keeps LogHub's public API free of runtime-specific naming — and
// lets us evolve the runtime contract independently of this hub.
type runtimePublisher struct{ hub *LogHub }

// Publish implements runtime.LogPublisher.
func (p runtimePublisher) Publish(module string, level int32, message string) {
	p.hub.Publish(LogEvent{
		Timestamp: time.Now().UTC(),
		Plugin:    module,
		Level:     LevelName(level),
		Message:   message,
	})
}

// AsPublisher returns a wrapper that satisfies runtime.LogPublisher.
// Pass the result to runtime.WithLogPublisher.
func (h *LogHub) AsPublisher() interface {
	Publish(module string, level int32, message string)
} {
	return runtimePublisher{hub: h}
}

// LevelName maps the runtime's int32 log-level encoding (debug=0,
// info=1, warn=2, error=3) to a textual label. Exported because the
// runtime hook calls it before emitting LogEvent.
//
// Unknown levels default to "info" rather than rejecting — a buggy
// guest passing a stray integer should still see its message land.
func LevelName(level int32) string {
	switch level {
	case 0:
		return "debug"
	case 1:
		return "info"
	case 2:
		return "warn"
	case 3:
		return "error"
	default:
		return "info"
	}
}

// FormatLine renders evt in the same shape the dev CLI prints, so
// tests and other tools can produce identical output without
// reimplementing the formatting. The format is:
//
//	[15:04:05.000] LEVEL plugin: message
//
// Coloring is applied by the CLI, not here — this helper is pure.
func FormatLine(evt LogEvent) string {
	return fmt.Sprintf("[%s] %-5s %s: %s",
		evt.Timestamp.Format("15:04:05.000"),
		strings.ToUpper(evt.Level),
		evt.Plugin,
		evt.Message)
}
