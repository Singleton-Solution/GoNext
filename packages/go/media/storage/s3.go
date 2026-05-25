package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Driver is the S3-compatible storage backend, layered on top of
// minio-go v7. Works against AWS S3, MinIO, Cloudflare R2, Wasabi,
// Backblaze B2, and any other store that speaks the S3 v4 protocol;
// the choice is driven entirely by S3Config.Endpoint + UseSSL +
// PathStyle.
//
// Why minio-go instead of aws-sdk-go-v2? Two reasons:
//
//   - The codebase already imports minio-go (the in-process Coalescer
//     and the test fixtures use it). Pulling in a second SDK would
//     double the transitive surface for the same protocol.
//   - minio-go's PresignedPutObject / PresignedGetObject return URLs
//     that work against every S3-compatible we test; the AWS SDK's
//     presigner emits AWS-shaped URLs that need post-processing for
//     non-AWS backends.
//
// The driver also satisfies MultipartDriver — see s3_multipart.go for
// the upload-id / part / abort / complete flow used by the admin
// multipart-presign endpoint.
type S3Driver struct {
	client *minio.Client
	cfg    S3Config
}

// S3Config holds the parameters S3Driver needs to talk to a bucket.
// Mirrors packages/go/config.StorageConfig field-for-field so the
// wiring layer at apps/api/cmd/server can copy values straight across.
type S3Config struct {
	// Endpoint is the host:port (or just host) of the S3 API. Empty
	// means "use AWS S3" (the minio client picks the regional endpoint
	// from Region). Required for MinIO/R2/Wasabi.
	Endpoint string

	// Region is the AWS-region label. Required for the v4 signature
	// — even MinIO requires a non-empty region in the signing string.
	Region string

	// Bucket is the bucket name. Required.
	Bucket string

	// AccessKey + SecretKey are static credentials. For AWS deployments
	// running with IAM instance profiles, leave empty — the
	// EnvAWSCredentials credential chain will pick up the role.
	AccessKey string
	SecretKey string

	// UseSSL toggles https:// vs http://. Production = true.
	// Development against a local MinIO without TLS = false.
	UseSSL bool

	// PathStyle forces "bucket.endpoint/key" → "endpoint/bucket/key".
	// Required for MinIO and most non-AWS S3 implementations. The
	// minio client treats true as the default for non-AWS endpoints,
	// but pinning it explicitly via the config keeps the wire shape
	// stable across upgrades.
	PathStyle bool

	// PublicBaseURL is the URL prefix to prepend to keys in
	// PublicURL. When empty, the driver builds the URL from
	// Endpoint + Bucket + key using the configured addressing style.
	// Override for deployments that serve media through a CDN
	// (CloudFront, R2 custom domain, etc.).
	PublicBaseURL string

	// DefaultPresignTTL is the default TTL applied when Presign is
	// called with ttl == 0. Defaults to 15 minutes. Capped at 7
	// days by S3's v4 signature spec.
	DefaultPresignTTL time.Duration
}

// NewS3Driver constructs an S3Driver from cfg. Performs no network IO
// at construction — the underlying minio.Client lazily opens the first
// HTTP connection when a method is called. Tests that want to fail
// fast on a misconfigured bucket should follow New with a Stat on a
// known key.
func NewS3Driver(cfg S3Config) (*S3Driver, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("storage/s3: Bucket is required")
	}
	if cfg.DefaultPresignTTL <= 0 {
		cfg.DefaultPresignTTL = 15 * time.Minute
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		// Default to AWS's regional endpoint for the supplied region.
		// minio.New will form "s3.<region>.amazonaws.com" internally
		// when Endpoint is one of the known AWS hosts, but we set it
		// explicitly so the connection target is reproducible from
		// the config alone.
		endpoint = "s3." + cfg.Region + ".amazonaws.com"
	}
	opts := &minio.Options{
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	if cfg.AccessKey != "" {
		opts.Creds = credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	} else {
		// Fall back to env / IAM / instance-profile chain.
		opts.Creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.IAM{},
		})
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	cli, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("storage/s3: minio client: %w", err)
	}
	return &S3Driver{client: cli, cfg: cfg}, nil
}

// Put uploads r to <bucket>/<key>. Stream length is unknown; minio
// chunks via the multipart machinery once the upload exceeds 16 MiB
// per the client's default.
func (s *S3Driver) Put(ctx context.Context, key string, r io.Reader, mime string) (int64, error) {
	if strings.TrimSpace(key) == "" {
		return 0, fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	info, err := s.client.PutObject(ctx, s.cfg.Bucket, key, r, -1, minio.PutObjectOptions{
		ContentType: mime,
	})
	if err != nil {
		return 0, fmt.Errorf("storage/s3: put: %w", err)
	}
	return info.Size, nil
}

// Get opens the object at key. The caller must close the returned
// ReadCloser to release the underlying HTTP connection.
func (s *S3Driver) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage/s3: get: %w", err)
	}
	// minio.GetObject is lazy — the request fires on first Read /
	// Stat. We probe with Stat here so a missing object surfaces as
	// ErrNotFound before the caller starts streaming.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage/s3: stat-before-get: %w", err)
	}
	return obj, nil
}

// Delete removes key. Missing is not an error per the Driver contract.
func (s *S3Driver) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("storage/s3: remove: %w", err)
	}
	return nil
}

// Stat returns metadata for key.
func (s *S3Driver) Stat(ctx context.Context, key string) (*Object, error) {
	info, err := s.client.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage/s3: stat: %w", err)
	}
	return &Object{
		Key:          key,
		Size:         info.Size,
		MimeType:     info.ContentType,
		LastModified: info.LastModified,
	}, nil
}

// Presign returns a V4-signed URL for op on key.
func (s *S3Driver) Presign(ctx context.Context, key string, op PresignOp, ttl time.Duration, mime string) (PresignedRequest, error) {
	if ttl <= 0 {
		ttl = s.cfg.DefaultPresignTTL
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	switch op {
	case PresignPut:
		u, err := s.client.PresignedPutObject(ctx, s.cfg.Bucket, key, ttl)
		if err != nil {
			return PresignedRequest{}, fmt.Errorf("storage/s3: presign put: %w", err)
		}
		headers := map[string]string{}
		if mime != "" {
			// Note: S3 does NOT bake Content-Type into a vanilla
			// PresignedPutObject signature. Browsers MAY send any
			// Content-Type and S3 will accept it; we surface the
			// caller's intended mime as a header hint so the client
			// sends what we recorded in the media row.
			headers["Content-Type"] = mime
		}
		return PresignedRequest{
			URL:       u.String(),
			Headers:   headers,
			ExpiresAt: time.Now().Add(ttl).UTC(),
		}, nil
	case PresignGet:
		q := url.Values{}
		u, err := s.client.PresignedGetObject(ctx, s.cfg.Bucket, key, ttl, q)
		if err != nil {
			return PresignedRequest{}, fmt.Errorf("storage/s3: presign get: %w", err)
		}
		return PresignedRequest{
			URL:       u.String(),
			Headers:   map[string]string{},
			ExpiresAt: time.Now().Add(ttl).UTC(),
		}, nil
	default:
		return PresignedRequest{}, fmt.Errorf("storage/s3: unsupported op %q", op)
	}
}

// PublicURL returns the unauthenticated, virtual-host-style URL for
// key. Uses S3Config.PublicBaseURL when set; otherwise builds from
// Endpoint + Bucket + key.
func (s *S3Driver) PublicURL(key string) string {
	if s.cfg.PublicBaseURL != "" {
		return strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/" + key
	}
	scheme := "https"
	if !s.cfg.UseSSL {
		scheme = "http"
	}
	endpoint := s.cfg.Endpoint
	if endpoint == "" {
		endpoint = "s3." + s.cfg.Region + ".amazonaws.com"
	}
	if s.cfg.PathStyle {
		return fmt.Sprintf("%s://%s/%s/%s", scheme, endpoint, s.cfg.Bucket, key)
	}
	return fmt.Sprintf("%s://%s.%s/%s", scheme, s.cfg.Bucket, endpoint, key)
}

// Bucket reports the configured bucket. Useful for tests and for the
// admin "where do my uploads live" diagnostic.
func (s *S3Driver) Bucket() string { return s.cfg.Bucket }

// Client returns the underlying minio client for callers that need
// to perform operations outside the Driver interface (e.g. bucket
// creation in tests). Production code should NOT use this — every
// storage operation should go through the Driver methods so the
// abstraction stays portable to GCS.
func (s *S3Driver) Client() *minio.Client { return s.client }

// isS3NotFound discriminates "key does not exist" from any other
// backend error. minio-go returns an ErrorResponse with Code
// "NoSuchKey" (AWS) or "NoSuchObject" (some compatibles); the
// HTTP status is 404 either way, which gives us a single check.
func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.StatusCode == 404 || resp.Code == "NoSuchKey" || resp.Code == "NoSuchObject"
	}
	// minio.ToErrorResponse covers the case where the SDK returns a
	// non-pointer value; harmless when the err isn't an ErrorResponse
	// (the returned zero value's StatusCode is 0).
	r := minio.ToErrorResponse(err)
	return r.StatusCode == 404 || r.Code == "NoSuchKey" || r.Code == "NoSuchObject"
}
