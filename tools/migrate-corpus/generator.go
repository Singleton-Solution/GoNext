package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateConfig parameters a corpus generation run. All randomness derives
// from Seed; the same config produces byte-identical output (modulo the
// freshly-created mtimes on disk).
type GenerateConfig struct {
	OutDir       string
	Sites        int
	PostsPerSite int
	Seed         int64
	Overwrite    bool
}

// GenerateReport summarises what was produced.
type GenerateReport struct {
	Sites     int
	Summaries []SiteSummary
}

// SiteSummary is one row in the generator's stdout output and is also
// echoed into each site's manifest.json.
type SiteSummary struct {
	Slug     string `json:"slug"`
	Profile  string `json:"profile"`
	Posts    int    `json:"posts"`
	Comments int    `json:"comments"`
	Terms    int    `json:"terms"`
	Media    int    `json:"media"`
}

// Generate emits a corpus on disk per cfg. It is deterministic in cfg.Seed.
func Generate(cfg GenerateConfig) (*GenerateReport, error) {
	if cfg.Overwrite {
		if err := os.RemoveAll(cfg.OutDir); err != nil {
			return nil, fmt.Errorf("clear out dir: %w", err)
		}
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir: %w", err)
	}

	profiles := Profiles()
	report := &GenerateReport{}

	for i := 0; i < cfg.Sites; i++ {
		profile := profiles[i%len(profiles)]

		// Each site gets its own RNG, seeded by (cfg.Seed, i). That way a
		// truncation of --sites still yields identical output for the
		// surviving sites.
		rng := rand.New(rand.NewPCG(uint64(cfg.Seed), uint64(i+1)))

		// Derive a stable site slug so the directory name is greppable but
		// still varies across the corpus.
		dirName := fmt.Sprintf("site-%02d-%s", i+1, profile.Slug)
		siteDir := filepath.Join(cfg.OutDir, dirName)
		if err := os.MkdirAll(siteDir, 0o755); err != nil {
			return nil, fmt.Errorf("create site dir: %w", err)
		}

		site := buildSite(rng, profile, cfg.PostsPerSite, i+1)

		if err := writeWXR(filepath.Join(siteDir, "wxr.xml"), site); err != nil {
			return nil, fmt.Errorf("%s: write wxr: %w", dirName, err)
		}
		if err := writeSQL(filepath.Join(siteDir, "wp_db.sql"), site); err != nil {
			return nil, fmt.Errorf("%s: write sql: %w", dirName, err)
		}
		if err := writeManifest(filepath.Join(siteDir, "manifest.json"), site, cfg.Seed); err != nil {
			return nil, fmt.Errorf("%s: write manifest: %w", dirName, err)
		}

		report.Summaries = append(report.Summaries, SiteSummary{
			Slug: dirName, Profile: profile.Slug,
			Posts: len(site.Posts), Comments: countComments(site),
			Terms: len(site.Terms), Media: countMedia(site),
		})
	}
	report.Sites = cfg.Sites
	return report, nil
}

// site is the in-memory model the writers serialise.
type site struct {
	Profile     Profile
	Index       int       // 1-based
	BaseURL     string    // e.g. https://site-03.example.test
	Title       string    // site title
	Tagline     string    // site tagline
	Generated   time.Time // deterministic timestamp
	WXRVersion  string    // pinned for shape stability
	WPVersion   string    // pinned for shape stability
	Authors     []author
	Terms       []term
	Posts       []post
	MediaItems  []mediaItem
	OptionsRows []optionRow // for wp_options dump
}

type author struct {
	ID          int
	Login       string
	Email       string
	DisplayName string
}

type term struct {
	ID       int
	Taxonomy string // category, post_tag, or custom
	Slug     string
	Name     string
	ParentID int // 0 if root
}

type post struct {
	ID         int
	AuthorID   int
	Type       string // post, page, attachment, product, ...
	Status     string // publish, draft, private
	Slug       string
	Title      string
	Content    string
	Excerpt    string
	Date       time.Time
	ParentID   int
	TermIDs    []int
	Comments   []comment
	Postmeta   []meta
	Attachment *attachmentInfo // non-nil if Type == "attachment"
}

type comment struct {
	ID       int
	ParentID int
	Author   string
	Email    string
	Date     time.Time
	Content  string
	Approved string // "1", "0", "spam", "trash"
}

type meta struct {
	Key   string
	Value string
}

type mediaItem struct {
	PostID int
	URL    string
	MIME   string
	Title  string
}

type attachmentInfo struct {
	URL  string
	MIME string
}

type optionRow struct {
	Name  string
	Value string
}

// buildSite produces the deterministic in-memory model for one site.
func buildSite(rng *rand.Rand, p Profile, basePosts int, idx int) *site {
	// Deterministic epoch: 2024-01-01 + idx days, so dates differ per site
	// but never depend on wall-clock time.
	epoch := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, idx)

	s := &site{
		Profile:    p,
		Index:      idx,
		BaseURL:    fmt.Sprintf("https://site-%02d.example.test", idx),
		Title:      fmt.Sprintf("Example Site %02d — %s", idx, p.Label),
		Tagline:    "Synthetic content; not from any real WordPress install.",
		Generated:  epoch,
		WXRVersion: "1.2",
		WPVersion:  "6.4.3",
	}

	// Authors: 1 admin + a small editorial team.
	authorCount := 1 + rng.IntN(3)
	for i := 0; i < authorCount; i++ {
		s.Authors = append(s.Authors, author{
			ID:          i + 1,
			Login:       fmt.Sprintf("author%d", i+1),
			Email:       fmt.Sprintf("author%d@site-%02d.example.test", i+1, idx),
			DisplayName: fmt.Sprintf("Author %d", i+1),
		})
	}

	// Terms: always one default category, plus a handful of tags.
	s.Terms = append(s.Terms, term{ID: 1, Taxonomy: "category", Slug: "uncategorized", Name: "Uncategorized"})
	for i := 0; i < 5; i++ {
		s.Terms = append(s.Terms, term{
			ID: len(s.Terms) + 1, Taxonomy: "post_tag",
			Slug: fmt.Sprintf("tag-%d", i+1), Name: fmt.Sprintf("Tag %d", i+1),
		})
	}
	if p.HierarchicalTaxa {
		// Two-level category tree.
		parentID := len(s.Terms) + 1
		s.Terms = append(s.Terms, term{
			ID: parentID, Taxonomy: "category",
			Slug: "topics", Name: "Topics",
		})
		for i := 0; i < 3; i++ {
			s.Terms = append(s.Terms, term{
				ID: len(s.Terms) + 1, Taxonomy: "category",
				Slug: fmt.Sprintf("topic-%d", i+1), Name: fmt.Sprintf("Topic %d", i+1),
				ParentID: parentID,
			})
		}
		// Custom taxonomy too, if CPTs are present.
		if len(p.PostTypes) > 2 {
			s.Terms = append(s.Terms, term{
				ID: len(s.Terms) + 1, Taxonomy: "region",
				Slug: "emea", Name: "EMEA",
			})
		}
	}

	target := int(float64(basePosts) * p.PostFactor)
	if target < 3 {
		target = 3 // always emit a few so tests have something to chew on
	}

	nextPostID := 100
	for i := 0; i < target; i++ {
		typ := p.PostTypes[rng.IntN(len(p.PostTypes))]
		ps := post{
			ID:       nextPostID,
			AuthorID: s.Authors[rng.IntN(len(s.Authors))].ID,
			Type:     typ,
			Status:   pickStatus(rng),
			Slug:     fmt.Sprintf("%s-%d", typ, i+1),
			Title:    fmt.Sprintf("%s %d on %s", titleCase(typ), i+1, s.Title),
			Date:     epoch.Add(time.Duration(i) * time.Hour),
		}

		ps.Content = generateContent(rng, p, ps.Title)
		ps.Excerpt = firstSentence(ps.Content)

		// Term assignment: default category + one tag.
		ps.TermIDs = []int{1}
		ps.TermIDs = append(ps.TermIDs, 2+rng.IntN(5))

		// Postmeta varies by profile.
		ps.Postmeta = []meta{
			{Key: "_edit_last", Value: fmt.Sprintf("%d", ps.AuthorID)},
		}
		if p.ACFLike {
			ps.Postmeta = append(ps.Postmeta, acfMeta(rng, i)...)
		}
		if typ == "product" {
			ps.Postmeta = append(ps.Postmeta,
				meta{Key: "_price", Value: fmt.Sprintf("%d.99", 10+rng.IntN(90))},
				meta{Key: "_stock_status", Value: "instock"},
			)
		}

		if p.WithComments && rng.IntN(3) != 0 {
			ps.Comments = generateComments(rng, ps.Date)
		}

		// Attachment posts get a synthetic URL.
		if typ == "attachment" {
			mime := pickMime(rng)
			url := fmt.Sprintf("%s/wp-content/uploads/2024/%02d/asset-%d%s",
				s.BaseURL, 1+rng.IntN(12), i+1, extForMime(mime))
			ps.Attachment = &attachmentInfo{URL: url, MIME: mime}
			s.MediaItems = append(s.MediaItems, mediaItem{
				PostID: ps.ID, URL: url, MIME: mime, Title: ps.Title,
			})
		} else if p.HasMedia && rng.IntN(4) == 0 {
			// Inline media reference: not its own attachment post, just a
			// URL embedded in content (we already wove some in via blocks).
			s.MediaItems = append(s.MediaItems, mediaItem{
				PostID: ps.ID,
				URL:    fmt.Sprintf("%s/wp-content/uploads/2024/01/inline-%d.jpg", s.BaseURL, i+1),
				MIME:   "image/jpeg", Title: ps.Title,
			})
		}

		s.Posts = append(s.Posts, ps)
		nextPostID++
	}

	// A small but realistic options table.
	s.OptionsRows = []optionRow{
		{Name: "siteurl", Value: s.BaseURL},
		{Name: "home", Value: s.BaseURL},
		{Name: "blogname", Value: s.Title},
		{Name: "blogdescription", Value: s.Tagline},
		{Name: "admin_email", Value: s.Authors[0].Email},
		{Name: "template", Value: "twentytwentyfour"},
		{Name: "stylesheet", Value: "twentytwentyfour"},
		{Name: "active_plugins", Value: serializedActivePlugins(p.Plugins)},
		{Name: "WPLANG", Value: ""},
		{Name: "permalink_structure", Value: "/%postname%/"},
	}

	return s
}

func countComments(s *site) int {
	n := 0
	for _, p := range s.Posts {
		n += len(p.Comments)
	}
	return n
}

func countMedia(s *site) int {
	return len(s.MediaItems)
}

func pickStatus(rng *rand.Rand) string {
	switch rng.IntN(10) {
	case 0:
		return "draft"
	case 1:
		return "private"
	default:
		return "publish"
	}
}

func pickMime(rng *rand.Rand) string {
	mimes := []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}
	return mimes[rng.IntN(len(mimes))]
}

func extForMime(m string) string {
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	}
	return ".bin"
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func generateContent(rng *rand.Rand, p Profile, title string) string {
	paragraphs := []string{
		"Lorem ipsum dolor sit amet, consectetur adipiscing elit. Synthetic content for migration tests.",
		"This text is generated by gonext-corpus and is not sourced from any real site.",
		"Pellentesque habitant morbi tristique senectus et netus et malesuada fames ac turpis egestas.",
		"Vestibulum ante ipsum primis in faucibus orci luctus et ultrices posuere cubilia curae.",
		"Sed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium.",
	}
	n := 2 + rng.IntN(4)
	chosen := make([]string, 0, n)
	for i := 0; i < n; i++ {
		chosen = append(chosen, paragraphs[rng.IntN(len(paragraphs))])
	}
	body := strings.Join(chosen, "\n\n")
	if p.Gutenberg {
		b := &strings.Builder{}
		fmt.Fprintf(b, "<!-- wp:heading -->\n<h2>%s</h2>\n<!-- /wp:heading -->\n\n", title)
		for _, para := range chosen {
			b.WriteString("<!-- wp:paragraph -->\n<p>")
			b.WriteString(para)
			b.WriteString("</p>\n<!-- /wp:paragraph -->\n\n")
		}
		// Throw in an image block sometimes.
		if p.HasMedia && rng.IntN(2) == 0 {
			b.WriteString("<!-- wp:image {\"id\":1} -->\n<figure class=\"wp-block-image\"><img src=\"https://example.test/image.jpg\" alt=\"\"/></figure>\n<!-- /wp:image -->\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	if p.HasMedia && rng.IntN(2) == 0 {
		body += "\n\n<img src=\"https://example.test/inline.jpg\" alt=\"\" />"
	}
	return body
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	if len(s) > 140 {
		return s[:140]
	}
	return s
}

func acfMeta(rng *rand.Rand, postIdx int) []meta {
	// A miniature ACF-style payload: scalar, repeater rows, flex content.
	// Real ACF stores the field-key indirection in keys like `_field_name`.
	rows := 1 + rng.IntN(3)
	out := []meta{
		{Key: "hero_headline", Value: fmt.Sprintf("Hero %d", postIdx)},
		{Key: "_hero_headline", Value: "field_hero_headline"},
		{Key: "gallery_items", Value: fmt.Sprintf("%d", rows)},
		{Key: "_gallery_items", Value: "field_gallery_items"},
	}
	for i := 0; i < rows; i++ {
		out = append(out,
			meta{Key: fmt.Sprintf("gallery_items_%d_caption", i), Value: fmt.Sprintf("Caption %d", i+1)},
			meta{Key: fmt.Sprintf("_gallery_items_%d_caption", i), Value: "field_gallery_items_caption"},
		)
	}
	return out
}

func generateComments(rng *rand.Rand, postDate time.Time) []comment {
	n := 1 + rng.IntN(5)
	out := make([]comment, 0, n)
	for i := 0; i < n; i++ {
		parent := 0
		if i > 0 && rng.IntN(3) == 0 {
			parent = out[rng.IntN(i)].ID
		}
		approved := "1"
		if rng.IntN(8) == 0 {
			approved = "0"
		} else if rng.IntN(20) == 0 {
			approved = "spam"
		}
		out = append(out, comment{
			ID:       i + 1,
			ParentID: parent,
			Author:   fmt.Sprintf("commenter-%d", i+1),
			Email:    fmt.Sprintf("c%d@example.test", i+1),
			Date:     postDate.Add(time.Duration(i+1) * time.Hour),
			Content:  "Synthetic comment text.",
			Approved: approved,
		})
	}
	return out
}

// serializedActivePlugins emits a tiny PHP-serialized array shape so that
// importer code paths that look for `a:N:{...}` have something realistic
// to chew on, without us pulling in a full PHP serialiser.
func serializedActivePlugins(plugins []string) string {
	if len(plugins) == 0 {
		return "a:0:{}"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "a:%d:{", len(plugins))
	for i, name := range plugins {
		path := fmt.Sprintf("%s/%s.php", name, name)
		fmt.Fprintf(b, "i:%d;s:%d:\"%s\";", i, len(path), path)
	}
	b.WriteString("}")
	return b.String()
}

// writeManifest serialises a stable site description.
func writeManifest(path string, s *site, seed int64) error {
	doc := map[string]any{
		"slug":              filepath.Base(filepath.Dir(path)),
		"profile":           s.Profile.Slug,
		"profile_label":     s.Profile.Label,
		"seed":              seed,
		"site_index":        s.Index,
		"base_url":          s.BaseURL,
		"title":             s.Title,
		"tagline":           s.Tagline,
		"generated_at":      s.Generated.UTC().Format(time.RFC3339),
		"wxr_version":       s.WXRVersion,
		"wp_version":        s.WPVersion,
		"post_types":        s.Profile.PostTypes,
		"with_comments":     s.Profile.WithComments,
		"hierarchical_taxa": s.Profile.HierarchicalTaxa,
		"acf_present":       s.Profile.ACFLike,
		"gutenberg_blocks":  s.Profile.Gutenberg,
		"has_media":         s.Profile.HasMedia,
		"plugins_assumed":   s.Profile.Plugins,
		"counts": map[string]int{
			"authors":  len(s.Authors),
			"terms":    len(s.Terms),
			"posts":    len(s.Posts),
			"comments": countComments(s),
			"media":    countMedia(s),
			"options":  len(s.OptionsRows),
		},
		"notes": "Generated by tools/migrate-corpus. Content is synthetic; no real WordPress data is included.",
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
