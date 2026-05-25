// Package urlrewrite walks HTML post bodies during migration and
// replaces WP-side media URLs with GoNext-side equivalents.
//
// A migrated WP site typically has every uploaded image referenced
// by an absolute URL into wp-content/uploads/YYYY/MM/. The media
// migrator (issue #187) re-uploads those binaries into GoNext's
// media storage and records a (old-url → new-id) mapping per file.
// This package consumes that map and rewrites the post-content
// references so they target the new storage rather than the long-
// dead WP origin.
//
// Five reference forms are supported:
//
//   - <a href="https://old/wp-content/uploads/…">
//   - <img src="…"> with optional srcset / sizes / data-src
//   - <video src="…">
//   - <audio src="…">  (same handling as video)
//   - <source src="…"> inside picture/video/audio elements
//   - inline url(…) inside style="…" attributes
//
// The rewriter never expands its scope to HTTP fetches or DOM-style
// HTML parsing: it works on the raw HTML byte stream with attribute-
// aware regex matching. Anything that doesn't match one of the
// patterns is left alone. False positives are not catastrophic — at
// worst a known-mapped URL gets a textual replacement somewhere it
// wasn't expected — but we keep the matchers anchored to attribute
// values so prose like "see https://old.example/wp-content/…" in a
// paragraph text node is not rewritten.
//
// Typical usage:
//
//	r := urlrewrite.New(urlrewrite.Options{
//	    Map: map[string]urlrewrite.MediaRef{
//	        "https://old.example/wp-content/uploads/2024/03/a.jpg": {
//	            ID:  mediaUUID, URL: "https://cdn.gonext.example/m/abc.jpg",
//	        },
//	    },
//	})
//	out, n := r.Rewrite(content)
//	// n is the count of substitutions performed.
//
// The rewriter is concurrency-safe after construction; the Map field
// is read-only at runtime.
//
// See issue #192.
package urlrewrite
