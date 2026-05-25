package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// InitMultipart begins a multipart upload at key. Returns the
// uploadID that the client must echo back with each PresignPart and
// finally CompleteMultipart / AbortMultipart call. See the
// MultipartDriver interface doc on driver.go for the lifecycle.
func (s *S3Driver) InitMultipart(ctx context.Context, key, mime string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	core := minio.Core{Client: s.client}
	uploadID, err := core.NewMultipartUpload(ctx, s.cfg.Bucket, key, minio.PutObjectOptions{
		ContentType: mime,
	})
	if err != nil {
		return "", fmt.Errorf("storage/s3: init multipart: %w", err)
	}
	return uploadID, nil
}

// PresignPart returns a V4-signed PUT URL the client uses to upload
// part `partNumber` (1-indexed) of the multipart upload identified by
// (key, uploadID). The signed URL carries uploadId + partNumber as
// query params so the S3 backend routes the PUT to the correct part
// slot.
//
// partNumber must be 1..10_000 per S3's spec; values outside that
// range are rejected.
func (s *S3Driver) PresignPart(ctx context.Context, key, uploadID string, partNumber int, ttl time.Duration) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	if strings.TrimSpace(uploadID) == "" {
		return "", fmt.Errorf("storage/s3: empty uploadID")
	}
	if partNumber < 1 || partNumber > 10000 {
		return "", fmt.Errorf("storage/s3: partNumber %d out of range (1..10000)", partNumber)
	}
	if ttl <= 0 {
		ttl = s.cfg.DefaultPresignTTL
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	q := url.Values{}
	q.Set("uploadId", uploadID)
	q.Set("partNumber", strconv.Itoa(partNumber))
	u, err := s.client.Presign(ctx, http.MethodPut, s.cfg.Bucket, key, ttl, q)
	if err != nil {
		return "", fmt.Errorf("storage/s3: presign part: %w", err)
	}
	return u.String(), nil
}

// CompleteMultipart finalises the upload, gluing the uploaded parts
// together into a single object. parts must be in ascending partNumber
// order; S3 returns InvalidPartOrder otherwise (the client side of the
// admin UI should already sort but we don't re-sort here because
// callers may want the server to reject a misordered manifest as a
// bug-signal rather than silently fixing it).
func (s *S3Driver) CompleteMultipart(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	if len(parts) == 0 {
		return fmt.Errorf("storage/s3: no parts to complete")
	}
	core := minio.Core{Client: s.client}
	mparts := make([]minio.CompletePart, len(parts))
	for i, p := range parts {
		mparts[i] = minio.CompletePart{
			PartNumber: p.PartNumber,
			ETag:       strings.Trim(p.ETag, `"`),
		}
	}
	_, err := core.CompleteMultipartUpload(ctx, s.cfg.Bucket, key, uploadID, mparts, minio.PutObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("storage/s3: complete multipart: %w", err)
	}
	return nil
}

// AbortMultipart cancels the upload identified by (key, uploadID),
// releasing whatever storage the partial parts consumed. Idempotent —
// aborting an already-aborted or never-existed upload returns nil.
func (s *S3Driver) AbortMultipart(ctx context.Context, key, uploadID string) error {
	core := minio.Core{Client: s.client}
	if err := core.AbortMultipartUpload(ctx, s.cfg.Bucket, key, uploadID); err != nil {
		if isS3NotFound(err) {
			return nil
		}
		// AWS returns NoSuchUpload (404) for absent uploadIDs; some
		// compatibles return NoSuchKey. isS3NotFound already covers
		// the latter; we treat NoSuchUpload as a soft success too so
		// the cron sweep is fully idempotent.
		if r := minio.ToErrorResponse(err); r.Code == "NoSuchUpload" {
			return nil
		}
		return fmt.Errorf("storage/s3: abort multipart: %w", err)
	}
	return nil
}

// ListIncompleteUploads returns multipart uploads initiated before
// olderThan, capped at limit entries. Used by the orphan-purge cron;
// the channel-backed minio API is drained into a slice here so the
// cron can iterate without holding the channel open for the full
// duration of its sweep.
func (s *S3Driver) ListIncompleteUploads(ctx context.Context, olderThan time.Time, limit int) ([]IncompleteUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	out := make([]IncompleteUpload, 0, limit)
	// The minio client exposes ListIncompleteUploads as a channel; we
	// pass an empty prefix to enumerate the whole bucket. Recursive=
	// true is required so multi-level keys (which our scheme always
	// uses — "yyyy/mm/<uuid>-...") are included.
	for info := range s.client.ListIncompleteUploads(ctx, s.cfg.Bucket, "", true) {
		if info.Err != nil {
			return nil, fmt.Errorf("storage/s3: list incomplete: %w", info.Err)
		}
		if !info.Initiated.IsZero() && info.Initiated.After(olderThan) {
			continue
		}
		out = append(out, IncompleteUpload{
			Key:       info.Key,
			UploadID:  info.UploadID,
			Initiated: info.Initiated,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
