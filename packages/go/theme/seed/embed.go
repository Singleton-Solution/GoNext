package seed

import "embed"

// BundledThemes is the embedded copy of every theme that ships in the
// binary as a seed candidate. Today that's exactly one theme,
// "gn-hello", which is the default theme installed by the first-run
// seeder so a fresh deploy renders a usable site without any operator
// configuration.
//
// The mirror under packages/go/theme/seed/gn-hello/ is the source of
// truth for the embedded bytes — the canonical authoring copy lives at
// /themes/gn-hello at the repo root and is duplicated here at release
// time so the binary stays self-contained (Go's //go:embed cannot
// reference paths outside the package directory). A CI check should
// keep the two in sync; the test suite verifies the bundled bytes
// parse + validate as a sanity check.
//
//go:embed all:gn-hello
var BundledThemes embed.FS

// DefaultThemeSlug is the slug of the theme the seeder installs when
// no active_theme option is present. It MUST match a top-level
// directory under BundledThemes; the seeder reads from
// "<DefaultThemeSlug>/" in the embed.FS and writes to
// "<ThemeDir>/<DefaultThemeSlug>/" on disk.
const DefaultThemeSlug = "gn-hello"
