package sdk

import (
	"encoding/json"
	"errors"
	"testing"
)

// hooks_test.go validates the dispatcher and registration semantics.
// The wasm-side gn_handle_hook lives in host_wasm.go which has a
// build tag — we exercise DispatchHook directly so this file builds
// under the stock toolchain.

func TestRegisterAndDispatchAction(t *testing.T) {
	resetRegistry()
	called := false
	var seenArgs []any
	RegisterAction("posts.publish", func(args []any) error {
		called = true
		seenArgs = args
		return nil
	})
	payload, _ := MarshalActionPayload([]any{"hello", 42.0})
	body, status := DispatchHook("posts.publish", payload)
	if status != StatusOK {
		t.Fatalf("status: got %d, want OK", status)
	}
	if body != nil {
		t.Errorf("action should return no body, got %s", body)
	}
	if !called {
		t.Error("handler not called")
	}
	if len(seenArgs) != 2 || seenArgs[0] != "hello" || seenArgs[1].(float64) != 42 {
		t.Errorf("args: %v", seenArgs)
	}
}

func TestRegisterAndDispatchFilter(t *testing.T) {
	resetRegistry()
	RegisterFilter("the_content", func(value json.RawMessage, args []any) (json.RawMessage, error) {
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return nil, err
		}
		s += " (modified)"
		return json.Marshal(s)
	})
	payload, _ := MarshalFilterPayload(json.RawMessage(`"hi"`), nil)
	body, status := DispatchHook("the_content", payload)
	if status != StatusOK {
		t.Fatalf("status: got %d, want OK", status)
	}
	out, err := UnmarshalFilterResult(body)
	if err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var got string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal value: %v", err)
	}
	if got != "hi (modified)" {
		t.Errorf("got %q, want 'hi (modified)'", got)
	}
}

func TestDispatchHookUnknown(t *testing.T) {
	resetRegistry()
	_, status := DispatchHook("missing.hook", []byte(`{"kind":"action","args":[]}`))
	if status != StatusUnknownHook {
		t.Errorf("got %d, want StatusUnknownHook", status)
	}
}

func TestDispatchHookBadPayload(t *testing.T) {
	resetRegistry()
	RegisterAction("test", func(args []any) error { return nil })
	_, status := DispatchHook("test", []byte(`{not json`))
	if status != StatusBadPayload {
		t.Errorf("got %d, want StatusBadPayload", status)
	}
}

func TestDispatchHookHandlerError(t *testing.T) {
	resetRegistry()
	RegisterAction("fails", func(args []any) error {
		return errors.New("nope")
	})
	payload, _ := MarshalActionPayload(nil)
	_, status := DispatchHook("fails", payload)
	if status != StatusError {
		t.Errorf("got %d, want StatusError", status)
	}
}

func TestRegisterActionReplacesHandler(t *testing.T) {
	resetRegistry()
	calls := []string{}
	RegisterAction("test", func(args []any) error {
		calls = append(calls, "first")
		return nil
	})
	RegisterAction("test", func(args []any) error {
		calls = append(calls, "second")
		return nil
	})
	payload, _ := MarshalActionPayload(nil)
	_, status := DispatchHook("test", payload)
	if status != StatusOK {
		t.Fatalf("status: %d", status)
	}
	if len(calls) != 1 || calls[0] != "second" {
		t.Errorf("expected second handler to be called, got %v", calls)
	}
}

func TestRegisterNilHandlerIgnored(t *testing.T) {
	resetRegistry()
	RegisterAction("nil-handler", nil)
	_, status := DispatchHook("nil-handler", []byte(`{"kind":"action","args":[]}`))
	if status != StatusUnknownHook {
		t.Errorf("expected UnknownHook for nil handler, got %d", status)
	}
}

func TestFilterHandlerError(t *testing.T) {
	resetRegistry()
	RegisterFilter("err-filter", func(value json.RawMessage, args []any) (json.RawMessage, error) {
		return nil, errors.New("filter failed")
	})
	payload, _ := MarshalFilterPayload(json.RawMessage(`"in"`), nil)
	_, status := DispatchHook("err-filter", payload)
	if status != StatusError {
		t.Errorf("got %d, want StatusError", status)
	}
}

func TestPluginInitAndCurrentManifest(t *testing.T) {
	m := NewManifest("test-init", "0.5.0").
		WithCapability("kv.write").
		MustBuild()
	PluginInit(m)
	got := CurrentManifest()
	if got.Name != "test-init" || got.Version != "0.5.0" {
		t.Errorf("CurrentManifest: %+v", got)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != "kv.write" {
		t.Errorf("capabilities: %v", got.Capabilities)
	}
}
