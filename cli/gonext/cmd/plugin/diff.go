package plugin

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Singleton-Solution/GoNext/cli/gonext/internal/plugintest"
)

// runDiff implements `gonext plugin diff [--json] <old-bundle> <new-bundle>`.
//
// Prints a human-readable summary of the capability changes between two
// versions of a bundle: which capabilities were added, which were
// removed, and which were unchanged. Used by the registry pipeline to
// build a "what surface am I accepting?" panel for the operator before
// they activate a new version.
//
// The exit code is always 0 unless the bundles can't be opened or
// parsed — capability changes are observational, not a pass/fail check.
func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gonext plugin diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, diffUsage) }

	jsonOut := fs.Bool("json", false, "emit the diff as a JSON object")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}

	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "gonext plugin diff: need exactly two bundle paths")
		fmt.Fprintln(stderr, diffUsage)
		return ExitUsage
	}

	oldCaps, oldMeta, err := readBundleCaps(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin diff: read %s: %s\n", rest[0], err)
		return ExitFail
	}
	newCaps, newMeta, err := readBundleCaps(rest[1])
	if err != nil {
		fmt.Fprintf(stderr, "gonext plugin diff: read %s: %s\n", rest[1], err)
		return ExitFail
	}

	diff := CapabilityDiff{
		OldVersion: oldMeta.Version,
		NewVersion: newMeta.Version,
		Slug:       newMeta.Slug,
	}
	diff.Added, diff.Removed, diff.Unchanged = diffCaps(oldCaps, newCaps)

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(diff); err != nil {
			fmt.Fprintf(stderr, "gonext plugin diff: write JSON: %s\n", err)
			return ExitFail
		}
		return ExitOK
	}

	if err := writeDiffHuman(stdout, diff); err != nil {
		fmt.Fprintf(stderr, "gonext plugin diff: write: %s\n", err)
		return ExitFail
	}
	return ExitOK
}

// CapabilityDiff is the JSON-serialisable shape of a diff. The registry
// pipeline emits this into the GitHub-summary panel; the admin UI
// renders it on the "review new version" screen.
type CapabilityDiff struct {
	Slug       string   `json:"slug"`
	OldVersion string   `json:"old_version"`
	NewVersion string   `json:"new_version"`
	Added      []string `json:"added"`
	Removed    []string `json:"removed"`
	Unchanged  []string `json:"unchanged"`
}

// bundleCapMeta carries the minimum metadata the diff renderer needs
// from each side beyond the capability list itself. Keeping it
// internal-only lets us add fields (publisher, signature mode) later
// without touching the public CapabilityDiff schema.
type bundleCapMeta struct {
	Slug    string
	Version string
}

// readBundleCaps opens a bundle, parses its manifest, and returns the
// declared capability list + the slug/version pair. The capability list
// is sorted for deterministic diffing.
func readBundleCaps(path string) ([]string, bundleCapMeta, error) {
	b, err := plugintest.OpenBundle(path)
	if err != nil {
		return nil, bundleCapMeta{}, err
	}
	defer b.Close()

	body, err := b.ReadManifest()
	if err != nil {
		return nil, bundleCapMeta{}, err
	}

	// Use a permissive decode shape so a bundle whose manifest uses
	// older field names (the lifecycle's "slug" vs the v1 schema's
	// "name") still produces a useful diff.
	var m struct {
		Slug         string   `json:"slug"`
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Capabilities any      `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, bundleCapMeta{}, fmt.Errorf("parse manifest: %w", err)
	}
	slug := m.Slug
	if slug == "" {
		slug = m.Name
	}
	caps, err := normaliseCapabilities(m.Capabilities)
	if err != nil {
		return nil, bundleCapMeta{}, err
	}
	return caps, bundleCapMeta{Slug: slug, Version: m.Version}, nil
}

// normaliseCapabilities flattens the two possible manifest shapes into
// a sorted slice of capability ids. The v1 schema declares capabilities
// as an array of strings; the lifecycle's legacy Manifest uses a
// map[string]any. Both are accepted so the diff works across the
// migration window.
func normaliseCapabilities(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("capabilities[%d] is not a string", i)
			}
			out = append(out, s)
		}
		sort.Strings(out)
		return out, nil
	case map[string]any:
		out := make([]string, 0, len(x))
		for k := range x {
			out = append(out, k)
		}
		sort.Strings(out)
		return out, nil
	default:
		return nil, errors.New("capabilities is neither an array nor a map")
	}
}

// diffCaps computes added / removed / unchanged sets. All slices are
// sorted; the input slices are assumed to already be sorted by
// readBundleCaps.
func diffCaps(oldList, newList []string) (added, removed, unchanged []string) {
	oldSet := make(map[string]struct{}, len(oldList))
	for _, c := range oldList {
		oldSet[c] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newList))
	for _, c := range newList {
		newSet[c] = struct{}{}
	}
	for _, c := range newList {
		if _, ok := oldSet[c]; ok {
			unchanged = append(unchanged, c)
		} else {
			added = append(added, c)
		}
	}
	for _, c := range oldList {
		if _, ok := newSet[c]; !ok {
			removed = append(removed, c)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(unchanged)
	return
}

// writeDiffHuman renders the diff in a compact, human-readable form.
// Format mirrors the registry-pipeline GitHub-summary output so an
// operator reading the workflow page sees the same text the CLI emits.
func writeDiffHuman(w io.Writer, d CapabilityDiff) error {
	var b strings.Builder
	header := fmt.Sprintf("Capability diff for %q: %s -> %s", d.Slug, d.OldVersion, d.NewVersion)
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("=", len(header)))
	b.WriteByte('\n')

	if len(d.Added)+len(d.Removed) == 0 {
		b.WriteString("No capability changes.\n")
	}
	if len(d.Added) > 0 {
		b.WriteString("\nAdded:\n")
		for _, c := range d.Added {
			b.WriteString("  + " + c + "\n")
		}
	}
	if len(d.Removed) > 0 {
		b.WriteString("\nRemoved:\n")
		for _, c := range d.Removed {
			b.WriteString("  - " + c + "\n")
		}
	}
	if len(d.Unchanged) > 0 {
		b.WriteString("\nUnchanged:\n")
		for _, c := range d.Unchanged {
			b.WriteString("    " + c + "\n")
		}
	}
	_, err := w.Write([]byte(b.String()))
	return err
}

const diffUsage = `gonext plugin diff — compute capability-diff between two bundle versions

Usage:
  gonext plugin diff [--json] <old-bundle> <new-bundle>

Arguments:
  <old-bundle>   Path to the previous version (directory or .gnplugin).
  <new-bundle>   Path to the new version.

Flags:
  --json     Emit the diff as a JSON document instead of human text.

Exit codes:
  0   diff computed (regardless of whether anything changed)
  1   could not open or parse one of the bundles
  2   usage error

Output sections:
  Added       Capabilities present in <new-bundle> but not <old-bundle>.
  Removed     Capabilities present in <old-bundle> but not <new-bundle>.
  Unchanged   Capabilities present in both.

This subcommand is used by the registry publishing pipeline to print
a "what surface am I accepting?" summary before a new version goes
live. The capability list is read from the manifest's "capabilities"
field; both v1-schema arrays and legacy map shapes are accepted.`
