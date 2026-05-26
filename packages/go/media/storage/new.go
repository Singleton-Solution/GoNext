package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DriverKind names the supported backends. The string values are the
// canonical env-var values for GONEXT_MEDIA_DRIVER.
type DriverKind string

const (
	// DriverLocal is the filesystem-backed driver. Default.
	DriverLocal DriverKind = "local"

	// DriverS3 is the S3-compatible driver (AWS, MinIO, R2, etc.).
	DriverS3 DriverKind = "s3"

	// DriverGCS is the GCS driver (currently a stub; see GCSDriver).
	DriverGCS DriverKind = "gcs"
)

// Options configures the New constructor. Every field has a
// reasonable default; pass an empty Options to get the default
// LocalDriver rooted under <CWD>/.gonext-media.
type Options struct {
	// Driver selects the backend. When empty, falls back to the
	// GONEXT_MEDIA_DRIVER env var; when that is also empty,
	// DriverLocal is used.
	Driver DriverKind

	// Local is the LocalDriver config. When empty, Root defaults to
	// $GONEXT_MEDIA_ROOT, then to "./.gonext-media" relative to the
	// process CWD.
	Local LocalConfig

	// S3 is the S3Driver config. Required when Driver=s3.
	S3 S3Config

	// GCS is the GCSDriver config. Required when Driver=gcs.
	GCS GCSConfig

	// Env is the source of environment variables. Used only for the
	// driver-selection fallback; tests pass a fixture map to override
	// the live process environment.
	Env map[string]string
}

// New returns the Driver matching opts.Driver (or the env var
// fallback). The returned Driver is ready to use; the function
// performs no network IO so a misconfigured S3 endpoint surfaces on
// the first real call rather than at boot.
//
// The selection rule:
//
//   1. opts.Driver if non-empty
//   2. otherwise the env var GONEXT_MEDIA_DRIVER (looked up via
//      opts.Env when non-nil, otherwise os.Getenv)
//   3. otherwise DriverLocal
//
// Returns an error when a required config field is missing (e.g.
// S3.Bucket empty when Driver=s3) or the driver name is unknown.
func New(_ context.Context, opts Options) (Driver, error) {
	kind := opts.Driver
	if kind == "" {
		kind = DriverKind(lookupEnv(opts.Env, "GONEXT_MEDIA_DRIVER"))
	}
	if kind == "" {
		kind = DriverLocal
	}
	switch kind {
	case DriverLocal:
		cfg := opts.Local
		if cfg.Root == "" {
			cfg.Root = lookupEnv(opts.Env, "GONEXT_MEDIA_ROOT")
		}
		if cfg.Root == "" {
			// Default to a directory under the process CWD. Made
			// absolute so the path does not change if the process
			// later chdir's.
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("storage: resolve cwd: %w", err)
			}
			cfg.Root = filepath.Join(cwd, ".gonext-media")
		}
		return NewLocalDriver(cfg)
	case DriverS3:
		return NewS3Driver(opts.S3)
	case DriverGCS:
		return NewGCSDriver(opts.GCS)
	default:
		return nil, fmt.Errorf("storage: unknown driver %q (want local|s3|gcs)", kind)
	}
}

// MustNew is the panic-on-error version of New, used in main() paths
// where a missing storage driver is a boot failure not worth
// recovering from. Production wiring uses New and surfaces the error
// to the process exit code; tests and tools use MustNew when the
// config is hard-coded.
func MustNew(ctx context.Context, opts Options) Driver {
	d, err := New(ctx, opts)
	if err != nil {
		panic(fmt.Sprintf("storage.MustNew: %v", err))
	}
	return d
}

func lookupEnv(envMap map[string]string, key string) string {
	if envMap != nil {
		if v, ok := envMap[key]; ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	return strings.TrimSpace(os.Getenv(key))
}
