# Migration corpus fixtures

This directory holds **10 small, hand-crafted WordPress eXtended RSS (WXR)
fixtures** plus expected-output JSON files. Each fixture targets a different
shape of WordPress site:

| #  | Slug                | Profile                                   |
| -- | ------------------- | ----------------------------------------- |
| 01 | tiny-blog           | Single author, single post, no comments   |
| 02 | photo-gallery       | Attachment-heavy, image references        |
| 03 | news-categories     | Hierarchical categories, multi-author     |
| 04 | multi-author        | Many authors, fewer posts each            |
| 05 | woocommerce-stub    | `product` post type (warn coverage)       |
| 06 | acf-heavy           | ACF-style postmeta repeaters              |
| 07 | comments-threaded   | Threaded comment trees                    |
| 08 | gutenberg-blocks    | `<!-- wp:* -->` block markers in content  |
| 09 | mixed-pages-posts   | Pages + posts + parent/child hierarchy    |
| 10 | tags-heavy          | Many tags, few categories                 |

## Expected-output schema

Each `expected/<slug>.json` carries the report fields that
`gonext migrate wp --dry-run --file <wxr>` must produce on a clean import.

```json
{
  "authors": 1,
  "categories": 1,
  "tags": 0,
  "posts": 1,
  "attachments": 0,
  "comments": 0,
  "errors_max": 0
}
```

`errors_max` is the upper bound — equal or fewer per-record errors than
this number means the importer is healthy. Tightening it later is fine;
loosening should warrant a code review of the importer change.

## How CI uses these

`.github/workflows/migrate-corpus.yml` runs:

1. `go build` the `gonext` CLI.
2. For each fixture: `gonext migrate wp --dry-run --file <wxr>` and capture
   the report counters.
3. Compare report against `expected/<slug>.json` (deep equal on the
   numeric fields). Mismatches fail the job.

For local runs: `make migrate-corpus-check` (see top-level Makefile).
