// Package main is the gonext-seo example plugin. This file holds the
// pure-Go domain logic — Post type, BuildHeadHTML, BuildJSONLD,
// ComputeSEOScore — that does NOT touch the WASM ABI.
//
// Splitting the domain helpers out of the TinyGo-only main.go lets the
// Go-side dummy host bus (used by the example's tests) import and call
// the same functions the WASM build calls, so the test contract proves
// the exact code the WASM blob runs.
//
// This file has no build tag. main.go has //go:build tinygo, so when
// stock Go compiles this package (during go test ./examples/plugins/seo)
// only domain.go is included — and the helpers below are reachable.
package main

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Post is the minimal post shape the plugin works with. The full shape
// the platform exposes (revisions, authors, categories) is intentionally
// elided — an example plugin should look like real-world plugin code,
// not like a stress test.
type Post struct {
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
	Content string `json:"content"`
	URL     string `json:"url"`
	Image   string `json:"image"`
	Brand   string `json:"brand"`
	Author  string `json:"author"`
	PubDate string `json:"pub_date"`
}

// postFromArgs pulls the first arg out of the args array and decodes
// it as a Post. Both hook payloads in this plugin ship the post as the
// first argument; if it's missing or shaped wrong we return a zero
// Post so the downstream HTML builder still produces a well-formed (if
// empty-content) document.
func postFromArgs(args []interface{}) Post {
	if len(args) == 0 {
		return Post{}
	}
	raw, err := json.Marshal(args[0])
	if err != nil {
		return Post{}
	}
	var p Post
	_ = json.Unmarshal(raw, &p)
	return p
}

// htmlEscape applies the minimum escaping required for an HTML attribute
// value or text node. We deliberately keep this hand-rolled rather than
// reach for html/template — TinyGo's reflection-heavy template stack
// inflates the WASM bundle by ~200 KB for no benefit here.
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstParagraph returns the leading paragraph of content, stripped of
// any HTML tags. Used as the fallback description when the post has no
// excerpt. We stop at the first double newline OR </p> tag, whichever
// comes first — both signal a paragraph break in the conventions the
// editor emits.
func firstParagraph(content string) string {
	idx := len(content)
	if i := strings.Index(content, "</p>"); i >= 0 && i < idx {
		idx = i
	}
	if i := strings.Index(content, "\n\n"); i >= 0 && i < idx {
		idx = i
	}
	chunk := content[:idx]
	return stripTags(chunk)
}

// stripTags removes everything between < and > and collapses runs of
// whitespace into single spaces. Not a full HTML parser — it doesn't
// need to be. The output is fed into meta-description, which the
// search engines treat as plain text anyway.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	prevSpace := false
	for _, r := range s {
		switch r {
		case '<':
			depth++
			continue
		case '>':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 {
			continue
		}
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// truncate clamps s to n runes, appending an ellipsis if it was cut. We
// truncate the meta description to 160 chars — the standard SERP cap —
// and og:description to the same. Twitter ignores past 200, so 160 is
// the safe lower bound.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

// BuildTitle composes the SEO title from the post title + the site
// brand. The "{title} | {brand}" pattern is the WordPress+Yoast default;
// operators can override the separator later via a setting.
func BuildTitle(p Post) string {
	if p.Title == "" {
		return p.Brand
	}
	if p.Brand == "" {
		return p.Title
	}
	return p.Title + " | " + p.Brand
}

// BuildDescription returns the description to use across meta tags.
// Preference order: explicit excerpt > first paragraph of content >
// post title (fallback). All outputs are truncated to 160 chars.
func BuildDescription(p Post) string {
	desc := p.Excerpt
	if desc == "" {
		desc = firstParagraph(p.Content)
	}
	if desc == "" {
		desc = p.Title
	}
	return truncate(desc, 160)
}

// BuildHeadHTML assembles every meta tag the plugin emits into <head>.
// The output is ordered for human readability: title, description,
// canonical, opengraph block, twitter block, JSON-LD.
func BuildHeadHTML(p Post) string {
	var b strings.Builder

	title := BuildTitle(p)
	desc := BuildDescription(p)

	b.WriteString("<title>")
	b.WriteString(htmlEscape(title))
	b.WriteString("</title>\n")

	if desc != "" {
		b.WriteString(`<meta name="description" content="`)
		b.WriteString(htmlEscape(desc))
		b.WriteString("\">\n")
	}

	if p.URL != "" {
		b.WriteString(`<link rel="canonical" href="`)
		b.WriteString(htmlEscape(p.URL))
		b.WriteString("\">\n")
	}

	// OpenGraph block — Facebook, LinkedIn, Slack, Discord all consume it.
	b.WriteString(`<meta property="og:type" content="article">` + "\n")
	b.WriteString(`<meta property="og:title" content="`)
	b.WriteString(htmlEscape(title))
	b.WriteString("\">\n")
	if desc != "" {
		b.WriteString(`<meta property="og:description" content="`)
		b.WriteString(htmlEscape(desc))
		b.WriteString("\">\n")
	}
	if p.URL != "" {
		b.WriteString(`<meta property="og:url" content="`)
		b.WriteString(htmlEscape(p.URL))
		b.WriteString("\">\n")
	}
	if p.Image != "" {
		b.WriteString(`<meta property="og:image" content="`)
		b.WriteString(htmlEscape(p.Image))
		b.WriteString("\">\n")
	}

	// Twitter card — separate property family. summary_large_image is
	// the right card when we have a hero image; without one, X falls
	// back to "summary" automatically.
	b.WriteString(`<meta name="twitter:card" content="summary_large_image">` + "\n")
	b.WriteString(`<meta name="twitter:title" content="`)
	b.WriteString(htmlEscape(title))
	b.WriteString("\">\n")
	if desc != "" {
		b.WriteString(`<meta name="twitter:description" content="`)
		b.WriteString(htmlEscape(desc))
		b.WriteString("\">\n")
	}
	if p.Image != "" {
		b.WriteString(`<meta name="twitter:image" content="`)
		b.WriteString(htmlEscape(p.Image))
		b.WriteString("\">\n")
	}

	b.WriteString(BuildJSONLD(p))
	b.WriteString("\n")
	return b.String()
}

// BuildJSONLD produces the schema.org Article JSON-LD block.
//
// The output is a single <script type="application/ld+json">...</script>
// tag. Search engines parse it for rich-result eligibility; we emit the
// fields Google's Article rich-result spec calls out as required or
// strongly recommended (headline, image, datePublished, author,
// publisher).
func BuildJSONLD(p Post) string {
	payload := map[string]interface{}{
		"@context":    "https://schema.org",
		"@type":       "Article",
		"headline":    BuildTitle(p),
		"description": BuildDescription(p),
	}
	if p.URL != "" {
		payload["mainEntityOfPage"] = p.URL
	}
	if p.Image != "" {
		payload["image"] = p.Image
	}
	if p.Author != "" {
		payload["author"] = map[string]string{
			"@type": "Person",
			"name":  p.Author,
		}
	}
	if p.PubDate != "" {
		payload["datePublished"] = p.PubDate
	}
	if p.Brand != "" {
		payload["publisher"] = map[string]interface{}{
			"@type": "Organization",
			"name":  p.Brand,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json">` + string(body) + `</script>`
}

// ComputeSEOScore is the headline scoring function. The score is the
// sum of the per-check weights; perfect content scores 100. Plugin
// authors typically expose this as a Yoast-style traffic-light: green
// >= 80, amber 50..79, red < 50.
//
// The scoring rubric is intentionally simple — operator and tutorial
// readers can extend it. The shape matches what Yoast checks publicly.
//
// Weight distribution (sums to 100):
//
//	title present + 30..60 chars  → 25 pts
//	description 70..160 chars     → 25 pts
//	hero image set                → 15 pts
//	canonical URL set             → 10 pts
//	body has >= 300 words         → 15 pts
//	author + pub date set         → 10 pts
func ComputeSEOScore(p Post) int {
	score := 0

	titleLen := len(p.Title)
	if titleLen > 0 {
		score += 5
		if titleLen >= 30 && titleLen <= 60 {
			score += 20
		} else if titleLen >= 15 {
			score += 10
		}
	}

	desc := p.Excerpt
	if desc == "" {
		desc = firstParagraph(p.Content)
	}
	dl := len(desc)
	if dl > 0 {
		score += 5
		if dl >= 70 && dl <= 160 {
			score += 20
		} else if dl >= 40 {
			score += 10
		}
	}

	if p.Image != "" {
		score += 15
	}
	if p.URL != "" {
		score += 10
	}

	wordCount := len(strings.Fields(stripTags(p.Content)))
	if wordCount >= 300 {
		score += 15
	} else if wordCount >= 100 {
		score += 5
	}

	if p.Author != "" {
		score += 5
	}
	if p.PubDate != "" {
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score
}
