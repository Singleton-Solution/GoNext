gonext-seo — worked example plugin
==================================

A Yoast-style SEO plugin that injects `<title>`, `<meta>`, OpenGraph,
Twitter card, and JSON-LD tags into rendered post pages, and computes a
0..100 SEO score on every save. Ships as the canonical worked example
for the GoNext plugin runtime.

This plugin is small on purpose — about 400 lines of TinyGo plus 300
lines of test — but it exercises every plumbed capability of the
plugin system:

| Capability        | How the plugin uses it                                              |
| ----------------- | ------------------------------------------------------------------- |
| `posts.read`      | Reads the post being saved when computing the SEO score.            |
| `posts.write`     | Persists the computed SEO score to post meta (via host call).       |
| `hooks.subscribe` | Subscribes to `the_content` (filter), `wp_head`, `save_post`.       |
| `jobs.enqueue`    | Schedules `seo.recompute-scores` to rebuild scores in the background. |

Subscriptions
-------------

| Hook         | Kind   | What it does                                                                   |
| ------------ | ------ | ------------------------------------------------------------------------------ |
| `the_content`| filter | Appends a `<script type="application/ld+json">` Article schema to post HTML.  |
| `wp_head`    | action | Emits `<title>`, `<meta>`, OpenGraph, and Twitter card tags into `<head>`.    |
| `save_post`  | action | Computes the SEO score and writes it to the post's `_seo_score` meta key.     |

Background jobs
---------------

| Job ID                  | Trigger    | What it does                                              |
| ----------------------- | ---------- | --------------------------------------------------------- |
| `seo.recompute-scores`  | Operator   | Re-evaluates every post and updates the saved score.      |

Installation
------------

Operators install the plugin in three steps:

```
# 1. Build the WASM bundle (TinyGo required).
cd examples/plugins/seo
./build.sh

# 2. Pack into a .gnplugin bundle.
zip -j seo.gnplugin manifest.json seo.wasm

# 3. Install + activate via the admin CLI.
gonext plugin install ./seo.gnplugin
gonext plugin activate gonext-seo
```

The lifecycle Manager validates `manifest.json` against
`packages/go/plugins/manifest/schema.json` during install, and gates
activation on the capability checker
(`packages/go/plugins/capabilities`). An operator who declines any of
the four caps above gets a typed `capabilities.ErrCapabilityDenied`
back, with the unmet caps named.

Capability rationale
--------------------

We ask for the minimum cap set that lets the plugin do its job:

- **`posts.read`** — needed to walk the post for the description fallback
  (excerpt → first paragraph → title) and to compute the score against
  the post body.
- **`posts.write`** — needed to write the score back to a meta key. A
  read-only variant of the plugin (linting mode only) would drop this.
- **`hooks.subscribe`** — required for any plugin that wants its hook
  handlers dispatched. Without it, the activation gate refuses to wire
  up the three subscriptions in `manifest.json`.
- **`jobs.enqueue`** — required to enqueue the `seo.recompute-scores`
  job from the admin UI's "rebuild all scores" button.

We deliberately do NOT request `http.fetch` or `email.send` — neither
is needed, and asking for them would force operators to read past noise
in the install dialog.

How `<head>` HTML looks
-----------------------

For a populated post, the plugin's `wp_head` action produces:

```html
<title>How to Plant a Garden in Springtime!!! | Acme Blog</title>
<meta name="description" content="A guide to planting your first garden, with practical tips for absolute beginners today.">
<link rel="canonical" href="https://acme.example/blog/garden">
<meta property="og:type" content="article">
<meta property="og:title" content="How to Plant a Garden in Springtime!!! | Acme Blog">
<meta property="og:description" content="A guide to planting your first garden, with practical tips for absolute beginners today.">
<meta property="og:url" content="https://acme.example/blog/garden">
<meta property="og:image" content="https://acme.example/garden.jpg">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="How to Plant a Garden in Springtime!!! | Acme Blog">
<meta name="twitter:description" content="A guide to planting your first garden, with practical tips for absolute beginners today.">
<meta name="twitter:image" content="https://acme.example/garden.jpg">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Article", ...}</script>
```

Screenshots
-----------

`docs/04-seo-plugin-tutorial.md` walks through what the plugin looks
like in the admin UI once these PRs are merged:

- `screenshots/admin-install-dialog.png` — the capability-grant dialog.
- `screenshots/seo-score-meta-box.png`   — the per-post score widget.
- `screenshots/seo-recompute-job.png`    — the "rebuild all" admin job button.

(Screenshots are placeholders today — they ship once the admin UI
lands in #350. See `docs/04-seo-plugin-tutorial.md` §6 for the wiring
diagram.)

Score rubric
------------

`ComputeSEOScore` is intentionally simple — 100 pts split across six
checks. Operators who want stricter scoring (Yoast Premium ships ~30
checks) extend the function in their fork:

| Check                                    | Weight |
| ---------------------------------------- | -----: |
| Title present + length 30..60 chars      |     25 |
| Description (or excerpt) 70..160 chars   |     25 |
| Hero image set                           |     15 |
| Canonical URL set                        |     10 |
| Body has ≥ 300 words                     |     15 |
| Author + publication date set            |     10 |

A post that scores below 50 renders a red traffic-light in the admin;
50..79 amber; 80+ green. The thresholds live in the admin UI, not the
plugin — the plugin only emits the integer score.

Extension points
----------------

Things you might want to change in a fork:

- **Add hreflang tags.** Add a new filter on `wp_head` that enumerates
  the post's translations and emits `<link rel="alternate" hreflang>`
  per language. Requires the `posts.read` capability you already hold.
- **Submit pingbacks.** Subscribe to `post.published` and post to the
  Google/Bing IndexNow endpoints. Requires `http.fetch`, which you'd
  add to `manifest.json` (operators will see the new cap on the next
  activation).
- **Customise the title separator.** The "|" between title and brand
  is hardcoded; pull it from a plugin setting once the settings ABI
  lands.
- **Extend the rubric.** `ComputeSEOScore` is a pure function over
  `Post`; add more checks and re-tune the weights to sum to 100.

Tests
-----

Run the example's tests with stock Go (no TinyGo required):

```
cd packages/go
go test -race -count=1 ./../../examples/plugins/seo/...
```

The test suite covers:

1. **Manifest schema** — confirms `manifest.json` validates against
   `packages/go/plugins/manifest/schema.json`.
2. **Unit tests** — `BuildTitle`, `BuildDescription`, `BuildHeadHTML`,
   `BuildJSONLD`, `ComputeSEOScore` against pinned fixtures.
3. **HTML escaping** — verifies `<script>` injection is escaped.
4. **JSON-LD validity** — parses the emitted block as JSON and asserts
   the schema.org Article required fields are present.
5. **End-to-end** — builds the same `FilterPayload` the wazero
   dispatcher would marshal, runs it through a dummy host bus, and
   asserts the returned HTML carries `<meta property="og:title">` and
   the JSON-LD block.

The end-to-end test does not require TinyGo: it exercises the same
domain functions the WASM build links, mirroring the wire format the
production dispatcher uses. The TinyGo build is only needed to ship
the plugin to operators.

License
-------

Same as the parent repository.
