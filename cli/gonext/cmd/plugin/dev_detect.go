package plugin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Language is the toolchain identifier the dev loop builds with. The
// string form is what surfaces on the CLI (`--lang go`) and in logs.
type Language string

const (
	// LangTinyGo selects `tinygo build -target=wasi`.
	LangTinyGo Language = "tinygo"
	// LangRust selects `cargo build --target wasm32-wasi --release`.
	LangRust Language = "rust"
)

// String satisfies fmt.Stringer so log lines stay readable.
func (l Language) String() string { return string(l) }

// resolveLanguage picks the build toolchain for projectDir. If hint is
// "auto" it sniffs the project: go.mod implies TinyGo (we don't yet
// support raw Go-to-WASI for plugins), Cargo.toml implies Rust. Any
// other hint is treated as an explicit selection — `go` and `tinygo`
// both map to [LangTinyGo].
func resolveLanguage(projectDir, hint string) (Language, error) {
	switch hint {
	case "", "auto":
		return detectLanguage(projectDir)
	case "go", "tinygo":
		return LangTinyGo, nil
	case "rust":
		return LangRust, nil
	default:
		return "", fmt.Errorf("unsupported --lang %q (expected auto, go, tinygo, or rust)", hint)
	}
}

// detectLanguage looks for canonical toolchain markers at the root of
// projectDir. It returns an error rather than guessing if more than one
// marker is present — silently picking the "wrong" one would lead to
// confusing build failures three layers deep.
func detectLanguage(projectDir string) (Language, error) {
	hasGo := fileExists(filepath.Join(projectDir, "go.mod"))
	hasRust := fileExists(filepath.Join(projectDir, "Cargo.toml"))

	switch {
	case hasGo && hasRust:
		return "", fmt.Errorf(
			"language detection ambiguous: both go.mod and Cargo.toml found in %s; pass --lang to select",
			projectDir,
		)
	case hasGo:
		return LangTinyGo, nil
	case hasRust:
		return LangRust, nil
	default:
		return "", fmt.Errorf(
			"language detection failed: no go.mod or Cargo.toml in %s; pass --lang or create a toolchain marker",
			projectDir,
		)
	}
}

// fileExists is a tiny helper that returns true iff path resolves to a
// non-directory regular file the current process can stat.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
