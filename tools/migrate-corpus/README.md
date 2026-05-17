# migrate-corpus

`gonext-corpus` is a small Go CLI that produces a deterministic, **synthetic**
corpus of WordPress-shape sites for the importer's migration tests.

It is intentionally **not** a snapshot of any real WordPress install. Per
`docs/proposals/14-proposals-ops-sec.md` Q11-5, sourcing corpus data from real
sites raises licensing and re-identification concerns that don't add testing
signal. What the importer needs is variety in *shape* — post-type mix,
taxonomy depth, presence of ACF / Gutenberg / comments / media — not
literal content.

See also:

- `docs/08-migration-compat.md` §16 — corpus catalog and CI plan.
- `docs/11-testing-ci.md` §10 — migration test plan.

## Status

This module lives outside `go.work` on purpose: it has zero runtime deps on
the rest of the repo and we don't want CI to rebuild it on every change to
`apps/api`. If we ever want to import it from another module, add a
`use ./tools/migrate-corpus` line to `go.work` then.

## Install / build

```bash
cd tools/migrate-corpus
go build -o gonext-corpus .
```

Or run it directly with `go run`:

```bash
go run . generate --out ./out --sites=2 --posts-per-site=5 --seed=42
```

## Usage

### Generate

```bash
gonext-corpus generate \
  --out ./corpus \
  --sites=10 \
  --posts-per-site=100 \
  --seed=42
```

Flags:

- `--out` (default `./corpus`) — output directory. Created if missing.
- `--sites` (default `10`) — number of sites to produce.
- `--posts-per-site` (default `100`) — approximate post count; the actual
  count per site is `posts-per-site * profile.PostFactor` (different
  profiles deliberately produce different sizes).
- `--seed` (default `42`) — deterministic seed. Same `(seed, sites,
  posts-per-site)` produces identical output (modulo on-disk mtimes).
- `--overwrite` — delete `--out` first.

Output layout:

```
corpus/
  site-01-01-tiny-classic/
    wxr.xml          — WordPress eXtended RSS (WXR 1.2) shape
    wp_db.sql        — MySQL dump shape (schema + representative rows)
    manifest.json    — site metadata, counts, declared profile
  site-02-02-news-classic/
    ...
  ...
```

The profile catalog is in `profiles.go`. The 10 stock profiles roughly map
to the catalog in `docs/08-migration-compat.md §16.1`:

| # | Profile slug         | Notes                                                  |
|---|----------------------|--------------------------------------------------------|
| 1 | `01-tiny-classic`    | Smoke test. No plugins, no comments.                   |
| 2 | `02-news-classic`    | Classic editor + Yoast/Jetpack stand-ins.              |
| 3 | `03-mixed-editor`    | Classic + Gutenberg block markers.                     |
| 4 | `04-pagebuilder`     | Elementor-style pages.                                 |
| 5 | `05-acf-heavy`       | ACF-style postmeta (repeaters, flex content).          |
| 6 | `06-cpt-taxonomy`    | Custom post types + custom taxonomies.                 |
| 7 | `07-comment-heavy`   | Threaded comments with mod queue + spam flags.         |
| 8 | `08-media-scale`     | Many attachment posts.                                 |
| 9 | `09-woocommerce`     | Product / shop_order CPTs (importer should warn).      |
|10 | `10-multilang`       | Polylang-shape, mixed content.                         |

If `--sites > 10`, profiles wrap (`profile = profiles[i % 10]`).

### Verify

Re-parse a generated corpus and assert basic well-formedness:

```bash
gonext-corpus verify --in ./corpus
```

What this checks:

- `wxr.xml` parses as XML, contains a `<channel>` element, uses the WP
  export namespace, and has at least one `<item>`.
- `wp_db.sql` contains the required `CREATE TABLE` statements (`wp_posts`,
  `wp_terms`, `wp_options`) and at least one `INSERT`.
- `manifest.json` parses and has the required top-level keys plus the
  `counts` sub-object.

This is a fast sanity check, not a full importer compatibility test. The
real importer integration runs nightly per `docs/11-testing-ci.md §10`.

## Determinism

Each site has its own PRNG seeded by `(--seed, site_index)`. Consequences:

- Truncating `--sites=10` to `--sites=5` yields the same first five sites.
- Same `--seed` across machines yields byte-identical XML/SQL/JSON.
- `time.Now()` is **never** called for content; all dates are derived from
  the fixed epoch `2024-01-01T12:00:00Z + site_index days`.

## What's deliberately *not* here

- No real WP content (see proposal Q11-5).
- No serialized PHP beyond a one-liner `a:N:{...}` for `active_plugins`.
  The importer is expected to ignore unfamiliar serialised payloads.
- No actual uploaded media. Attachment posts reference URLs under
  `https://site-NN.example.test/wp-content/uploads/...` that resolve to
  nothing; importer tests stub the HTTP layer.

## Adding profiles

1. Append a new entry to `Profiles()` in `profiles.go`. Keep the slug
   greppable (`NN-short-tag`) and pick a `PostFactor` reflecting expected
   relative size.
2. If the profile needs a shape the current `buildSite` doesn't emit (e.g.
   menus, multisite), extend `buildSite` and the writers.
3. Re-run `gonext-corpus generate` against your existing tests and update
   any goldens; they will diverge by design.
