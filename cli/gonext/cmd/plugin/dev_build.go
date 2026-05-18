package plugin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// CommandRunner abstracts process invocation so the build orchestrator
// can be tested without forking. Production wires this to [execRunner].
type CommandRunner interface {
	// Run executes name with args inside dir, forwarding stdout/stderr.
	// Returning a non-nil error fails the build phase.
	Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error
}

// execRunner is the production CommandRunner. It wires the child
// process to the supplied stdout/stderr and inherits the parent
// environment.
type execRunner struct{}

// Run satisfies [CommandRunner].
func (execRunner) Run(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// buildArtifact runs the language-specific toolchain to produce a WASM
// file at <projectDir>/build/plugin.wasm. It creates the build
// directory if missing, then delegates the actual compile to
// [runBuildCommand] so the only thing tests need to fake is the
// CommandRunner.
//
// The Rust path produces the artifact at
// target/wasm32-wasi/release/plugin.wasm by convention and we copy
// (rename) it into build/ so the upload path is uniform across
// toolchains.
func buildArtifact(ctx context.Context, runner CommandRunner, projectDir string, lang Language, stdout, stderr io.Writer) error {
	buildDir := filepath.Join(projectDir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	outPath := filepath.Join(buildDir, "plugin.wasm")

	switch lang {
	case LangTinyGo:
		return runBuildCommand(
			ctx, runner, projectDir,
			"tinygo",
			[]string{"build", "-o", outPath, "-target=wasi", "."},
			stdout, stderr,
		)
	case LangRust:
		if err := runBuildCommand(
			ctx, runner, projectDir,
			"cargo",
			[]string{"build", "--target", "wasm32-wasi", "--release"},
			stdout, stderr,
		); err != nil {
			return err
		}
		// Cargo writes to target/wasm32-wasi/release/<crate>.wasm. We
		// normalise that to build/plugin.wasm so downstream callers
		// (upload, --build-only docs) only need to know one path.
		return collectCargoArtifact(projectDir, outPath)
	default:
		return fmt.Errorf("buildArtifact: unsupported language %q", lang)
	}
}

// runBuildCommand is a thin wrapper so the dev-loop control flow can
// stay focused on phases and the toolchain-specific argv lives in one
// place per language.
func runBuildCommand(ctx context.Context, runner CommandRunner, dir, name string, args []string, stdout, stderr io.Writer) error {
	if err := runner.Run(ctx, dir, name, args, stdout, stderr); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

// collectCargoArtifact finds the single .wasm file under
// target/wasm32-wasi/release/ and copies it to outPath. Cargo names
// the artifact after the crate, which the dev loop doesn't know
// without parsing Cargo.toml — searching for the single .wasm sidesteps
// that and matches what wasm-pack does.
func collectCargoArtifact(projectDir, outPath string) error {
	releaseDir := filepath.Join(projectDir, "target", "wasm32-wasi", "release")
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return fmt.Errorf("locate cargo artifact: %w", err)
	}
	var picks []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".wasm" {
			picks = append(picks, filepath.Join(releaseDir, e.Name()))
		}
	}
	switch len(picks) {
	case 0:
		return fmt.Errorf("locate cargo artifact: no .wasm produced in %s", releaseDir)
	case 1:
		return copyFile(picks[0], outPath)
	default:
		return fmt.Errorf("locate cargo artifact: multiple .wasm in %s (%v); set the crate's [lib].name to disambiguate", releaseDir, picks)
	}
}

// copyFile copies src to dst, truncating dst. We use copy rather than
// rename so cargo's incremental cache stays usable.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
