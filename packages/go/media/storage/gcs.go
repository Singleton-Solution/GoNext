package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// GCSDriver is the Google Cloud Storage backend skeleton.
//
// # Status
//
// This is a stub. Every method on the Driver interface is satisfied,
// so the wiring code (the New(ctx, Options) entrypoint) can target
// GONEXT_MEDIA_DRIVER=gcs today, but at runtime every method returns
// ErrUnimplemented (wrapped with the operation name). A follow-up
// issue swaps the implementation for a real cloud.google.com/go/storage
// client; the consuming code does not change because the public
// surface is fixed.
//
// # Why ship the stub
//
// Two reasons. First, a single env switch keeps the boot wiring
// uniform across local / s3 / gcs — if a deployer flips to gcs by
// mistake, they get a clean ErrUnimplemented error path rather than
// a missing-case panic. Second, the GCS interface surface is
// non-trivial (it has its own bucket-creation, IAM, and signed-URL
// libraries) and we don't want to pull those into go.mod until a
// real implementation lands, but we DO want the type to exist so
// that imports referencing storage.GCSDriver compile.
//
// # Configuration
//
// GCSConfig holds the parameters the real implementation will need:
// bucket, project id, and the service-account JSON path. The stub
// only validates that Bucket is non-empty; the rest are accepted
// but not used.
type GCSDriver struct {
	cfg GCSConfig
}

// GCSConfig configures a GCSDriver. The stub uses only Bucket; the
// remaining fields are accepted to keep the call sites stable when
// the real implementation lands.
type GCSConfig struct {
	// Bucket is the GCS bucket name. Required.
	Bucket string

	// ProjectID is the GCP project that owns the bucket. Required by
	// the real implementation for signed-URL minting; unused in the
	// stub.
	ProjectID string

	// CredentialsFile is the path to a service-account JSON key. If
	// empty, the real implementation falls back to Application
	// Default Credentials. Unused in the stub.
	CredentialsFile string

	// PublicBaseURL overrides the default storage.googleapis.com URL
	// prefix. Useful for buckets fronted by a custom domain. Unused
	// in the stub.
	PublicBaseURL string

	// DefaultPresignTTL is the TTL used when Presign is called with
	// ttl == 0. Capped at 7 days by the GCS signing protocol.
	DefaultPresignTTL time.Duration
}

// NewGCSDriver returns the stub. Validates Bucket is non-empty; the
// remaining fields are recorded but not used.
//
// TODO(#64-followup): replace with cloud.google.com/go/storage-backed
// implementation. The interface surface stays the same; only the
// method bodies change.
func NewGCSDriver(cfg GCSConfig) (*GCSDriver, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("storage/gcs: Bucket is required")
	}
	if cfg.DefaultPresignTTL <= 0 {
		cfg.DefaultPresignTTL = 15 * time.Minute
	}
	return &GCSDriver{cfg: cfg}, nil
}

// Put returns ErrUnimplemented. TODO(#64-followup).
func (g *GCSDriver) Put(_ context.Context, _ string, _ io.Reader, _ string) (int64, error) {
	return 0, fmt.Errorf("%w: GCSDriver.Put", ErrUnimplemented)
}

// Get returns ErrUnimplemented. TODO(#64-followup).
func (g *GCSDriver) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%w: GCSDriver.Get", ErrUnimplemented)
}

// Delete returns ErrUnimplemented. TODO(#64-followup).
func (g *GCSDriver) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("%w: GCSDriver.Delete", ErrUnimplemented)
}

// Stat returns ErrUnimplemented. TODO(#64-followup).
func (g *GCSDriver) Stat(_ context.Context, _ string) (*Object, error) {
	return nil, fmt.Errorf("%w: GCSDriver.Stat", ErrUnimplemented)
}

// Presign returns ErrUnimplemented. TODO(#64-followup) wires this to
// storage.SignedURL with the V4 signing scheme.
func (g *GCSDriver) Presign(_ context.Context, _ string, _ PresignOp, _ time.Duration, _ string) (PresignedRequest, error) {
	return PresignedRequest{}, fmt.Errorf("%w: GCSDriver.Presign", ErrUnimplemented)
}

// PublicURL synthesises the URL the real implementation will return.
// Returned in the stub because the value is statically derivable from
// the config (it does not require a live client), and rendering the
// public URL is the lowest-risk operation to satisfy at stub time.
func (g *GCSDriver) PublicURL(key string) string {
	if g.cfg.PublicBaseURL != "" {
		return strings.TrimRight(g.cfg.PublicBaseURL, "/") + "/" + key
	}
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", g.cfg.Bucket, key)
}
