package plugin

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// runInit implements `gonext plugin init [--template=go] <project-dir>`.
//
// It scaffolds a fresh plugin project from one of the embedded
// templates. Today only --template=go is supported (the
// TinyGo-targeted SDK template); future templates (rust,
// assemblyscript) land here as additional subdirs.
//
// The scaffold writes:
//
//   <project-dir>/main.go         the entry point with one action +
//                                 one filter stub and a manifest
//                                 builder
//   <project-dir>/manifest.json   the canonical manifest (matches
//                                 the SDK's builder output)
//   <project-dir>/go.mod          the Go module declaration
//   <project-dir>/Makefile        build / bundle / test targets
//   <project-dir>/.gitignore      exclude plugin.wasm, *.gnplugin
//
// Existing files at the target are NOT overwritten unless --force is
// passed. The conservative default surfaces a clear error so a user
// doesn't lose work by accident.

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, initUsage)
	}

	template := fs.String("template", "go", "template name (go|rust)")
	pluginName := fs.String("name", "", "plugin slug for the manifest (default: project dir basename)")
	force := fs.Bool("force", false, "overwrite existing files at the target")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "gonext plugin init: missing project directory")
		fmt.Fprintln(stderr, initUsage)
		return ExitUsage
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "gonext plugin init: unexpected extra arguments: %v\n", rest[1:])
		fmt.Fprintln(stderr, initUsage)
		return ExitUsage
	}

	projectDir, err := filepath.Abs(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin init: resolving project dir: %s\n", err)
		return ExitFail
	}
	slug := *pluginName
	if slug == "" {
		slug = sanitizeSlug(filepath.Base(projectDir))
	}

	switch *template {
	case "go", "rust":
		// supported
	default:
		fmt.Fprintf(stderr, "gonext plugin init: unknown template %q (supported: go, rust)\n", *template)
		return ExitUsage
	}

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "gonext plugin init: creating project dir: %s\n", err)
		return ExitFail
	}

	switch *template {
	case "go":
		if err := writeTemplateGo(projectDir, slug, *force); err != nil {
			fmt.Fprintf(stderr, "gonext plugin init: %s\n", err)
			return ExitFail
		}
		fmt.Fprintf(stdout, "Initialized GoNext Go plugin in %s\n", projectDir)
		fmt.Fprintln(stdout, "Next steps:")
		fmt.Fprintln(stdout, "  cd "+projectDir)
		fmt.Fprintln(stdout, "  go mod tidy")
		fmt.Fprintln(stdout, "  make           # builds plugin.wasm via TinyGo")
		fmt.Fprintln(stdout, "  make bundle    # packs the .gnplugin ZIP")
	case "rust":
		if err := writeTemplateRust(projectDir, slug, *force); err != nil {
			fmt.Fprintf(stderr, "gonext plugin init: %s\n", err)
			return ExitFail
		}
		fmt.Fprintf(stdout, "Initialized GoNext Rust plugin in %s\n", projectDir)
		fmt.Fprintln(stdout, "Next steps:")
		fmt.Fprintln(stdout, "  cd "+projectDir)
		fmt.Fprintln(stdout, "  cargo build --target wasm32-wasip1 --release")
		fmt.Fprintln(stdout, "  make bundle    # packs the .gnplugin ZIP")
	}
	return ExitOK
}

const initUsage = `gonext plugin init — scaffold a new plugin project

Usage:
  gonext plugin init [flags] <project-dir>

Flags:
  --template=<name>   template to use (default: go)
  --name=<slug>       plugin slug to embed in manifest.json (default: basename
                      of <project-dir>)
  --force             overwrite existing files at the target

Templates:
  go     TinyGo-targeted Go plugin using packages/go/sdk
  rust   Rust crate compiled to wasm32-wasip1 using packages/rust/gonext-sdk

Example:
  gonext plugin init --template=go ./my-plugin
  gonext plugin init --template=rust ./my-rust-plugin`

// templatesFS embeds the templates directory tree. Each file is
// rendered by trivial token substitution — {{PLUGIN_NAME}} becomes
// the manifest slug. We deliberately don't pull in text/template
// because the rendering is straight-line.
//
//go:embed templates/go/* templates/rust/* templates/rust/src/*
var templatesFS embed.FS

// writeTemplateGo renders the Go template into dir. Returns an error
// if a target file already exists and force is false.
func writeTemplateGo(dir, slug string, force bool) error {
	root := "templates/go"
	return fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativise template path %q: %w", path, err)
		}
		// Rename foo.tmpl -> foo so a user editing the template
		// in-place doesn't accidentally trip Go's build on the
		// .tmpl extension. The convention applies to every file
		// that benefits from it; today only main.go.tmpl uses it.
		target := filepath.Join(dir, strings.TrimSuffix(rel, ".tmpl"))
		// gitignore.tmpl becomes .gitignore — the leading-dot
		// rename is special-cased because the embedded
		// filesystem can't carry dotfiles cleanly.
		if filepath.Base(target) == "gitignore" {
			target = filepath.Join(filepath.Dir(target), ".gitignore")
		}

		if !force {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("file already exists: %s (use --force to overwrite)", target)
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", target, err)
		}
		data, err := templatesFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %q: %w", path, err)
		}
		rendered := strings.ReplaceAll(string(data), "{{PLUGIN_NAME}}", slug)
		rendered = strings.ReplaceAll(rendered, "{{PLUGIN_NAME_LITERAL}}", slug)
		if err := os.WriteFile(target, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		return nil
	})
}

// writeTemplateRust renders the Rust template into dir. Mirrors
// writeTemplateGo (walk + .tmpl rename + token substitution); a
// dedicated function rather than a shared helper keeps the
// per-template special cases (file extensions, dotfile renames)
// explicit in their own scope.
//
// The substitution map carries one extra token beyond {{PLUGIN_NAME}}:
// {{CRATE_UNDERSCORED}} — Cargo turns "my-plugin" into "my_plugin"
// when producing the cdylib artefact, and Makefiles need that form to
// reference the build output. We compute it inline since it's only
// used here.
func writeTemplateRust(dir, slug string, force bool) error {
	crateUnderscored := strings.ReplaceAll(slug, "-", "_")
	root := "templates/rust"
	return fs.WalkDir(templatesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativise template path %q: %w", path, err)
		}
		target := filepath.Join(dir, strings.TrimSuffix(rel, ".tmpl"))

		if !force {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("file already exists: %s (use --force to overwrite)", target)
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", target, err)
		}
		data, err := templatesFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %q: %w", path, err)
		}
		rendered := strings.ReplaceAll(string(data), "{{PLUGIN_NAME}}", slug)
		rendered = strings.ReplaceAll(rendered, "{{PLUGIN_NAME_LITERAL}}", slug)
		rendered = strings.ReplaceAll(rendered, "{{CRATE_UNDERSCORED}}", crateUnderscored)
		if err := os.WriteFile(target, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		return nil
	})
}

// sanitizeSlug converts a directory basename into a plugin-manifest-
// safe slug: lowercase ASCII, hyphens for non-alphanumerics, no
// leading/trailing hyphens, falling back to "my-plugin" if nothing
// remains.
//
// The host's manifest schema accepts /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;
// the sanitiser produces output that always satisfies that regex.
func sanitizeSlug(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range strings.ToLower(in) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ', r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		return "my-plugin"
	}
	return out
}
