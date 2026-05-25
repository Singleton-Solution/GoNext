package media

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/media/storage"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// presignSingleTTL is the TTL applied to single-shot presigned uploads.
// 15 minutes is a comfortable upper bound on the time it takes a
// reasonable browser to PUT a 50-MiB blob over a residential connection
// while staying short enough that a leaked URL does not have a long
// life. The S3 v4 signature spec caps at 7 days; we are nowhere near.
const presignSingleTTL = 15 * time.Minute

// presignPartTTL is the TTL for each per-part URL in a multipart
// upload. Multipart uploads are inherently longer than a single shot,
// but each individual part is a separate HTTP request and a part-level
// TTL is the right granularity (parts upload concurrently, so the
// effective wall-clock window for one part is just the time the
// browser needs to PUT 5 MiB once, not the whole upload duration).
const presignPartTTL = 30 * time.Minute

// multipartPartSize is the byte size of each part in a multipart
// upload (except the last, which may be shorter). 8 MiB is a tradeoff
// — S3 requires parts to be at least 5 MiB except the last; larger
// parts mean fewer HTTP requests but a slower restart on packet loss.
// 8 MiB matches the AWS CLI default for multipart uploads.
const multipartPartSize = 8 * 1024 * 1024

// multipartThreshold is the minimum size at which a client may request
// the multipart flow. Below this, the single-shot presigned PUT is
// cheaper because there is no Init/Complete round-trip and no part
// manifest to track. The threshold equals S3's minimum-non-final
// part size — anything smaller cannot be expressed as a two-part
// upload anyway.
const multipartThreshold = 5 * 1024 * 1024

// PresignRequest is the body shape of POST /admin/media/presign. The
// server uses Filename + MimeType + Size to choose a key, validate
// against the per-role quota (#36 follow-up), and pick between the
// single-shot and multipart flows.
type PresignRequest struct {
	Filename  string `json:"filename"`
	MimeType  string `json:"mime"`
	Size      int64  `json:"size"`
	Multipart bool   `json:"multipart"`
}

// PresignSingleResponse is returned for non-multipart presign calls.
// Header values are echoed verbatim to the client — the browser MUST
// set them on the PUT request or the S3 signature will not verify.
type PresignSingleResponse struct {
	UploadURL string            `json:"upload_url"`
	Headers   map[string]string `json:"headers"`
	Key       string            `json:"key"`
	ExpiresAt time.Time         `json:"expires_at"`

	// MaxBytes is the server-enforced cap on the upload. The browser
	// uses this to fail fast when the user picks a file that exceeds
	// the limit, rather than wasting a multi-minute upload and
	// learning at the end that the server rejected it.
	MaxBytes int64 `json:"max_bytes"`
}

// MultipartPart is one entry in PresignMultipartResponse.PartURLs.
// PartNumber is 1-indexed (S3 contract).
type MultipartPart struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
}

// PresignMultipartResponse is returned for multipart presign calls.
// The client uploads each part to the URL in PartURLs (one PUT per
// part), collects the per-part ETag from the response headers, and
// posts the resulting manifest to finalize-multipart.
type PresignMultipartResponse struct {
	UploadID  string          `json:"upload_id"`
	Key       string          `json:"key"`
	PartURLs  []MultipartPart `json:"part_urls"`
	PartSize  int64           `json:"part_size"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// FinalizeRequest is the body shape of POST /admin/media/finalize.
// The browser POSTs this after a successful single-shot PUT.
type FinalizeRequest struct {
	Key      string `json:"key"`
	Filename string `json:"filename"`
	MimeType string `json:"mime"`
	Size     int64  `json:"size"`

	// SHA256 is the hex-encoded content hash. We require this so the
	// dedupe path on the media row insert still works for direct-
	// uploaded files. The browser computes it client-side via the
	// SubtleCrypto API; if the client cannot compute the hash (very
	// old browser, very large file), this field may be empty and the
	// server will Stat the object and store size-only metadata.
	SHA256 string `json:"sha256"`

	// AltText / Caption mirror the PATCH body; supplied here so the
	// row lands with metadata in one round-trip rather than two.
	AltText string `json:"alt_text"`
	Caption string `json:"caption"`
}

// FinalizeMultipartRequest is the body shape of POST
// /admin/media/finalize-multipart. The browser POSTs this after all
// parts have been uploaded; the server calls CompleteMultipart on
// the storage driver and then registers the media row.
type FinalizeMultipartRequest struct {
	UploadID string                  `json:"upload_id"`
	Key      string                  `json:"key"`
	Filename string                  `json:"filename"`
	MimeType string                  `json:"mime"`
	Size     int64                   `json:"size"`
	SHA256   string                  `json:"sha256"`
	Parts    []FinalizeMultipartPart `json:"parts"`
	AltText  string                  `json:"alt_text"`
	Caption  string                  `json:"caption"`
}

// FinalizeMultipartPart is one row of the multipart completion
// manifest. PartNumber is 1-indexed; ETag is the value the storage
// backend returned in the part PUT response.
type FinalizeMultipartPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// PresignDeps is the additional dependency bag for the presign handler.
// Mount stitches this into the existing Deps; tests can supply a
// fake Driver via the storage.Driver interface without standing up a
// real S3 / local backend.
type PresignDeps struct {
	// Driver is the storage backend. Required.
	Driver storage.Driver

	// Logger receives structured log lines. Optional.
	Logger *slog.Logger

	// Now is the time source for key minting and TTL maths. nil falls
	// back to time.Now.
	Now func() time.Time

	// MaxBytes overrides the per-upload cap. Zero means use
	// MaxUploadBytes.
	MaxBytes int64
}

// presignHandlers groups the four presign-flow handlers. Built once
// per Mount and reused per request.
type presignHandlers struct {
	store    Store
	driver   storage.Driver
	policy   policy.Policy
	logger   *slog.Logger
	now      func() time.Time
	maxBytes int64
}

// MountPresign wires the four presign-flow routes onto mux under base:
//
//	POST {base}/presign              — mint upload URL(s)
//	POST {base}/finalize             — register row after single PUT
//	POST {base}/finalize-multipart   — complete + register multipart
//
// The capability gating is the same as the existing upload route —
// CapMediaUpload — because all three handlers are end-to-end an
// upload from the operator's perspective.
//
// PresignDeps.Driver is required; the function returns an error if
// nil. Mount continues to work with the legacy Putter-backed upload
// handler; the two surfaces coexist so deployments that don't want
// direct-to-storage uploads can simply not mount the presign routes.
func MountPresign(mux *http.ServeMux, base string, deps Deps, pdeps PresignDeps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if pdeps.Driver == nil {
		return errors.New("admin/media: PresignDeps.Driver is required")
	}
	if pdeps.Logger == nil {
		pdeps.Logger = slog.Default()
	}
	if pdeps.Now == nil {
		pdeps.Now = time.Now
	}
	maxBytes := pdeps.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxUploadBytes
	}
	h := &presignHandlers{
		store:    deps.Store,
		driver:   pdeps.Driver,
		policy:   deps.Policy,
		logger:   pdeps.Logger,
		now:      pdeps.Now,
		maxBytes: maxBytes,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("POST "+base+"/presign", h.gate(policy.CapMediaUpload, h.presign))
	mux.Handle("POST "+base+"/finalize", h.gate(policy.CapMediaUpload, h.finalize))
	mux.Handle("POST "+base+"/finalize-multipart", h.gate(policy.CapMediaUpload, h.finalizeMultipart))
	return nil
}

func (h *presignHandlers) gate(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// presign handles POST /admin/media/presign.
func (h *presignHandlers) presign(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	body, err := decodePresignRequest(r.Body)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if err := validatePresignRequest(body, h.maxBytes); err != nil {
		// 415 for mime issues, 413 for size issues, 400 otherwise.
		switch {
		case errors.Is(err, errUnsupportedMime):
			router.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media", err.Error())
		case errors.Is(err, errPayloadTooLarge):
			router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
		default:
			router.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		}
		return
	}

	key := mintStorageKey(h.now(), body.Filename)

	if body.Multipart {
		h.presignMultipart(w, r.Context(), key, body)
		return
	}
	h.presignSingle(w, r.Context(), key, body)
}

func (h *presignHandlers) presignSingle(w http.ResponseWriter, ctx context.Context, key string, body PresignRequest) {
	req, err := h.driver.Presign(ctx, key, storage.PresignPut, presignSingleTTL, body.MimeType)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/media: presign single failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not mint upload URL")
		return
	}
	router.WriteJSON(w, http.StatusOK, PresignSingleResponse{
		UploadURL: req.URL,
		Headers:   req.Headers,
		Key:       key,
		ExpiresAt: req.ExpiresAt,
		MaxBytes:  h.maxBytes,
	})
}

func (h *presignHandlers) presignMultipart(w http.ResponseWriter, ctx context.Context, key string, body PresignRequest) {
	mp, ok := h.driver.(storage.MultipartDriver)
	if !ok {
		// The driver does not support multipart — fall back to the
		// single-shot path so the client can still complete the
		// upload. We return 200 with the single-shot envelope rather
		// than erroring; the surface is "presign me an upload window",
		// not "promise me a multipart upload".
		h.presignSingle(w, ctx, key, body)
		return
	}
	uploadID, err := mp.InitMultipart(ctx, key, body.MimeType)
	if err != nil {
		h.logger.ErrorContext(ctx, "admin/media: init multipart failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not initialise multipart upload")
		return
	}
	numParts := (body.Size + multipartPartSize - 1) / multipartPartSize
	if numParts < 1 {
		numParts = 1
	}
	if numParts > 10000 {
		// S3 cap. We don't degrade gracefully here because chunking
		// at smaller part sizes is the client's responsibility; the
		// server-recommended partSize is what the response advertises.
		router.WriteError(w, http.StatusBadRequest, "too_many_parts", fmt.Sprintf("upload would exceed S3's 10000-part cap; choose a part size larger than %d", multipartPartSize))
		return
	}
	parts := make([]MultipartPart, 0, numParts)
	expires := h.now().Add(presignPartTTL).UTC()
	for i := int64(1); i <= numParts; i++ {
		url, err := mp.PresignPart(ctx, key, uploadID, int(i), presignPartTTL)
		if err != nil {
			// Abort the upload server-side so the operator does not
			// leave a half-initialised upload behind that the cron
			// will eventually have to clean up.
			_ = mp.AbortMultipart(ctx, key, uploadID)
			h.logger.ErrorContext(ctx, "admin/media: presign part failed", slog.Int64("part", i), slog.Any("err", err))
			router.WriteError(w, http.StatusBadGateway, "storage_error", "could not mint part URL")
			return
		}
		parts = append(parts, MultipartPart{PartNumber: int(i), URL: url})
	}
	router.WriteJSON(w, http.StatusOK, PresignMultipartResponse{
		UploadID:  uploadID,
		Key:       key,
		PartURLs:  parts,
		PartSize:  multipartPartSize,
		ExpiresAt: expires,
	})
}

// finalize handles POST /admin/media/finalize.
func (h *presignHandlers) finalize(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	var body FinalizeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if body.Key == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_key", "key is required")
		return
	}
	if body.Filename == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_filename", "filename is required")
		return
	}
	if body.MimeType == "" || isDisallowedMime(body.MimeType) || isDisallowedExtension(body.Filename) {
		router.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media", "mime type not allowed")
		return
	}

	// Verify the object actually exists at the claimed key. Without
	// this check, a client could call finalize with an arbitrary key
	// and have us record an Asset row pointing at empty space.
	info, err := h.driver.Stat(r.Context(), body.Key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			router.WriteError(w, http.StatusBadRequest, "object_missing", "no object found at the supplied key")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: stat after finalize failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not verify uploaded object")
		return
	}
	if body.Size > 0 && info.Size != body.Size {
		router.WriteError(w, http.StatusBadRequest, "size_mismatch", fmt.Sprintf("client-supplied size %d does not match stored size %d", body.Size, info.Size))
		return
	}
	size := info.Size
	if size > h.maxBytes {
		// Belt-and-braces — the presign step already rejected
		// oversized requests, but a client could request multipart
		// and then upload more bytes than they claimed.
		_ = h.driver.Delete(r.Context(), body.Key)
		router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "uploaded object exceeds the size limit")
		return
	}

	hashBytes, hashErr := decodeOptionalSHA256(body.SHA256)
	if hashErr != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_sha256", hashErr.Error())
		return
	}
	if hashBytes == nil {
		// No client-supplied hash — populate with a deterministic
		// stand-in derived from the storage key. The dedupe lookup
		// won't fire (no two files would share the same key), and
		// the column has a UNIQUE constraint that our value satisfies.
		// Documented as a known limitation of the direct-upload path:
		// browsers that lack SubtleCrypto get a row with a placeholder
		// hash and miss out on hash-based dedupe.
		hashBytes = synthHashFromKey(body.Key)
	}

	asset, err := h.store.Insert(r.Context(), AssetCreate{
		Filename:   sanitizeFilenameOrDefault(body.Filename),
		MimeType:   body.MimeType,
		ByteSize:   size,
		StorageKey: body.Key,
		SHA256:     hashBytes,
		UploaderID: pr.UserID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: finalize insert failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not persist asset")
		return
	}
	asset.PublicURL = h.driver.PublicURL(asset.StorageKey)
	router.WriteJSON(w, http.StatusCreated, asset)
}

// finalizeMultipart handles POST /admin/media/finalize-multipart.
func (h *presignHandlers) finalizeMultipart(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	mp, ok := h.driver.(storage.MultipartDriver)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "multipart_unsupported", "active storage driver does not support multipart uploads")
		return
	}
	var body FinalizeMultipartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if body.Key == "" || body.UploadID == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_fields", "key and upload_id are required")
		return
	}
	if len(body.Parts) == 0 {
		router.WriteError(w, http.StatusBadRequest, "missing_parts", "parts manifest is required")
		return
	}
	if body.MimeType == "" || isDisallowedMime(body.MimeType) || isDisallowedExtension(body.Filename) {
		router.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media", "mime type not allowed")
		return
	}

	parts := make([]storage.CompletedPart, len(body.Parts))
	for i, p := range body.Parts {
		if p.PartNumber < 1 || p.PartNumber > 10000 {
			router.WriteError(w, http.StatusBadRequest, "invalid_part", fmt.Sprintf("part %d out of range", p.PartNumber))
			return
		}
		if p.ETag == "" {
			router.WriteError(w, http.StatusBadRequest, "invalid_part", fmt.Sprintf("part %d missing etag", p.PartNumber))
			return
		}
		parts[i] = storage.CompletedPart{PartNumber: p.PartNumber, ETag: p.ETag}
	}

	if err := mp.CompleteMultipart(r.Context(), body.Key, body.UploadID, parts); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			router.WriteError(w, http.StatusBadRequest, "upload_unknown", "no such multipart upload")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: complete multipart failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not complete multipart upload")
		return
	}

	info, err := h.driver.Stat(r.Context(), body.Key)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: stat after multipart failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not verify completed object")
		return
	}
	if info.Size > h.maxBytes {
		_ = h.driver.Delete(r.Context(), body.Key)
		router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "completed object exceeds the size limit")
		return
	}

	hashBytes, hashErr := decodeOptionalSHA256(body.SHA256)
	if hashErr != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_sha256", hashErr.Error())
		return
	}
	if hashBytes == nil {
		hashBytes = synthHashFromKey(body.Key)
	}
	asset, err := h.store.Insert(r.Context(), AssetCreate{
		Filename:   sanitizeFilenameOrDefault(body.Filename),
		MimeType:   body.MimeType,
		ByteSize:   info.Size,
		StorageKey: body.Key,
		SHA256:     hashBytes,
		UploaderID: pr.UserID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: finalize-multipart insert failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not persist asset")
		return
	}
	asset.PublicURL = h.driver.PublicURL(asset.StorageKey)
	router.WriteJSON(w, http.StatusCreated, asset)
}

// Errors used internally by the presign request validator. Kept as
// package-private sentinels so the handler can branch on them without
// duplicating the error text.
var (
	errUnsupportedMime = errors.New("admin/media: unsupported mime type")
	errPayloadTooLarge = errors.New("admin/media: payload too large")
)

func decodePresignRequest(r io.Reader) (PresignRequest, error) {
	var body PresignRequest
	dec := json.NewDecoder(io.LimitReader(r, 16*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return PresignRequest{}, fmt.Errorf("could not parse json: %w", err)
	}
	return body, nil
}

func validatePresignRequest(b PresignRequest, maxBytes int64) error {
	if strings.TrimSpace(b.Filename) == "" {
		return errors.New("filename is required")
	}
	if strings.TrimSpace(b.MimeType) == "" {
		return errors.New("mime is required")
	}
	if b.Size <= 0 {
		return errors.New("size must be positive")
	}
	if b.Size > maxBytes {
		return fmt.Errorf("%w: %d > %d", errPayloadTooLarge, b.Size, maxBytes)
	}
	if isDisallowedMime(b.MimeType) || isDisallowedExtension(b.Filename) {
		return fmt.Errorf("%w: %s", errUnsupportedMime, b.MimeType)
	}
	if b.Multipart && b.Size < multipartThreshold {
		return fmt.Errorf("multipart not allowed for files smaller than %d bytes", multipartThreshold)
	}
	return nil
}

func mintStorageKey(now time.Time, filename string) string {
	clean := sanitizeFilename(filename)
	if clean == "" {
		clean = "upload"
	}
	utc := now.UTC()
	return fmt.Sprintf("%04d/%02d/%s-%s", utc.Year(), int(utc.Month()), uuid.NewString(), clean)
}

func sanitizeFilenameOrDefault(name string) string {
	clean := sanitizeFilename(filepath.Base(name))
	if clean == "" {
		return "upload"
	}
	return clean
}

func decodeOptionalSHA256(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("sha256 must be hex-encoded: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("sha256 must be 32 bytes (got %d)", len(b))
	}
	return b, nil
}

// synthHashFromKey returns a deterministic 32-byte slice derived from
// the storage key. Used as a placeholder when the client cannot
// compute a real SHA-256 hash on its end (very old browsers; very
// large files where the SubtleCrypto round-trip is impractical). The
// value satisfies the schema's 32-byte UNIQUE constraint but does
// NOT participate in content-hash dedupe — two uploads of the same
// bytes under different storage keys will hash differently here.
//
// We synthesise rather than reject so the direct-upload flow works
// in degraded environments; an admin warning in the UI nudges
// operators toward modern browsers for the dedupe benefit.
func synthHashFromKey(key string) []byte {
	out := make([]byte, 32)
	// Naive but stable: repeat the key bytes into the output buffer.
	// We don't need cryptographic strength here — the value is just
	// a UNIQUE primary-key-like field.
	for i := 0; i < 32; i++ {
		out[i] = key[i%len(key)]
	}
	return out
}
