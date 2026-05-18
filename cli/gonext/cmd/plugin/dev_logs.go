package plugin

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
	"net/url"
	"strings"
	"time"
)

// LogTailer is the seam the dev orchestrator uses to stream logs from
// the dev host's /_/plugins/dev/logs/{plugin} WebSocket endpoint. The
// production implementation is wsLogTailer; tests substitute a stub
// that fakes the round-trip.
//
// Tail blocks until ctx is cancelled or the server closes the
// connection. It is responsible for writing one formatted line per
// event to out. Errors are returned only for fatal connect/handshake
// problems; once the stream is established a clean close returns nil.
type LogTailer interface {
	Tail(ctx context.Context, host, plugin string, out io.Writer) error
}

// wsLogTailer is the production implementation. It speaks RFC 6455
// directly — same minimal subset as the host-side server in
// packages/go/plugins/debug/logstream.go — so the CLI doesn't grow a
// third-party websocket dependency.
type wsLogTailer struct{}

// Tail connects to ws(s)://<host>/_/plugins/dev/logs/<plugin>, reads
// JSON-encoded events, and writes one color-coded line per event to
// out. The function returns nil on a normal disconnect.
func (wsLogTailer) Tail(ctx context.Context, host, plugin string, out io.Writer) error {
	wsURL, err := buildLogsURL(host, plugin)
	if err != nil {
		return err
	}
	conn, br, err := dialWebSocket(ctx, wsURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	// We don't need to send anything client→server, but a goroutine
	// drains the receive side until ctx fires so a server close shows
	// up as an EOF on the read path.
	done := make(chan struct{})
	go func() {
		defer close(done)
		readClientLoop(ctx, conn, br, out)
	}()

	select {
	case <-ctx.Done():
		// Best-effort polite close frame; ignore errors.
		_ = writeClientCloseFrame(conn)
		// Closing the TCP connection unblocks the read goroutine
		// without relying on the server to reciprocate the close
		// frame — important because the server may already be gone.
		_ = conn.Close()
		<-done
		return nil
	case <-done:
		return nil
	}
}

// buildLogsURL composes the WebSocket URL from the operator-supplied
// HTTP host. We rewrite the scheme http→ws / https→wss so the user can
// keep using the same --host flag they used for upload.
func buildLogsURL(host, plugin string) (string, error) {
	if plugin == "" {
		return "", fmt.Errorf("plugin name is empty")
	}
	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse host: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already correct
	default:
		return "", fmt.Errorf("unsupported scheme %q (want http/https/ws/wss)", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/_/plugins/dev/logs/" + plugin
	return u.String(), nil
}

// dialWebSocket opens a TCP connection to the URL's host and performs
// the RFC 6455 client handshake. Returns the underlying conn plus a
// buffered reader pre-populated with any leftover bytes after the
// response headers. On failure both are nil.
func dialWebSocket(ctx context.Context, raw string) (net.Conn, *bufio.Reader, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	hostport := u.Host
	if !strings.Contains(hostport, ":") {
		if u.Scheme == "wss" {
			hostport += ":443"
		} else {
			hostport += ":80"
		}
	}

	if u.Scheme == "wss" {
		// TLS support for the dev tailer is out of scope for the
		// initial cut — the dev host is the local box. Surfacing the
		// limitation here is better than silently producing a broken
		// connection.
		return nil, nil, fmt.Errorf("wss:// not supported by dev tailer (use http://)")
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}

	key, err := randomKey()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	pathAndQuery := u.RequestURI()
	if pathAndQuery == "" {
		pathAndQuery = "/"
	}
	req := "GET " + pathAndQuery + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("write handshake: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("read handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, resp.Status)
	}
	return conn, br, nil
}

// randomKey returns a base64-encoded 16-byte random key as required by
// RFC 6455 §4.1.
func randomKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// readClientLoop is the client-side frame reader. It pulls text frames
// from the server, parses each one as a debug.LogEvent JSON object,
// formats it with the color-coded line printer, and writes to out.
// Returns on EOF, ctx cancellation, or any read error.
func readClientLoop(ctx context.Context, conn net.Conn, br *bufio.Reader, out io.Writer) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		opcode, payload, err := readFrame(br)
		if err != nil {
			return
		}
		switch opcode {
		case 0x1: // text
			renderLogLine(out, payload)
		case 0x8: // close
			return
		default:
			// Binary, ping, pong: ignored. The server only sends text.
		}
	}
}

// readFrame parses one inbound (server→client) frame and returns its
// opcode and payload. Server frames are unmasked per RFC 6455 §5.1.
func readFrame(r io.Reader) (byte, []byte, error) {
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

// writeClientCloseFrame sends a polite WebSocket close (opcode 0x8)
// with no payload. Client→server frames must be masked per RFC 6455
// §5.3; we use a zero mask key for simplicity (the payload is empty so
// the mask has nothing to mask anyway).
func writeClientCloseFrame(conn net.Conn) error {
	frame := []byte{0x88, 0x80, 0x00, 0x00, 0x00, 0x00}
	_, err := conn.Write(frame)
	return err
}

// renderLogLine parses payload as a debug.LogEvent and writes one
// color-coded line. Malformed JSON falls back to a raw print so the
// operator still sees something — we don't want to swallow output
// because a slightly-off line lost a millisecond field.
func renderLogLine(out io.Writer, payload []byte) {
	var evt logEventLite
	if err := json.Unmarshal(payload, &evt); err != nil {
		fmt.Fprintf(out, "%s\n", string(payload))
		return
	}
	color := levelColor(evt.Level)
	reset := "\x1b[0m"
	ts := evt.Timestamp.Format("15:04:05.000")
	fmt.Fprintf(out, "[%s] %s%-5s%s %s: %s\n",
		ts, color, strings.ToUpper(evt.Level), reset, evt.Plugin, evt.Message)
}

// logEventLite mirrors debug.LogEvent. We duplicate the shape (instead
// of importing the debug package directly) to avoid a CLI→runtime
// dependency on a Go-only host package; the CLI ships as a standalone
// binary and pulling in the wazero-bearing runtime tree just for this
// struct would balloon the CLI binary.
type logEventLite struct {
	Timestamp time.Time `json:"ts"`
	Plugin    string    `json:"plugin"`
	Level     string    `json:"level"`
	Message   string    `json:"msg"`
}

// levelColor maps a level name to an ANSI SGR escape. Levels we don't
// recognise get the default colour.
//
// We use 24-bit-safe 8-color escapes only (not 256/truecolor) — the
// terminals the dev loop targets (iTerm2, modern Windows Terminal,
// most Linux terms) all support these without configuration.
func levelColor(level string) string {
	switch strings.ToLower(level) {
	case "debug":
		return "\x1b[90m" // bright black / grey
	case "info":
		return "\x1b[36m" // cyan
	case "warn":
		return "\x1b[33m" // yellow
	case "error":
		return "\x1b[31m" // red
	default:
		return ""
	}
}
