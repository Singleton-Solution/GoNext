package search

// Renderer contract — `search.html` template wiring
// ===================================================
//
// The public site renderer (the `apps/web` Next.js app, in flight as
// of issue #6) renders the front-end search page by resolving the
// theme's `search` template via the templates.DefaultResolver
// (themes/gn-pro ships `search.html`; gn-hello falls back to
// `index.html` per the §4.2 hierarchy).
//
// When the renderer is asked to serve `?s=<query>` (the canonical
// front-end search query string) it MUST:
//
//   1. Set Request.Type = RequestTypeSearch on the resolver request
//      so the template hierarchy lands on `search.tsx`/`search.html`
//      rather than `index.*`.
//
//   2. Populate the renderer's `query` context with the results of
//      calling Searcher.Search(ctx, q, SearchOpts{
//          Status:    "published",   // matches publicStatus here
//          SkipTotal: false,         // the template needs the total
//                                    //  for query-pagination-numbers
//          Limit:     perPage,       // from the wp:query block
//          Offset:    (page-1)*perPage,
//      }).
//
//   3. Render the resulting search.Results into the wp:query /
//      wp:post-template block tree. Each Hit maps to one post-template
//      iteration. Hit.ExcerptHTML can be dropped into the
//      `post-excerpt` block verbatim — the highlight tokens
//      (`<mark>`) are part of the public-facing contract.
//
// JS / progressive enhancement path
// ---------------------------------
//
// Themes that want an in-page live search (without a full page
// reload) call this package's HTTP endpoint directly:
//
//   GET /api/v1/search?q=<term>&limit=10
//
// The endpoint is anonymous + rate-limited; the response shape is
// the same search.Results JSON the renderer consumes server-side.
//
// Why both surfaces?
// ------------------
//
// The renderer reads the package directly (in-process) because it
// already holds a *pgxpool.Pool and can avoid a needless HTTP hop.
// The HTTP endpoint exists for client-side JS, for plugins, and for
// the (rare) theme that wants to drive search via a fetch call
// without spinning up the renderer's hooks.
//
// This file has no executable code. It exists so a reader landing
// on the rest/search package sees the renderer wiring contract
// alongside the handler, without having to grep the docs tree.

// ContractVersion pins the renderer-side contract this package
// satisfies. Increment when the wire shape changes incompatibly so
// the renderer can refuse to mount against an outdated API binary.
//
// V1: search.Hit shape from packages/go/search; Status pinned to
// "published"; ExcerptHTML uses <mark> for matched terms; SkipTotal
// defaults to true.
const ContractVersion = 1
