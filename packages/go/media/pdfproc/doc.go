// Package pdfproc is the upload-time PDF processing pipeline for the
// GoNext media library — closes issue #60.
//
// # What it does
//
// When an operator uploads a PDF through the admin Media Library, two
// derivative artifacts are useful:
//
//   1. A first-page thumbnail — the same grid that shows an image
//      preview should show a recognisable cover for documents, not a
//      generic file icon.
//   2. Extracted full text — used by the admin search index so an
//      operator can find a document by its contents, not just by
//      filename.
//
// The package produces both. Page-1 thumbnail rendering goes through
// pdftoppm (or pdfcpu when pdftoppm is missing — pdfcpu is a pure-Go
// fallback that doesn't need poppler installed). Text extraction goes
// through pdftotext, with a configurable byte cap on the result.
//
// # External-binary policy
//
// poppler-utils (pdftoppm, pdftotext) is the canonical Unix toolchain
// for PDF processing. We don't reimplement it in Go because the
// existing tools handle the long tail of malformed PDFs better than a
// from-scratch implementation could in a release cycle, and shelling
// adds <50ms of overhead per invocation. The trade-off is that the
// worker container has to ship the binaries on PATH; a deployment
// without them must degrade gracefully — the upload still succeeds,
// the row commits, and the admin UI shows a generic-document icon
// for the asset.
//
// # Skip-graceful when poppler is missing
//
// IsAvailable probes the PATH at worker boot. The worker's task
// registration uses the flag: if pdftoppm/pdftotext are not present,
// the spec is registered with a stub handler that logs at warn and
// returns nil for every payload. Boot does NOT fail.
//
// # Testability
//
// The package uses an injectable Runner interface for both binary
// invocations. Production wires it to os/exec; tests substitute a
// recording fake that captures the arguments and fabricates the
// output files. The test path never spawns a subprocess.
package pdfproc
