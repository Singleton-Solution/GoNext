// Package search is the canonical entry point for full-text queries
// against GoNext's content corpus.
//
// The package is a thin layer over the Postgres FTS facility wired up
// by migration 000011_search (posts.search_vector tsvector + GIN
// index + a BEFORE-INSERT-OR-UPDATE trigger that maintains it). The
// design choices behind the schema — A/B/C/D weights, English
// dictionary, in-write trigger — are documented in that migration; we
// don't repeat them here.
//
// What this package adds on top:
//
//   - A Go-facing Search(ctx, q, opts) entry point that turns a free-text
//     query into a `plainto_tsquery`-driven SELECT against posts. Use of
//     `plainto_tsquery` over `to_tsquery` is deliberate: the former
//     accepts arbitrary user input safely (no operator parsing, no
//     syntax errors that surface as 500s), at the cost of slightly less
//     expressive queries. The trade-off is right for a baseline public
//     site search; an "advanced search" surface can layer
//     `websearch_to_tsquery` on top later.
//
//   - A Results envelope (Hits, Total, QueryDuration) that the REST and
//     admin handlers serialize verbatim. QueryDuration is measured
//     inside the package so the handler doesn't have to time the DB
//     call itself — useful for the System Status surface and for unit
//     budgets in tests.
//
//   - A Highlight helper that wraps matched terms in <mark>…</mark>
//     spans for in-snippet rendering. The helper escapes HTML first,
//     so a hit whose title contains "<script>" is safe to drop into a
//     template that does {{ . | safehtml }}.
//
// Out of scope for this package:
//
//   - Indexing. The trigger does that.
//   - Facets / aggregations. Not in the public-search MVP.
//   - Multi-language. The migration hardcodes the English dictionary;
//     when posts.search_language lands as a follow-up the SearchOpts
//     struct will grow a Language field and the SQL will switch to
//     regconfig coalescing.
//
// Concurrency: every exported type is safe to share across goroutines.
// The Store implementation holds a *pgxpool.Pool which is itself
// thread-safe.
package search
