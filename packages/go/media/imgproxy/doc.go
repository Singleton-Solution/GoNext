// Package imgproxy is the on-the-fly image variant generator for the
// public /img/{id}/{spec} route.
//
// # What it does
//
// Where packages/go/media/imageproc precomputes a fixed set of
// variants at upload time (thumbnail / medium / large × WebP/AVIF),
// imgproxy generates variants on demand from a URL spec. The route
// surfaces a sibling of imageproc — both decode the same source bytes
// and run a resize-encode pipeline — but the read path is different:
// imageproc writes variants back to storage at upload time;
// imgproxy renders into a local disk cache on first hit and serves
// from cache on every subsequent hit.
//
// The spec language is dot-separated key-value pairs:
//
//	w-800        — width in pixels (1..8192)
//	h-600        — height in pixels (1..8192)
//	q-85         — encoder quality (1..100)
//	fit-cover    — resize mode: cover (crop to fill) or contain (letterbox)
//	webp         — output format: webp | jpeg | png
//
// Example: GET /img/<uuid>/w-800.h-600.q-85.fit-cover.webp
//
// At least one of w/h must be supplied; the other is computed from
// the source aspect ratio. The format token is positional anywhere
// in the spec — the parser does not require it to come last.
//
// # Why a separate package
//
// imageproc's surface is "upload-time pipeline" — the entry point is
// Process, the inputs are a ProcessOptions struct, and the outputs are
// a Result holding every produced variant. That shape is wrong for
// the proxy: the proxy has one (width, height, format) tuple and one
// io.Reader output, and threading that through Process would mean
// faking out variant slices the proxy doesn't care about.
//
// imgproxy shares the *transform* (decode → resize → encode) with
// imageproc — both can call into the same libvips-or-stdlib backend
// via the Transform function exported here — but the request shape
// and the cache + coalescer story is proxy-specific. Keeping the two
// packages separate also lets imageproc keep its narrow upload-time
// dep set (no http, no os/file).
//
// # Backends
//
// Transform dispatches to one of two backends depending on the
// GONEXT_IMAGEPROC env var and which backends were compiled in:
//
//   - "govips" (default when the `vips` build tag is set and libvips
//     starts cleanly): wraps github.com/davidbyttow/govips/v2/vips.
//     Faster and produces visibly better output than the pure-Go path,
//     at the cost of a CGO dep on libvips at link time.
//
//   - "stdlib" (default for builds without the `vips` tag): wraps
//     image/jpeg, image/png, and HugoSmits86/nativewebp via the same
//     primitives imageproc uses. Always available, slower on large
//     inputs, fine for the development loop and for CI.
//
// If GONEXT_IMAGEPROC is set to an unknown value the package logs a
// warning and falls back to stdlib — operators get a deterministic
// fallback rather than a startup panic.
//
// # Concurrency
//
// Transform is safe for concurrent use. The govips backend wraps a
// per-call vips.NewImageFromBuffer; the stdlib backend operates on
// caller-owned bytes. The package never holds image bytes across
// calls.
//
// The package-level lazy initialization of the vips backend is
// guarded by a sync.Once so a flurry of cold-start requests collapses
// to a single Startup call. Subsequent Transform calls observe the
// already-initialized backend with no lock.
package imgproxy
