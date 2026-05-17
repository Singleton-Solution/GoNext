package wprest

// Pages and posts share the same WP envelope; the differences are
// purely routing (collection path, error codes). Both list/get handlers
// live in posts.go (listGeneric, getGeneric) and dispatch on the
// PostSource passed in.
//
// This file exists so future page-specific behavior (e.g. menu_order
// surfacing, parent-id resolution) has an obvious home without making
// posts.go grow legs. As of #89 there is no page-specific surface — the
// translator is shared verbatim.
//
// listPages / getPage entry points are declared in posts.go alongside
// their post-typed cousins to keep the dispatch table in one place.
