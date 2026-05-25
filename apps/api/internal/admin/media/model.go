package media

import (
	"context"
	"errors"
	"time"
)

// MaxUploadBytes is the hard ceiling on a single upload. 50 MiB covers
// the 99th percentile of admin uploads (hero images, PDFs, short video
// clips) while keeping the worst-case memory footprint of a buggy
// client well below the API server's per-request budget.
//
// The handler enforces this with http.MaxBytesReader; passing the
// limit through MaxBytesReader (rather than rejecting after-the-fact)
// means a hostile client can't tie up a goroutine streaming gigabytes
// only to be denied at the end.
const MaxUploadBytes = 50 * 1024 * 1024

// detectionSnifLen is the number of bytes passed to http.DetectContentType.
// 512 is the standard library's documented minimum (and the buffer it
// allocates internally); using exactly 512 means the sniffer sees its
// full signature window and the result matches what the stdlib uses
// elsewhere.
const detectionSnifLen = 512

// DefaultListLimit is the grid page size when the client supplies no
// `limit` query param. Matches the rest of the admin REST surface for
// muscle memory.
const DefaultListLimit = 30

// MaxListLimit caps the page size. Higher than this and a single
// response wedges the grid's virtualised renderer while the JSON
// reflows; the limit also keeps a "list everything" call from going
// linear over the bucket.
const MaxListLimit = 100

// Asset is the wire shape returned by the list + detail endpoints. The
// JSON tags double as the persisted column names; keeping them aligned
// makes the store's row scan a one-liner.
//
// PublicURL is computed at render time — it depends on the storage
// config (endpoint + bucket + path-style flag) rather than being a
// stored column. Storing the URL would couple the row to one bucket
// layout; we'd have to migrate every row when the operator switches
// from path-style to virtual-host-style addressing.
type Asset struct {
	ID         string    `json:"id"`
	Filename   string    `json:"filename"`
	MimeType   string    `json:"mime_type"`
	ByteSize   int64     `json:"byte_size"`
	Width      *int      `json:"width,omitempty"`
	Height     *int      `json:"height,omitempty"`
	AltText    string    `json:"alt_text"`
	Caption    string    `json:"caption"`
	StorageKey string    `json:"storage_key"`
	PublicURL  string    `json:"public_url,omitempty"`
	UploaderID string    `json:"uploader_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// CollectionID names the folder the asset lives in (the
	// media_collections.id foreign key). Nil for assets sitting at
	// the implicit "root" view. Issue #69.
	CollectionID *string `json:"collection_id,omitempty"`

	// Tags is a JSONB array of normalised (lowercase, deduplicated)
	// tag strings. Populated by the bulk "tag" operation and the
	// detail-page editor. Issue #71.
	Tags []string `json:"tags"`

	// Variants is the list of generated renditions (thumbnail,
	// medium, large, re-encoded original) the upload-time image-
	// processing pipeline produced. Populated by the worker via
	// Store.MarkProcessed after the media.process task runs. Empty
	// for non-image assets and for image assets whose processing has
	// not yet completed; clients should treat absence as "fall back
	// to the original via PublicURL".
	Variants []Variant `json:"variants,omitempty"`

	// HLSURL is the public URL of the HLS playlist produced by the
	// media.video.transcode task (#52). Empty for non-video assets
	// and for video assets whose transcode hasn't completed yet; the
	// public player should fall back to PublicURL when this is
	// empty.
	HLSURL string `json:"hls_url,omitempty"`

	// HasExtractedText is true when the media_text table has a row
	// for this asset (#60). The detail page surfaces a "View
	// extracted text" link based on this flag; the full payload
	// lives behind a separate endpoint to keep the list response
	// from ballooning on long documents.
	HasExtractedText bool `json:"has_extracted_text,omitempty"`

	// IsProxied is true when the row represents a remotely-hosted
	// asset registered in proxy mode by the migration importer
	// (#187). The image proxy serves the bytes via SourceURL; the
	// admin grid surfaces a "proxied" badge so an operator can tell
	// at a glance which assets are local vs remote.
	IsProxied bool `json:"is_proxied,omitempty"`

	// SourceURL is the origin URL for proxied assets. Empty for
	// locally-stored assets.
	SourceURL string `json:"source_url,omitempty"`
}

// Variant is one rendition produced by packages/go/media/imageproc.
// Mirrors the imageproc.ManifestEntry shape; defined locally so the
// REST package's wire surface does not leak the internal package's
// type names into JSON consumers. PublicURL is computed at render
// time the same way Asset.PublicURL is.
type Variant struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	StorageKey string `json:"storage_key"`
	MimeType   string `json:"mime_type"`
	PublicURL  string `json:"public_url,omitempty"`
}

// AssetCreate is the input shape for Store.Insert. Separated from
// Asset because the persistence layer chooses the ID + timestamps;
// callers only supply what the upload handler actually knows.
type AssetCreate struct {
	Filename   string
	MimeType   string
	ByteSize   int64
	Width      *int
	Height     *int
	StorageKey string
	SHA256     []byte
	UploaderID string
}

// AssetUpdate is the body shape for PATCH. Both fields are pointers
// so the handler can distinguish "client did not supply this field"
// (leave it untouched) from "client cleared this field" (set to "").
type AssetUpdate struct {
	AltText *string `json:"alt_text,omitempty"`
	Caption *string `json:"caption,omitempty"`
}

// ListFilter narrows the GET /admin/media response. All fields are
// optional; the handler builds the filter from query params.
type ListFilter struct {
	// MimeClass is one of "", "image", "video", "document". The
	// dispatch happens in the store via mimeClassPredicate; the wire
	// surface keeps the abstraction so the UI doesn't have to think
	// about "is application/pdf a document or other?".
	MimeClass string

	// Limit is the page size; the store clamps to MaxListLimit.
	Limit int

	// Cursor is opaque; the store layer encodes the (created_at, id)
	// pair and the handler treats it as a string.
	Cursor string

	// CollectionID, when non-nil, narrows the list to media whose
	// collection_id matches. The special pointer-to-empty-string ""
	// matches assets sitting at the implicit root (collection_id IS
	// NULL); a pointer to a UUID matches that one folder. Issue #69.
	CollectionID *string
}

// Page is the paginated list envelope. NextCursor is empty when there
// is no more data.
type Page struct {
	Data       []Asset    `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// Pagination is the same shape as router.PageInfo but defined locally
// to avoid the JSON-tag drift that would happen if the router struct
// gained fields.
type Pagination struct {
	NextCursor string `json:"next_cursor"`
}

// Store is the persistence boundary for media rows. Two backends:
// in-memory for tests; Postgres for production. The interface is small
// so the test fake stays cheap to build.
type Store interface {
	// Insert persists a brand-new asset. Returns the populated Asset
	// (id + timestamps filled in by the store).
	Insert(ctx context.Context, a AssetCreate) (Asset, error)

	// GetByHash looks up an asset by its sha256 content hash. Returns
	// ErrNotFound when no row matches — the upload handler uses this
	// as the dedupe probe before doing the S3 PUT.
	GetByHash(ctx context.Context, sha256 []byte) (Asset, error)

	// GetByID fetches a single asset. Honours soft-delete: a deleted
	// row returns ErrNotFound (the trash view, when it lands, will use
	// a separate method that skips the filter).
	GetByID(ctx context.Context, id string) (Asset, error)

	// List returns a page of assets matching the filter. Soft-deleted
	// rows are excluded; ordering is created_at DESC.
	List(ctx context.Context, f ListFilter) (Page, error)

	// UpdateMetadata changes alt_text and/or caption. Only the
	// non-nil fields on the update struct are applied — see the
	// AssetUpdate doc block.
	UpdateMetadata(ctx context.Context, id string, u AssetUpdate) (Asset, error)

	// SoftDelete sets deleted_at on the row. Returns ErrNotFound if no
	// active row matches.
	SoftDelete(ctx context.Context, id string) error

	// SetVariants records the renditions produced by the image-
	// processing pipeline on the asset's row. Called by the worker
	// once the media.process task has written variants to storage;
	// idempotent — re-running on the same asset replaces the
	// previous variant list. Returns ErrNotFound if the asset has
	// been soft-deleted between enqueue and handler.
	SetVariants(ctx context.Context, id string, variants []Variant) error

	// SetCollection re-files an asset into a collection. nil
	// collectionID puts the asset back at the implicit root.
	// Returns ErrNotFound if the asset has been soft-deleted.
	// Issue #69.
	SetCollection(ctx context.Context, id string, collectionID *string) error

	// SetTags replaces the asset's tag list. The handler normalises
	// the incoming list (lowercase, deduplicated) before calling.
	// Returns ErrNotFound if the asset has been soft-deleted.
	// Issue #71.
	SetTags(ctx context.Context, id string, tags []string) error
}

// ErrNotFound is the sentinel returned by store reads when the row is
// missing (or soft-deleted). Handler translates to HTTP 404.
var ErrNotFound = errors.New("admin/media: asset not found")

// ObjectPutter is the subset of the S3 client the upload handler
// depends on. Defined here so tests can substitute a fake without
// requiring a live MinIO container.
//
// Production wires this to a thin wrapper around *minio.Client; the
// wrapper lives outside this package because the S3 client is shared
// between the admin upload path and the public variant proxy.
type ObjectPutter interface {
	// PutObject uploads body at key. size is the exact content length;
	// implementations may pass -1 for unknown size, but the upload
	// handler always knows the size after the multipart read.
	PutObject(ctx context.Context, key string, body []byte, contentType string) error

	// PublicURL returns the externally addressable URL for key. The
	// implementation chooses between path-style and virtual-host
	// addressing based on its configured storage layout.
	PublicURL(key string) string
}

// ProcessEnqueuer fires the upload-time image-processing pipeline.
// The upload handler invokes Enqueue after the row is committed; the
// worker runs the corresponding handler from packages/go/media/imageproc
// and calls Store.SetVariants when the variants land in storage.
//
// Implementations live outside this package — production wires a
// thin adapter around taskspec.Enqueue; tests use an in-process
// closure that runs the handler synchronously. Defined here because
// the handler's dependency bag should be the one place that names
// all the collaborators.
type ProcessEnqueuer interface {
	// Enqueue requests processing for the asset whose original bytes
	// were just uploaded to storageKey. A non-nil error from this
	// method does NOT fail the upload — the row and S3 object are
	// already committed, and a worker-queue outage should not lose
	// uploads. The handler logs and moves on; an operator can rerun
	// processing later via an admin reprocess endpoint.
	Enqueue(ctx context.Context, assetID, storageKey, mimeType string) error
}

// disallowedMimePrefixes is the set of sniffed MIME types we refuse on
// upload. The list is intentionally short — anything not on it goes
// through. The intent is to keep an operator from uploading an .exe
// (which one of the linters might happily render as a download link
// elsewhere in the surface); a full deny-by-default whitelist would
// be louder than needed for the operator surface.
var disallowedMimePrefixes = []string{
	"application/x-msdownload",
	"application/x-msdos-program",
	"application/x-msi",
	"application/x-executable",
	"application/x-mach-binary",
	"application/x-elf",
	"application/x-sh",
	"application/x-bat",
	"application/vnd.microsoft.portable-executable",
}

// disallowedExtensions is the second half of the deny check. The Go
// stdlib's http.DetectContentType does NOT sniff Windows PE binaries
// (it returns "application/octet-stream" for an MZ header), so a
// MIME-only deny check would let .exe sail through. We pair it with
// an extension check on the original filename: if the operator
// uploads "evil.exe" we refuse regardless of what bytes are inside,
// because the file is going to be referenced by that name later and
// the rendering surface would treat it as an executable download.
//
// This is intentionally a small, well-known list — the goal is not
// to be an antivirus, it's to prevent an obvious foot-gun in the
// admin UI.
var disallowedExtensions = map[string]struct{}{
	".exe":   {},
	".dll":   {},
	".bat":   {},
	".cmd":   {},
	".sh":    {},
	".com":   {},
	".msi":   {},
	".scr":   {},
	".ps1":   {},
	".jar":   {},
	".vbs":   {},
	".vbe":   {},
	".wsf":   {},
	".wsh":   {},
	".so":    {},
	".dylib": {},
}

// isDisallowedMime reports whether mime falls in the executable-ish
// deny list. Case-insensitive on the type/subtype prefix because the
// sniffer's output is canonical but the operator-supplied content-type
// (which we no longer trust but log) sometimes isn't.
func isDisallowedMime(mime string) bool {
	for _, p := range disallowedMimePrefixes {
		if len(mime) >= len(p) && equalFoldASCII(mime[:len(p)], p) {
			return true
		}
	}
	return false
}

// isDisallowedExtension reports whether name ends in one of the
// known-bad extensions. The check is case-insensitive (Windows uploads
// often arrive as .EXE).
func isDisallowedExtension(name string) bool {
	dot := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			dot = i
			break
		}
		if name[i] == '/' || name[i] == '\\' {
			break
		}
	}
	if dot < 0 {
		return false
	}
	ext := name[dot:]
	lower := make([]byte, len(ext))
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		lower[i] = c
	}
	_, ok := disallowedExtensions[string(lower)]
	return ok
}

// equalFoldASCII is a tiny ASCII-only case-insensitive compare. We
// roll it ourselves rather than reaching for strings.EqualFold because
// the inputs are bounded MIME types and the stdlib function does a
// Unicode-aware case fold that has more allocations than this hot
// path needs.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
