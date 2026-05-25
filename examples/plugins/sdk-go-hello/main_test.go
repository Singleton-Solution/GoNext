package main

import (
	"encoding/json"
	"testing"
)

// main_test.go validates the handler logic under the stock Go
// toolchain. The SDK stubs out host calls in this build, so we focus
// on parameter handling, error paths, and the filter's
// value-transformation contract.

func TestOnPostsPublishHappyPath(t *testing.T) {
	// The SDK stub for KV.Set / Audit.Emit returns an error
	// (statusHostUnavailable). The handler logs but continues —
	// we assert it doesn't return an error to the caller for the
	// "happy path" arg shape.
	post := map[string]any{"id": "post-42", "title": "Hello"}
	if err := onPostsPublish([]any{post}); err != nil {
		t.Fatalf("onPostsPublish returned error: %v", err)
	}
}

func TestOnPostsPublishMissingArgs(t *testing.T) {
	if err := onPostsPublish(nil); err == nil {
		t.Error("expected error for nil args")
	}
	if err := onPostsPublish([]any{}); err == nil {
		t.Error("expected error for empty args")
	}
}

func TestOnPostsPublishWrongArgType(t *testing.T) {
	if err := onPostsPublish([]any{"not a map"}); err == nil {
		t.Error("expected error for non-map arg")
	}
}

func TestOnPostsPublishMissingID(t *testing.T) {
	post := map[string]any{"title": "Hello"} // no id
	if err := onPostsPublish([]any{post}); err == nil {
		t.Error("expected error for missing id")
	}
	post = map[string]any{"id": ""} // empty id
	if err := onPostsPublish([]any{post}); err == nil {
		t.Error("expected error for empty id")
	}
}

func TestOnTheContentAppendsMarker(t *testing.T) {
	value := json.RawMessage(`"original body"`)
	out, err := onTheContent(value, nil)
	if err != nil {
		t.Fatalf("onTheContent error: %v", err)
	}
	var body string
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	want := "original body\n<!-- enhanced by gonext-sdk-go-hello -->"
	if body != want {
		t.Errorf("got %q, want %q", body, want)
	}
}

func TestOnTheContentBadValue(t *testing.T) {
	// Non-string JSON value — the handler expects the post body
	// as a JSON-encoded string and should reject anything else.
	value := json.RawMessage(`42`)
	if _, err := onTheContent(value, nil); err == nil {
		t.Error("expected error for non-string value")
	}
}
