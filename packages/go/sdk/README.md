# GoNext Plugin SDK (Go)

TinyGo-targeted Go SDK for writing GoNext plugins.

## Hello, world

```go
package main

import "github.com/Singleton-Solution/GoNext/packages/go/sdk"

func main() {
    sdk.RegisterAction("posts.publish", func(args []any) error {
        sdk.Host.KV.Set("last-published-id", []byte(args[0].(string)))
        sdk.Host.Audit.Emit("post.indexed", map[string]any{"id": args[0]})
        return nil
    })
    sdk.PluginInit(sdk.NewManifest("hello", "0.1.0").
        WithCapability("kv.write").WithCapability("audit.emit").
        WithAction("posts.publish").MustBuild())
}
```

Build with `tinygo build -target=wasi -no-debug -o plugin.wasm .` and
ship the wasm + a matching `manifest.json` as a `.gnplugin` ZIP. The
`gonext plugin init --template=go` CLI scaffold writes the manifest
and `Makefile` for you.

## What's in the SDK

- `RegisterAction(name, handler)` / `RegisterFilter(name, handler)` —
  the hook bus mounted via the plugin's `gn_handle_hook` export.
- `sdk.Host.HTTP.Fetch`, `sdk.Host.DB.Read/Write`, `sdk.Host.KV.*`,
  `sdk.Host.Cache.Invalidate`, `sdk.Host.Media.Read`,
  `sdk.Host.Users.Read`, `sdk.Host.Secrets.Get`, `sdk.Host.Audit.Emit`,
  `sdk.Host.Cron.Register`, `sdk.Host.Metric.Observe`,
  `sdk.Host.Event.Emit`, `sdk.Host.Span.AddEvent`,
  `sdk.Host.I18n.Translate`, `sdk.Host.Log.Info`, `sdk.Host.Time.NowMs`
  — typed wrappers over every `gn_*` host ABI. Capability gates,
  audit emission, SSRF guards, and rate limits all run host-side.
- `sdk.NewManifest(name, version).With*().Build()` — a fluent
  manifest builder. Emits the JSON the host's schema validator
  accepts.

See `examples/plugins/sdk-go-hello/` for a complete worked example
that exercises an action handler, a filter handler, a `gn_kv_set`
call, and a `gn_audit_emit` call.

## Constraints

- TinyGo's stdlib is a subset of Go's. Avoid `net/http`,
  `database/sql`, `crypto/tls` — the host provides those via the ABIs
  above. `encoding/json`, `strings`, `strconv`, `errors`, `sync`,
  `unsafe` all work.
- The SDK's own dependency graph is empty (stdlib only) so a plugin's
  wasm stays small. A typical hello-world compiles to ~80 KiB
  uncompressed.
- The same SDK package compiles under the stock Go toolchain (with
  the host-call layer stubbed out) so plugin authors can write
  ordinary unit tests against their handler functions.
