// Package storage is the GoNext media storage-driver abstraction.
//
// # What this package is
//
// The admin Media Library (apps/api/internal/admin/media) and the
// upload-time image-processing pipeline (packages/go/media/imageproc)
// both need to PUT, GET, and DELETE blobs in some byte-addressable
// store. The original wiring went straight at *minio.Client; that
// works for an S3-shaped deployment but locks the codebase out of two
// equally legitimate footings:
//
//   - local filesystem — handy for `make dev`, for self-hosters
//     running on a single VPS, and for any CI environment that
//     doesn't want a MinIO sidecar just to upload a 200-byte test
//     fixture;
//   - GCS — Google Cloud Storage; reaches the same shape as S3 but
//     through a different HTTP surface, so the V4-signed-URL helpers
//     don't translate one-to-one.
//
// Driver normalizes the surface every storage backend has to expose:
// Put, Get, Delete, Stat, and Presign. A consumer holds a Driver and
// doesn't know whether the bytes land under /var/lib/gonext-media or
// behind an S3 V4 signature; the selection happens once at boot from
// GONEXT_MEDIA_DRIVER and the rest of the code is backend-agnostic.
//
// # Driver implementations
//
// Three drivers ship in this package:
//
//   - LocalDriver — filesystem under GONEXT_MEDIA_ROOT. Presigned
//     URLs are server-relative paths backed by a one-shot handler
//     guarded by an HMAC signature; the URL is only valid while the
//     handler is mounted, so there is no inter-process trust to
//     coordinate. Targets the dev + small-deploy story.
//
//   - S3Driver — minio-go-backed driver that talks to AWS S3, MinIO,
//     R2, Wasabi, B2, Backblaze, or any other S3-compatible store
//     via the existing config.StorageConfig (endpoint, region,
//     bucket, useSSL, pathStyle). Presigned URLs use S3 V4
//     signatures with a configurable TTL (default 15 minutes).
//     Multipart upload is also supported here — see InitMultipart /
//     PresignPart / CompleteMultipart / AbortMultipart.
//
//   - GCSDriver — skeleton + TODOs. The interface is satisfied so
//     wiring code can target GONEXT_MEDIA_DRIVER=gcs today, but the
//     calls return ErrUnimplemented at runtime. A follow-up issue
//     wires the cloud.google.com/go/storage client when the
//     deployment surface needs it.
//
// # Selecting a driver at boot
//
// New(ctx, Options{...}) is the single entrypoint. It reads the env
// var GONEXT_MEDIA_DRIVER (one of "local", "s3", "gcs"; defaults to
// "local") and returns the matching implementation pre-wired with the
// rest of the config. Tests can bypass the env var by passing a
// Driver field explicitly on Options.
//
// # Concurrency
//
// Every Driver implementation is safe for concurrent use; the
// LocalDriver serialises writes per-key via per-key locks so two
// uploads to the same key cannot interleave, and the S3 client is
// already goroutine-safe by minio-go's contract.
package storage
