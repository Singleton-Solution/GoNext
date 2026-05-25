package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// PresignOp is the operation a presigned URL is minted for. The two
// stable values are Put (the client uploads bytes via HTTP PUT) and
// Get (the client downloads bytes via HTTP GET). Modeling this as a
// typed enum rather than a string keeps the call sites self-checking
// — a typo at the use site is a compile error rather than a runtime
// "unsupported op" branch.
type PresignOp string

const (
	// PresignPut is the upload-side signature. The browser PUTs the
	// file body to the returned URL; the storage backend validates
	// the signature, accepts the bytes, and persists them at the key
	// the URL encodes.
	PresignPut PresignOp = "put"

	// PresignGet is the download-side signature. Used for time-
	// limited preview URLs and for the "download original" affordance
	// in the admin grid. Not the same as a public CDN URL — those
	// don't carry a signature and are served unauthenticated.
	PresignGet PresignOp = "get"
)

// Object is the metadata a Stat call returns. Intentionally small —
// the admin UI and the orphan-purge cron want size, mime, and the
// last-modified timestamp; everything else (ETag, version id, custom
// headers) is omitted because callers don't need it yet and the cross-
// backend coverage is uneven.
type Object struct {
	// Key is the storage path the object lives at. Echoed back so a
	// caller that already has the key doesn't have to keep its own
	// copy alongside the Stat result.
	Key string

	// Size is the byte length of the object.
	Size int64

	// MimeType is what the backend recorded at PUT time. For
	// LocalDriver this is the mime passed to Put; for S3Driver it is
	// the object's Content-Type metadata as returned by HEAD.
	MimeType string

	// LastModified is the timestamp the backend recorded for the
	// object. The cron that aborts orphaned multipart uploads sorts
	// by this; without a well-defined ordering the "older than 24h"
	// filter is meaningless.
	LastModified time.Time
}

// Driver is the storage backend abstraction. Every method takes a
// context so callers can cancel a long-running PUT / GET; backends
// that do not natively respect ctx cancellation (the local filesystem
// does not, for example) honour the deadline by checking before each
// IO step rather than mid-stream.
//
// Implementations are safe for concurrent use; the documentation on
// each implementation calls out any per-key serialisation it does.
type Driver interface {
	// Put uploads the contents of r at key with the given mime type.
	// The reader is consumed in full; implementations may buffer or
	// stream depending on backend constraints. Returns the number of
	// bytes written and any error from the backend.
	//
	// The mime parameter is metadata only — it is recorded with the
	// object so a later Stat or download response can echo it, but
	// the bytes themselves are not interpreted. Callers should pass
	// the sniffed (not the client-claimed) mime to keep an
	// authenticated chain of custody from upload to render.
	//
	// Errors are returned as-is from the backend; callers that need
	// to distinguish "object exists" from "backend down" should
	// check the Stat path first.
	Put(ctx context.Context, key string, r io.Reader, mime string) (int64, error)

	// Get opens key for reading. The returned ReadCloser MUST be
	// closed by the caller; implementations may stream the body from
	// the network connection, so leaking the closer leaks the
	// connection. Returns ErrNotFound when the key does not exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes key. A missing key is NOT an error — the call
	// is idempotent so a re-run of the purge cron does not error
	// when an earlier sweep already cleaned the orphan. Returns the
	// backend error on any other failure.
	Delete(ctx context.Context, key string) error

	// Stat returns the object's metadata or ErrNotFound if the key
	// does not exist. Used by the admin detail view for "object size
	// in bucket" and by the orphan-purge cron to find stale rows.
	Stat(ctx context.Context, key string) (*Object, error)

	// Presign returns a URL the client can use to perform op against
	// key without further server interaction. ttl is the validity
	// window; implementations clamp to a backend-specific maximum
	// (S3 V4 signatures cap at 7 days, the local driver caps at the
	// process lifetime since the HMAC key rotates on restart).
	//
	// The returned headers map is populated when the URL requires
	// specific request headers to be set by the client (the S3
	// driver sets Content-Type so the signature covers the upload's
	// mime; the local driver returns an empty map). Callers send
	// these verbatim alongside the request.
	Presign(ctx context.Context, key string, op PresignOp, ttl time.Duration, mime string) (PresignedRequest, error)

	// PublicURL returns the externally-addressable, unauthenticated
	// URL for key. For S3Driver this is the bucket's
	// virtual-host-style URL; for LocalDriver this is a server-
	// relative path served by a static handler. Callers that need
	// authenticated/expiring URLs use Presign instead.
	PublicURL(key string) string
}

// MultipartDriver is the optional capability for drivers that
// support S3 multipart uploads. The S3Driver implements this; the
// LocalDriver does not (large files on the local filesystem are
// handled by the single-shot Put path) and the GCSDriver will gain
// it when the Google client lands. Callers do a type assertion to
// check whether multipart is available:
//
//	if mp, ok := driver.(MultipartDriver); ok {
//	    init := mp.InitMultipart(ctx, key, mime)
//	    ...
//	}
type MultipartDriver interface {
	// InitMultipart begins a multipart upload at key with the given
	// mime type. Returns the upload id the caller must echo back
	// when presigning parts or completing/aborting the upload.
	InitMultipart(ctx context.Context, key, mime string) (uploadID string, err error)

	// PresignPart returns a presigned PUT URL for one part of an
	// in-flight multipart upload. partNumber is 1-indexed; S3 caps
	// at 10,000 parts per upload.
	PresignPart(ctx context.Context, key, uploadID string, partNumber int, ttl time.Duration) (string, error)

	// CompleteMultipart finalises the upload, gluing the parts
	// together server-side. parts is the list of (partNumber, etag)
	// the client collected as each part's PUT response. Returns
	// ErrNotFound when the upload id is unknown (already completed,
	// aborted, or never existed).
	CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error

	// AbortMultipart cancels the upload and releases any storage
	// the partial parts consumed. Idempotent — calling on an
	// already-aborted or never-existed upload returns nil.
	AbortMultipart(ctx context.Context, key, uploadID string) error

	// ListIncompleteUploads enumerates multipart uploads whose
	// initiation time is older than olderThan. Returns at most
	// limit entries. Used by the orphan-purge cron to abort
	// uploads abandoned by a tab close or a flaky network.
	ListIncompleteUploads(ctx context.Context, olderThan time.Time, limit int) ([]IncompleteUpload, error)
}

// CompletedPart is one row of a multipart completion manifest. ETag
// is the value the backend returned in the part PUT response — the
// client echoes it back here so the backend can verify the part
// hasn't been altered on the wire between PUT and Complete.
type CompletedPart struct {
	PartNumber int
	ETag       string
}

// IncompleteUpload is one entry returned by ListIncompleteUploads.
// The orphan-purge cron iterates these and calls AbortMultipart on
// each one. UploadID is the value passed to AbortMultipart; Initiated
// is the timestamp the backend recorded when the upload was started
// (used for the "older than X hours" filter).
type IncompleteUpload struct {
	Key       string
	UploadID  string
	Initiated time.Time
}

// PresignedRequest is the bundle Presign returns. URL is the
// authenticated, time-limited address the client uses; Headers is
// the (possibly empty) set of request headers the signature requires
// the client to send unchanged.
//
// ExpiresAt is the wall-clock instant the URL stops working. The
// admin UI shows this to the operator as a "this upload window
// closes at HH:MM" banner so an upload that goes wrong is debuggable
// without guessing why a fresh URL would succeed.
type PresignedRequest struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// Sentinel errors. Wrapped by every Driver implementation; callers
// check via errors.Is.
var (
	// ErrNotFound is returned by Get / Stat / Delete-on-missing when
	// the key does not exist. The handler layer translates to HTTP
	// 404; the orphan-purge cron uses it to skip "already gone"
	// entries without surfacing a noisy error.
	ErrNotFound = errors.New("storage: object not found")

	// ErrUnimplemented is returned by stub methods (notably the
	// GCSDriver's). Useful for the wiring layer to log a friendly
	// "driver=gcs is not yet implemented, please use s3 or local"
	// rather than crashing with a nil-dereference.
	ErrUnimplemented = errors.New("storage: operation not implemented by this driver")

	// ErrInvalidKey is returned when a key would escape the storage
	// root (contains "..", an absolute path, or backslashes). All
	// keys are conceptually forward-slash-separated relative paths
	// inside the bucket; anything else is a wiring bug we want to
	// surface loudly.
	ErrInvalidKey = errors.New("storage: invalid key")
)
