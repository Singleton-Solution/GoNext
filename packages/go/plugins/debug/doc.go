// Package debug provides developer-facing tooling for plugin authoring:
// source-map-backed symbolication of WebAssembly traps, and a streaming
// log endpoint that surfaces gn_log calls from a running plugin back to
// the dev CLI in real time.
//
// The package is split into three concerns:
//
//   - sourcemap.go — parses a sidecar `.wasm.map` produced by the
//     plugin's build toolchain (Source Map V3 JSON, the same format
//     mainstream WASM toolchains and emscripten emit). The parser
//     resolves a WASM code-section offset to an `(file, line, column,
//     function)` tuple.
//
//   - inspector.go — wraps a runtime.TrapError and produces a
//     SymbolicatedTrap with a Frame slice ready to print. When no source
//     map is loaded the inspector still returns a usable Frame: the
//     file is "<no source map>", the line/col are zero, and the
//     function is the trap's reason text. The dev tools render that
//     fallback rather than dropping the error.
//
//   - logstream.go — an in-memory pub/sub hub keyed by plugin name,
//     plus the HTTP / WebSocket bridge that exposes one subscriber per
//     incoming connection. The runtime publishes one Event per gn_log
//     call; subscribers receive them in order until the context is
//     cancelled or the connection drops.
//
// The package is deliberately host-side only — none of its types cross
// the WASM ABI. The runtime hooks into LogHub via a single
// PublishLog call (#271), which we expose as a function value so the
// runtime package doesn't take a hard dependency on this one.
//
// # Threading model
//
// All exported types are goroutine-safe. The LogHub is a fan-out
// pub/sub: publishers never block on slow subscribers — a subscriber
// whose channel buffer fills is marked "lagged" and gets a synthetic
// Event with Level="warn" before the next message lands. This matches
// the contract slog handlers and other host-facing log facilities use
// (#107's capability host follows the same rule).
//
// # Wire format
//
// The WebSocket endpoint speaks the RFC 6455 framing protocol
// directly — we deliberately avoid pulling in gorilla/websocket or
// nhooyr to keep packages/go's dependency surface minimal. The framing
// helper handles only the subset we need: server-side handshake, text
// frames, and close frames. Binary frames, fragmentation, and
// extensions are unsupported (we have no use case for them and the
// extra code would be dead weight).
//
// Each event is serialised as a single-line JSON object:
//
//	{"ts":"2026-05-17T12:34:56.789Z","plugin":"blog","level":"info","msg":"hello"}
//
// The dev CLI parses this and renders one coloured line per event.
package debug
