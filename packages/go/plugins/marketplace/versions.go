package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Versions is the store for the plugin_versions table.
//
// The signature of Publish is intentionally byte-oriented: callers hand
// the wasm bytes in, the store computes the SHA-256 digest and writes
// the version row. The bytes themselves are NOT written to the database
// — production callers are expected to push them to object storage
// (Minio/S3) keyed by that digest before or after Publish. The order
// doesn't matter because the digest is the content address.
type Versions struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for the PublishedAt default
	// Publish assigns when the caller leaves it zero.
	NowFunc nowFunc
}

func NewVersions(db PgxQuerier) *Versions {
	if db == nil {
		panic("marketplace.NewVersions: db is required")
	}
	return &Versions{db: db}
}

const versionsSelectColumns = `
    id, listing_id, version, wasm_sha256, manifest,
    COALESCE(signature_hex, ''),
    published_at,
    COALESCE(deprecated_at, 'epoch'::timestamptz)
`

// Publish inserts a new version row.
//
// The caller supplies the wasm bytes; Publish computes the SHA-256
// digest and stores the digest (not the bytes) on the new row. A
// duplicate (listing_id, version) tuple returns ErrAlreadyExists.
//
// The signature is optional — pass an empty string when the artefact
// is unsigned. The manifest is required only as a JSON-shaped blob;
// schema validation lives in plugins/manifest.
func (v *Versions) Publish(ctx context.Context, in Version, wasmBytes []byte) (Version, error) {
	if in.ListingID == uuid.Nil {
		return Version{}, fmt.Errorf("%w: listing_id is required", ErrInvalidInput)
	}
	if in.Version == "" {
		return Version{}, fmt.Errorf("%w: version is required", ErrInvalidInput)
	}
	if len(wasmBytes) == 0 {
		return Version{}, fmt.Errorf("%w: wasm bytes are required", ErrInvalidInput)
	}

	digest := sha256.Sum256(wasmBytes)
	manifest := in.Manifest
	if len(manifest) == 0 {
		manifest = json.RawMessage("{}")
	}
	// Empty signature is sent as NULL — keeps the column semantics
	// honest (NULL = unsigned, "" would be a meaningless explicit
	// empty string).
	var signature any
	if in.SignatureHex != "" {
		signature = in.SignatureHex
	}
	var publishedAt any
	if !in.PublishedAt.IsZero() {
		publishedAt = in.PublishedAt.UTC()
	} else if v.NowFunc != nil {
		publishedAt = v.NowFunc().UTC()
	}
	// publishedAt may be nil here; the column default (now()) takes
	// over on the SQL side.

	const sql = `
		INSERT INTO plugin_versions
			(listing_id, version, wasm_sha256, manifest, signature_hex, published_at)
		VALUES
			($1, $2, $3, $4::jsonb, $5, COALESCE($6, now()))
		RETURNING ` + versionsSelectColumns

	row := v.db.QueryRow(ctx, sql,
		in.ListingID, in.Version, digest[:], string(manifest), signature, publishedAt,
	)
	out, err := scanVersion(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Version{}, fmt.Errorf("%w: listing %s version %q", ErrAlreadyExists, in.ListingID, in.Version)
		}
		return Version{}, fmt.Errorf("marketplace.Versions.Publish: %w", err)
	}
	return out, nil
}

// Get returns the version identified by id, or ErrNotFound.
func (v *Versions) Get(ctx context.Context, id uuid.UUID) (Version, error) {
	row := v.db.QueryRow(ctx,
		`SELECT `+versionsSelectColumns+` FROM plugin_versions WHERE id = $1`,
		id,
	)
	out, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, fmt.Errorf("%w: version id %s", ErrNotFound, id)
		}
		return Version{}, fmt.Errorf("marketplace.Versions.Get: %w", err)
	}
	return out, nil
}

// ListByListing returns every version of a listing, ordered by
// published_at DESC (newest first). Empty result is not an error.
func (v *Versions) ListByListing(ctx context.Context, listingID uuid.UUID) ([]Version, error) {
	if listingID == uuid.Nil {
		return nil, fmt.Errorf("%w: listing_id is required", ErrInvalidInput)
	}
	rows, err := v.db.Query(ctx,
		`SELECT `+versionsSelectColumns+`
		   FROM plugin_versions
		  WHERE listing_id = $1
		  ORDER BY published_at DESC, id ASC`,
		listingID,
	)
	if err != nil {
		return nil, fmt.Errorf("marketplace.Versions.ListByListing: %w", err)
	}
	defer rows.Close()

	out := []Version{}
	for rows.Next() {
		ver, scanErr := scanVersion(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("marketplace.Versions.ListByListing: scan: %w", scanErr)
		}
		out = append(out, ver)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace.Versions.ListByListing: rows: %w", err)
	}
	return out, nil
}

// Deprecate marks the version as deprecated. Idempotent — calling on
// an already-deprecated version updates the timestamp to "now". The
// row remains installable; the marketplace UI is the surface that
// renders the deprecation banner.
//
// Returns ErrNotFound when no row exists for id.
func (v *Versions) Deprecate(ctx context.Context, id uuid.UUID) (Version, error) {
	now := resolveNow(v.NowFunc)
	const sql = `
		UPDATE plugin_versions
		   SET deprecated_at = $2
		 WHERE id = $1
		 RETURNING ` + versionsSelectColumns
	row := v.db.QueryRow(ctx, sql, id, now)
	out, err := scanVersion(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Version{}, fmt.Errorf("%w: version id %s", ErrNotFound, id)
		}
		return Version{}, fmt.Errorf("marketplace.Versions.Deprecate: %w", err)
	}
	return out, nil
}

// scanVersion reads one plugin_versions row.
func scanVersion(s pgxScannable) (Version, error) {
	var (
		v             Version
		manifestRaw   []byte
		signature     string
		publishedAt   time.Time
		deprecatedRaw time.Time
	)
	if err := s.Scan(
		&v.ID, &v.ListingID, &v.Version, &v.WasmSHA256, &manifestRaw,
		&signature, &publishedAt, &deprecatedRaw,
	); err != nil {
		return Version{}, err
	}
	v.Manifest = manifestRaw
	v.SignatureHex = signature
	v.PublishedAt = publishedAt
	// epoch sentinel = the column was NULL.
	if !isEpoch(deprecatedRaw) {
		v.DeprecatedAt = deprecatedRaw
	}
	return v, nil
}

// isEpoch reports whether t is the SQL epoch — our "this column was
// NULL" sentinel returned by COALESCE in the SELECT list.
func isEpoch(t time.Time) bool {
	return t.IsZero() || t.Unix() == 0
}
