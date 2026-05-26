// Command gonext-corpus generates synthetic WordPress-shape corpora for
// migration importer tests.
//
// The corpus is deterministic given a seed and is intended to exercise the
// importer's WXR, dbdirect, and REST-shim paths without sourcing any data
// from real WordPress installations. See docs/proposals/14-proposals-ops-sec.md
// Q11-5 for the design rationale, and docs/08-migration-compat.md §16 for
// the corpus catalog this generator backs.
//
// Usage:
//
//	gonext-corpus generate --out ./corpus --sites=10 --posts-per-site=100 --seed=42
//	gonext-corpus verify   --in  ./corpus
//
// Each generated site is a directory containing:
//
//	wxr.xml       — WordPress eXtended RSS export shape
//	wp_db.sql     — minimal MySQL dump shape (schema + representative rows)
//	manifest.json — metadata about what the site contains and assumes
//
// Sites vary across the corpus: post-type mix, presence/absence of comments,
// hierarchical taxonomies, ACF-style postmeta, Gutenberg block markers.
package main

import (
	"flag"
	"fmt"
	"os"
)

const usageMain = `gonext-corpus — synthetic WordPress-shape corpus generator

Usage:
  gonext-corpus <subcommand> [flags]

Subcommands:
  generate   Produce a fresh corpus on disk.
  verify     Re-parse a generated corpus and assert well-formedness.
  assert     Run gonext migrate wp --dry-run against every fixture under
             fixtures/wxr and compare against fixtures/expected. Used by
             the migrate-corpus CI workflow.

Run ` + "`gonext-corpus <subcommand> -h`" + ` for subcommand-specific flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageMain)
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "generate":
		if err := runGenerate(args); err != nil {
			fmt.Fprintf(os.Stderr, "generate: %v\n", err)
			os.Exit(1)
		}
	case "verify":
		if err := runVerify(args); err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
			os.Exit(1)
		}
	case "assert":
		if err := runAssert(args); err != nil {
			fmt.Fprintf(os.Stderr, "assert: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageMain)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", sub, usageMain)
		os.Exit(2)
	}
}

func runGenerate(argv []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	out := fs.String("out", "./corpus", "output directory (created if absent)")
	sites := fs.Int("sites", 10, "number of synthetic sites to generate")
	posts := fs.Int("posts-per-site", 100, "approximate posts per site (varies by profile)")
	seed := fs.Int64("seed", 42, "deterministic seed for reproducible output")
	overwrite := fs.Bool("overwrite", false, "wipe --out before generating")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *sites <= 0 {
		return fmt.Errorf("--sites must be > 0, got %d", *sites)
	}
	if *posts <= 0 {
		return fmt.Errorf("--posts-per-site must be > 0, got %d", *posts)
	}
	cfg := GenerateConfig{
		OutDir:       *out,
		Sites:        *sites,
		PostsPerSite: *posts,
		Seed:         *seed,
		Overwrite:    *overwrite,
	}
	report, err := Generate(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("generated %d site(s) under %s (seed=%d)\n", report.Sites, *out, *seed)
	for _, s := range report.Summaries {
		fmt.Printf("  %s — profile=%q posts=%d comments=%d terms=%d media=%d\n",
			s.Slug, s.Profile, s.Posts, s.Comments, s.Terms, s.Media)
	}
	return nil
}

func runVerify(argv []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	in := fs.String("in", "./corpus", "corpus directory previously produced by `generate`")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	report, err := Verify(*in)
	if err != nil {
		return err
	}
	fmt.Printf("verified %d site(s) under %s\n", report.Sites, *in)
	for _, s := range report.Summaries {
		fmt.Printf("  %s — wxr_items=%d sql_inserts=%d manifest_ok=%t\n",
			s.Slug, s.WXRItems, s.SQLInserts, s.ManifestOK)
	}
	return nil
}
