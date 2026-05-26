package templates

// SearchPath is an ordered list of ThemeFiles backing stores, walked
// from index 0 (child / most-specific) toward the last (parent /
// least-specific). It is the cornerstone of child-theme override
// resolution: a child theme overrides a parent template by shipping
// the same filename under its own root; the resolver returns the
// child's file when present and falls back to the parent otherwise.
//
// The walk order matters: child-first matches the WordPress contract
// authors expect, where a child can shadow any parent template
// without forking the parent's directory.
//
// A nil SearchPath is treated as an empty one — ResolveTemplate
// returns ("", false) without panicking. An empty entry inside the
// slice is also skipped, so callers can build the path conditionally
// without filtering nils out beforehand.
type SearchPath []ThemeFiles

// ResolveTemplate walks search in order and returns the first entry
// whose ThemeFiles.Has(name) reports the template present. The
// boolean return mirrors the comma-ok idiom Go callers already use
// for map lookups and registry probes, so the call site stays terse:
//
//	idx, ok := templates.ResolveTemplate("single.tsx", search)
//	if !ok { /* fall back */ }
//
// The returned int is the index in `search` that owned the match,
// not a pointer into the slice — that keeps the value immutable
// (callers cannot accidentally mutate the original ThemeFiles
// through it) and lets the caller distinguish "child won" (index 0)
// from "parent won" (index 1) for diagnostics or audit logging
// without re-walking.
//
// The function is pure: no I/O, no globals, no panics on a nil or
// empty path. It is safe to call concurrently as long as the
// underlying ThemeFiles implementations are safe themselves.
func ResolveTemplate(name string, search SearchPath) (int, bool) {
	if name == "" {
		return 0, false
	}
	for i, files := range search {
		if files == nil {
			continue
		}
		if files.Has(name) {
			return i, true
		}
	}
	return 0, false
}

// ResolveWithChild is a thin convenience wrapper around the
// Resolver.Resolve precedence walk, layered over a SearchPath so a
// child theme can override any candidate the resolver would otherwise
// pick from the parent. It is the two-stage form most callers want
// in production: classify the request, then resolve against the
// child→parent path.
//
// The walk is depth-first across the precedence list rather than
// breadth-first across the path: for every candidate basename the
// resolver would try, ResolveWithChild walks the SearchPath in order
// and returns the first hit. This matches the WordPress child-theme
// contract — a child's single-book.tsx overrides the parent's
// single-book.tsx even though the parent also ships an index.tsx
// the child does not.
//
// On match it returns (filename, owningIndex, nil). owningIndex is
// the same "which entry in search owned the match" datum
// ResolveTemplate returns, so the caller can audit "this render came
// from the child theme" vs "this render came from the parent" without
// re-walking.
//
// On failure it returns ErrNoIndex (the SearchPath collectively did
// not ship an index template) or ErrUnknownRequestType (the Request
// was unclassified). The error semantics mirror DefaultResolver so
// switching from Resolve to ResolveWithChild is a one-line change.
func ResolveWithChild(req Request, search SearchPath) (string, int, error) {
	candidates := buildCandidates(req)
	if candidates == nil {
		return "", 0, ErrUnknownRequestType
	}
	if len(search) == 0 {
		return "", 0, ErrNoIndex
	}
	for _, base := range candidates {
		if base == "" {
			continue
		}
		for _, ext := range extensions {
			name := base + ext
			if idx, ok := ResolveTemplate(name, search); ok {
				return name, idx, nil
			}
		}
	}
	return "", 0, ErrNoIndex
}
