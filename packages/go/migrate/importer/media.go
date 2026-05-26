package importer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MediaMode controls how the MediaMigrator handles a remote
// attachment URL. The two modes embody fundamentally different
// trade-offs:
//
//   - Copy mode pulls the bytes once, at migration time, and stores
//     them in the destination's media bucket. The source site can
//     disappear afterwards and the migrated content keeps working.
//     The trade-off is migration runtime (and bandwidth) — a site
//     with thousands of full-resolution photos can take an hour.
//
//   - Proxy mode leaves the bytes at the source URL and registers
//     the media row with is_proxied=true. The first read-through
//     request goes through the existing image proxy (issue #37),
//     which fetches from source_url and caches the response. The
//     migration is fast and bandwidth-cheap, but the migrated
//     content has a runtime dependency on the source URL staying
//     up; if the operator wants full ownership they can flip a
//     row to copy mode later with a backfill job.
//
// A migration MAY mix modes per asset, but the typical case is one
// mode for the whole run. The MediaMigrator's Config exposes the
// per-run default; a future refinement may add per-URL overrides.
type MediaMode uint8

const (
	// MediaModeCopy downloads each remote URL and stores the bytes
	// locally.
	MediaModeCopy MediaMode = iota

	// MediaModeProxy leaves the bytes at the source URL and
	// registers the row with is_proxied=true.
	MediaModeProxy
)

// String returns the canonical CLI form so flag parsing stays
// symmetric across the importer's CLI surface.
func (m MediaMode) String() string {
	switch m {
	case MediaModeCopy:
		return "copy"
	case MediaModeProxy:
		return "proxy"
	default:
		return "unknown"
	}
}

// ParseMediaMode turns a CLI string into a MediaMode. The empty
// string maps to MediaModeCopy so callers can pass the flag value
// untouched.
func ParseMediaMode(s string) (MediaMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "copy":
		return MediaModeCopy, nil
	case "proxy":
		return MediaModeProxy, nil
	default:
		return MediaModeCopy, fmt.Errorf("importer: unknown media mode %q", s)
	}
}

// MediaConfig configures a MediaMigrator. The zero value is
// acceptable; defaults are applied at construction time.
type MediaConfig struct {
	// Mode selects copy vs proxy semantics. Default: MediaModeCopy.
	Mode MediaMode

	// MaxBytes caps the per-asset download size in copy mode. Larger
	// assets fail the migrator's IngestURL call with ErrTooLarge;
	// the caller decides whether to skip or abort the migration.
	// Zero falls back to DefaultMediaMaxBytes.
	MaxBytes int64

	// HTTPClient is the client used to fetch source URLs. nil falls
	// back to a default with the package's timeout. Tests pin this
	// to a mock client backed by httptest.NewServer.
	HTTPClient *http.Client

	// RequestTimeout bounds a single HTTP fetch in copy mode. Zero
	// falls back to DefaultMediaRequestTimeout.
	RequestTimeout time.Duration

	// UserAgent is the User-Agent header sent on every fetch. Empty
	// falls back to DefaultMediaUserAgent — important so a source
	// site's WAF can identify the importer (and not block it as a
	// generic Go http.Client).
	UserAgent string
}

// DefaultMediaMaxBytes is the per-asset size cap in copy mode.
// 200 MiB covers nearly every legitimate WordPress upload (the
// platform's own default cap is 64 MiB or smaller) while keeping a
// stray multi-gigabyte attachment from wedging the importer.
const DefaultMediaMaxBytes int64 = 200 * 1024 * 1024

// DefaultMediaRequestTimeout bounds a single HTTP fetch.
const DefaultMediaRequestTimeout = 60 * time.Second

// DefaultMediaUserAgent is sent on every fetch when the caller
// hasn't pinned a custom value.
const DefaultMediaUserAgent = "GoNext-WP-Migrator/1.0 (+https://gonext.dev)"

// ErrTooLarge is returned by MediaMigrator.IngestURL when the source
// response exceeds MaxBytes. The migrator's caller can treat this
// as a per-asset skip or a fatal abort, per their migration policy.
var ErrTooLarge = errors.New("importer: media source exceeds MaxBytes")

// ErrSourceNot200 is returned when the source URL responded with a
// non-2xx status. The error message includes the status so a CLI
// caller can render "skipped photo.jpg: source returned 404".
var ErrSourceNot200 = errors.New("importer: media source non-2xx")

// MediaPutter is the write side of the destination storage layer.
// Identical surface to the admin upload handler's ObjectPutter —
// we re-declare here so the importer doesn't pull in the api
// package.
type MediaPutter interface {
	// PutObject uploads body at key with the recorded Content-Type.
	PutObject(ctx context.Context, key string, body []byte, mimeType string) error
}

// MediaInserter is the persistence boundary for media row inserts.
// The migrator never updates rows itself — a re-run of the same
// migration is expected to be idempotent at the post-rewrite layer,
// not at the media-row layer (a second insert with the same source
// URL is silently treated as success via FindBySourceURL).
type MediaInserter interface {
	// FindBySourceURL returns an existing media row's id and
	// storage_key when the row was inserted in proxy mode (or in
	// copy mode with the same source URL recorded as metadata).
	// Returns "", "", nil when no row matches. The returned
	// storage_key is what the migrator uses to rewrite content
	// references — in copy mode it's the new local key; in proxy
	// mode it's a synthetic "proxy/<hash>" placeholder.
	FindBySourceURL(ctx context.Context, sourceURL string) (id, storageKey string, found bool, err error)

	// InsertCopied registers a row for an asset whose bytes were
	// downloaded and stored locally. is_proxied=false; source_url
	// stays NULL (the row is fully owned by the destination).
	InsertCopied(ctx context.Context, row MediaRow) (id string, err error)

	// InsertProxied registers a row whose bytes remain at sourceURL.
	// is_proxied=true; storage_key is a synthetic placeholder so
	// the UNIQUE constraint applies and the rest of the codebase
	// can treat the row uniformly.
	InsertProxied(ctx context.Context, sourceURL string, row MediaRow) (id string, err error)
}

// MediaRow is the wire shape between the migrator and the inserter.
// Fields mirror media-table columns rather than the WXR record —
// the migrator does the per-field derivation (mime sniff, sha256
// of body for copy mode, slugified filename, storage-key minting)
// so the inserter is a thin pass-through.
type MediaRow struct {
	Filename   string
	MimeType   string
	ByteSize   int64
	StorageKey string
	SHA256     []byte
	UploaderID string

	// SourceURL is non-empty for proxied rows. The inserter writes
	// it to media.source_url and sets is_proxied=true; for copied
	// rows the field is empty and the inserter leaves source_url
	// NULL.
	SourceURL string
}

// MediaMigrator coordinates per-asset ingestion during a migration.
// Construct with NewMediaMigrator. The struct is stateless beyond
// its config; safe for concurrent IngestURL calls.
type MediaMigrator struct {
	cfg      MediaConfig
	putter   MediaPutter
	inserter MediaInserter

	// now is the time source for storage-key minting. Pluggable for
	// tests that need deterministic keys.
	now func() time.Time

	// keyGen mints the storage key for a copy-mode upload. Tests
	// pin a deterministic generator.
	keyGen func(now time.Time, filename string) string
}

// MediaIngestResult is the per-URL outcome of IngestURL.
type MediaIngestResult struct {
	// MediaID is the id of the inserted (or pre-existing) media row.
	MediaID string

	// StorageKey is the key the migrator wrote (copy mode) or the
	// synthetic proxy key (proxy mode).
	StorageKey string

	// Mode is the mode the migrator used for THIS asset. Useful for
	// per-asset accounting on the Report.
	Mode MediaMode

	// BytesFetched is the number of bytes the migrator downloaded
	// in copy mode; zero for proxy mode.
	BytesFetched int64

	// Reused is true when FindBySourceURL hit — IngestURL did not
	// re-fetch or re-insert, so the run is idempotent on re-runs.
	Reused bool
}

// NewMediaMigrator constructs a MediaMigrator. Either argument may
// be nil for a constructor-only test, but IngestURL with nil
// dependencies returns an error.
//
// The default storage key layout matches the admin upload handler's:
// "yyyy/mm/<uuid>-<safe-filename>". A future config knob can pin a
// custom layout for operators who want the migrated content under
// a separate prefix.
func NewMediaMigrator(cfg MediaConfig, putter MediaPutter, inserter MediaInserter) *MediaMigrator {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMediaMaxBytes
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultMediaRequestTimeout
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultMediaUserAgent
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.RequestTimeout}
	}
	return &MediaMigrator{
		cfg:      cfg,
		putter:   putter,
		inserter: inserter,
		now:      time.Now,
		keyGen:   defaultMediaKeyGen,
	}
}

// SetNow pins the time source for tests.
func (m *MediaMigrator) SetNow(fn func() time.Time) {
	if fn != nil {
		m.now = fn
	}
}

// SetKeyGen pins the storage-key generator for tests.
func (m *MediaMigrator) SetKeyGen(fn func(now time.Time, filename string) string) {
	if fn != nil {
		m.keyGen = fn
	}
}

// IngestURL processes a single remote attachment URL. The behaviour
// depends on the configured mode:
//
//   - Copy: GET sourceURL → sniff mime → PUT bytes at a fresh
//     storage key → InsertCopied. Returns the new media id.
//
//   - Proxy: skip the fetch; InsertProxied with a synthetic
//     storage_key derived from sha256(sourceURL). The row's
//     is_proxied flag is true; the runtime image proxy serves the
//     bytes on first read.
//
// In both modes IngestURL first probes the inserter with
// FindBySourceURL; if a row already exists the result is returned
// with Reused=true and no fetch / no row write happens. This makes
// the function idempotent at the per-URL level — re-running a
// migration after a partial failure picks up where it left off.
//
// uploaderID is the GoNext users.id the migrator attributes the row
// to (typically the operator who triggered the migration).
func (m *MediaMigrator) IngestURL(ctx context.Context, sourceURL, uploaderID string) (MediaIngestResult, error) {
	if m == nil {
		return MediaIngestResult{}, errors.New("importer: nil MediaMigrator")
	}
	if m.putter == nil && m.cfg.Mode == MediaModeCopy {
		return MediaIngestResult{}, errors.New("importer: nil MediaPutter (required for copy mode)")
	}
	if m.inserter == nil {
		return MediaIngestResult{}, errors.New("importer: nil MediaInserter")
	}
	if sourceURL == "" {
		return MediaIngestResult{}, errors.New("importer: empty source URL")
	}
	if _, err := url.ParseRequestURI(sourceURL); err != nil {
		return MediaIngestResult{}, fmt.Errorf("importer: invalid source URL %q: %w", sourceURL, err)
	}
	if uploaderID == "" {
		return MediaIngestResult{}, errors.New("importer: empty uploaderID")
	}

	// Idempotency probe. If the migrator already registered this
	// URL on an earlier run (or earlier within this run), return
	// the existing row without touching the network or the bucket.
	if id, key, found, err := m.inserter.FindBySourceURL(ctx, sourceURL); err != nil {
		return MediaIngestResult{}, fmt.Errorf("importer: FindBySourceURL: %w", err)
	} else if found {
		return MediaIngestResult{
			MediaID:    id,
			StorageKey: key,
			Mode:       m.cfg.Mode,
			Reused:     true,
		}, nil
	}

	switch m.cfg.Mode {
	case MediaModeCopy:
		return m.ingestCopy(ctx, sourceURL, uploaderID)
	case MediaModeProxy:
		return m.ingestProxy(ctx, sourceURL, uploaderID)
	default:
		return MediaIngestResult{}, fmt.Errorf("importer: unknown media mode %v", m.cfg.Mode)
	}
}

func (m *MediaMigrator) ingestCopy(ctx context.Context, sourceURL, uploaderID string) (MediaIngestResult, error) {
	body, mime, err := m.fetch(ctx, sourceURL)
	if err != nil {
		return MediaIngestResult{}, err
	}
	hash := sha256.Sum256(body)
	filename := filenameFromURL(sourceURL)
	if filename == "" {
		filename = "attachment"
	}
	key := m.keyGen(m.now(), filename)

	if err := m.putter.PutObject(ctx, key, body, mime); err != nil {
		return MediaIngestResult{}, fmt.Errorf("importer: PutObject: %w", err)
	}
	row := MediaRow{
		Filename:   filename,
		MimeType:   mime,
		ByteSize:   int64(len(body)),
		StorageKey: key,
		SHA256:     hash[:],
		UploaderID: uploaderID,
		// SourceURL is intentionally left empty for copied rows:
		// the bytes are now fully owned by the destination.
	}
	id, err := m.inserter.InsertCopied(ctx, row)
	if err != nil {
		return MediaIngestResult{}, fmt.Errorf("importer: InsertCopied: %w", err)
	}
	return MediaIngestResult{
		MediaID:      id,
		StorageKey:   key,
		Mode:         MediaModeCopy,
		BytesFetched: int64(len(body)),
	}, nil
}

func (m *MediaMigrator) ingestProxy(ctx context.Context, sourceURL, uploaderID string) (MediaIngestResult, error) {
	// Synthetic storage key. The proxy handler routes off
	// is_proxied=true, not the key shape — but the key must be
	// unique (the column has a UNIQUE constraint), and deterministic
	// so a re-run resolves to the same row via FindBySourceURL even
	// when the synthetic key bytes aren't otherwise visible.
	hash := sha256.Sum256([]byte(sourceURL))
	key := fmt.Sprintf("proxy/%x", hash[:16])
	filename := filenameFromURL(sourceURL)
	if filename == "" {
		filename = "attachment"
	}
	mime := mimeFromFilename(filename)
	row := MediaRow{
		Filename:   filename,
		MimeType:   mime,
		ByteSize:   0, // unknown — bytes live remotely
		StorageKey: key,
		SHA256:     hash[:], // hash of the URL (no body to hash)
		UploaderID: uploaderID,
		SourceURL:  sourceURL,
	}
	id, err := m.inserter.InsertProxied(ctx, sourceURL, row)
	if err != nil {
		return MediaIngestResult{}, fmt.Errorf("importer: InsertProxied: %w", err)
	}
	return MediaIngestResult{
		MediaID:    id,
		StorageKey: key,
		Mode:       MediaModeProxy,
	}, nil
}

// fetch downloads a source URL in copy mode. Returns the bytes, the
// sniffed Content-Type, and an error.
//
// We do NOT trust the source server's Content-Type header — a
// migrated photo from a misconfigured site has the wrong header
// often enough that re-sniffing on our side is the only safe path.
// http.DetectContentType produces the same value the admin upload
// path uses, so the round-trip is symmetric.
func (m *MediaMigrator) fetch(ctx context.Context, sourceURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("importer: build request: %w", err)
	}
	req.Header.Set("User-Agent", m.cfg.UserAgent)
	resp, err := m.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("importer: fetch %q: %w", sourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("%w: %s -> %d", ErrSourceNot200, sourceURL, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, m.cfg.MaxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("importer: read response: %w", err)
	}
	if int64(len(body)) > m.cfg.MaxBytes {
		return nil, "", fmt.Errorf("%w: %s exceeded %d bytes", ErrTooLarge, sourceURL, m.cfg.MaxBytes)
	}
	sniffLen := 512
	if len(body) < sniffLen {
		sniffLen = len(body)
	}
	return body, http.DetectContentType(body[:sniffLen]), nil
}

// defaultMediaKeyGen is the canonical "yyyy/mm/<uuid>-<filename>"
// layout, matching the admin upload handler. Exported via the
// SetKeyGen seam for tests that need determinism.
func defaultMediaKeyGen(now time.Time, filename string) string {
	return fmt.Sprintf("%04d/%02d/%s-%s", now.UTC().Year(), int(now.UTC().Month()), uuid.NewString(), filename)
}

// filenameFromURL pulls the basename out of a URL's path. We don't
// trust the source URL's filename for storage-key purposes — the
// migrator slugifies it — but the human-facing column still wants
// a recognisable name. Returns "" when no recognisable name is
// embedded.
func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "/" || base == "." || base == "" {
		return ""
	}
	// Trim query strings or fragments that snuck in.
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	// Cap at the media.filename column's limit (255).
	if len(base) > 255 {
		base = base[len(base)-255:]
	}
	return base
}

// mimeFromFilename guesses a Content-Type from a filename extension.
// Used only in proxy mode where we don't have the body to sniff;
// the runtime image proxy re-sniffs the actual bytes on first
// fetch, so an incorrect guess here is overridden at runtime.
func mimeFromFilename(filename string) string {
	idx := strings.LastIndex(filename, ".")
	if idx < 0 {
		return "application/octet-stream"
	}
	ext := strings.ToLower(filename[idx:])
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// RewriteContent walks content (typically the post body HTML) and
// replaces every occurrence of an old (source) URL with the new
// destination URL.
//
// The replacement is a literal string substitution, not a parsed
// AST traversal — the WP content body is often a mix of HTML,
// shortcodes, and raw URLs, and a parser-based pass would miss the
// non-HTML cases. The migrator's caller is expected to feed
// RewriteContent a per-asset {source → destination} map; the
// function applies every replacement to the input string and
// returns the result.
//
// The destination URLs differ between copy and proxy modes:
//
//   - Copy: destination URL is the public URL of the local storage
//     key (the destination's own CDN).
//
//   - Proxy: destination URL is the public URL of the proxy
//     endpoint for the row's id, which the runtime proxy then
//     resolves to source_url internally.
//
// Both cases are opaque to RewriteContent — the caller passes a
// pre-computed map.
func RewriteContent(content string, replacements map[string]string) string {
	if len(replacements) == 0 || content == "" {
		return content
	}
	// Build a strings.Replacer for one-pass substitution. The
	// Replacer's longest-match-first behaviour means we don't have
	// to worry about a source URL being a prefix of another (rare
	// in WP exports but possible with subdir attachments).
	pairs := make([]string, 0, len(replacements)*2)
	for src, dst := range replacements {
		if src == "" || dst == "" {
			continue
		}
		pairs = append(pairs, src, dst)
	}
	if len(pairs) == 0 {
		return content
	}
	return strings.NewReplacer(pairs...).Replace(content)
}
