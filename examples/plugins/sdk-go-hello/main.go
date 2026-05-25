// Package main is the SDK-based hello-world plugin for GoNext.
//
// Unlike examples/plugins/seo (which is built directly against the
// raw ABI), this example uses the public SDK at
// packages/go/sdk — so it doubles as a worked example of every
// surface a plugin author touches.
//
// Build:
//
//	tinygo build -target=wasi -no-debug -o plugin.wasm .
//
// or via the Makefile:
//
//	make plugin.wasm
//
// The output is a wasm32-wasi module the GoNext lifecycle Manager
// accepts. Ship plugin.wasm + manifest.json as a .gnplugin ZIP:
//
//	zip -j hello.gnplugin manifest.json plugin.wasm
//
// # What this plugin does
//
//   - Registers an action handler for posts.publish — stores the
//     published post's id under the plugin's KV namespace and emits
//     a `post.indexed` audit row.
//
//   - Registers a filter handler for the_content — appends a
//     hello-world marker to the post body.
//
//   - Declares the matching manifest (capabilities + hooks +
//     storage budget) programmatically via the SDK's builder. The
//     actual manifest.json in the bundle is the canonical source of
//     truth that the lifecycle Manager validates; the SDK's
//     PluginInit call is the runtime self-description.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/Singleton-Solution/GoNext/packages/go/sdk"
)

// main is the TinyGo entry point. We register every hook handler
// here, then call sdk.PluginInit with the manifest the plugin
// declares. Dispatch is via the SDK's gn_handle_hook export, not
// via main — main only configures the registry.
func main() {
	sdk.RegisterAction("posts.publish", onPostsPublish)
	sdk.RegisterFilter("the_content", onTheContent)

	// Self-describe via the manifest builder. The host validates
	// against the bundled manifest.json — the SDK's record exists
	// for dev tooling that wants to introspect the running plugin
	// without unpacking the bundle.
	sdk.PluginInit(sdk.NewManifest("gonext-sdk-go-hello", "0.1.0").
		WithCapability("kv.write").
		WithCapability("audit.emit").
		WithCapability("hooks.subscribe").
		WithAction("posts.publish").
		WithFilter("the_content").
		WithHostRequirement(">=0.1.0").
		MustBuild())
}

// onPostsPublish is the action handler for the posts.publish hook.
//
// The host bus calls posts.publish with one positional argument: the
// JSON-encoded post object. We extract the id, persist it as the
// "last-published-id" KV value (so an admin dashboard can show the
// most recent indexed post), and emit an audit row tagged with the
// post id.
//
// Returning a non-nil error from this handler would surface as
// ResultStatusError to the host bus — the rest of the chain still
// runs, but the operator sees the failure in audit + slog.
func onPostsPublish(args []any) error {
	if len(args) == 0 {
		return fmt.Errorf("posts.publish: no post argument")
	}
	post, ok := args[0].(map[string]any)
	if !ok {
		return fmt.Errorf("posts.publish: arg[0] is %T, want map", args[0])
	}
	id, _ := post["id"].(string)
	if id == "" {
		return fmt.Errorf("posts.publish: missing or empty id")
	}

	// Persist the id under the plugin's KV namespace. The host
	// adds the per-plugin prefix; we see our own bare keys.
	if err := sdk.Host.KV.Set("last-published-id", []byte(id)); err != nil {
		// KV failures aren't fatal to the action — the audit
		// emission below is the load-bearing side effect.
		sdk.Host.Log.Warn("kv.set failed: " + err.Error())
	}

	// Emit one audit row. Metadata becomes the audit_log row's
	// metadata JSON column; the host tags it with our slug.
	if err := sdk.Host.Audit.Emit("post.indexed", map[string]any{
		"post_id": id,
		"plugin":  "gonext-sdk-go-hello",
	}); err != nil {
		// Audit failures are best-effort host-side; a failure
		// here typically means the audit store is down — not the
		// plugin's fault. Log and continue.
		sdk.Host.Log.Warn("audit.emit failed: " + err.Error())
	}

	return nil
}

// onTheContent is the filter handler for the_content. It receives
// the post body as a JSON-encoded string (raw bytes) and returns the
// transformed body — same shape.
//
// We append a hello-world marker to demonstrate value-transforming
// filters. A real plugin would do something useful like injecting
// SEO meta, rewriting links, or running a markdown pipeline.
func onTheContent(value json.RawMessage, _ []any) (json.RawMessage, error) {
	var body string
	if err := json.Unmarshal(value, &body); err != nil {
		return nil, fmt.Errorf("the_content: unmarshal value: %w", err)
	}
	body += "\n<!-- enhanced by gonext-sdk-go-hello -->"
	return json.Marshal(body)
}
