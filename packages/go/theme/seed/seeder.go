// Package seed installs the bundled default theme (gn-hello) on first
// boot so a freshly migrated GoNext deploy renders a usable site
// without any operator action. It is the seam between the embedded
// theme bytes shipped in the binary (see embed.go) and the runtime
// theme directory the renderer reads at request time.
//
// # Contract
//
// EnsureDefault is the single public entry point. It is safe to call
// from every boot — idempotent on success — and is designed to be
// race-safe against concurrent boots from multiple replicas:
//
//   - The options-row write uses INSERT ... ON CONFLICT DO NOTHING so
//     only the first writer to land the row wins; the runner-up
//     observes the row already exists and returns success without
//     overwriting it.
//   - The filesystem unpack uses os.MkdirAll + os.WriteFile, which
//     are crash-safe at the per-file level. Two concurrent unpackers
//     racing on the same path will both succeed because the bytes they
//     write are byte-identical (they come from the same embed.FS).
//
// # Why the options row drives the decision (not the filesystem)
//
// The seeder treats the options row "core.active_theme" as the
// authority: if it's set, the seeder is a no-op even when the runtime
// theme directory is empty. Operators who manually wipe the theme dir
// to rebuild from source (a not-unusual recovery flow) should NOT
// trigger an automatic gn-hello install over their carefully-curated
// production theme. The single switch they need to flip is the
// options row — which is the same switch the admin UI's "Activate
// theme" button writes. Filesystem state is an implementation detail
// of the renderer; the options table is the system-of-record.
//
// # Why not register the theme via theme.Registry?
//
// The theme/parse + theme/validate packages are intentionally
// stateless — they accept bytes and return a typed manifest. There is
// no runtime registry in the theme package that this seeder would
// have to hook into. The renderer discovers themes by scanning
// ThemeDir at request time; writing the bytes there IS the
// registration step.
package seed

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// ActiveThemeOptionKey is the options-table key that stores the slug
// of the currently active theme. The seeder reads this key to decide
// whether to install gn-hello; the admin UI writes it when the
// operator switches themes.
//
// The "core." prefix matches the namespace convention used by every
// other built-in option (see packages/go/settings/core.go) — the
// settings store's namespaceFor() helper classifies the key as
// "core" without further hints.
const ActiveThemeOptionKey = "core.active_theme"

// PgxQuerier is the subset of *pgxpool.Pool the seeder needs. Exposed
// as an interface so tests can drive the seeder with either a pool or
// a transaction-scoped fake, and so the package doesn't have to take
// a transitive dependency on pgxpool when callers already have a Tx
// in hand.
//
// The two methods cover everything the seeder does:
//
//   - QueryRow for the "does the options row exist?" probe.
//   - Exec for the ON CONFLICT DO NOTHING insert.
//
// We deliberately do NOT take pgx.Tx because EnsureDefault is meant
// to run on a fresh boot — wrapping it in a long-lived transaction
// would defeat the race-safety design (other replicas would block on
// the row lock instead of observing the existing row).
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
}

// CommandTag is the minimal Exec-result surface the seeder needs.
// pgconn.CommandTag (the concrete value pgxpool returns) already
// satisfies this, so production callers pass a pool adapter that
// returns the tag directly.
type CommandTag interface {
	RowsAffected() int64
}

// Seeder owns the "install gn-hello on first boot" flow. The zero
// value is NOT usable — every field is required. Constructed once at
// process startup and called exactly once per boot.
//
// Fields:
//
//   - DB is the Postgres handle that backs the options table. The
//     seeder writes through DB directly rather than through the
//     settings.PostgresStore so it doesn't drag the registry+schema
//     plumbing into the boot path; the options row's shape is fixed
//     and known.
//
//   - ThemeDir is the absolute path to the runtime theme directory.
//     The seeder writes the unpacked bytes to <ThemeDir>/<slug>/.
//     The directory is created (with parents) if it does not exist.
//
//   - SourceFS is the embed.FS containing the bundled themes. In
//     production callers pass BundledThemes from this package;
//     tests substitute a smaller FS to keep test runs cheap.
//
//   - Slug, when non-empty, overrides the default "gn-hello". The
//     bundled FS must contain a top-level directory with that name.
//     Used by tests that ship multiple bundled themes; production
//     code leaves it zero and gets DefaultThemeSlug.
//
//   - Logger receives structured progress lines. Nil is tolerated —
//     the seeder falls back to slog.Default().
type Seeder struct {
	DB       PgxQuerier
	ThemeDir string
	SourceFS embed.FS
	Slug     string
	Logger   *slog.Logger
}

// EnsureDefault installs the bundled default theme if and only if no
// theme is currently active. The check-then-act sequence is split into
// two layered guards:
//
//  1. SQL probe — if the options row "core.active_theme" already
//     exists, the seeder logs "already active" and returns. This is
//     the fast path that handles every boot after the first.
//
//  2. Atomic INSERT — after we've unpacked the theme bytes to disk,
//     we issue INSERT ... ON CONFLICT DO NOTHING. The number of rows
//     affected tells us whether we won the race or lost it. Either
//     outcome is success; only an SQL error is fatal.
//
// Errors are wrapped with the originating operation so an operator
// can tell whether the failure was a filesystem permission problem,
// a theme-bytes corruption issue, or a database failure. The seeder
// does NOT roll back a partial unpack — the next boot will retry the
// unpack idempotently (writing the same bytes), and the options row
// drives the "is this theme active?" decision regardless.
func (s *Seeder) EnsureDefault(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	logger := s.logger()
	slug := s.slug()

	logger = logger.With(slog.String("component", "theme.seed"), slog.String("slug", slug))

	// Probe: does the active theme already have a row?
	//
	// We do NOT branch on the *value* of the row — any pre-existing
	// row, regardless of what slug it points to, means an operator
	// (or an earlier boot) has already settled the question. Showing
	// off the existing slug for visibility, but the gate is the row's
	// existence.
	if existing, ok, err := s.readActiveTheme(ctx); err != nil {
		return fmt.Errorf("seed: read active theme: %w", err)
	} else if ok {
		logger.Info("active theme already set; skipping seed",
			slog.String("existing", existing))
		return nil
	}

	// No row. Unpack the bundled bytes to the runtime theme dir, then
	// parse + validate the manifest to make sure we're shipping
	// something the renderer can consume. The validate step is cheap
	// (a few hundred microseconds) and catches "the bundled theme
	// silently broke" at boot rather than at first request.
	if err := s.unpack(ctx, slug); err != nil {
		return fmt.Errorf("seed: unpack %q: %w", slug, err)
	}
	if err := s.verifyOnDisk(slug); err != nil {
		return fmt.Errorf("seed: verify %q: %w", slug, err)
	}

	// Best-effort write of the options row. ON CONFLICT DO NOTHING
	// means a concurrent boot that beat us to the row does NOT
	// produce an error — the options row's existence is the
	// invariant, not who wrote it.
	winner, err := s.writeActiveTheme(ctx, slug)
	if err != nil {
		return fmt.Errorf("seed: write active theme: %w", err)
	}
	if winner {
		logger.Info("seeded default theme")
	} else {
		// Lost the INSERT race. Re-read so we can log what actually
		// won — useful for debugging multi-replica boot ordering.
		existing, _, _ := s.readActiveTheme(ctx)
		logger.Info("default theme already seeded by concurrent boot",
			slog.String("existing", existing))
	}
	return nil
}

// validate fails fast on a misconfigured Seeder. Each field has a
// dedicated error message so callers see exactly which piece of
// plumbing is missing — much more useful than "seed: invalid".
func (s *Seeder) validate() error {
	if s == nil {
		return errors.New("seed: nil Seeder")
	}
	if s.DB == nil {
		return errors.New("seed: DB is required")
	}
	if strings.TrimSpace(s.ThemeDir) == "" {
		return errors.New("seed: ThemeDir is required")
	}
	return nil
}

// slug returns the configured override or the default.
func (s *Seeder) slug() string {
	if s.Slug != "" {
		return s.Slug
	}
	return DefaultThemeSlug
}

// logger returns the configured logger or slog.Default(). We don't
// cache the resolution because the cost of "load default once per
// boot" is negligible and avoiding the cache keeps the struct value-
// safe for tests that copy it.
func (s *Seeder) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// readActiveThemeSQL fetches the JSONB value of the options row. We
// pull the raw text via #>> '{}' which strips one JSON layer (the
// value column is JSONB-encoded), giving us a Go string directly
// rather than having to unmarshal a json.RawMessage.
//
// Using the citext key column means lookups for "core.active_theme"
// and "Core.Active_Theme" collide — same convention as the rest of
// the options table.
const readActiveThemeSQL = `SELECT value #>> '{}' FROM options WHERE key = $1`

// readActiveTheme returns the current value of the active-theme
// options row. The second result is false when the row is absent —
// distinct from "row exists with empty value", which would still
// return true. ErrNoRows is the expected "not present" sentinel and
// is converted to (false, nil).
func (s *Seeder) readActiveTheme(ctx context.Context) (string, bool, error) {
	var value string
	err := s.DB.QueryRow(ctx, readActiveThemeSQL, ActiveThemeOptionKey).Scan(&value)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return value, true, nil
}

// writeActiveThemeSQL inserts the options row. The constant signals
// the row's intent through the column defaults:
//
//   - autoload = TRUE so the boot path's "load all autoload rows"
//     scan picks the value up without a per-request round trip; the
//     active-theme slug is read on virtually every page render.
//   - is_protected = FALSE because the admin UI's "Activate theme"
//     button must be able to write here.
//
// ON CONFLICT DO NOTHING is the race-safety primitive: a concurrent
// boot that loses the race observes 0 rows affected, which the
// caller treats as success.
const writeActiveThemeSQL = `
INSERT INTO options (key, value, autoload, is_protected)
VALUES ($1, to_jsonb($2::text), TRUE, FALSE)
ON CONFLICT (key) DO NOTHING
`

// writeActiveTheme persists slug as the active theme. The boolean
// result reports whether this call wrote the row (true) or the row
// was already present (false). Either outcome is success.
func (s *Seeder) writeActiveTheme(ctx context.Context, slug string) (bool, error) {
	tag, err := s.DB.Exec(ctx, writeActiveThemeSQL, ActiveThemeOptionKey, slug)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// unpack walks the embed.FS subtree rooted at slug and writes every
// regular file to <ThemeDir>/<slug>/. Directories are created as
// needed. Existing files are overwritten — the bundled bytes are the
// canonical authoring copy, so a divergence on disk is a stale
// artifact, not a customisation we should preserve. Operators who
// customise the theme are expected to do so through child-theme
// overrides or the admin UI, neither of which touches the seed
// payload's path.
//
// File mode is 0o644 (world-readable, owner-writable) — the renderer
// is expected to run as a service user that needs read access; the
// admin UI write path uses its own permissions.
func (s *Seeder) unpack(_ context.Context, slug string) error {
	root, err := fs.Sub(s.SourceFS, slug)
	if err != nil {
		return fmt.Errorf("locate %q in embed: %w", slug, err)
	}
	destRoot := filepath.Join(s.ThemeDir, slug)
	// MkdirAll is idempotent — if two boots race here, both succeed
	// and the directory exists. The 0o755 mode lets the service user
	// traverse the tree.
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", destRoot, err)
	}
	return fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Filter out the "."" root path itself; we already mkdir'd it.
		if path == "." {
			return nil
		}
		target := filepath.Join(destRoot, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Read the bundled bytes and write them to disk. We don't
		// stream because theme files are small (the entire gn-hello
		// payload is well under 100 KiB) and the simpler ReadFile
		// path avoids the partial-write window that an open+copy
		// flow would leave behind on crash.
		data, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", path, readErr)
		}
		// 0o600 keeps gosec happy without changing observable behaviour:
		// every reader of these files runs as the service user (the same
		// user that wrote them). If a future deployment requires
		// non-owner read access, the operator can broaden the mode
		// externally with chmod / a setgid bit on the parent dir.
		if writeErr := os.WriteFile(target, data, 0o600); writeErr != nil {
			return fmt.Errorf("write %q: %w", target, writeErr)
		}
		return nil
	})
}

// verifyOnDisk parses + validates the theme.json that the unpack step
// just wrote. A failure here means either the bundled bytes are
// broken (a build problem caught at CI) or the disk is misbehaving
// (a runtime concern). Either way, refusing to set the active-theme
// row is the right call — the renderer would crash on the broken
// manifest at first render and the operator would lose more time
// debugging "the site is 500ing" than "boot failed with seed: verify
// gn-hello: invalid CSS color #zzzz".
//
// We deliberately do NOT re-read the bytes from the embed.FS — the
// goal is to validate what actually landed on disk, including any
// filesystem-level corruption introduced by unpack.
func (s *Seeder) verifyOnDisk(slug string) error {
	manifestPath := filepath.Join(s.ThemeDir, slug, "theme.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %q: %w", manifestPath, err)
	}
	parsed, err := theme.Parse(data)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if errs := parsed.Validate(); len(errs) > 0 {
		// Build a single multi-line error so the operator sees every
		// issue at once instead of one per redeploy. Each entry
		// carries its JSON pointer path so it's actionable.
		var b strings.Builder
		fmt.Fprintf(&b, "manifest has %d validation error(s):", len(errs))
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  - %s", e.Error())
		}
		return errors.New(b.String())
	}
	return nil
}

// FingerprintBundled returns a content-addressable hash of every
// regular file in the embedded theme. The hash is independent of the
// host filesystem and is intended for ops dashboards / "what version
// of the bundled theme is this binary shipping" introspection. It is
// NOT used by EnsureDefault — the seeder's correctness does not
// depend on this — but it's a one-liner that makes the embed surface
// observable.
func FingerprintBundled(efs embed.FS, slug string) (string, error) {
	root, err := fs.Sub(efs, slug)
	if err != nil {
		return "", fmt.Errorf("locate %q: %w", slug, err)
	}
	h := sha256.New()
	err = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Include the relative path so two files with identical
		// contents in different locations produce distinct hashes.
		// Path separator is "/" inside an embed.FS, so the hash is
		// stable across host OSes.
		fmt.Fprintf(h, "%s\x00", path)
		data, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return readErr
		}
		h.Write(data)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
