# Building a SEO Plugin from Scratch — a 30-minute tutorial

Audience: a plugin author with Go background who wants to ship their
first plugin against the GoNext plugin runtime. By the end of this
walkthrough you'll have a working SEO plugin that injects meta tags,
emits JSON-LD, and computes a per-post SEO score — the same plugin that
ships in `examples/plugins/seo/` as the canonical worked example.

If you just want the finished code, read it there. This file walks
through *why* each piece is the way it is, in the order you'd write it.

> The completed example also serves as a smoke test for the runtime:
> when CI green-lights `go test ./examples/plugins/seo/...` the whole
> plugin stack — manifest validation, schema gates, hook ABI, job ABI,
> capability registry — has at least one consumer-facing test in front
> of it.

## What we're building

A clone of WordPress's Yoast SEO plugin, simplified for the tutorial:

- Inject `<title>` + `<meta name="description">` into `<head>`.
- Add OpenGraph (`og:*`) and Twitter card (`twitter:*`) meta tags.
- Append a schema.org Article JSON-LD `<script>` block to post content.
- Compute a 0..100 SEO score on every post save.
- Expose a background "rebuild all scores" job operators can trigger.

The plugin is ~400 lines of TinyGo, plus ~300 lines of tests.

## Prerequisites

- Go 1.25 or later (host toolchain).
- TinyGo 0.31 or later (to build the WASM blob).
- A local GoNext checkout (the host you'll load the plugin into).

If you don't have TinyGo installed, you can still complete steps 1–5
and run the test suite — the build step is the only one that requires
it.

## 1. Manifest first

Every plugin starts with `manifest.json`. The manifest tells the host
which capabilities to grant, which hooks to subscribe to, and which
background jobs to register.

Create `examples/plugins/seo/manifest.json`:

```json
{
  "apiVersion": "gonext.io/v1",
  "name": "gonext-seo",
  "version": "0.1.0",
  "entry": "seo.wasm",
  "capabilities": [
    "posts.read",
    "posts.write",
    "hooks.subscribe",
    "jobs.enqueue"
  ],
  "hooks": {
    "filters": ["the_content"],
    "actions": ["wp_head", "save_post"]
  },
  "jobs": ["seo.recompute-scores"],
  "requires": { "host": ">=0.1.0" }
}
```

Three things to notice:

- **`name` matches the slug** the lifecycle Manager will use in the
  database. The regex in `packages/go/plugins/manifest/schema.json`
  forces lowercase ASCII + hyphens.
- **Capabilities are declared up front.** The operator who installs
  the plugin sees this list in the install dialog and approves it
  once. The runtime then refuses any host call the plugin makes
  outside that grant — see `packages/go/plugins/capabilities/checker.go`.
- **Job names use dots and hyphens** but not underscores — the regex
  is `^[a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)*$`. The hooks regex is
  more permissive (it allows underscores) because hook names mirror
  WordPress conventions (`the_content`, `wp_head`, etc.).

Validate the manifest before writing any code:

```go
// short test driver
import "github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"

data, _ := os.ReadFile("manifest.json")
m, err := manifest.Validate(data)
if err != nil { panic(err) }
fmt.Printf("%+v\n", m)
```

If the manifest passes here, the lifecycle Manager will accept it. If
it fails, the error tells you exactly which JSON pointer is wrong —
the schema is the source of truth.

## 2. The plugin's ABI surface

Every plugin must export four functions. The runtime resolves them by
name when it loads your WASM blob:

```
gn_alloc(size i32) -> i32           // allocate guest memory
gn_free(ptr i32, size i32)          // free guest memory
gn_handle_hook(...) -> i64          // dispatch hooks
gn_handle_job(...) -> i64           // dispatch background jobs
```

The contracts are documented in:

- `packages/go/plugins/abi/hooks/abi.go` — hook entry point + result-packing.
- `packages/go/plugins/abi/jobs/abi.go`  — job entry point + result-packing.

The packed-i64 return is the load-bearing simplification: instead of
two host-bound callbacks for `(ptr, len)`, the guest returns a single
i64 where the high half is the pointer and the low half is the length
(int32 — so a negative status sentinel is distinguishable from a real
length).

### Why one entry point per ABI?

Both ABIs use a single multiplexing entry point — `gn_handle_hook`
takes the hook name as an argument, and the guest does its own switch.
Two reasons:

1. **Stable ABI.** New hooks don't require re-signing or re-publishing
   the plugin — they're just new names dispatched through the same
   entry.
2. **Bounded export table.** Without multiplexing, a plugin with N
   hooks would export N functions, blowing up wazero's lookup cost and
   forcing the manifest to be known at compile time.

## 3. TinyGo skeleton

Create `examples/plugins/seo/main.go` with the build tag:

```go
//go:build tinygo

package main

import (
    "encoding/json"
    "unsafe"
)

func main() {} // TinyGo requires it, even though dispatch is via exports.

//export gn_alloc
func gnAlloc(size uint32) uint32 {
    if size == 0 { return 1 }
    buf := make([]byte, size)
    allocations = append(allocations, buf) // keep a reference so GC doesn't reclaim
    return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

//export gn_free
func gnFree(ptr uint32, size uint32) {}

var allocations [][]byte
```

Allocator strategy: keep a global `[][]byte` slice of every
allocation we've made. The retained reference is what prevents
TinyGo's GC from reclaiming the bytes while the host still holds a
pointer. A real plugin SDK would back this with a per-invocation bump
arena — but for an example, leak-on-purpose is the simplest correct
choice.

The `//go:build tinygo` constraint is important: it lets stock Go
ignore this file (so `go test` against the package only picks up
`domain.go` and `seo_test.go`). Without the constraint, `unsafe.Slice`
on raw pointers fails at runtime under stock Go.

## 4. Domain logic — pure functions, no ABI

Put the SEO logic in a separate file (`domain.go`) with no build tag:

```go
package main

type Post struct {
    Title, Excerpt, Content, URL, Image, Brand, Author, PubDate string
}

func BuildTitle(p Post) string         { ... }
func BuildDescription(p Post) string   { ... }
func BuildHeadHTML(p Post) string      { ... }
func BuildJSONLD(p Post) string        { ... }
func ComputeSEOScore(p Post) int       { ... }
```

These are pure functions over `Post`. Three benefits:

1. **Testable.** Stock Go can call them directly — `go test` doesn't
   need TinyGo installed.
2. **Reusable.** The dummy host bus (next section) calls the same
   functions the TinyGo build links into the WASM blob, so the test
   contract proves the same code the operator will run.
3. **Forkable.** Operators who want to extend the rubric edit
   `ComputeSEOScore` — no WASM internals required.

The full implementation is in `examples/plugins/seo/domain.go`.
Highlights:

- `BuildDescription` falls back excerpt → first paragraph → title.
- `BuildJSONLD` emits Google's required Article rich-result fields.
- `ComputeSEOScore` distributes 100 pts across six checks (see the
  example's README for the rubric table).

## 5. Hook + job dispatch

The two dispatch functions are the glue between the WASM ABI and the
domain logic:

```go
//export gn_handle_hook
func gnHandleHook(namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
    name := string(readGuestBytes(namePtr, nameLen))
    payload := readGuestBytes(payloadPtr, payloadLen)
    switch name {
    case "the_content":
        return invokeContentFilter(payload)
    case "wp_head":
        return invokeWPHead(payload)
    case "save_post":
        return invokeSavePost(payload)
    default:
        return packResult(0, statusUnknownHook)
    }
}

//export gn_handle_job
func gnHandleJob(namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
    name := string(readGuestBytes(namePtr, nameLen))
    payload := readGuestBytes(payloadPtr, payloadLen)
    switch name {
    case "seo.recompute-scores":
        return invokeRecomputeScoresJob(payload)
    default:
        return packResult(0, statusError)
    }
}
```

Each `invoke*` function:

1. Unmarshals the payload (FilterPayload or ActionPayload — see
   `packages/go/plugins/abi/hooks/marshal.go`).
2. Calls the relevant domain function.
3. For filters, marshals the transformed value back as a
   `FilterResult` envelope and writes it to fresh guest memory via
   `gn_alloc`, returning the (ptr, len) packed into the i64 result.
4. For actions, returns `packResult(0, statusOK)` — actions have no
   body.

The full dispatch in `examples/plugins/seo/main.go` is annotated line
by line.

## 6. The dummy host bus (proving the contract without TinyGo)

The example ships a `dummy_host_test.go` file that simulates what the
production wazero dispatcher does:

```go
func runFilterThroughBus(ctx context.Context, hookName string, payloadBytes []byte) (json.RawMessage, error) {
    // mirror invokeContentFilter, but without the unsafe.Slice pointer math
    var fp struct {
        Kind  string          `json:"kind"`
        Value json.RawMessage `json:"value"`
        Args  []interface{}   `json:"args"`
    }
    json.Unmarshal(payloadBytes, &fp)
    var inputHTML string
    json.Unmarshal(fp.Value, &inputHTML)
    post := postFromArgs(fp.Args)
    jsonld := BuildJSONLD(post)
    out := inputHTML + "\n" + jsonld
    encoded, _ := json.Marshal(out)
    return encoded, nil
}
```

The test then builds the same `FilterPayload` the production
dispatcher would marshal, runs the dummy bus, and asserts the
returned HTML carries `<meta property="og:title">` and the JSON-LD
block:

```go
func TestE2E_ContentFilter_EmitsOpenGraph(t *testing.T) {
    post := samplePost()
    value, _ := json.Marshal("<p>Hi from a post.</p>")
    payload, _ := json.Marshal(map[string]interface{}{
        "kind": "filter", "value": json.RawMessage(value),
        "args": []interface{}{post},
    })
    result, _ := runFilterThroughBus(context.Background(), "the_content", payload)
    var outHTML string
    json.Unmarshal(result, &outHTML)
    if !strings.Contains(outHTML, `<script type="application/ld+json">`) {
        t.Fatal("missing JSON-LD")
    }
}
```

This proves the contract works end-to-end without needing TinyGo,
which keeps CI fast and developer onboarding friction low.

A canonical copy of the dummy-bus contract also lives at
`packages/go/plugins/internal/_seo_dummy.go` — the leading underscore
in the filename tells the Go tool to skip it for builds, but the
documentation in that file mirrors what the working copy in the
example does.

## 7. Building the WASM blob

The example ships `build.sh`. It runs:

```bash
tinygo build -target=wasi -no-debug -o seo.wasm .
```

Then it verifies the four required exports are present in the blob:

```bash
required_exports=(gn_handle_hook gn_handle_job gn_alloc gn_free)
for sym in "${required_exports[@]}"; do
  grep -aq "$sym" seo.wasm || exit 1
done
```

The grep is a quick sanity check; in CI you'd run
`wasm-tools strip --print-imports` for a stricter check.

To pack into a `.gnplugin` bundle (a ZIP):

```bash
zip -j seo.gnplugin manifest.json seo.wasm
```

## 8. Installing into the host

With the bundle ready:

```bash
gonext plugin install ./seo.gnplugin
gonext plugin activate gonext-seo
```

`Install` validates the manifest against the schema and persists a
row in the `plugins` table (state: `installed`). `Activate` runs the
capability gate, loads the WASM module via wazero, calls the
plugin's exported `on_activate` (if any), and flips the row to
`active`. Both calls are documented in
`packages/go/plugins/lifecycle/manager.go`.

If the operator declined any of the four capabilities, the activation
fails with `capabilities.ErrCapabilityDenied` naming the missing cap.

## 9. Watching it run

Trigger each subscription:

- **`the_content` filter.** Visit any post on the site. The renderer
  invokes the filter; your JSON-LD block appears at the end of the
  post HTML.
- **`wp_head` action.** Same page load. The plugin runs in fan-out
  with the renderer's head-block assembly; the meta tags appear
  inside `<head>`.
- **`save_post` action.** Edit a post and click Save. The plugin
  computes the SEO score and (in a real implementation) writes it to
  `_seo_score` post meta via `posts.write`.
- **`seo.recompute-scores` job.** From the admin UI, click "Rebuild
  all SEO scores" — that enqueues an Asynq task with this job ID, the
  worker picks it up, and your plugin's `gn_handle_job` runs once per
  batch.

## 10. Extension ideas

Once the plugin is working, the natural extensions are:

- **Add `hreflang`.** Read the post's translations via a host call
  and emit `<link rel="alternate" hreflang>` tags. No new capability
  required — `posts.read` covers it.
- **Submit pingbacks.** Subscribe to `post.published`, POST to the
  Google/Bing IndexNow APIs. Add `http.fetch` to your manifest.
- **Author the site description from the brand.** Pull a "site
  description" setting from KV (declare `kv.read`) and append it to
  every `og:site_name` tag.

Each extension follows the same pattern: declare the capability,
subscribe to a hook, write the domain function, add a test.

## 11. What you've learned

By the time the tests pass, you've exercised:

| Subsystem                                           | What the plugin did                          |
| --------------------------------------------------- | -------------------------------------------- |
| `packages/go/plugins/manifest`                      | Authored and validated `manifest.json`.      |
| `packages/go/plugins/capabilities`                  | Requested 4 caps; runtime enforced them.     |
| `packages/go/plugins/abi/hooks`                     | Filter + 2 actions through `gn_handle_hook`. |
| `packages/go/plugins/abi/jobs`                      | One background job through `gn_handle_job`.  |
| `packages/go/plugins/lifecycle`                     | Install → activate → deactivate.             |
| `packages/go/plugins/runtime` (wazero)              | Loaded the WASM blob, called the exports.    |

That's the full plumbed surface of the plugin system, in 30 minutes,
with a real-world plugin to show for it.

Now go fork the example and ship something more interesting.
