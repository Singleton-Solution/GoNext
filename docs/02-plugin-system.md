# 02 — Plugin System

> The plugin subsystem. WASM-sandboxed server logic + ES-module-loaded admin UI, glued together by a hook/filter bus and a capability-scoped host ABI. This is **the** highest-risk piece of the platform; if this isn't viable, the project isn't viable.
>
> Read [`00-architecture-overview.md`](00-architecture-overview.md) first. Reader assumed: senior backend engineer familiar with WordPress's plugin model, WASM runtimes, and the general "plugin host" problem space.

---

## 0. Why this doc is long

WordPress's plugin ecosystem is what made WordPress win. It is also what made WordPress a security nightmare, a performance nightmare, and a forwards-compatibility nightmare. Every architectural decision in this doc is in tension with that history:

- **Match WP's expressiveness** — hooks, filters, priorities, side effects everywhere — or authors won't be able to port what they know.
- **Don't match WP's danger model** — where every plugin runs as the host process, owns the database, and can `eval()` arbitrary HTTP-fetched code — or we ship the same disaster on a different runtime.

The way to thread this needle is: **expressive surface, narrow privilege**. Plugins get a WP-shaped API. They do not get the keys to the box.

This document is opinionated. Where there is a real disagreement among reasonable engineers, it is called out in **Trade-offs & rejected alternatives** (§13) or **Open questions** (§15). Everywhere else, treat the recommendation as the design intent.

---

## 1. High-level architecture

```
                       ┌─────────────────────────────────────────────────────┐
                       │                   Go API Server                     │
                       │                                                     │
   HTTP req ──▶ Router ──▶ Handler ──▶ HookBus.ApplyFilter("the_content", v) │
                       │                       │                             │
                       │                       ▼                             │
                       │              ┌───────────────────┐                  │
                       │              │  Plugin Manager   │                  │
                       │              │  - registry       │                  │
                       │              │  - lifecycle      │                  │
                       │              │  - capability mux │                  │
                       │              └────────┬──────────┘                  │
                       │                       │                             │
                       │           dispatch in priority order                │
                       │                       │                             │
                       │       ┌───────────────┼───────────────┐             │
                       │       ▼               ▼               ▼             │
                       │  ┌─────────┐    ┌─────────┐    ┌─────────┐          │
                       │  │ WASM    │    │ WASM    │    │ WASM    │          │
                       │  │ instance│    │ instance│    │ instance│          │
                       │  │ pluginA │    │ pluginB │    │ pluginC │          │
                       │  └────┬────┘    └────┬────┘    └────┬────┘          │
                       │       │              │              │               │
                       │       └──────────────┴──────────────┘               │
                       │              host_* import calls                    │
                       │              (db, kv, http, log, ...)               │
                       │                       │                             │
                       │                       ▼                             │
                       │              ┌───────────────────┐                  │
                       │              │  Capability gate  │── deny ─▶ trap   │
                       │              └────────┬──────────┘                  │
                       │                       │ allow                       │
                       │                       ▼                             │
                       │              ┌───────────────────┐                  │
                       │              │ Scoped resource   │                  │
                       │              │ (DB/Redis/HTTP/…) │                  │
                       │              └───────────────────┘                  │
                       └─────────────────────────────────────────────────────┘

                Browser (admin/site) loads frontend extensions separately:

   <script type="importmap">
     { "imports": {
         "@host/sdk":      "/_host/sdk.js",
         "@plugin/seo":    "/_plugins/seo/1.4.2/index.js",
         "@plugin/forms":  "/_plugins/forms/2.1.0/index.js"
       } }
   </script>
```

Two execution surfaces:

1. **Server-side WASM** for hook handlers, REST endpoints, cron jobs, filters on saved content.
2. **Client-side ES modules** for admin pages, block registrations, editor sidebars, frontend interactive blocks.

They are **separate artifacts** in the same plugin bundle. They share a `slug`, a manifest, and the plugin's namespace, but they cross-talk only through:

- The plugin's own REST endpoints (`/api/plugins/{slug}/...`), exposed via `http.serve` capability.
- Plugin-owned tables / KV namespaces.
- The hook bus (server-side only — JS doesn't directly call WASM hooks; instead, the JS extension calls the plugin's REST endpoint, which then runs in WASM and can call hooks).

This split is opinionated: **we deliberately do not run server logic in browser-style JS**, and we deliberately do not run UI logic in WASM. Each side uses the tool it's best at.

---

## 2. Plugin package format

### 2.1 The `.gnplugin` bundle

A plugin ships as a single `.gnplugin` file. It is a ZIP archive with a fixed layout:

```
my-seo-plugin-1.4.2.gnplugin
├── manifest.json
├── server/
│   ├── plugin.wasm              # main WASM module
│   └── plugin.wasm.sig          # detached signature (sigstore bundle)
├── web/
│   ├── index.js                 # ES module entry, the admin/editor extension
│   ├── editor.js                # (optional) editor-only entry
│   ├── frontend.js              # (optional) site-frontend entry
│   ├── assets/                  # static assets (svgs, css, fonts)
│   └── blocks/
│       └── seo-meta/
│           ├── block.json
│           └── index.js
├── migrations/
│   ├── 0001_init.up.sql
│   ├── 0001_init.down.sql
│   └── 0002_add_redirects.up.sql
├── translations/
│   ├── en.json
│   ├── es.json
│   └── ja.json
├── README.md
└── LICENSE
```

Hard rules:

- The bundle is **immutable on disk** after install. Updates produce a new versioned directory. No plugin can write to its own bundle.
- The bundle's content hash is recorded in the DB. Hash mismatch on load = refuse to load.
- Total bundle size cap: **50 MB** (configurable, but enforced). Anything larger is almost always a packaging mistake.
- WASM module size cap: **20 MB**. Frontend JS cap: **5 MB** per entry.

### 2.2 `manifest.json` schema

The manifest is the single source of truth for what a plugin **is**, what it **needs**, and what it **provides**.

```json
{
  "$schema": "https://wpc.dev/schemas/plugin-manifest-v1.json",

  "slug": "gn-seo",
  "name": "WPC SEO",
  "version": "1.4.2",
  "abi_version": 1,
  "license": "GPL-3.0-or-later",
  "homepage": "https://example.com/gn-seo",
  "author": { "name": "Acme Co", "email": "hello@acme.dev" },

  "description": "Adds sitemaps, meta tags, redirects, and structured data.",

  "platform": {
    "min_core_version": "1.0.0",
    "max_core_version": "2.x"
  },

  "server": {
    "wasm": "server/plugin.wasm",
    "memory_limit_mb": 64,
    "fuel_per_invocation": 50000000,
    "invocation_timeout_ms": 250,
    "exports": {
      "init":           { "required": true,  "signature": "() -> i32" },
      "shutdown":       { "required": false, "signature": "() -> i32" },
      "on_activate":    { "required": false, "signature": "() -> i32" },
      "on_deactivate":  { "required": false, "signature": "() -> i32" },
      "hook_handler":   { "required": true,  "signature": "(i32 ptr, i32 len) -> i64" }
    }
  },

  "hooks": {
    "actions": [
      { "name": "post.published",   "priority": 10, "handler": "on_post_published" },
      { "name": "post.deleted",     "priority": 20, "handler": "on_post_deleted" }
    ],
    "filters": [
      { "name": "the_content",      "priority": 50, "handler": "filter_content" },
      { "name": "the_title",        "priority": 10, "handler": "filter_title" },
      { "name": "rest.post.serialize", "priority": 10, "handler": "filter_rest_post" }
    ]
  },

  "capabilities": {
    "db":          { "read": ["core.posts", "core.terms"], "write": ["plugin.tables"] },
    "kv":           true,
    "queue":        true,
    "cron":         { "jobs": [
      { "name": "rebuild_sitemap", "schedule": "0 */6 * * *" }
    ]},
    "http":         { "fetch": { "allow_hosts": ["api.googleapis.com", "api.bing.com"] } },
    "http.serve":   { "routes": [
      { "method": "GET",  "path": "/sitemap.xml",            "handler": "route_sitemap" },
      { "method": "POST", "path": "/redirects",              "handler": "route_create_redirect" },
      { "method": "GET",  "path": "/redirects/:id",          "handler": "route_get_redirect" }
    ]},
    "media.read":   true,
    "users.read":   { "fields": ["id", "display_name", "roles"] },
    "cache.invalidate": true,
    "audit.emit":   true,
    "email":        false
  },

  "grants_capabilities": [
    { "slug": "manage_seo",        "description": "Configure SEO settings and view SEO reports." },
    { "slug": "view_seo_reports",  "description": "View SEO reports (read-only)." }
  ],

  "secrets": { "keys": ["google_indexing_api_token"] },

  "admin_pages": [
    {
      "slug":       "gn-seo/dashboard",
      "parent":     "tools",
      "title":      "SEO",
      "icon":       "search",
      "capability": "manage_seo",
      "entry":      "ui/admin/dashboard.js"
    },
    {
      "slug":       "gn-seo/redirects",
      "parent":     "gn-seo/dashboard",
      "title":      "Redirects",
      "capability": "manage_seo",
      "entry":      "ui/admin/redirects.js"
    }
  ],

  "web": {
    "admin":    { "entry": "web/index.js" },
    "editor": {
      "entry": "web/editor.js",
      "panels": [
        { "id": "gn-seo-sidebar", "title": "SEO", "target": "document-sidebar" }
      ]
    },
    "frontend": { "entry": "web/frontend.js" },
    "blocks":   [ "web/blocks/seo-meta/block.json" ]
  },

  "migrations": {
    "dir": "migrations/",
    "table_prefix": "plg_seo_"
  },

  "translations": {
    "dir": "translations/",
    "default_locale": "en"
  },

  "signing": {
    "issuer": "https://fulcio.sigstore.dev",
    "bundle": "server/plugin.wasm.sig"
  }
}
```

Schema notes — all opinionated:

- **`slug`** is the namespace. It is also the route prefix (`/api/plugins/{slug}`), the table prefix root (`plg_{slug}_*`), the KV namespace, and the JS import name (`@plugin/{slug}`). It is `^[a-z][a-z0-9-]{2,40}$`. It is unique platform-wide and cannot change without re-registering.
- **`abi_version`** is the host ABI the plugin was compiled against. The host advertises the list of ABI versions it supports; a plugin compiled against ABI 1 keeps working as long as the host still supports ABI 1, even after ABI 2 ships. This is the one place we **must** be backwards compatible.
- **`hooks`** declared in the manifest is the **static** registration. Plugins can also register hooks dynamically at runtime (e.g., conditional on a setting), but a manifest declaration lets the host warm up the dispatcher's routing table without instantiating the plugin.
- **`capabilities`** is the explicit list of host APIs the plugin uses — the **sandbox** vocabulary. Grammar is dotted with scopes carried as separate string arrays inside the capability's value object: `{"db": {"read": ["core.posts"], "write": ["plugin.tables"]}}`, `{"http": {"fetch": {"allow_hosts": [...]}}}`. Simple boolean toggles (`"kv": true`, `"cache.invalidate": true`) are accepted for capabilities without scopes. The user sees this list verbatim on install. **No capability not in the manifest is grantable at runtime.** This is the single most important property of the system. (§6, §10.)
- **`grants_capabilities`** is the **user-facing** capability registration — a separate concept from the sandbox `capabilities` block above. Each entry registers a user capability (a slug from the role/capability system in [`06-auth-permissions.md`](06-auth-permissions.md) §6) that humans can be assigned through the admin UI. The two vocabularies are intentionally disjoint and live in two different slots. (See [`06-auth-permissions.md`](06-auth-permissions.md) §14 for the full distinction.)
- **`admin_pages`** is the single canonical slot for plugin-supplied admin pages (§7.3). The admin shell composes its menu from this array.
- **`secrets`** declares the named secret keys the plugin reads via `host.secrets.get(key)`. The host serves them from its encrypted store (§6.7). The capability is implicit — declaring `secrets.keys` is the opt-in.
- **`exports`** are the WASM exports the host calls. We standardize on a tiny set: `init`, `shutdown`, `on_activate`, `on_deactivate`, plus one generic `hook_handler` entry point that the host dispatches into by hook name. (§4.4.)
- **`platform.min_core_version` / `max_core_version`** are SemVer ranges. The plugin installer refuses to install a plugin whose range doesn't include the running core version.

<!-- fixed per review (P1, P2, P4, B6, C7, C15): canonical manifest is `manifest.json` with two-vocabulary capability declarations — top-level `capabilities` (sandbox) and top-level `grants_capabilities` (user-facing). DB grammar uses dotted names + separate scope strings. Admin pages live in a top-level `admin_pages` slot. JSON Schema for the manifest itself, for block attributes (§7.4), and for plugin-supplied settings is pinned to **JSON Schema 2020-12** (§7.7). -->


### 2.3 Why one big bundle and not multiple URLs

Tempting alternative: serve the WASM, the JS, the assets, etc. from a CDN with hashes pinned in the manifest. We **don't** do this because:

- Plugins are sensitive code. We want a single signed artifact, downloaded once, scanned once, verified once.
- Self-hosters often run airgapped. A bundle that works from disk is a hard requirement.
- The hash-of-the-bundle is the unit of "is this thing what I think it is?" Single artifact = single hash.

We may later add lazy-loaded WASM chunks for very large plugins. Not in v1.

---

## 3. Plugin lifecycle

### 3.1 States

A plugin moves through these states. Transitions are explicit and recorded in `plugins` table.

```
        ┌─────────────┐
        │   absent    │
        └──────┬──────┘
               │ upload + verify
               ▼
        ┌─────────────┐
        │  installed  │   (on disk, registered in DB, NOT running)
        └──────┬──────┘
               │ activate (DB migrations up, capabilities granted)
               ▼
        ┌─────────────┐
        │   active    │   (instantiated on demand, hooks dispatched)
        └──────┬──────┘
               │ deactivate
               ▼
        ┌─────────────┐
        │  installed  │   (still on disk, hooks dormant)
        └──────┬──────┘
               │ uninstall (DB migrations down, files removed, capabilities revoked)
               ▼
        ┌─────────────┐
        │   absent    │
        └─────────────┘
```

Additional terminal-ish states:

- **`failed`**: instantiation or migration failed; plugin is on disk but cannot be activated until the operator clears the failure.
- **`disabled_by_policy`**: the operator has banned this plugin slug (or this exact hash). Cannot be reactivated.

### 3.2 Install

```
┌────────────────────────────────────────────────────────────────────────┐
│  Install flow (admin uploads .gnplugin or installs from registry)     │
└────────────────────────────────────────────────────────────────────────┘

  admin                 core                disk                 db
   │                     │                    │                   │
   │ POST /api/plugins   │                    │                   │
   │ (bundle)            │                    │                   │
   │────────────────────▶│                    │                   │
   │                     │  unzip to temp     │                   │
   │                     │───────────────────▶│                   │
   │                     │  parse manifest    │                   │
   │                     │  validate schema   │                   │
   │                     │  verify signature  │                   │
   │                     │  size/abi gates    │                   │
   │                     │  pre-compile wasm  │                   │
   │                     │  (wazero compile)  │                   │
   │                     │                    │                   │
   │                     │  if all OK:        │                   │
   │                     │  move to           │                   │
   │                     │  plugins/{slug}/{version}/             │
   │                     │───────────────────▶│                   │
   │                     │                    │                   │
   │                     │  INSERT plugins    │                   │
   │                     │  (state=installed) │                   │
   │                     │──────────────────────────────────────▶ │
   │                     │                    │                   │
   │ 201 Created         │                    │                   │
   │  + capabilities[]   │                    │                   │
   │◀────────────────────│                    │                   │
   │                     │                    │                   │
   │ user reviews caps   │                    │                   │
   │ POST /activate      │                    │                   │
   │────────────────────▶│                    │                   │
   │                     │  run migrations up │                   │
   │                     │──────────────────────────────────────▶ │
   │                     │  instantiate wasm  │                   │
   │                     │  call on_activate()│                   │
   │                     │  update state      │                   │
   │                     │  → active          │                   │
   │                     │──────────────────────────────────────▶ │
   │ 200 OK              │                    │                   │
   │◀────────────────────│                    │                   │
```

**Crucial:** install never auto-activates. The user reviews the capability list, sees the diff vs. installed plugins, and explicitly clicks Activate.

### 3.3 Migrations

Plugin-owned tables are namespaced (`plg_{slug}_*`). Each migration file is plain SQL.

- **`activate`** runs all pending `*.up.sql` in order, inside a transaction.
- **`deactivate`** does **not** run down migrations. The data persists. (Otherwise users would lose data every time they toggled a plugin off.)
- **`uninstall`** runs all `*.down.sql` in reverse order, then drops all `plg_{slug}_*` tables that survived (belt and suspenders), then deletes the bundle directory.

The `plg_` prefix is enforced. A migration that tries to `CREATE TABLE posts` is rejected by a static SQL linter that runs as part of bundle verification. (We are not running the plugin's SQL with a fully unrestricted Postgres role anyway; see §6.2.)

### 3.4 Versioning & updates

Updates are install-of-new-version then atomic swap:

```
plugins/
└── gn-seo/
    ├── 1.4.1/          ← still on disk, swappable back
    ├── 1.4.2/          ← currently active (symlink target)
    └── current ──▶ 1.4.2
```

Update flow:

1. Upload `gn-seo-1.4.3.gnplugin`.
2. Verify + pre-compile WASM, write to `1.4.3/`.
3. Run migrations from previous version's last applied to new version's latest.
4. Atomically retarget `current` symlink.
5. Drain in-flight invocations for the old WASM module (give them up to 5s), close the old module, instantiate the new one.
6. Mark old version as "retained for rollback" for N days (default 30), then GC.

Downgrades work the same way in reverse, but the **down** migrations between the two versions must exist or the downgrade is refused.

ABI breaks (a new `abi_version`) **don't auto-update**. The user explicitly opts into the new ABI per-plugin. The old ABI is supported for at least 18 months after a new ABI ships.

---

## 4. WASM runtime

### 4.1 Why `wazero`

[`wazero`](https://github.com/tetratelabs/wazero) is a pure-Go WebAssembly runtime. We pick it over alternatives for these reasons, in order:

1. **No CGO.** The whole Go binary stays cross-compilable, deployable as a single static binary, debuggable with normal Go tools. This matters more than people think. The minute we take a CGO dep, our deploy story degrades, our `go test` story degrades, our cross-compile story degrades.
2. **Compile-once, instantiate-many.** `wazero` exposes `CompiledModule` separately from instances. Compilation is expensive; we pay it once per plugin version at install time, then instantiate many times cheaply.
3. **Resource limits are first-class.** Memory caps, the experimental fuel/instruction counter, context-based cancellation, `Listener` hooks for tracing all work without wrestling the runtime.
4. **Active maintenance, sane API.** Tetrate maintains it and they use it themselves.

Trade-offs we accept by picking wazero (vs. Wasmtime via CGO or vs. wasmer-go):

- **No native code JIT today.** wazero has an optimizing compiler (the "compiler" engine, faster than the interpreter), but it does not produce SIMD-tuned native machine code at the level of Wasmtime/Cranelift. For hook handlers — short, hot, called millions of times — this matters. Mitigation: we benchmark, and we keep the host ABI escape-hatch so heavyweight number-crunching plugins can call a host function backed by Go rather than do the work in WASM. We will revisit if real workloads demand it.
- **No `wasi-preview-2` / WIT support today.** wazero supports WASI preview 1. The Component Model and WIT are still pre-1.0 across the ecosystem. We use raw imports and design our own ABI. (§4.3.)

### 4.2 Compile vs. instantiate

```
┌─────────────────────────────────────────────────────────────────┐
│   On install:                                                   │
│     CompiledModule = runtime.CompileModule(ctx, wasmBytes)      │
│     cache.Put(slug+version, CompiledModule)                     │
│                                                                 │
│   On first hook dispatch (cold start):                          │
│     Module = runtime.InstantiateModule(ctx, CompiledModule, …)  │
│     Module.ExportedFunction("init").Call(ctx)                   │
│     pool.Put(slug, Module)                                      │
│                                                                 │
│   On subsequent dispatches (warm):                              │
│     Module = pool.Get(slug)                                     │
│     Module.ExportedFunction("hook_handler").Call(ctx, ...)      │
│     pool.Put(slug, Module)  // return to pool                   │
└─────────────────────────────────────────────────────────────────┘
```

`CompiledModule` is the heavy artifact. We persist it in memory for the life of the process and on disk in a cache directory keyed by `(wazero_version, wasm_hash)` so restarts are warm.

### 4.3 Host ABI: raw imports, not WIT

Until WIT/Component Model is broadly supported in our toolchain, we ship raw host imports. The full ABI is in §6, but the shape is:

- **Module name:** `host`. (One module name. Don't fragment.)
- **Function names:** snake_case verbs grouped by capability, e.g., `db_query`, `db_exec`, `kv_get`, `kv_set`, `http_fetch`, `log`, `register_hook`, `emit_event`, `time_now`, etc.
- **Calling convention:** all data crossing the boundary is **length-prefixed bytes in linear memory**. Args of complex shape are serialized as **MessagePack** (small, fast, schema-light) in the guest's linear memory, with a `(ptr, len)` pair passed across.

Why not WIT/Component Model? We will adopt it once:

1. Toolchains for Go, Rust, TS all produce WIT-compatible components without weird workarounds, and
2. wazero (or a viable replacement) lands stable Component Model support.

At that point WIT becomes a thin layer on top of the same ABI shape; the SDKs change, the wire format barely does. Plugins compiled against ABI 1 (raw imports) keep working as long as the host supports ABI 1.

Why not protobuf or JSON across the boundary? Protobuf: needs a descriptor / generated code on both sides, large dep. JSON: text encoding for what is mostly binary structs is wasteful. MessagePack hits the sweet spot: schema-free at the wire level, fast, tiny generators, supported in every plausible plugin language.

### 4.4 The single `hook_handler` export

Every plugin exports exactly one entry point for hook dispatch:

```
hook_handler(ptr i32, len i32) -> i64
```

The host writes a MessagePack-encoded `HookCall` into guest memory at `ptr`/`len`. The guest unpacks, dispatches internally by hook name to the user's handler function, repacks the result, writes the result back via a host-provided `set_result(ptr, len)` call, and returns a status code (the low 32 bits of the `i64`) plus a result-length hint (high 32 bits).

```
                 host                                   guest (WASM)
  ─────────────────────────────────────       ─────────────────────────────────────

  call HookCall { hook="the_content",                  hook_handler(ptr,len)
                  args=[ "<p>hi</p>" ],
                  request_id="...",
                  caps_token=... }                     unpack MessagePack
       │                                                       │
       │ encode MessagePack                                    ▼
       │ allocate in guest memory via         lookup handler for "the_content"
       │   host_alloc(len)                                     │
       │ write to guest memory                                 ▼
       │                                       call user handler:
       │ instance.Call(hook_handler, ptr,len)   func filter_content(s string)
       │                              ─────▶    → string { ... }
       │                                                       │
       │                                                       ▼
       │                                       pack MessagePack
       │                                       host_set_result(ptr, len)
       │                                                       │
       │                              ◀─────                   │
       │      i64 status_and_len                               │
       │                                                       │
       ▼
  unpack result from guest memory
```

Why one entry point and not one export per hook? Two reasons:

1. **Stable ABI.** New hooks don't require re-signing or republishing; they're just new names dispatched through the same entry.
2. **Plugin authors register handlers in their own code.** The SDK gives them `hook.AddFilter("the_content", fn)`; nothing about that is visible to the host's WASM linker.

The manifest's `hooks.*[].handler` field is **documentation** for the runtime registry / UI; the actual dispatch table lives inside the guest, populated by `init()` calling `host.register_hook(name, priority, ...)` as a side effect.

### 4.5 Limits, fuel, timeouts

Each plugin invocation is bound by **all** of these — first one to trip wins:

| Limit | Default | Configurable per-plugin? | Mechanism |
|---|---|---|---|
| Memory (linear) | 64 MB | yes (manifest) | wazero `MemoryLimitPages` |
| Stack depth | 1024 frames | global | wazero default |
| Fuel (instructions) | 50,000,000 per invocation | yes (manifest) | wazero `WithCloseOnContextDone` + custom counter |
| Wall-clock | 250 ms per invocation | yes (manifest) | `context.WithTimeout` |
| Host call concurrency | 1 host call at a time per instance | global | mutex on the instance |
| HTTP fetches per invocation | 8 | yes (capability config) | counter in host code |
| DB queries per invocation | 32 | yes (capability config) | counter in host code |
| Bytes written to KV per invocation | 1 MB | yes | counter |

The fuel and wall-clock limits are belt-and-suspenders. Fuel is precise but can be cheated by a plugin that calls a slow host function (think a 5-second DB query); the wall-clock cap catches that. Wall-clock alone can't catch tight CPU loops fast enough; fuel does.

Going over any limit:

- The host injects a trap (closes the wazero `Module`'s context, which deterministically aborts the guest).
- Returns the hook call's "neutral" value: filters return the input unchanged, actions return success (the action is logged as failed but doesn't bubble).
- Increments an error counter for the plugin. If a plugin trips its limits more than N times per minute (default 5), it is **circuit-broken**: marked degraded, hooks skipped for 60s, then re-tried. After 3 consecutive circuit-break cycles in an hour the plugin is auto-deactivated, an admin event is fired, and the operator gets a notification.

### 4.6 Instance pooling

WASM module instantiation is cheap-ish (single-digit milliseconds with wazero's compiled engine) but not free, and instantiation **destroys all in-module state** — which is mostly a feature, but not on every call.

Model:

- **One `CompiledModule` per plugin version** (immutable, shared).
- **A pool of `Module` instances per active plugin**, sized to `min(maxInstances, currentConcurrency * 2)`.
- **Each `Module` instance is single-threaded.** Hook dispatch acquires an instance, runs the call, returns it.
- **State inside the instance is allowed to live across calls.** This is important: SEO plugin can build a cache of sitemap entries once and reuse it. (The plugin author is responsible for not leaking memory; we cap the heap.)
- **Instances are recycled on `shutdown`** (graceful) or on a TTL (default 1 hour) to bound state drift, or when memory hits 80% of cap.

Cold start (first instance for a freshly activated plugin): ~5–20 ms in our prototypes for small plugins. Warm dispatch: ~50–500 µs of host overhead plus whatever the guest does.

For a plugin handling `the_content` on every request, we want ~99% of dispatches to hit a warm instance. Pool sizing math: P99 page-render concurrency × 2, capped at 16 instances per plugin. (Tunable.)

```
┌────────────────────────────────────────────────────────────────────┐
│   Per-plugin instance pool                                         │
│                                                                    │
│       ┌──── available ────┐         ┌──── in-flight ────┐          │
│       │  Module instance  │         │  Module instance  │          │
│       │  Module instance  │  ──▶    │  Module instance  │  ──▶ ret │
│       │  Module instance  │         │                   │          │
│       └───────────────────┘         └───────────────────┘          │
│                                                                    │
│   On Get(): pop available; if empty and len<max, instantiate new   │
│   On Put(): push back to available (or destroy if memory > 80%)    │
└────────────────────────────────────────────────────────────────────┘
```

### 4.7 One module per plugin (yes)

Tempting alternative: link all plugin WASMs into a single module and dispatch internally. Rejected:

- Blast radius. A bug in one plugin shouldn't touch another's memory.
- Capability scoping. Per-instance capability tokens are the cleanest place to enforce permission.
- Lifecycle. Deactivating plugin A should not interrupt plugin B.

The cost is duplicated runtime overhead (a few hundred KB per plugin), which we eat.

---

## 5. Hook bus

### 5.1 Model

Two kinds, just like WordPress:

- **Action**: side-effect, returns nothing useful. `do_action("post.published", post)`.
- **Filter**: transform a value through a chain. `value = apply_filters("the_content", value, ctx)`.

Both have:

- **Priority**: lower = earlier. Default 10. Ties broken by registration order.
- **Args**: arbitrary serializable payload. The first arg of a filter is the value being transformed.
- **Sync or async**: filters are sync (a chain that has to return a value). Actions can be sync or async. Async actions are enqueued and run in the background; the hook bus returns immediately.

### 5.2 Naming convention

WordPress hooks are flat strings — `the_content`, `init`, `wp_head`, `save_post_{type}`. We adopt **dotted, namespaced** names:

- Core hooks: `core.post.published`, `core.post.delete.before`, `core.render.head`, `core.user.created`.
- Filters use the same scheme: `core.filter.the_content`, `core.filter.rest.post.serialize`.
- Plugin-defined hooks: `plg.{slug}.something_happened`.

Reasons:

- Globbing for permissioning (`db.read: core.posts:read`).
- Documentation tooling can group by namespace.
- Easier to reason about ownership.

We provide a WP-compat alias table for the most common WP hook names so ported plugins can keep using `the_content` etc. as a pseudonym for `core.filter.the_content`. Aliases are a thin shim, not a parallel system.

### 5.3 Registration

Three ways a handler is registered, in order of precedence:

1. **Manifest declaration** at install time. The plugin says "I want to handle `core.filter.the_content` at priority 50 with internal name `filter_content`". The host populates its routing table without instantiating the plugin. This means the dispatcher knows whether a plugin wants a hook before paying the cost of instantiation.
2. **Dynamic registration at `init()`**. The plugin's `init()` (run on first instantiation) calls `host.register_hook("core.filter.the_title", 10, "filter_title")` to add or supersede a manifest declaration. This is for hooks whose existence depends on settings.
3. **Within a hook handler**, you may register *other* hooks. This is WP-style and we allow it, with the caveat that newly registered hooks don't take effect until the current dispatch finishes.

Manifest declarations are the **fast path**. If a request only fires hooks no plugin has manifest-declared, no plugin is instantiated at all — and the dispatcher knows this at lookup time, not after the fact.

### 5.4 Dispatch algorithm

```
ApplyFilter(name, value, args...):
  handlers = registry.LookupFilter(name)            # sorted by (priority, regOrder)
  if len(handlers) == 0:
    return value

  ctx, cancel = WithTimeout(parent, 250ms * len(handlers))   # caller-side outer
  defer cancel()

  for h in handlers:
    instance = pool[h.pluginSlug].Get()
    defer pool[h.pluginSlug].Put(instance)

    callCtx, callCancel = WithTimeout(ctx, h.plugin.invocationTimeout)
    result, err = invokeHook(callCtx, instance, HookCall{
      hook: name, value: value, args: args, callKind: FILTER,
      caps: h.plugin.capabilitiesToken,
    })
    callCancel()

    if err != nil:
      circuitBreaker.RecordFailure(h.pluginSlug, err)
      if filter.ShortCircuitOnError:
        break
      continue   # filter: skip this handler, value flows unchanged
    value = result

  return value


DoAction(name, args...):
  handlers = registry.LookupAction(name)
  if len(handlers) == 0:
    return

  for h in handlers:
    if h.async:
      jobQueue.Enqueue(ActionJob{plugin: h.pluginSlug, hook: name, args: args})
      continue
    # sync action: same as filter but discard result
    instance := pool[h.pluginSlug].Get()
    ...
    invokeHook(ctx, instance, HookCall{ hook: name, args: args, callKind: ACTION })
```

### 5.5 Short-circuit semantics

WordPress filter chains can "early return" by … just returning the value. We add explicit options at the call site:

- `ShortCircuitOnError` (default false for filters, true for `before`-style actions).
- `ShortCircuitOnSentinel` (a filter handler may return a sentinel value that says "stop the chain here, use this value"). The sentinel is part of the host ABI.

### 5.6 Concrete Go sketch — dispatcher

```go
// Package hooks implements the hook bus.
package hooks

import (
    "context"
    "errors"
    "sort"
    "sync"
    "time"

    "github.com/tetratelabs/wazero/api"
)

type CallKind uint8

const (
    KindAction CallKind = iota
    KindFilter
)

// Handler is a single registered hook entry.
type Handler struct {
    PluginSlug string
    Hook       string
    Priority   int
    RegOrder   uint64 // for tie-breaking
    HandlerID  string // internal name in the guest
    Async      bool   // actions only
}

// Bus is the dispatcher.
type Bus struct {
    mu       sync.RWMutex
    filters  map[string][]Handler
    actions  map[string][]Handler
    regSeq   uint64

    plugins  PluginRegistry // resolves slug -> wasm instance pool
    jobs     JobQueue
    breaker  *CircuitBreaker
    log      Logger
}

func NewBus(pr PluginRegistry, jq JobQueue, cb *CircuitBreaker, log Logger) *Bus {
    return &Bus{
        filters: map[string][]Handler{},
        actions: map[string][]Handler{},
        plugins: pr, jobs: jq, breaker: cb, log: log,
    }
}

func (b *Bus) Register(h Handler, kind CallKind) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.regSeq++
    h.RegOrder = b.regSeq

    var bucket map[string][]Handler
    if kind == KindFilter {
        bucket = b.filters
    } else {
        bucket = b.actions
    }
    list := append(bucket[h.Hook], h)
    sort.SliceStable(list, func(i, j int) bool {
        if list[i].Priority != list[j].Priority {
            return list[i].Priority < list[j].Priority
        }
        return list[i].RegOrder < list[j].RegOrder
    })
    bucket[h.Hook] = list
}

// ApplyFilter runs the filter chain for `name`, threading `value` through.
func (b *Bus) ApplyFilter(ctx context.Context, name string, value any, args ...any) (any, error) {
    b.mu.RLock()
    handlers := b.filters[name]
    b.mu.RUnlock()
    if len(handlers) == 0 {
        return value, nil
    }

    for _, h := range handlers {
        if !b.breaker.Allow(h.PluginSlug) {
            continue
        }

        result, err := b.invoke(ctx, h, KindFilter, value, args)
        if err != nil {
            b.breaker.RecordFailure(h.PluginSlug, err)
            b.log.Warn("filter handler failed",
                "plugin", h.PluginSlug, "hook", name, "err", err)
            continue // filter: drop this handler, value flows unchanged
        }
        if isShortCircuit(result) {
            return unwrapShortCircuit(result), nil
        }
        value = result
    }
    return value, nil
}

// DoAction fires action `name`. Sync handlers run immediately; async are enqueued.
func (b *Bus) DoAction(ctx context.Context, name string, args ...any) {
    b.mu.RLock()
    handlers := b.actions[name]
    b.mu.RUnlock()
    if len(handlers) == 0 {
        return
    }
    for _, h := range handlers {
        if h.Async {
            _ = b.jobs.Enqueue(ActionJob{
                Plugin: h.PluginSlug, Hook: name, Args: args,
            })
            continue
        }
        if !b.breaker.Allow(h.PluginSlug) {
            continue
        }
        if _, err := b.invoke(ctx, h, KindAction, nil, args); err != nil {
            b.breaker.RecordFailure(h.PluginSlug, err)
            b.log.Warn("action handler failed",
                "plugin", h.PluginSlug, "hook", name, "err", err)
        }
    }
}

func (b *Bus) invoke(
    ctx context.Context,
    h Handler,
    kind CallKind,
    value any,
    args []any,
) (any, error) {
    pool, ok := b.plugins.Pool(h.PluginSlug)
    if !ok {
        return nil, errors.New("plugin not active")
    }

    inst, err := pool.Get(ctx)
    if err != nil {
        return nil, err
    }
    defer pool.Put(inst)

    invCtx, cancel := context.WithTimeout(ctx, inst.InvocationTimeout())
    defer cancel()

    call := HookCall{
        Hook:      h.Hook,
        Handler:   h.HandlerID,
        Kind:      kind,
        Value:     value,
        Args:      args,
        RequestID: requestIDFrom(ctx),
        CapsToken: inst.CapsToken(),
    }
    payload, err := packMsgpack(call)
    if err != nil {
        return nil, err
    }

    ptr, err := inst.Alloc(invCtx, uint32(len(payload)))
    if err != nil {
        return nil, err
    }
    if !inst.Memory().Write(ptr, payload) {
        return nil, errors.New("guest memory write failed")
    }

    fn := inst.ExportedFunction("hook_handler")
    res, err := fn.Call(invCtx, uint64(ptr), uint64(len(payload)))
    if err != nil {
        // includes context deadline exceeded, fuel exhausted, panics
        return nil, err
    }

    statusAndLen := res[0]
    status := int32(statusAndLen & 0xFFFFFFFF)
    resLen := uint32(statusAndLen >> 32)

    if status != 0 {
        return nil, errFromStatus(status)
    }
    resPtr, _ := inst.LastResultPtr()
    out, ok := inst.Memory().Read(resPtr, resLen)
    if !ok {
        return nil, errors.New("guest result read failed")
    }
    var unpacked any
    if err := unpackMsgpack(out, &unpacked); err != nil {
        return nil, err
    }
    return unpacked, nil
}

// Memory + Alloc are wrappers around wazero api.Memory and a guest-exported
// `host_alloc` function that the SDK provides on the guest side.

var _ = api.Memory(nil) // keep import
var _ = time.Duration(0)
```

This is rough — production code grows error wrapping, tracing, metrics — but the shape is real.

### 5.7 Performance

Cost of `apply_filters("the_content", ...)` with one handler, on a warm pool, on a 2024-class server, in our prototypes:

- Lookup + dispatch: ~10 µs
- Msgpack encode: ~5 µs (a 4 KB post body)
- Guest call + decode + handler work (no host calls): ~50–200 µs
- Msgpack decode of result: ~5 µs

A page with 10 filters and 5 actions is ~5 ms of plugin overhead, dominated by guest work. For comparison, a typical WP page burns 200–500 ms in PHP. We have headroom.

The danger isn't single-call cost. It's the **N**: WordPress fires `wp_head` 30+ times in a single page render, and a maximalist plugin set with priority-fanout can stack up thousands of filter calls per page. Mitigation:

- **Hook batching for hot filters.** If a filter is called with the same value repeatedly (e.g., `the_title` for a list of 20 posts), the dispatcher can offer a batched form `apply_filters_batch` that crosses the WASM boundary once with all values. SDKs can transparently use the batch form for handlers that opt in.
- **Lazy hooks.** Cold instances are only spun up when a hook handler is actually needed for that request. Plugins with no manifest-declared hooks for a given request never instantiate.

---

## 6. Host ABI (capabilities)

Every host function checks the plugin's capability token before executing. Tokens are scoped, expiry-bounded, and signed by the host on plugin activation. The plugin never sees a "secret"; the token is opaque and the host has the verification key.

The complete v1 capability list:

| Capability | Host functions | Scoping | Notes |
|---|---|---|---|
| `db.read` | `db_query`, `db_query_one`, `db_exists` | scope list of `table:op` patterns | Always parameterized. Read-only role. Row-level security filters injected. |
| `db.write` | `db_exec`, `db_tx_begin`, `db_tx_commit`, `db_tx_rollback` | scope list, almost always `plugin.tables:*` | Plugin's own tables only by default; explicit grants for core tables are rare and audited. |
| `kv` | `kv_get`, `kv_set`, `kv_del`, `kv_incr`, `kv_scan` | namespace = plugin slug | Redis-backed, TTL supported. Per-plugin size + ops quotas. |
| `queue` | `queue_enqueue`, `queue_status` | named jobs declared in manifest | Asynq job names are namespaced. |
| `cron` | (declared in manifest, not callable) | named schedules in manifest | The plugin's `hook_handler` is invoked with hook `cron.{job_name}`. |
| `http.fetch` | `http_get`, `http_post`, `http_request` | host allowlist (manifest) | All requests proxied through the host; redirects controlled; private IP ranges blocked. |
| `http.serve` | (declared in manifest, plus `http_respond`) | routes under `/api/plugins/{slug}/...` | Incoming HTTP routes to the plugin via the hook bus. |
| `email` | `email_send`, `email_send_template` | optional per-template scope | Uses the host's mailer, so SPF/DKIM/rate-limits apply uniformly. |
| `media.read` | `media_get`, `media_list`, `media_url` | optional collection scope | Returns signed URLs, never raw bytes (unless plugin wants and operator allows). |
| `media.write` | `media_create`, `media_update_meta` | optional collection scope | New uploads go through the host's media pipeline. |
| `users.read` | `users_get`, `users_list`, `users_current` | field allowlist (manifest) | Default fields are non-sensitive (id, display_name, roles). Email requires explicit field grant. |
| `secrets` | `secret_get` | named keys (manifest) | Plugin secrets stored encrypted with a per-plugin DEK. Plugin can only read its own. Implicit grant from declaring `secrets.keys`. (§6.7.) |
| `cache.invalidate` | `cache_invalidate(tags)` | boolean toggle in `capabilities` | Lets the plugin invalidate ISR/page caches by tag. Tag-name conventions are owned by [`07-media-performance.md`](07-media-performance.md) §15; plugins must use those names. Per-plugin rate-limit applies (numbers in the ops doc). (§6.6.) |
| `audit.emit` | `audit_emit(event, metadata)` | boolean toggle in `capabilities` | Plugin emits rows into `audit_log` with `actor_kind='plugin'`. Event-name pattern is enforced (`{slug}.{noun}.{verb}`). See [`06-auth-permissions.md`](06-auth-permissions.md) §14.6. |
| `log` | `log_debug`, `log_info`, `log_warn`, `log_error` | — | Always available, not a real capability — listed for completeness. |
| `i18n` | `t`, `tn` | — | Translations resolved against plugin's bundle. |
| `clock` | `time_now`, `time_now_unix_ms` | — | Always available. |

<!-- fixed per review (P7, B7, B13): added `cache.invalidate`, `audit.emit`, and clarified `secrets` semantics in the capability table. -->


### 6.1 Capability tokens

```
+-----------------------------------------+
| capability token (256 bits, opaque)     |
+-----------------------------------------+
| {                                       |
|   plugin: "gn-seo",                    |
|   version: "1.4.2",                     |
|   instance: "<uuid>",                   |
|   caps: ["db.read:core.posts:read",     |
|          "db.write:plugin.tables:*",    |
|          "kv", "http.fetch", ...],      |
|   exp: 1730000000,                      |
|   sig: ed25519(host_key, payload)       |
| }                                       |
+-----------------------------------------+
```

The host verifies the sig on every call. Tokens auto-expire (default 5 minutes); the host transparently rotates by re-issuing on the guest's next call. A revoked or downgraded plugin's tokens become unverifiable immediately.

### 6.2 `db.read` / `db.write` — the careful part

The single biggest break from WordPress: **a plugin does not get an unrestricted DB connection.**

Plugin DB isolation is enforced at the application layer: each plugin runs queries through a connection scoped to a per-plugin Postgres role (created on plugin activation) that has `GRANT`ed access only to plugin tables + read-only views into core. RLS policies on core tables are an optional defense-in-depth layer reserved for v2 multi-tenant; we deliberately do **not** rely on RLS for plugin DB scoping in v1. (See [`06-auth-permissions.md`](06-auth-permissions.md) §8, which makes the parallel decision for user-facing CMS authorization.)

<!-- fixed per review (P6, C24): plugin DB isolation in v1 is app-level + per-plugin Postgres roles + GRANTed views, not RLS. RLS was incorrectly claimed as the enforcement boundary in an earlier revision. Doc 06 §8 makes the consistent decision for the user-facing authorization layer. -->

- Plugins get a Postgres connection running as one of two roles:
  - `gn_plugin_ro_{slug}` — read-only, `GRANT SELECT` on:
    - All `plg_{slug}_*` tables
    - Read-only views over core tables present in the plugin's `db.read` scopes (e.g., a `core.posts` scope yields a view that filters `status='published'` unless the plugin separately declared `read_drafts`).
  - `gn_plugin_rw_{slug}` — read-write on `plg_{slug}_*`, read-only on the same core views per scope.

The views (rather than direct table grants) are how we encode "this plugin sees only published posts" without `USING` clauses. The views are defined at plugin activation from the manifest's `db.read` scopes; uninstall drops them.
- All queries flow through `db_query` / `db_exec` host functions that:
  - **Force parameterization.** The guest sends `(sql, args[])`. There is no string-concat API.
  - **Statement timeout** of 250 ms (per query, additive but bounded by invocation budget).
  - **Row cap** of 10,000 by default (configurable).
  - **EXPLAIN-prefix on any new query shape** (in dev) to log estimated cost, helping plugin authors avoid table scans.

The plugin **cannot** open transactions across hook invocations. Transactions are bound to a single hook invocation. (Reason: blocked transactions across plugins are an extinction event.)

### 6.3 `http.fetch` — proxied, allowlisted

```
guest: http.fetch("https://api.example.com/v1/x", {method: GET, headers: {...}})
   │
   ▼
host: check capability has http.fetch
      check URL host matches plugin's allowlist (manifest)
      check URL not in deny list (RFC1918, loopback, link-local, metadata IPs)
      apply per-plugin rate limit (default 60 req/min)
      apply per-invocation count limit
      add User-Agent: GoNext/1.0 (+https://wpc.dev/bot; plugin=gn-seo/1.4.2)
      do the request, observe redirects (max 3), enforce response size cap (10 MB)
      return body + headers + status to guest
```

We are very strict here. SSRF via plugin is a real WordPress disaster pattern and we close it at the host.

### 6.4 `http.serve` — plugins owning REST endpoints

A plugin's manifest declares routes:

```json
{ "method": "GET", "path": "/sitemap.xml", "handler": "route_sitemap" }
```

The core router mounts these under `/api/plugins/{slug}/sitemap.xml`. On request:

1. Core matches the route, finds the plugin.
2. Core dispatches via the hook bus as a synthetic hook `http.serve.{slug}` with `(method, path, headers, body)`.
3. The plugin's `hook_handler` produces a `Response{status, headers, body}`.
4. Core writes it back. (Headers are filtered — plugin can't set arbitrary `Set-Cookie` or `X-Forwarded-For`, etc.)

For the SEO plugin's `/sitemap.xml` to be served at the **site root**, not `/api/plugins/gn-seo/sitemap.xml`, the plugin separately registers a **path alias** via a host call (`http_register_root_alias("/sitemap.xml", "/api/plugins/gn-seo/sitemap.xml")`), which requires the special `http.serve.root_alias` capability. We treat root aliases as privileged because they collide with core routes.

### 6.5 `kv`, `queue`, `email`, etc.

Each is documented in detail in its own subsection of the host API reference (not in this design doc), but the pattern is identical: namespaced, quota'd, capability-gated, no escape hatch.

### 6.6 `cache.invalidate` — plugin-driven page invalidation

<!-- fixed per review (P7, B4): plugins need a sanctioned way to invalidate cached pages/tags when their state mutations affect rendered output (e.g., a redirects plugin mutating routing tables, a SEO plugin updating meta). Added as an explicit capability + host ABI. -->

Plugins invalidate page caches by **tag** via the `cache.invalidate` host ABI:

```
host.cache.invalidate(tags: []string) -> void
```

- **Capability**: `cache.invalidate` in the manifest's `capabilities` block (boolean toggle).
- **Tag format**: the canonical tag vocabulary is owned by [`07-media-performance.md`](07-media-performance.md) §15. Plugins must use those names (`post:{id}`, `term:{id}`, `nav:{menu-id}`, `theme`, `global`, etc.); host validates against the registry. Unknown tags are rejected.
- **Rate limit**: a per-plugin invalidation budget applies. Exact numbers are deferred to the ops doc (`A1` deployment); the host returns a rate-limit error when the budget is exhausted in a window.
- **Effect**: the call writes into the `cache_invalidations` transactional outbox table (per doc 07's design); the invalidation worker drains it and fires the Next.js `revalidateTag` webhook. Plugins **never** call Next.js directly.

Plugins that don't declare `cache.invalidate` get implicit invalidation only through the host's auto-invalidation path (e.g., the host invalidates `post:{id}` when a plugin writes to a plugin table that joins to a post; this is a doc 07 concern).

### 6.7 `secrets` — host-managed secret store

<!-- fixed per review (B6, B13): document the plugin secret store — storage, access, rotation, lifecycle — with a forward reference to a future security doc that owns the encrypted KV implementation. -->

A plugin declares the named secrets it needs in the manifest's top-level `secrets.keys` field. Examples: API tokens for upstream services, signing keys, third-party credentials. The keys are **names**, not values; values are populated by the operator after install.

- **Storage.** The host keeps secret values in an encrypted KV store, separate from the plugin's regular DB tables. Each secret is encrypted with a per-plugin Data Encryption Key (DEK), wrapped by a host master key. The exact storage backend (TBD — KMS-backed envelope encryption + Postgres, sealed Vault, or filesystem with a key-wrap manifest depending on deployment shape) is owned by the forthcoming security doc 13. From this doc's perspective the contract is: secrets are encrypted at rest, the plaintext is only ever held in memory inside the host during a `secret_get` call, and the values are never written to logs.
- **Access.** Plugins read via `host.secrets.get(key) -> string`. Reads are scoped to keys named in the plugin's manifest; reading another key (or another plugin's keys) returns `-1 = no cap`. The capability is **implicit** — declaring `secrets.keys` is the opt-in; no separate capability toggle is required. Writes are admin-only and go through the admin UI; plugins cannot set their own secrets.
- **Rotation.** Admin-triggered. Rotating a secret swaps the stored value; the next `secret_get` returns the new value. There is no in-memory cache inside the host that survives across requests, so rotation takes effect immediately (no plugin restart required). Plugins that hold a fetched value in their own per-instance state for the duration of a request will continue to use the old value within that request — this is acceptable.
- **Lifecycle.** Plugin install: the manifest's `secrets.keys` materializes rows in the secret store with empty values; the admin is prompted to populate them before activation if `required: true` is set on the key (default false). Uninstall: all the plugin's secrets are purged.
- **Audit.** Every `secret_get` call is auto-emitted to `audit_log` (sampled at 1/100 in steady state; always emitted on first read after rotation and on read failures).

### 6.8 What plugins cannot do

A non-exhaustive list, contrasted with WordPress:

| Capability in WP today | Plugins here |
|---|---|
| `eval()` arbitrary PHP | No code execution outside the WASM sandbox. |
| Read/write any file on disk | No filesystem access at all. |
| Connect to the DB directly | No raw DB connections; host-mediated only. |
| Open arbitrary network sockets | No `net.Dial`. HTTP only, allowlisted, proxied. |
| Shell out (`system()`, `exec`) | No process spawning. |
| Override core functions | No monkey-patching of core. Hooks only. |
| Read or write any plugin's options | Plugin KV / DB is namespaced. |
| Read all user data | `users.read` scoped; fields explicit. |
| Schedule arbitrary cron with arbitrary code | Cron jobs declared in manifest, dispatched through the hook bus, run as WASM. |
| Send email to arbitrary addresses unmediated | `email` cap; subject to host rate-limit and templates. |
| Set arbitrary HTTP response headers | Header allowlist; cookies, auth, CSP excluded. |

The exact set is the **threat boundary**. If a capability isn't on the list, it isn't accessible. New capabilities go through design review, not a pull request to a `host.go` file.

---

## 7. Frontend extension points

The frontend story is the dual of the WASM story. **Server logic = WASM; UI logic = ES modules.** They share manifests, slugs, signing, capability declarations (UI-specific ones; see below), and lifecycle.

### 7.1 Loading model: import maps

A core admin page response includes an import map:

```html
<script type="importmap">
{
  "imports": {
    "@host/sdk":     "/_host/sdk-1.0.0.js",
    "react":         "/_host/react-18.3.0.js",
    "react-dom":     "/_host/react-dom-18.3.0.js",
    "@plugin/gn-seo":   "/_plugins/gn-seo/1.4.2/admin.js",
    "@plugin/gn-forms": "/_plugins/gn-forms/2.1.0/admin.js"
  }
}
</script>

<script type="module">
  import { host } from "@host/sdk";
  for (const p of host.activePlugins) {
    await import(p.entry); // resolves via import map
  }
  host.boot();
</script>
```

This lets plugins write idiomatic ES module code (`import { registerMenu } from "@host/sdk"`) without us bundling them. The host serves each plugin's `web/` directory under a hashed path, with cache-forever headers.

Why import maps and not a module federation thing (Webpack MF, Vite MF)? Import maps are a browser standard, do not require a bundler at the consuming side, and give us per-plugin versioning for free.

### 7.2 The `@host/sdk` surface

Plugins import a stable façade. The SDK is versioned in lockstep with the core ABI version (`abi_version` in manifest). Breaking changes bump the SDK major; non-breaking adds bump minor.

```ts
// @host/sdk (typescript ambient module declaration)

export const host: HostBootApi;

export interface HostBootApi {
  readonly activePlugins: ReadonlyArray<{ slug: string; entry: string; version: string }>;
  boot(): void;
}

// --- admin
export function registerMenu(item: MenuItem): void;
export function registerAdminRoute(route: AdminRoute): void;

// --- editor (block-editor surface)
export function registerBlock(definition: BlockDefinition): void;
export function registerEditorPanel(panel: EditorPanel): void;
export function registerEditorPlugin(plugin: EditorPlugin): void;

// --- frontend (site)
export function registerInteractiveBlock(name: string, hydrate: HydrateFn): void;
export function registerShortcode(name: string, render: ShortcodeRender): void;

// --- glue
export function api<T>(path: string, init?: RequestInit): Promise<T>; // hits /api/plugins/{slug}/...
export const i18n: { t(key: string, vars?: Record<string,string>): string };
export const log: { debug; info; warn; error };
export const events: { on(name: string, fn: Fn): Off; emit(name: string, data?: unknown): void };
export const settings: { get<T>(key: string): T | undefined; set<T>(key: string, v: T): void };
```

A few opinionated points:

- Plugins do **not** import React directly from their own bundle. They get it from the host import map. This means every plugin uses the same React instance and there is no version conflict.
- Plugins do **not** make raw HTTP calls. `host.api()` is the only blessed way; it routes to the plugin's REST surface, attaches the auth token, and applies rate-limit hints.
- Plugins do **not** poke at the DOM globally. The SDK provides scoped mount points (a `<div data-slot="...">` for each registered extension).

### 7.3 Admin pages

Admin pages are declared in `manifest.json` under the top-level `admin_pages` array. This is the **single canonical mechanism**: the manifest is the source of truth; the SDK's `AdminMenu.register(...)` / `registerAdminRoute(...)` calls (if exposed for runtime use) are sugar that writes into this manifest array at build time, not a separate runtime registry. The admin shell (see [`05-admin-api.md`](05-admin-api.md)) reads these entries to compose the menu, route the request, and gate access — without instantiating the plugin's JS until the route is actually visited.

```json
"admin_pages": [
  {
    "slug": "gn-seo/dashboard",
    "parent": "tools",             // or null for a top-level menu entry
    "title": "SEO",
    "icon": "search",
    "capability": "manage_seo",    // user-cap required to view; admin shell 403s client-side, server independently enforces
    "entry": "ui/admin/dashboard.js" // ES module path inside the plugin bundle
  },
  {
    "slug": "gn-seo/redirects",
    "parent": "gn-seo/dashboard",
    "title": "Redirects",
    "capability": "manage_seo",
    "entry": "ui/admin/redirects.js"
  }
]
```

<!-- fixed per review (P4, B6): admin pages are declared in the manifest as a single canonical slot — `admin_pages`. The earlier `web.admin.menu` runtime registry and any SDK `AdminMenu.register(...)` calls are now sugar that writes into this slot at build time. Doc 05 reads from this slot when composing the admin shell. -->

The manifest entry's `capability` field is the **user capability** required to view the page (a slug from the role/capability system in [`06-auth-permissions.md`](06-auth-permissions.md) §6). The admin shell uses it for a client-side 403; the underlying plugin REST endpoints serving that page's data independently re-check on the server. The `entry` is an ES module path (lazy-imported via the host's import map); CSP confines it (more on that below).

For backward compatibility with older `web.admin.menu` declarations, the manifest validator accepts the old shape and rewrites it into `admin_pages` at install time. New plugins should target `admin_pages` directly.

### 7.4 Block registration

Block registration uses the canonical `BlockTypeDefinition` shape owned by [`04-block-editor.md`](04-block-editor.md) §2.1. The plugin SDK's `registerBlock` is a thin wrapper that constructs that shape; nothing about plugin blocks differs from core blocks at the type level.

```ts
import { registerBlock } from "@host/sdk";

registerBlock({
  name: "gn-seo/meta",
  title: "SEO Meta",
  icon: "search",
  category: "seo",
  attributes: {
    // JSON Schema 2020-12 (see §7.7 — same dialect for manifest, block attrs, settings).
    title:       { type: "string", default: "" },
    description: { type: "string", default: "" },
  },
  edit: "./blocks/seo-meta/edit.js",   // ESM import path, client-side React
  save: "./blocks/seo-meta/save.js",   // optional, for static blocks
  render: { handler: "seo_meta.render" } // optional, names a WASM export for server render
});
```

<!-- fixed per review (P3, C8, C25): block registration shape adopts doc 04 §2.1's `BlockTypeDefinition`. The earlier `serverRender: true` boolean is gone; the `render: { handler }` object names the plugin-side handler, which the host bridges through the single `hook_handler` export (§4.4). -->

**WASM↔JS bridge for `render`.** When `render` is set, the renderer pipeline does the following on the server side:

1. Look up the registering plugin from the block's namespace (`gn-seo/meta` → `gn-seo`).
2. Construct a synthetic hook name: `block.render:{plugin_slug}/{handler}` — for the example above, `block.render:gn-seo/seo_meta.render`.
3. Dispatch that hook through the bus (§5). The host invokes the plugin's single `hook_handler` export (§4.4) with a `HookCall` whose payload is `{block: {name, attributes}, context: {post_id, view, locale, ...}}` and whose `kind` is `FILTER` (the handler returns a string of HTML or a structured block tree fragment).
4. The plugin's SDK dispatches internally by handler name (`seo_meta.render`) — exactly the same mechanism as any other hook (see §4.4 and §5.3): the manifest's `hooks` may pre-declare `block.render:gn-seo/seo_meta.render` to enable the fast-path lookup, or the plugin registers it at `init()` via `host_register_hook`.
5. The renderer caches the result by `(block_type, attrs_hash, content_version)` per [`07-media-performance.md`](07-media-performance.md) §15.5; cache tags emitted by the plugin (via the `cache.invalidate` capability — see §6.6) follow the conventions in doc 07 §15.

The bridge has **no separate WASM export** — `render_block` is purely an SDK-level developer surface; on the wire it is one more dispatch through the single `hook_handler` entry point. This satisfies §4.4's "exactly one entry point" rule.

Editor side: `edit` and `save` are ESM import paths inside the plugin bundle (under `web/`). The host's import map resolves them; the editor lazy-imports `edit` when the block is opened. A block with `render` but no `save` is dynamic (server-rendered on every fetch unless cached); a block with both `save` and `render` saves static HTML for the public site **and** allows the server-render path for preview/admin scenarios.

### 7.5 Shortcodes / dynamic blocks on the public site

```ts
import { registerInteractiveBlock } from "@host/sdk";

registerInteractiveBlock("gn-forms/contact", async ({ root, attributes }) => {
  const m = await import("./contact-form.tsx");
  m.mount(root, attributes);
});
```

These are loaded lazily — the public site's import map only includes plugins that the current page actually uses (computed at render time from the block tree).

### 7.6 CSP and isolation

Plugin frontends run in the same origin as core (we can't iframe everything without breaking the editor). We harden via:

- A strict CSP (`script-src 'self' 'wasm-unsafe-eval'; object-src 'none'; …`). Inline scripts and `eval` are blocked.
- Trusted Types policy enforced on the DOM sinks the SDK exposes.
- Subresource integrity (SRI) on every plugin script tag, with the hash recorded in the manifest.
- Per-plugin import map prefixes prevent name squatting.

We are honest with ourselves: client-side isolation is **defense in depth**. The serious privilege boundary is the WASM sandbox + the REST surface. If a plugin author wants to ship malicious client JS, the CSP / SRI / TT story raises the cost but doesn't reduce it to zero. We rely on signing and review (§10).

### 7.7 JSON Schema dialect

<!-- fixed per review (C15): pin all JSON Schema-shaped specs to JSON Schema 2020-12. -->

Wherever this doc or the SDK introduces JSON Schema (the manifest itself, block attribute schemas in §7.4, plugin-supplied settings registered through the `@host/sdk` settings API), the dialect is **JSON Schema 2020-12** (`$schema: "https://json-schema.org/draft/2020-12/schema"`). The host's manifest validator and the editor's attribute validator both target 2020-12 features (`prefixItems`, dynamic `$ref`, `unevaluatedProperties`). Plugins authored against earlier drafts are accepted by the validator only when their declared `$schema` is one we explicitly map (currently draft-07 and 2020-12); other drafts fail validation at install time.

---

## 8. Inter-plugin communication

### 8.1 The WP problem

WordPress's "everyone filters `the_content`" is a power-and-curse. Two plugins both prepending markup to a post body collide regularly. The conflict is silent: the order they ran in determines which one "wins" the final layout.

### 8.2 Our answer

Three mechanisms, in order of how much they help:

1. **Typed hook signatures.** Every core hook has a documented payload schema (TypeScript types for the JS side, JSON Schema for the WASM ABI). The SDK validates the value on both sides. A plugin that returns a malformed shape from `core.filter.the_content` is rejected and counts as a handler error. This rules out "plugin A returned a string of HTML but plugin B's filter expected a structured AST"; everyone agrees on shape.

2. **Priority bands.** The hook system honors numeric priorities but the docs partition the space into reserved bands so coexistence is predictable:
   - `0–9`: core early hooks (rarely overridden).
   - `10–49`: "logically before any content transform" (sanitizers, canonical URL fixers).
   - `50–99`: content transforms (markdown, oembed expansion, syntax highlighters).
   - `100–199`: "after content transforms" (SEO injection, analytics tags).
   - `200+`: late stage / cleanup.

   This is doc, not enforcement. But it gives plugin authors a clear convention.

3. **Capability-scoped data spaces.** When plugins A and B both want to add SEO metadata to a post, they don't fight over the same `<meta>` tags. Each writes into its own namespace (`plg_seo_a_meta`, `plg_seo_b_meta`) and the renderer's `core.filter.the_head` consults the registry. If both are registered as SEO providers, the user picks a primary in admin. The host resolves the conflict explicitly.

For plugin-to-plugin direct calls, we offer a structured mechanism: a plugin can declare in its manifest the public hooks it **provides** (`plg.{slug}.something_happened`), and other plugins can listen. There is no shared-memory or function-pointer back channel — only the hook bus. This forces the dependency graph to be visible.

### 8.3 Versioned interfaces

When plugin A provides `plg.seo.redirect_added`, that hook's payload shape is part of plugin A's public API. Plugin B that listens to it pins a major version in its manifest:

```json
"depends": [
  { "slug": "gn-seo", "version": "^1.0.0", "hooks": ["plg.seo.redirect_added"] }
]
```

The host refuses to activate B if A's version doesn't satisfy. This makes plugin-to-plugin coupling explicit and safe across upgrades.

---

## 9. Plugin SDKs

We ship official SDKs for **Go**, **Rust**, **TypeScript** (compiled via Javy to WASM). AssemblyScript is supported as a community-maintained option but not first-class — its ecosystem is too small.

A plugin in any of these languages produces a `plugin.wasm` that satisfies the ABI. The SDK abstracts:

- Msgpack pack/unpack across the boundary.
- A `hook.AddAction(name, fn)` / `hook.AddFilter(name, fn)` developer surface.
- Typed host calls (`db.Query`, `kv.Get`, etc.).
- Manifest generation (a tiny CLI command synthesizes `manifest.json` from code annotations + a config file).

### 9.1 Go SDK — Hello World

```go
package main

import (
    "github.com/gonext/sdk-go/hook"
    "github.com/gonext/sdk-go/host"
    "github.com/gonext/sdk-go/log"
)

//go:wasmexport init
func initPlugin() int32 {
    hook.AddFilter("core.filter.the_title", 10, func(title string) string {
        return "* " + title
    })
    hook.AddAction("core.post.published", 10, func(p host.Post) {
        log.Info("post published", "id", p.ID, "title", p.Title)
    })
    return 0
}

func main() {} // required by tinygo
```

Built with TinyGo:

```
tinygo build -target=wasi -o server/plugin.wasm ./
```

(The SDK itself imports a tiny `host` package that wraps the host_* calls.)

### 9.2 Rust SDK — Hello World

```rust
use gonext_sdk::{hook, host, log};

#[gonext_sdk::plugin_init]
fn init() -> Result<(), host::Error> {
    hook::add_filter("core.filter.the_title", 10, |title: String| -> String {
        format!("* {title}")
    });
    hook::add_action("core.post.published", 10, |post: host::Post| {
        log::info!("post published id={} title={}", post.id, post.title);
    });
    Ok(())
}
```

Built with:

```
cargo build --release --target wasm32-wasi
```

### 9.3 TypeScript SDK — Hello World (Javy)

```ts
import { hook, host, log } from "@gonext/sdk";

hook.addFilter("core.filter.the_title", 10, (title: string) => `* ${title}`);

hook.addAction("core.post.published", 10, (post: host.Post) => {
  log.info("post published", { id: post.id, title: post.title });
});

// Required entrypoint:
export function init(): number { return 0; }
```

Built with Javy (Shopify's QuickJS-to-WASM compiler):

```
javy compile src/index.ts -o server/plugin.wasm
```

Trade-off: Javy bundles a JS interpreter into the WASM blob, which is ~1–2 MB of binary regardless of plugin size. Cold start is slower. For UI-heavy plugins that already ship TS for the frontend, this is the lowest-friction path. For perf-critical server plugins, prefer Go or Rust.

### 9.4 SDK feature matrix

| Feature | Go | Rust | TS (Javy) |
|---|---|---|---|
| Bundle base size | ~300 KB (TinyGo) | ~50 KB | ~1.5 MB |
| Cold start | ~10 ms | ~5 ms | ~30 ms |
| Idiomatic codegen | ✅ | ✅ | ✅ |
| Async hooks | via goroutine→callback | via async/await | via Promise |
| GC pauses inside handler | TinyGo conservative GC | none (ownership) | QuickJS GC |
| Most ergonomic for new authors | ⭐⭐ | ⭐⭐ | ⭐⭐⭐ |

We recommend **Go** as the "default" SDK for backend-shaped plugins; **TS** for plugin authors coming from the JS world who can tolerate the size; **Rust** for plugins that touch hot loops.

### 9.5 Hello-world frontend (shared across languages)

The web side is always TypeScript regardless of which server SDK you use:

```ts
// web/index.js
import { registerAdminRoute, registerMenu } from "@host/sdk";
import * as React from "react";

registerMenu({
  id: "hello",
  title: "Hello",
  icon: "smile",
  route: "/admin/hello",
});

registerAdminRoute({
  path: "/admin/hello",
  component: () => Promise.resolve({
    default: () => React.createElement("h1", null, "Hello, world."),
  }),
});
```

---

## 10. Security model

### 10.1 Threat model

We model these attackers:

1. **Malicious plugin author.** Uploads a plugin that tries to exfiltrate user data, install a backdoor, run a coinminer, or pivot to the host.
2. **Compromised plugin author.** A previously-good plugin's release pipeline is taken over and a malicious update is pushed.
3. **Bug-y plugin author.** Not malicious, but writes a plugin with an SQL injection or SSRF vuln, hoping the host catches them.
4. **Site visitor.** Sends crafted input that flows into a plugin's filter or REST endpoint.
5. **Site admin pressured into installing a sketchy plugin.** Social-engineering attack via "install this to get feature X".

The system aims to make (1), (2), (3) **bounded** — they can break their own plugin, but cannot pivot to the host or the DB. (4) is the plugin author's responsibility for their own logic; the host provides parameterized DB and proxied HTTP to make safe code easy. (5) is mitigated by capability review at install time.

### 10.2 Signing

Plugins are signed with [sigstore](https://www.sigstore.dev/) (keyless OIDC-based signing). At install:

1. Verify the sigstore bundle against the bundled WASM hash.
2. Resolve the signing identity (e.g., `name@acme.dev` via GitHub OIDC).
3. Check the identity against the plugin's `author` in the manifest.
4. Check the identity against the plugin's **registry record** (the platform's record of who is the canonical publisher of slug `gn-seo`).
5. Refuse if any check fails.

For air-gapped installs without a registry, signing is still verified but the registry-record check is optional. Operators can configure a local trust root.

### 10.3 Publishing pipeline

For plugins distributed via the official registry:

```
   author → CI build → sigstore sign → upload to registry → automated scan
                                                                │
                                                                ▼
                                                  capability diff vs prior version
                                                                │
                                                                ▼
                                       static analysis (banned imports, large blobs)
                                                                │
                                                                ▼
                                                 manual review (new caps or new author)
                                                                │
                                                                ▼
                                                              release
```

The new-capability gate is critical. A v1.4 plugin that suddenly requests `db.write: core.users:*` triggers manual review. Reusing capabilities the plugin already had does not.

### 10.4 Capability review

When a user installs a plugin, the UI shows:

```
WPC SEO 1.4.2 wants to:
  ✓ Read your posts and tags
  ✓ Create and modify its own data (33 tables in plg_seo_*)
  ✓ Schedule background jobs
  ✓ Make outbound HTTP requests to:
      api.googleapis.com, api.bing.com
  ✓ Serve 4 REST endpoints under /api/plugins/gn-seo/
  ✓ Read media metadata
  ✓ Read user display names and roles
  ✓ Store secrets (1: google_indexing_api_token)
```

Every line is verbatim from the manifest. Diff is highlighted on update.

### 10.5 Sandboxing guarantees

What the WASM sandbox **does** guarantee:

- No memory access outside the linear memory we assign.
- No syscalls outside the imports we expose.
- No filesystem access. (WASI preview 1 is wired up but every fs op resolves to "not permitted" in our host.)
- No raw network. HTTP only via host proxy.
- Deterministic resource bounds (memory, fuel, wall-clock).
- Crash isolation: a plugin trap, panic, OOM, or fuel exhaustion does **not** crash the host process. It returns an error from the hook dispatch.

What it does **not** guarantee:

- Side-channel resistance (timing, cache). A motivated plugin author could observe coarse timing of host calls; we accept this.
- Resistance to wazero engine bugs. We rely on wazero being a non-trivial security boundary and follow its disclosures.
- Resistance to plugin-to-plugin denial of service via shared host resources (Redis, DB connections). Per-plugin quotas mitigate but don't eliminate.

### 10.6 What a malicious plugin cannot do (vs. WordPress)

The comparison table from §6.8 is the elevator-pitch version of this. The longer version:

- A malicious WP plugin can read `wp_users` and exfiltrate every email + password hash in 30 lines. **In our system, `users.read` is scoped to declared fields, and the user sees the field list at install time.**
- A WP plugin can stage a webshell by writing PHP to `wp-content/uploads/`. **Plugins here have no filesystem access at all.**
- A WP plugin can run `mysql_query("DROP TABLE wp_users")`. **Plugins here have no DB connection; queries go through a role with only the grants its manifest declared, and a static linter rejects forbidden table names.**
- A WP plugin can fetch any URL — including `http://169.254.169.254/` on AWS. **HTTP fetches here are allowlisted and private/metadata IPs are blocked at the host.**
- A WP plugin can hot-patch `wp_authenticate()` via `remove_filter` + replacement. **No monkey-patching here. Hooks compose; they don't replace.**

---

## 11. Developer experience

A great plugin system is the union of a good runtime and a good developer loop. The runtime is half the work; the inner loop is the other half.

### 11.1 `gonext plugin dev`

```
$ gonext plugin dev .
✓ detected: Go plugin (manifest.json, main.go)
✓ tinygo build → server/plugin.wasm (312 KB)
✓ esbuild web/index.js → web/dist/index.js (47 KB)
✓ pre-compiled WASM
✓ uploaded to local server at http://localhost:3000
✓ activated: gn-seo @ 1.4.2-dev+abc123

watching for changes...

[10:21:43] manifest.json changed → re-validating capabilities (1 added: cron)
            ⚠  new capability detected, restarting plugin
[10:21:44] server/main.go changed → rebuild
            ✓ tinygo build (1.1s)
            ✓ swapped WASM module (drained 0 in-flight)
[10:21:55] web/pages/Dashboard.tsx changed → HMR pushed to 2 admin tabs
```

Key DX moves:

- **Hot reload of frontend** is straightforward — Vite-style HMR via the dev server.
- **Hot reload of WASM** is harder. We do a drain-and-swap: stop accepting new dispatches for the old module, wait up to 2s for in-flight to drain, instantiate the new module, switch the pointer, run `on_deactivate`/`on_activate` if migrations changed.
- **Capability changes** invalidate the dev session and require explicit re-acknowledgment (a confirm in the CLI), matching what production install requires.

### 11.2 Debugging WASM

This is the genuinely hard part. Options we expose:

- **`println` via host log.** The SDK's `log.Debug` shows up in the dev server console immediately. This is 80% of debugging.
- **Source maps.** TinyGo + `wasm-tools` + the SDK build process produces a `.wasm.map` linking WASM offsets to source files. Errors include resolved source positions.
- **Trap inspector.** When the host catches a trap, it captures the guest's stack via wazero's `Listener` API and resolves it through the source map.
- **DWARF-aware debuggers.** Rust + Chrome DevTools has functioning step-debugging for WASM with DWARF; we ship configs for it. Go + TinyGo is less mature; we accept this.
- **Replay.** Every hook dispatch is loggable in dev with full inputs. `gonext plugin replay <id>` re-runs a recorded dispatch against the local plugin for deterministic debugging.

### 11.3 Error reporting

In production, every guest trap produces a structured event:

```json
{
  "ts": "2026-04-13T10:21:43Z",
  "plugin": "gn-seo",
  "version": "1.4.2",
  "hook": "core.filter.the_content",
  "kind": "trap",
  "trap": "out_of_fuel",
  "stack": [
    "gn_seo::filter_content (filter.rs:42)",
    "gn_seo::expand_internal_links (links.rs:71)"
  ],
  "request_id": "req_abcd",
  "duration_ms": 254
}
```

The admin "Plugin health" dashboard groups these by `(plugin, version, kind, stack[0])` and surfaces top failures. Plugin authors who opt into the registry's telemetry receive aggregated reports of failures across all installs.

### 11.4 Testing

The SDK ships a test harness that runs the plugin's WASM in-process against a fake host:

```go
// Go SDK testing example
func TestFilterTheTitle(t *testing.T) {
    h := wptest.NewHost(t)
    h.LoadPlugin("./testdata/gn-seo.wasm")
    result, err := h.ApplyFilter("core.filter.the_title", "Hello")
    require.NoError(t, err)
    require.Equal(t, "* Hello", result)
}
```

The fake host implements the full ABI in memory (in-process Postgres via `pgmock`, in-memory KV, recorded HTTP). Plugin authors get end-to-end-ish testing without docker.

---

## 12. Marketplace (data model only — out of scope for design)

A future doc owns the marketplace. We note here only what the data model needs to support, so the rest of the system doesn't paint itself into a corner:

```sql
plugin_listings (
  slug TEXT PRIMARY KEY,
  display_name TEXT,
  description TEXT,
  publisher_id UUID,
  category TEXT[],
  total_installs BIGINT,
  rating_avg NUMERIC(2,1),
  rating_count INT,
  created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ
)

plugin_versions (
  slug TEXT,
  version TEXT,
  abi_version INT,
  min_core_version TEXT,
  max_core_version TEXT,
  bundle_hash BYTEA,
  bundle_size INT,
  capabilities JSONB,
  signing_identity TEXT,
  published_at TIMESTAMPTZ,
  yanked BOOLEAN,
  yank_reason TEXT,
  PRIMARY KEY (slug, version)
)

plugin_compat_matrix (
  slug TEXT,
  version TEXT,
  core_version TEXT,
  status TEXT, -- ok | broken | untested
  reported_at TIMESTAMPTZ,
  source TEXT  -- self | community | ci
)

plugin_ratings (
  id BIGSERIAL PRIMARY KEY,
  slug TEXT,
  version TEXT,
  user_id UUID,
  rating SMALLINT,        -- 1-5
  body TEXT,
  created_at TIMESTAMPTZ
)

plugin_install_events (
  id BIGSERIAL,
  slug TEXT,
  version TEXT,
  site_id UUID,           -- pseudonymous
  event TEXT,             -- install | update | activate | deactivate | uninstall
  ts TIMESTAMPTZ
)
```

The thing the platform must commit to here: **the `slug → version → bundle_hash` triple is the immutable name of a plugin release.** Everything else (ratings, install counts, listings) revolves around it.

---

## 13. Trade-offs & rejected alternatives

### 13.1 Why not native Go plugins (`plugin` pkg)?

- Only works on Linux.
- ABI is fragile against Go version changes (recompile the host, every plugin breaks).
- **No isolation whatsoever.** A native Go plugin can `os.Exit` the host, deadlock the runtime, or `unsafe`-pointer-spray any data structure.
- Memory and CPU bounding is impossible without per-process separation.

We never seriously considered this. It's listed for completeness because every Go-shop architect asks.

### 13.2 Why not Hashicorp `go-plugin` (gRPC subprocess)?

- Mature, real-world tested by Terraform, Vault, Consul.
- Real isolation (separate process).
- But: **heavy.** Each plugin is a subprocess. 100 active plugins is 100 child processes, each with its own runtime, GC, network stack. Memory baseline: ~10–30 MB per plugin minimum.
- IPC latency: gRPC over Unix socket round-trips are ~100–500 µs. Bearable for one call. For a page firing dozens of hooks, it stacks up.
- Plugin authors must write a gRPC server, which constrains the SDK design and limits language support to gRPC's supported ones.
- Resource limits are at the OS level (cgroups, prlimit). Doable, but a different OS-specific story per platform.

WASM wins on density (a 1 MB compiled module + ~5 MB pool overhead per plugin instance vs. 30+ MB), per-call latency (single-digit µs in-process vs. ~100+ µs across a socket), and uniformity (one runtime, all OSes).

We **respect** Hashicorp's choice — Vault plugins are mostly long-lived and don't fire per-request hooks. Our profile is different: many short hot calls per request.

### 13.3 Why not embedded JS (V8 / QuickJS)?

- Locks plugin authors to JS / TS.
- V8 is enormous and CGO. Disqualifying for the host's deploy story (single static Go binary).
- QuickJS is small, but no JIT, slow. We considered it for the "TS plugins" path; QuickJS-via-Javy compiled to WASM is what we landed on instead. Best of both worlds: TS authors, no JS engine in the host.
- WP-style hooks in JS would naturally bias plugin authors toward "do everything in JS" including DB-heavy logic. We prefer pushing perf-sensitive work to compiled languages.

### 13.4 Why not a PHP compat layer?

- Massive scope. To run a real WP plugin you'd need: the WP function library, the WP DB schema, the WP hook system, the WP cron, etc. Effectively, a partial WP fork.
- Defeats the point: we are explicitly **not** PHP for performance and operational reasons.
- WP plugins are riddled with assumptions about the global PHP environment (`$_POST`, `wpdb`, ad-hoc `add_filter` at file include time). Simulating this is a quagmire.
- A migration shim that imports **data** (posts, options, users) and pairs the user with **equivalent new plugins** is a much better use of effort. That lives in [`08-migration-compat.md`](08-migration-compat.md).

### 13.5 Why not WIT / Component Model now?

- Toolchain support is uneven. Go support is via wit-bindgen-go, which is alpha and produces non-idiomatic code today.
- wazero's Component Model support is partial.
- We'd ship a worse SDK to lock in a "more correct" wire format two years too early.
- Our ABI is intentionally shaped to be migrate-able to WIT: one entry point per role, MessagePack records that map naturally onto Component records, capability tokens that map onto resource handles. When the ecosystem catches up (likely 2026–2027) we adopt WIT as the **second** ABI version. Old plugins keep working.

### 13.6 Why MessagePack and not Cap'n Proto / FlatBuffers / Protobuf / JSON?

- **JSON.** Text encoding for binary data is wasteful; numeric precision quirks; no native bytes. Rejected.
- **Protobuf.** Strong tooling but heavy generated code, schema management overhead on both sides, fights with dynamic hook payloads (we'd need a generic `Any` wrapper). Rejected for the hot path; we use protobuf in places where schemas are stable (e.g., the internal control plane).
- **Cap'n Proto / FlatBuffers.** Zero-copy is genuinely attractive across the WASM boundary. The downside: SDK ergonomics. Plugin authors should write `func filter(s string) string`, not deal with builder APIs. We may revisit FlatBuffers for very hot internal paths.
- **MessagePack.** Schema-free, tiny encoders in every language, fast enough. The "Postgres-of-binary-encodings." Wins.

### 13.7 Why not let plugins call each other directly?

Tempting: plugin A exports a function, plugin B imports it. Rejected because:

- Couples plugins at the linker level. Update plugin A → plugin B's import resolution may fail.
- Breaks capability scoping (whose capability token applies for a call from B into A?).
- Defeats the "everything is observable via the hook bus" property that makes the system debuggable.

The hook bus is the **only** inter-plugin channel. This is a feature.

### 13.8 Why not a "plugin runs as the host process" hot path for trusted plugins?

I.e., a fast-track tier where, say, the official SEO plugin is just a Go package linked into the host. Rejected for v1 because:

- Two-tier systems erode. Once "trusted" plugins exist, the temptation to bless more is constant.
- The argument for it is performance, and our prototypes don't show WASM as the bottleneck.
- If a piece of functionality is so universal it should be in-process, it should probably be **in core**, not "a special plugin."

We may walk this back if real load tests demand it. For v1: every plugin is a WASM plugin.

---

## 14. Worked example: SEO plugin, end-to-end

To make the surface concrete, here's the SEO plugin's full life:

1. **Author writes the plugin.** `gonext plugin new gn-seo --lang=go` scaffolds a directory with `manifest.json`, `main.go`, `web/index.js`. Writes `filter_content` to inject `<meta>` tags, `route_sitemap` to generate `/sitemap.xml`, an admin dashboard in React.
2. **Author tests locally.** `gonext plugin dev .` boots the dev server, the local site shows the SEO sidebar in the editor, edits hot-reload.
3. **Author publishes.** `gonext plugin publish .` runs `gonext plugin build` (TinyGo + esbuild), signs with sigstore using GitHub OIDC, uploads to the registry. Manual review approves the `secrets` capability (new in this release).
4. **Site admin installs.** Clicks Install in the admin UI. The capability dialog shows the list above. Admin clicks Approve.
5. **Activation.** Core verifies the signature, runs migrations (`0001_init.up.sql` creates `plg_seo_meta`, `plg_seo_redirects`, etc.), instantiates the WASM module, calls `on_activate`. The plugin's `init()` registers its hooks. Admin sees "Active."
6. **Site visitor loads a post.** The Next.js renderer hits `/api/posts/123/render`. Core fetches the post, calls `apply_filters("core.filter.the_content", body)`. The hook bus dispatches to gn-seo's `filter_content`. The plugin reads metadata from `plg_seo_meta` via `db.read`, builds the `<meta>` tags, returns them prepended to body. ~200 µs.
7. **Crawler hits `/sitemap.xml`.** Core's router sees the path is mapped via plugin route alias. It dispatches through the hook bus to gn-seo's `route_sitemap`. The plugin queries `plg_seo_meta` for all indexed posts, returns XML, core writes the response.
8. **Cron fires every 6h.** Asynq triggers `cron.rebuild_sitemap`. The bus invokes gn-seo's handler. The handler rebuilds a denormalized sitemap and writes it back to `plg_seo_*` tables.
9. **Plugin author ships 1.4.3.** Site admin updates. Core verifies signature, runs migration `0003_add_canonicals.up.sql`, drains the 1.4.2 pool, swaps to 1.4.3, calls 1.4.3's `on_activate`. Total downtime per in-flight request: <1s.
10. **Plugin trips its fuel cap on a malformed input.** The host catches the trap, records the failure, returns the unmodified value to the filter chain, increments the circuit breaker. The post still renders, the page is fine, the admin gets a notification.

Every step is observable. Every privilege is declared. Every artifact is signed and content-addressed. None of this is harder for the plugin author than writing a WP plugin — and in several places it's easier (no `$wpdb` quoting drama, no global `$wp_query`, no "where do I put my menu icon" hunt).

---

## 15. Open questions

These are unresolved and need decisions before we cut v1. They are not blocked by anything in this doc.

1. **Cron jitter / horizontal scaling.** If we run multiple Go API server replicas, who fires the cron? Asynq has a "scheduler" mode but it requires a single source of truth. Likely answer: a leader-elected scheduler; needs design. (Cross-cuts with [`07-media-performance.md`](07-media-performance.md)?)
2. **Plugin DB connection pool sizing.** Each plugin gets its own Postgres role. If a host has 200 active plugins, we don't want 200 separate pools. Likely answer: shared pgbouncer with role-switching per checkout. Open: latency cost of `SET ROLE` per acquire.
3. **WP hook alias completeness.** Do we ship aliases for `the_content`, `the_title`, `init`, `wp_head`, `save_post`, `wp_login`, ~20 others? Or only the top-5 most-used? Lots of bikeshedding awaits.
4. **Frontend SDK and React versioning.** If a plugin built against React 18 stays installed when the host upgrades to React 19, do we ship both? Two React copies in the page is degraded but works. Three is not OK. Need a policy.
5. **Sigstore for self-hosted.** For air-gapped installs without internet, sigstore's "Fulcio" identity check doesn't work. We support offline cosign-key verification as a fallback; need to spec the key-distribution story for site operators.
6. **Plugin reviewing scale.** Manual review of new capabilities is the right policy at 100 plugins. It is not the right policy at 10,000. What automated scanners do we trust to replace human review on the "common diffs"? (Static analysis for known-bad imports, manifest diffs, rate-limit checks.)
7. **AOT-compiled WASM caching across hosts.** wazero's compiled artifacts are not portable across Go versions or wazero versions. A site cluster wants to share `CompiledModule` across replicas to skip per-host compile. Options: shared cache directory on networked storage; per-replica re-compile on plugin update. Lean: per-replica, accept the cost.
8. **Plugin author identity verification.** Sigstore says "this WASM was uploaded by alice@acme.dev via GitHub OIDC." It does not say "Acme is a real company." For the registry, do we require domain verification? KYC for paid plugins? Out of scope for plugin runtime, but blocks marketplace launch.
9. **Versioned host SDK rollout.** When we ship ABI v2 (e.g., to adopt WIT), how do we communicate the change to plugin authors? Hard deprecation of v1 in 18 months is the plan; need a real comms + migration tooling story.
10. **Plugin-to-plugin filter composition semantics in edge cases.** If two plugins register at priority 50 on `the_content` and both want to be "last", who wins? Doc says regOrder breaks ties, but should the manifest expose a `before: [other-slug]` / `after: [other-slug]` constraint so authors can specify ordering explicitly? Likely yes; needs design and conflict resolution rules.
11. **Async hook ordering.** Async actions are queued; their dispatch order is best-effort, not strict priority. Is that OK? For most actions (analytics fire-and-forget) yes. For ordered side effects (notify-then-publish-then-index) no. Open: do we offer a "strict-ordered async" mode? Probably yes, costs us on throughput.
12. **Multi-tenant story.** If/when multisite (overview §7.3) lands, do plugins scope per-site or per-cluster? Per-site is much more work (separate DB schemas, separate WASM instances) but the right answer if we go SaaS.

---

## Appendix A — full ABI reference (host imports, abridged)

```
// All functions are imports under module name "host". All pointer/length pairs
// reference the *guest* linear memory. Results are returned via `host_set_result`
// (ptr, len) which the host reads after the call returns.

// --- core glue
host_set_result(ptr i32, len i32)
host_alloc(len i32) -> i32       // host calls guest's exported allocator, returns ptr
host_log(level i32, ptr i32, len i32)

// --- hook registration (called from init/anywhere)
host_register_hook(name_ptr i32, name_len i32, priority i32, kind i32 /*0=action,1=filter*/, handler_ptr i32, handler_len i32) -> i32

// --- db
host_db_query(sql_ptr i32, sql_len i32, args_ptr i32, args_len i32) -> i64  // status<<32 | result_len
host_db_exec (sql_ptr i32, sql_len i32, args_ptr i32, args_len i32) -> i64  // status<<32 | rows_affected_lo
host_db_tx_begin() -> i32
host_db_tx_commit() -> i32
host_db_tx_rollback() -> i32

// --- kv
host_kv_get(key_ptr i32, key_len i32) -> i64
host_kv_set(key_ptr i32, key_len i32, val_ptr i32, val_len i32, ttl_seconds i32) -> i32
host_kv_del(key_ptr i32, key_len i32) -> i32
host_kv_incr(key_ptr i32, key_len i32, by i64) -> i64

// --- http (outbound)
host_http_request(req_ptr i32, req_len i32) -> i64   // msgpack-encoded Request → Response

// --- http (inbound, only called from http.serve hook)
host_http_respond(resp_ptr i32, resp_len i32) -> i32

// --- queue
host_queue_enqueue(job_ptr i32, job_len i32) -> i32

// --- email
host_email_send(msg_ptr i32, msg_len i32) -> i32

// --- media
host_media_get(id_ptr i32, id_len i32) -> i64        // metadata, signed URL
host_media_create(req_ptr i32, req_len i32) -> i64

// --- users
host_users_get(id_ptr i32, id_len i32) -> i64
host_users_current() -> i64

// --- secrets
host_secret_get(key_ptr i32, key_len i32) -> i64

// --- cache invalidation (capability: cache.invalidate)
host_cache_invalidate(tags_ptr i32, tags_len i32) -> i32   // msgpack-encoded []string of tags

// --- audit log emission (capability: audit.emit)
host_audit_emit(event_ptr i32, event_len i32, meta_ptr i32, meta_len i32) -> i32

// --- i18n
host_i18n_t(key_ptr i32, key_len i32, vars_ptr i32, vars_len i32) -> i64

// --- clock
host_time_now_unix_ms() -> i64
```

<!-- fixed per review (P7, B7): added host_cache_invalidate and host_audit_emit to the ABI reference. -->


Status codes are negative for errors (`-1 = no cap`, `-2 = quota exceeded`, `-3 = bad input`, `-4 = host internal`, `-5 = not found`, …). Zero is success. Positive values are bitfields where noted (status_low | length_high).

---

## Appendix B — host code sketch for instantiating a plugin

```go
// Package plugin loads and runs WASM plugins.
package plugin

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/tetratelabs/wazero"
    "github.com/tetratelabs/wazero/api"
    "github.com/tetratelabs/wazero/experimental"
    "github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Manager is the per-process plugin manager.
type Manager struct {
    runtime   wazero.Runtime
    compiled  map[string]wazero.CompiledModule // slug+version -> compiled
    pools     map[string]*InstancePool         // slug -> pool
    hostMod   api.Module                        // shared "host" module instance
    log       Logger
}

func NewManager(ctx context.Context, log Logger) (*Manager, error) {
    cfg := wazero.NewRuntimeConfigCompiler().
        WithCloseOnContextDone(true).
        WithMemoryLimitPages(1024) // 64 MiB, overridden per-plugin

    rt := wazero.NewRuntimeWithConfig(ctx, cfg)
    if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
        return nil, fmt.Errorf("wasi instantiate: %w", err)
    }

    m := &Manager{
        runtime:  rt,
        compiled: map[string]wazero.CompiledModule{},
        pools:    map[string]*InstancePool{},
        log:      log,
    }
    if err := m.registerHostModule(ctx); err != nil {
        return nil, err
    }
    return m, nil
}

// registerHostModule wires up the host imports under module name "host".
func (m *Manager) registerHostModule(ctx context.Context) error {
    b := m.runtime.NewHostModuleBuilder("host")

    // simplified: only a couple of imports shown
    b.NewFunctionBuilder().
        WithFunc(func(ctx context.Context, mod api.Module, level int32, ptr, length uint32) {
            buf, ok := mod.Memory().Read(ptr, length)
            if !ok { return }
            m.log.Log(logLevel(level), string(buf))
        }).
        Export("host_log")

    b.NewFunctionBuilder().
        WithFunc(func(ctx context.Context, mod api.Module, sqlPtr, sqlLen, argsPtr, argsLen uint32) uint64 {
            return m.dbQuery(ctx, mod, sqlPtr, sqlLen, argsPtr, argsLen)
        }).
        Export("host_db_query")

    // ... all other host_* functions

    _, err := b.Instantiate(ctx)
    return err
}

// Install verifies and compiles a plugin bundle.
func (m *Manager) Install(ctx context.Context, bundlePath string) (*InstalledPlugin, error) {
    pkg, err := openBundle(bundlePath)
    if err != nil { return nil, err }
    defer pkg.Close()

    manifest, err := pkg.Manifest()
    if err != nil { return nil, err }

    if err := manifest.Validate(); err != nil {
        return nil, fmt.Errorf("manifest: %w", err)
    }
    if err := verifySignature(pkg, manifest); err != nil {
        return nil, fmt.Errorf("signing: %w", err)
    }

    wasmBytes, err := pkg.ReadFile(manifest.Server.WASM)
    if err != nil { return nil, err }

    hash := sha256.Sum256(wasmBytes)
    hashHex := hex.EncodeToString(hash[:])

    compiled, err := m.runtime.CompileModule(ctx, wasmBytes)
    if err != nil { return nil, fmt.Errorf("compile: %w", err) }

    key := manifest.Slug + "@" + manifest.Version
    m.compiled[key] = compiled

    dest := filepath.Join("plugins", manifest.Slug, manifest.Version)
    if err := os.MkdirAll(dest, 0o755); err != nil { return nil, err }
    if err := pkg.ExtractTo(dest); err != nil { return nil, err }

    return &InstalledPlugin{
        Slug: manifest.Slug, Version: manifest.Version,
        Manifest: manifest, Hash: hashHex, Path: dest,
    }, nil
}

// Activate runs migrations and instantiates the first plugin instance.
func (m *Manager) Activate(ctx context.Context, p *InstalledPlugin) error {
    if err := runMigrationsUp(ctx, p); err != nil {
        return fmt.Errorf("migrations: %w", err)
    }
    pool := NewInstancePool(p, m.runtime, m.compiled[p.Slug+"@"+p.Version], m.log)
    m.pools[p.Slug] = pool

    inst, err := pool.Get(ctx)
    if err != nil { return err }
    defer pool.Put(inst)

    if onActivate := inst.ExportedFunction("on_activate"); onActivate != nil {
        if _, err := onActivate.Call(ctx); err != nil {
            return fmt.Errorf("on_activate trap: %w", err)
        }
    }
    if initFn := inst.ExportedFunction("init"); initFn != nil {
        if _, err := initFn.Call(ctx); err != nil {
            return fmt.Errorf("init trap: %w", err)
        }
    }
    return nil
}

// InstancePool is per-plugin. Implementations are out of scope here.
type InstancePool struct {
    p       *InstalledPlugin
    rt      wazero.Runtime
    cm      wazero.CompiledModule
    log     Logger
    // ... idle list, in-flight count, mu, etc.
}

func NewInstancePool(p *InstalledPlugin, rt wazero.Runtime, cm wazero.CompiledModule, log Logger) *InstancePool {
    return &InstancePool{p: p, rt: rt, cm: cm, log: log}
}

// Get acquires an instance, creating one if the pool is empty.
func (p *InstancePool) Get(ctx context.Context) (*Instance, error) {
    // sketch only
    cfg := wazero.NewModuleConfig().
        WithName(p.p.Slug + "-" + randSuffix()).
        WithStartFunctions() // we call init ourselves
    mod, err := p.rt.InstantiateModule(ctx, p.cm, cfg)
    if err != nil { return nil, err }

    ctx, cancel := context.WithTimeout(ctx, p.p.Manifest.Server.InvocationTimeout())
    _ = cancel // attached to instance for the duration of one call

    // Optional: install fuel meter via experimental Listener.
    _ = experimental.NewLoopDetector

    return &Instance{
        mod: mod,
        invTimeout: p.p.Manifest.Server.InvocationTimeout(),
        capsToken:  mintCapsToken(p.p),
        started: time.Now(),
    }, nil
}

func (p *InstancePool) Put(inst *Instance) {
    // return to pool, or destroy if memory > threshold
}

// Instance wraps an api.Module plus per-instance state.
type Instance struct {
    mod        api.Module
    invTimeout time.Duration
    capsToken  []byte
    started    time.Time
}

func (i *Instance) ExportedFunction(name string) api.Function { return i.mod.ExportedFunction(name) }
func (i *Instance) Memory() api.Memory                        { return i.mod.Memory() }
func (i *Instance) InvocationTimeout() time.Duration          { return i.invTimeout }
func (i *Instance) CapsToken() []byte                         { return i.capsToken }
func (i *Instance) Alloc(ctx context.Context, n uint32) (uint32, error) {
    fn := i.mod.ExportedFunction("plugin_alloc")
    out, err := fn.Call(ctx, uint64(n))
    if err != nil { return 0, err }
    return uint32(out[0]), nil
}
func (i *Instance) LastResultPtr() (uint32, bool) { /* read from agreed-upon global */ return 0, true }

// ---- Misc

type InstalledPlugin struct {
    Slug, Version, Hash, Path string
    Manifest *Manifest
}

type Manifest struct {
    Slug, Version string
    Server struct {
        WASM             string
        MemoryLimitMB    int
        FuelPerInvocation int64
        InvocationMS     int
    }
    // ...
}

func (m *Manifest) Validate() error    { return nil /* schema check */ }
func (s *Manifest) Server_InvocationTimeout() time.Duration {
    return time.Duration(s.Server.InvocationMS) * time.Millisecond
}
func (m *Manifest) Server_InvocationTimeoutFn() time.Duration { return 0 }
func (s *Manifest) Slug_() string { return s.Slug }

// Sentinel symbols used in this sketch but defined elsewhere:
func openBundle(path string) (*bundle, error)                                            { return nil, errors.New("todo") }
func verifySignature(*bundle, *Manifest) error                                           { return nil }
func runMigrationsUp(context.Context, *InstalledPlugin) error                            { return nil }
func mintCapsToken(*InstalledPlugin) []byte                                              { return nil }
func randSuffix() string                                                                  { return "" }
func logLevel(int32) int                                                                  { return 0 }

type bundle struct{}
func (b *bundle) Manifest() (*Manifest, error)         { return nil, nil }
func (b *bundle) ReadFile(string) ([]byte, error)      { return nil, nil }
func (b *bundle) ExtractTo(string) error               { return nil }
func (b *bundle) Close() error                          { return nil }

type Logger interface{ Log(int, string); Warn(string, ...any); Info(string, ...any) }

// helper used by manifest above
func (s *Manifest) Server_M() { _ = s.Server.MemoryLimitMB }
```

This sketch is missing a lot — error wrapping, metrics, the capability gate inside each host function, the actual fuel meter, etc. It is the shape only. The intent: nothing about this needs anything exotic. wazero + standard Go + Postgres + Redis. No new infrastructure.

---

## Appendix C — TS SDK sketch (frontend)

```ts
// @host/sdk implementation (rough)

type Off = () => void;

const slots = new Map<string, HTMLElement>();
const blocks = new Map<string, BlockDefinition>();
const interactiveBlocks = new Map<string, HydrateFn>();
const adminRoutes = new Map<string, AdminRoute>();

export const host = {
  activePlugins: window.__host_state.plugins as ReadonlyArray<PluginEntry>,
  boot,
};

function boot() {
  hydrateMenuFromManifest();
  router.subscribe((path) => {
    const r = adminRoutes.get(matchRoute(path));
    if (r) mountComponent(r, slots.get("admin-main")!);
  });
  for (const node of document.querySelectorAll("[data-block]")) {
    const name = node.getAttribute("data-block")!;
    const fn = interactiveBlocks.get(name);
    if (fn) fn({ root: node as HTMLElement, attributes: readAttrs(node) });
  }
}

export function registerMenu(item: MenuItem): void { /* mutate menu state */ }
export function registerAdminRoute(r: AdminRoute): void { adminRoutes.set(r.path, r); }
export function registerBlock(def: BlockDefinition): void {
  blocks.set(def.name, def);
  editorBus.emit("block-registered", def);
}
export function registerInteractiveBlock(name: string, fn: HydrateFn): void {
  interactiveBlocks.set(name, fn);
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const slug = currentPluginSlug();
  const res = await fetch(`/api/plugins/${slug}${path}`, withAuth(init));
  if (!res.ok) throw new HttpError(res);
  return res.json();
}

export const i18n = {
  t(key: string, vars?: Record<string,string>) {
    const slug = currentPluginSlug();
    return resolve(translations[slug] ?? {}, key, vars);
  },
};

// internal helpers omitted
```

A plugin imports from `"@host/sdk"`; the import map resolves to the host's served module. SDK upgrades are independent of plugin upgrades.

---

## End

Two things to repeat, because they are the foundation of every other decision in this doc:

1. **Expressive surface, narrow privilege.** Plugins get a WordPress-shaped developer experience. They do not get a WordPress-shaped privilege model.
2. **One privilege boundary, declared in one place.** The manifest's `capabilities` block is the single source of truth for what a plugin can do. Everything else — the host ABI gate, the install-time UI, the registry review pipeline, the audit log — derives from it.

If we get those two right, we ship a viable plugin ecosystem. If we wobble on either, we don't.
