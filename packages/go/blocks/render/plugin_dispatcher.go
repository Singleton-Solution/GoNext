// plugin_dispatcher.go wires the walker's plugin-block path to the
// host hook bus (issue #222). The walker calls Dispatch with a
// (slug, handler, request) tuple; this dispatcher translates that into
// an ApplyFilters call on the bus under the canonical hook key
//
//	block.render:{slug}/{handler}
//
// The plugin's WASM module subscribes to that key, decodes the JSON
// payload (a PluginBlockRequest), and returns the rendered HTML
// string. The dispatcher unmarshals the bus return value into a
// template.HTML and hands it to the walker, which splices it into the
// output stream.
//
// The bus dependency is injected via a tiny interface so this package
// doesn't take a hard import on packages/go/hooks — the cycle would
// be: hooks → schemas → blocks (eventually) → hooks. The Dispatcher
// interface keeps the render package's import graph small.

package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
)

// HookFilterBus is the subset of *hooks.Bus this dispatcher needs.
// Declared here (rather than imported) so the render package stays
// free of a compile-time dependency on packages/go/hooks. The
// production wiring passes *hooks.Bus, which satisfies this interface
// without any adapter.
type HookFilterBus interface {
	ApplyFilters(ctx context.Context, name string, value any, args ...any) (any, error)
}

// HookBusDispatcher implements PluginBlockDispatcher by issuing a
// filter call on the host hook bus. Construct via NewHookBusDispatcher.
//
// The Context the bus call uses is captured at construction time —
// callers wanting per-request cancellation should construct one
// dispatcher per request, or pass a longer-lived context (e.g. the
// HTTP handler's). The walker has no Context surface of its own;
// adding one would touch every renderer signature.
type HookBusDispatcher struct {
	bus HookFilterBus
	ctx context.Context
}

// NewHookBusDispatcher constructs a dispatcher rooted at the supplied
// bus and context. ctx must be non-nil; pass context.Background() for
// the long-lived dispatcher used by SSR rendering.
func NewHookBusDispatcher(ctx context.Context, bus HookFilterBus) *HookBusDispatcher {
	if bus == nil {
		panic("render.NewHookBusDispatcher: bus is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &HookBusDispatcher{bus: bus, ctx: ctx}
}

// Dispatch fires the bus filter and unwraps the result into HTML.
//
// Hook name format: "block.render:<slug>/<handler>". Plugins
// subscribe under this exact key; the colon separator is chosen so
// the slug+handler segment can hold "/" without ambiguity (the
// walker splits the block type on "/" inside the plugin/ namespace,
// but here we collapse them again for a single hook key).
//
// Bus return contract: the plugin handler returns a value the bus
// threads as the new "value". The walker accepts any of these
// equivalent shapes:
//
//   - template.HTML (the canonical case)
//   - string
//   - []byte
//   - json.RawMessage
//
// Any other type is rejected with a wrapped error so a misbehaving
// handler can't silently inject a Go-marshalled struct as page
// content.
func (d *HookBusDispatcher) Dispatch(slug, handler string, req PluginBlockRequest) (template.HTML, error) {
	hookName := "block.render:" + slug + "/" + handler

	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("render: marshal plugin-block request: %w", err)
	}

	out, err := d.bus.ApplyFilters(d.ctx, hookName, json.RawMessage(payload))
	if err != nil {
		return "", fmt.Errorf("render: plugin block %q/%q: %w", slug, handler, err)
	}

	switch v := out.(type) {
	case template.HTML:
		return v, nil
	case string:
		return template.HTML(v), nil
	case []byte:
		return template.HTML(v), nil
	case json.RawMessage:
		// Unwrap a JSON-string payload: plugins commonly return
		// json.Marshal("<html>...</html>") rather than the raw bytes.
		// If the value is a JSON string, decode it; otherwise treat
		// the raw bytes as HTML.
		if len(v) > 0 && v[0] == '"' {
			var s string
			if jerr := json.Unmarshal(v, &s); jerr == nil {
				return template.HTML(s), nil
			}
		}
		return template.HTML(v), nil
	case nil:
		return "", errors.New("render: plugin block returned nil")
	default:
		return "", fmt.Errorf("render: plugin block returned unsupported type %T", out)
	}
}

// Compile-time check.
var _ PluginBlockDispatcher = (*HookBusDispatcher)(nil)
