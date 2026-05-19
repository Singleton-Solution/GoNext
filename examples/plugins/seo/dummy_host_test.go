// Package main — dummy host bus for the gonext-seo example.
//
// This file lives next to the plugin's TinyGo source so the tests can
// run the same domain logic the WASM blob runs, without needing TinyGo
// installed. It mirrors the path the production wazero dispatcher
// takes:
//
//   marshal -> guest memory -> gn_handle_hook -> unmarshal result
//
// The "guest memory" step is replaced with a direct Go call, so the
// test exercises everything in the contract except the WASM linker
// itself. That gap is closed at activation time by the lifecycle
// integration tests (packages/go/plugins/lifecycle).
//
// See also: packages/go/plugins/internal/_seo_dummy.go — a copy of
// this file lives there under a leading-underscore name so the canon
// path advertised in docs/04-seo-plugin-tutorial.md resolves; that
// file is excluded from builds by the Go tool's filename rule (any
// file beginning with "_" is skipped).
package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// runFilterThroughBus simulates the wazero host's filter dispatch
// path. It takes the marshalled host-side payload (the same bytes the
// production dispatcher would copy into guest memory), runs the
// plugin's filter logic against it, and returns the json.RawMessage
// value the dispatcher would extract from the FilterResult envelope.
//
// The plumbing matches the production path in
// packages/go/plugins/abi/hooks/dispatcher.go:
//
//  1. The host marshals a FilterPayload (kind+value+args) — done by
//     the caller.
//  2. The host writes those bytes to the guest's memory and calls
//     gn_handle_hook — we skip the memcpy and call the handler
//     directly.
//  3. The guest returns a (ptr, len) tuple naming the FilterResult
//     bytes — we return them as []byte.
//  4. The host decodes them into json.RawMessage — we hand the bytes
//     to encoding/json.
//
// hookName is the same string the runtime would pass through (name_ptr,
// name_len) on the WASM ABI. Filter hooks served by this plugin:
// "the_content".
func runFilterThroughBus(ctx context.Context, hookName string, payloadBytes []byte) (json.RawMessage, error) {
	_ = ctx // reserved for cancellation propagation when the host adds it

	if hookName != "the_content" {
		return nil, fmt.Errorf("dummy bus: only the_content is filter-routable in this plugin, got %q", hookName)
	}

	// Mirror invokeContentFilter in main.go (which has //go:build
	// tinygo). We can't share the function body because the TinyGo
	// build uses unsafe.Slice on raw pointers — the Go-side variant
	// works directly on []byte. The wire-format contract is identical.
	var fp struct {
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
		Args  []interface{}   `json:"args"`
	}
	if err := json.Unmarshal(payloadBytes, &fp); err != nil {
		return nil, fmt.Errorf("dummy bus: decode FilterPayload: %w", err)
	}
	if fp.Kind != "filter" {
		return nil, fmt.Errorf("dummy bus: kind=%q want filter", fp.Kind)
	}
	var inputHTML string
	if err := json.Unmarshal(fp.Value, &inputHTML); err != nil {
		return nil, fmt.Errorf("dummy bus: decode value: %w", err)
	}

	post := postFromArgs(fp.Args)
	jsonld := BuildJSONLD(post)
	out := inputHTML + "\n" + jsonld

	// The production guest returns a FilterResult-envelope object;
	// the dispatcher unwraps it and hands the inner value back to the
	// host bus as json.RawMessage. Mirror that here so callers see
	// the same bytes the host bus would forward.
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("dummy bus: encode value: %w", err)
	}
	envelope, err := json.Marshal(struct {
		Value json.RawMessage `json:"value"`
	}{Value: encoded})
	if err != nil {
		return nil, fmt.Errorf("dummy bus: encode envelope: %w", err)
	}

	// Reverse step 4 from the dispatcher: unmarshal the envelope to
	// get the inner Value, return that. This keeps the test in lock-
	// step with what the production code returns to the bus.
	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(envelope, &result); err != nil {
		return nil, fmt.Errorf("dummy bus: decode envelope: %w", err)
	}
	return result.Value, nil
}
