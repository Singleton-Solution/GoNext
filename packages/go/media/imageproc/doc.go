// Package imageproc is the upload-time image processing pipeline for
// the GoNext media library.
//
// # What it does
//
// When an operator uploads an image through the admin Media Library
// (apps/api/internal/admin/media — see PR #405), a single source-of-
// truth bitmap lands in S3 at <key>. That bitmap is too large for a
// hero on a phone, too small for a 4K display, contains a possibly-
// privacy-sensitive EXIF block, and is encoded in whichever format the
// uploader's camera or laptop happened to write — usually JPEG, often
// PNG, occasionally an animated GIF. Rendering it on a marketing page
// would mean shipping all those bytes to every viewer regardless of
// their viewport, and quietly leaking the photographer's GPS
// coordinates in the bargain.
//
// imageproc.Process closes that gap. Given the original bytes, it
// produces a Result holding the original (re-encoded, EXIF-stripped)
// plus three size-classed variants — thumbnail (256px), medium
// (768px), large (1536px) — encoded into a small handful of modern
// formats (WebP by default, AVIF when the build tag enables libaom).
// The set is deterministic for a given input, so a re-process of the
// same source bytes yields byte-identical variants modulo encoder
// non-determinism.
//
// The pipeline is invoked asynchronously: the upload handler enqueues
// a media.process task via packages/go/jobs/taskspec, and the worker
// runs Process and writes each variant back to storage as a sibling
// key (<key>.thumb.webp, <key>.medium.webp, etc.). The Run helper in
// task.go is the spec.Handler wired by Dispatch.
//
// # Why a sub-package
//
// packages/go/media currently ships only the single-flight Coalescer
// from #334. The image-processing surface is large enough — four
// pieces (Process, EXIF strip, srcset builder, task) and a handful of
// format-specific encoders — that mixing it with the Coalescer would
// muddy the read. The sub-package keeps the import boundary narrow:
// consumers that only need the coalescer don't pay for image/png and
// the WebP encoder.
//
// What's in this package
//
//   - imageproc.go — Process(ctx, src, opts) → *Result; the core
//     decode-resize-encode-strip loop. ProcessOptions configures
//     variant sizes, formats, JPEG/WebP quality, and the storage key
//     scheme so callers can swap the layout under test.
//   - exif.go — StripEXIF reads any JPEG that carries an APP1/Exif
//     marker and returns the bytes with the marker removed; the
//     decode → encode round-trip in Process drops EXIF for every
//     other format because none of the std-lib encoders propagate it.
//   - srcset.go — BuildSrcSet renders a Result into the `srcset="..."`
//     HTML attribute the theme renderer and the REST surface emit.
//     Variants are ordered narrowest-first so the browser picks the
//     smallest matching width.
//   - task.go — TaskSpec for the media.process task. The handler
//     consumes the upload event, calls Process on the bytes pulled from
//     storage, and writes each variant back as a sibling key.
//
// # Dependencies and build tags
//
// Pure-Go decoders for JPEG, PNG, GIF and WebP ship with the standard
// library and golang.org/x/image/webp. The default WebP *encoder* is
// HugoSmits86/nativewebp (pure Go, no cgo). AVIF encoding requires
// libaom via Kagami/go-avif and is gated behind the "avif" build
// tag; default builds (and CI) treat AVIF as a no-op and emit only
// WebP variants, so a deployment that doesn't ship libaom degrades
// gracefully. See avif_libaom.go and avif_stub.go for the two
// implementations.
//
// The package is safe for concurrent use; Process holds no state
// between calls and the Result is read-only after return.
package imageproc
