package media

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bag for Mount. The wire is intentionally
// small — every field is either user-supplied or has a documented
// fall-back. validate() catches missing fields at boot rather than
// NPE'ing on the first request.
type Deps struct {
	// Store persists media rows. Required.
	Store Store

	// Putter uploads bytes to S3 (or the test in-memory equivalent).
	// Required.
	Putter ObjectPutter

	// Policy resolves the media.* capability checks. Required.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests; production wiring should always
	// pass a service logger.
	Logger *slog.Logger

	// Now is the time source for storage-key minting. nil falls back
	// to time.Now. Tests pin this to a deterministic clock so the
	// storage path is reproducible.
	Now func() time.Time

	// MaxBytes overrides the default per-upload byte cap. Zero means
	// "use MaxUploadBytes". The override exists so an integration test
	// can prove the MaxBytesReader path with a tiny payload without
	// allocating 50 MiB of bytes.
	MaxBytes int64
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/media: Store is required")
	}
	if d.Putter == nil {
		return errors.New("admin/media: Putter is required")
	}
	if d.Policy == nil {
		return errors.New("admin/media: Policy is required")
	}
	return nil
}

type handlers struct {
	store    Store
	putter   ObjectPutter
	policy   policy.Policy
	logger   *slog.Logger
	now      func() time.Time
	maxBytes int64
}

// Mount wires the media routes onto mux under base (typically
// "/api/v1/admin/media"). The five routes share the same authn check;
// each one then re-checks the capability appropriate to its verb.
//
//	POST   {base}        — upload (media.upload)
//	GET    {base}        — list   (media.read)
//	GET    {base}/{id}   — detail (media.read)
//	PATCH  {base}/{id}   — update (media.upload)
//	DELETE {base}/{id}   — delete (media.delete)
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	maxBytes := deps.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxUploadBytes
	}

	h := &handlers{
		store:    deps.Store,
		putter:   deps.Putter,
		policy:   deps.Policy,
		logger:   deps.Logger,
		now:      deps.Now,
		maxBytes: maxBytes,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("POST "+base, h.gate(policy.CapMediaUpload, h.upload))
	mux.Handle("GET "+base, h.gate(policy.CapMediaRead, h.list))
	mux.Handle("GET "+base+"/{id}", h.gate(policy.CapMediaRead, h.get))
	mux.Handle("PATCH "+base+"/{id}", h.gate(policy.CapMediaUpload, h.update))
	mux.Handle("DELETE "+base+"/{id}", h.gate(policy.CapMediaDelete, h.delete))
	return nil
}

// gate wraps a handler with the authn + capability check. Two failure
// modes:
//
//   - 401 unauthenticated when no Principal is on the context (auth
//     middleware hasn't run, or the request is anonymous).
//   - 403 forbidden when the principal lacks the required capability.
//
// The decision Reason is plumbed through to the response so an
// operator who lost a capability sees a meaningful "you no longer have
// media.delete" message, not a bare 403.
func (h *handlers) gate(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
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

// upload handles POST /admin/media. Single multipart file field named
// "file". The flow:
//
//  1. Wrap the body in MaxBytesReader to enforce the size cap before
//     we allocate big buffers. A hostile client can't tie us up
//     streaming gigabytes only to be rejected at the end.
//  2. Pull the first part (the only one we care about) and stream it
//     into a buffer while computing the SHA-256 — the hash is
//     mandatory for the dedupe lookup, and we need the full byte
//     count anyway.
//  3. Sniff the MIME type via http.DetectContentType on the first 512
//     bytes. We deliberately ignore the client-supplied content-type
//     here; clients lie.
//  4. Reject executable-ish MIME types — see disallowedMimePrefixes.
//  5. Look up by hash; if a row exists, return it (HTTP 200) without
//     re-uploading to S3. Otherwise mint a storage key, PUT to S3,
//     INSERT the row, return 201.
func (h *handlers) upload(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes)

	// 32 MiB in-memory part buffer; anything larger spills to a
	// tempfile inside ParseMultipartForm. We pass 0 to opt out of
	// the form's own multipart helpers and use FormFile directly
	// — FormFile internally calls ParseMultipartForm with a sane
	// default, so the explicit call gives us nothing here.
	file, header, err := r.FormFile("file")
	if err != nil {
		// http.ErrMissingFile means the client forgot the "file" field;
		// any other error is almost always the MaxBytesReader fail. We
		// branch on the message because MaxBytesReader returns a
		// wrapped error that doesn't implement a sentinel.
		if errors.Is(err, http.ErrMissingFile) {
			router.WriteError(w, http.StatusBadRequest, "missing_file", "multipart field \"file\" is required")
			return
		}
		// Distinguish a payload-too-large from a generic parse error
		// by sniffing the error message — net/http's MaxBytesReader
		// returns a *MaxBytesError on this path which stringifies
		// containing "http: request body too large".
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large") {
			router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "upload exceeds size limit")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "invalid_multipart", "could not parse upload: "+err.Error())
		return
	}
	defer file.Close()

	// Read the whole payload into memory. The size is already capped
	// at h.maxBytes so the allocation is bounded. We need the full
	// byte buffer for the S3 PUT anyway (the minio client's PutObject
	// takes a length-bounded reader; we feed it the in-memory slice).
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)
	body, err := io.ReadAll(tee)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large") {
			router.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "upload exceeds size limit")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: read body failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not read upload")
		return
	}
	if len(body) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_file", "uploaded file is empty")
		return
	}

	// Sniff MIME. http.DetectContentType wants at most 512 bytes — we
	// pass a slice so a small file doesn't index out-of-range.
	sniffLen := detectionSnifLen
	if len(body) < sniffLen {
		sniffLen = len(body)
	}
	sniffedMime := http.DetectContentType(body[:sniffLen])
	if isDisallowedMime(sniffedMime) || isDisallowedExtension(header.Filename) {
		router.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media", fmt.Sprintf("file type %q is not allowed", sniffedMime))
		return
	}

	hashSum := hasher.Sum(nil)

	// Dedupe probe. If a row with this hash already exists we return
	// it verbatim — the operator gets back the canonical asset rather
	// than a duplicate. The Insert path also dedupes (it returns the
	// existing row instead of erroring), so this probe is a fast
	// path that avoids the S3 PUT for the common case.
	if existing, err := h.store.GetByHash(r.Context(), hashSum); err == nil {
		router.WriteJSON(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, ErrNotFound) {
		h.logger.ErrorContext(r.Context(), "admin/media: dedupe probe failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "dedupe lookup failed")
		return
	}

	// Mint the storage key. Layout: yyyy/mm/<uuid>-<safe-filename>.
	// The date prefix keeps a long-lived bucket browsable; the UUID
	// guarantees uniqueness even if two operators upload "logo.png"
	// in the same month.
	cleanName := sanitizeFilename(header.Filename)
	if cleanName == "" {
		cleanName = "upload"
	}
	now := h.now().UTC()
	key := fmt.Sprintf("%04d/%02d/%s-%s", now.Year(), int(now.Month()), uuid.NewString(), cleanName)

	if err := h.putter.PutObject(r.Context(), key, body, sniffedMime); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: storage put failed", slog.Any("err", err))
		router.WriteError(w, http.StatusBadGateway, "storage_error", "could not upload to storage")
		return
	}

	asset, err := h.store.Insert(r.Context(), AssetCreate{
		Filename:   cleanName,
		MimeType:   sniffedMime,
		ByteSize:   int64(len(body)),
		StorageKey: key,
		SHA256:     hashSum,
		UploaderID: pr.UserID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: insert failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not persist asset")
		return
	}
	asset.PublicURL = h.putter.PublicURL(asset.StorageKey)
	router.WriteJSON(w, http.StatusCreated, asset)
}

// list handles GET /admin/media. Query params:
//
//	type   — optional; one of "image" | "video" | "document". Empty
//	         means "all". Maps to the storage layer's MIME-class
//	         predicate.
//	limit  — optional; page size 1..MaxListLimit, default
//	         DefaultListLimit.
//	cursor — optional; opaque cursor from a previous response.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	q := r.URL.Query()

	filter := ListFilter{
		MimeClass: q.Get("type"),
		Cursor:    q.Get("cursor"),
		Limit:     DefaultListLimit,
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		filter.Limit = n
	}
	switch filter.MimeClass {
	case "", "image", "video", "document":
		// ok
	default:
		router.WriteError(w, http.StatusBadRequest, "invalid_type", "type must be one of image|video|document")
		return
	}

	page, err := h.store.List(r.Context(), filter)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/media: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list media")
		return
	}
	for i := range page.Data {
		page.Data[i].PublicURL = h.putter.PublicURL(page.Data[i].StorageKey)
	}
	router.WriteJSON(w, http.StatusOK, page)
}

// get handles GET /admin/media/{id}.
func (h *handlers) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "asset id is required")
		return
	}
	asset, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "asset not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: get failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not load asset")
		return
	}
	asset.PublicURL = h.putter.PublicURL(asset.StorageKey)
	router.WriteJSON(w, http.StatusOK, asset)
}

// update handles PATCH /admin/media/{id}. Only alt_text and caption
// are mutable — the server rejects any other field at the body parse
// layer (unknown fields are simply ignored, not 400'd, to stay
// forward-compatible with future schema additions).
func (h *handlers) update(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "asset id is required")
		return
	}
	var body AssetUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if body.AltText == nil && body.Caption == nil {
		router.WriteError(w, http.StatusBadRequest, "empty_update", "at least one of alt_text|caption is required")
		return
	}
	// Length guards mirror the migration's CHECK constraints. The
	// schema-level check is the source of truth; this is the friendly
	// failure mode that gives the operator a 400 with a meaningful
	// message instead of an opaque DB constraint error.
	if body.AltText != nil && len(*body.AltText) > 2048 {
		router.WriteError(w, http.StatusBadRequest, "alt_text_too_long", "alt_text exceeds 2048 characters")
		return
	}
	if body.Caption != nil && len(*body.Caption) > 4096 {
		router.WriteError(w, http.StatusBadRequest, "caption_too_long", "caption exceeds 4096 characters")
		return
	}

	asset, err := h.store.UpdateMetadata(r.Context(), id, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "asset not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: update failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not update asset")
		return
	}
	asset.PublicURL = h.putter.PublicURL(asset.StorageKey)
	router.WriteJSON(w, http.StatusOK, asset)
}

// delete handles DELETE /admin/media/{id}. Soft-delete only — the S3
// object stays in place until the nightly purge cron removes it. The
// row stays around with deleted_at set so an "undo" can clear it.
func (h *handlers) delete(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "asset id is required")
		return
	}
	if err := h.store.SoftDelete(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "asset not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin/media: delete failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete asset")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sanitizeFilename trims an upload's filename to the basename and
// strips characters that would be hostile inside an S3 key. The result
// is a forward-slash-free, ASCII-only-ish string at most 200 chars; the
// caller prepends a date + UUID so collisions are impossible.
//
// We don't try to preserve Unicode — operators see the original name
// in the `filename` column, and the storage key is an opaque path the
// UI never renders directly.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 200 {
			break
		}
	}
	out := b.String()
	// Avoid leading dots — they're hidden on POSIX and confuse some
	// S3 browsers.
	out = strings.TrimLeft(out, ".")
	return out
}
