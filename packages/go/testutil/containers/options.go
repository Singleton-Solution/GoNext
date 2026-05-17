package containers

// config holds the resolved options for a single container helper call.
// Each helper has its own defaults; options mutate this struct before the
// container is started. The zero value is not directly useful — helpers
// initialize defaults and then apply options on top.
type config struct {
	// image is the fully-qualified image override. When set, it wins over
	// version (the caller has full control).
	image string

	// version is the image tag (e.g. "16-alpine"). Combined with the
	// helper-specific base image when image is empty.
	version string

	// db is the Postgres database name. Ignored by other helpers.
	db string
}

// Option mutates the helper's config. The same Option type is shared
// across Postgres/Redis/MinIO; individual helpers ignore fields that
// don't apply to them (e.g. Redis ignores WithDB). Keeping a single
// Option type means callers can pass options through generic plumbing
// without a type assertion per backend.
type Option func(*config)

// PGOption, RedisOption, and MinIOOption are aliases for Option that
// document intent at the call site. They're interchangeable — the helper
// signatures use the specific alias to make IDE completion show only
// the options that make sense for that backend, but the underlying
// type is identical.
type (
	PGOption    = Option
	RedisOption = Option
	MinIOOption = Option
)

// WithVersion pins the image tag (e.g. "16-alpine", "7-alpine",
// "RELEASE.2024-10-13T13-34-11Z"). Defaults are baked into each helper
// and chosen to be recent-but-not-bleeding so CI doesn't break when a
// new upstream release lands.
func WithVersion(ver string) Option {
	return func(c *config) {
		c.version = ver
	}
}

// WithImage overrides the entire image reference, including registry and
// repository. Use this when you need a mirror or a custom-built image
// (e.g. Postgres with extensions pre-installed). When set, WithVersion
// is ignored — the caller picked the exact image they want.
func WithImage(img string) Option {
	return func(c *config) {
		c.image = img
	}
}

// WithDB sets the initial database name for Postgres containers. Ignored
// by Redis and MinIO helpers. Default is "gonext_test".
func WithDB(name string) Option {
	return func(c *config) {
		c.db = name
	}
}

// apply runs the options on top of a defaults baseline and returns the
// resolved config. Splitting this out keeps each helper's setup short.
func apply(defaults config, opts []Option) config {
	cfg := defaults
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}
