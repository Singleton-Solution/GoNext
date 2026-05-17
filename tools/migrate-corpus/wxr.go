package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"time"
)

// WXR is intentionally shaped after WordPress eXtended RSS (WXR 1.2). We use
// encoding/xml so the output is well-formed by construction. We do NOT
// reach for a third-party PHP-WXR library on purpose — the goal here is a
// minimal, reproducible *shape* the importer can parse, not byte-equivalence
// with `wp export`.

type wxrChannel struct {
	XMLName     xml.Name `xml:"channel"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate"`
	Language    string   `xml:"language"`
	WXRVersion  string   `xml:"http://wordpress.org/export/1.2/ wxr_version"`
	BaseSiteURL string   `xml:"http://wordpress.org/export/1.2/ base_site_url"`
	BaseBlogURL string   `xml:"http://wordpress.org/export/1.2/ base_blog_url"`

	Authors []wxrAuthor `xml:"http://wordpress.org/export/1.2/ author"`
	Terms   []wxrTerm   `xml:"-"` // emitted manually because tag name varies by taxonomy class

	Items []wxrItem `xml:"item"`
}

type wxrAuthor struct {
	XMLName       xml.Name `xml:"http://wordpress.org/export/1.2/ author"`
	ID            int      `xml:"http://wordpress.org/export/1.2/ author_id"`
	Login         string   `xml:"http://wordpress.org/export/1.2/ author_login"`
	Email         string   `xml:"http://wordpress.org/export/1.2/ author_email"`
	DisplayName   string   `xml:"http://wordpress.org/export/1.2/ author_display_name"`
	FirstName     string   `xml:"http://wordpress.org/export/1.2/ author_first_name"`
	LastName      string   `xml:"http://wordpress.org/export/1.2/ author_last_name"`
}

type wxrTerm struct {
	Taxonomy string
	Slug     string
	Parent   string // empty if root
	Name     string
}

type wxrItem struct {
	XMLName  xml.Name `xml:"item"`
	Title    string   `xml:"title"`
	Link     string   `xml:"link"`
	PubDate  string   `xml:"pubDate"`
	Creator  string   `xml:"http://purl.org/dc/elements/1.1/ creator"`
	GUID     wxrGUID  `xml:"guid"`
	Desc     string   `xml:"description"`
	Content  wxrCDATA `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Excerpt  wxrCDATA `xml:"http://wordpress.org/export/1.2/excerpt/ encoded"`

	PostID       int    `xml:"http://wordpress.org/export/1.2/ post_id"`
	PostDate     string `xml:"http://wordpress.org/export/1.2/ post_date"`
	PostDateGMT  string `xml:"http://wordpress.org/export/1.2/ post_date_gmt"`
	CommentStat  string `xml:"http://wordpress.org/export/1.2/ comment_status"`
	PingStat     string `xml:"http://wordpress.org/export/1.2/ ping_status"`
	PostName     string `xml:"http://wordpress.org/export/1.2/ post_name"`
	Status       string `xml:"http://wordpress.org/export/1.2/ status"`
	PostParent   int    `xml:"http://wordpress.org/export/1.2/ post_parent"`
	MenuOrder    int    `xml:"http://wordpress.org/export/1.2/ menu_order"`
	PostType     string `xml:"http://wordpress.org/export/1.2/ post_type"`
	PostPassword string `xml:"http://wordpress.org/export/1.2/ post_password"`
	IsSticky     int    `xml:"http://wordpress.org/export/1.2/ is_sticky"`
	AttachURL    string `xml:"http://wordpress.org/export/1.2/ attachment_url,omitempty"`

	Categories []wxrCategoryRef `xml:"category"`
	Postmeta   []wxrPostmeta    `xml:"http://wordpress.org/export/1.2/ postmeta"`
	Comments   []wxrComment     `xml:"http://wordpress.org/export/1.2/ comment"`
}

type wxrGUID struct {
	XMLName     xml.Name `xml:"guid"`
	IsPermalink string   `xml:"isPermaLink,attr"`
	Value       string   `xml:",chardata"`
}

type wxrCDATA struct {
	Value string `xml:",cdata"`
}

type wxrCategoryRef struct {
	XMLName  xml.Name `xml:"category"`
	Domain   string   `xml:"domain,attr"`
	Nicename string   `xml:"nicename,attr"`
	Value    string   `xml:",chardata"`
}

type wxrPostmeta struct {
	XMLName xml.Name `xml:"http://wordpress.org/export/1.2/ postmeta"`
	Key     wxrCDATA `xml:"http://wordpress.org/export/1.2/ meta_key"`
	Value   wxrCDATA `xml:"http://wordpress.org/export/1.2/ meta_value"`
}

type wxrComment struct {
	XMLName  xml.Name `xml:"http://wordpress.org/export/1.2/ comment"`
	ID       int      `xml:"http://wordpress.org/export/1.2/ comment_id"`
	Author   string   `xml:"http://wordpress.org/export/1.2/ comment_author"`
	Email    string   `xml:"http://wordpress.org/export/1.2/ comment_author_email"`
	Date     string   `xml:"http://wordpress.org/export/1.2/ comment_date"`
	DateGMT  string   `xml:"http://wordpress.org/export/1.2/ comment_date_gmt"`
	Content  wxrCDATA `xml:"http://wordpress.org/export/1.2/ comment_content"`
	Approved string   `xml:"http://wordpress.org/export/1.2/ comment_approved"`
	Type     string   `xml:"http://wordpress.org/export/1.2/ comment_type"`
	Parent   int      `xml:"http://wordpress.org/export/1.2/ comment_parent"`
	UserID   int      `xml:"http://wordpress.org/export/1.2/ comment_user_id"`
}

// writeWXR builds a WXR-shape XML file for s.
//
// We write the XML by hand at the top level (the <rss> wrapper and the term
// elements that vary by class), then defer to encoding/xml for the per-item
// body. This keeps the parser-facing output well-formed while staying simple.
func writeWXR(path string, s *site) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	pubDate := s.Generated.UTC().Format(time.RFC1123Z)

	// Manually write the rss header + namespace declarations.
	header := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"
     xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/"
     xmlns:content="http://purl.org/rss/1.0/modules/content/"
     xmlns:wfw="http://wellformedweb.org/CommentAPI/"
     xmlns:dc="http://purl.org/dc/elements/1.1/"
     xmlns:wp="http://wordpress.org/export/1.2/">
  <channel>
`
	if _, err := f.WriteString(header); err != nil {
		return err
	}
	chanScalars := fmt.Sprintf(`    <title>%s</title>
    <link>%s</link>
    <description>%s</description>
    <pubDate>%s</pubDate>
    <language>en-US</language>
    <wp:wxr_version>%s</wp:wxr_version>
    <wp:base_site_url>%s</wp:base_site_url>
    <wp:base_blog_url>%s</wp:base_blog_url>
`,
		xmlEscape(s.Title), xmlEscape(s.BaseURL), xmlEscape(s.Tagline),
		pubDate, s.WXRVersion, xmlEscape(s.BaseURL), xmlEscape(s.BaseURL),
	)
	if _, err := f.WriteString(chanScalars); err != nil {
		return err
	}

	// Authors.
	for _, a := range s.Authors {
		fmt.Fprintf(f, `    <wp:author>
      <wp:author_id>%d</wp:author_id>
      <wp:author_login>%s</wp:author_login>
      <wp:author_email>%s</wp:author_email>
      <wp:author_display_name><![CDATA[%s]]></wp:author_display_name>
      <wp:author_first_name><![CDATA[]]></wp:author_first_name>
      <wp:author_last_name><![CDATA[]]></wp:author_last_name>
    </wp:author>
`, a.ID, xmlEscape(a.Login), xmlEscape(a.Email), a.DisplayName)
	}

	// Terms. WordPress emits <wp:category>, <wp:tag>, or <wp:term> depending
	// on taxonomy. We approximate with <wp:term> for everything — the
	// importer treats them uniformly anyway.
	for _, t := range s.Terms {
		parent := ""
		if t.ParentID != 0 {
			for _, pt := range s.Terms {
				if pt.ID == t.ParentID {
					parent = pt.Slug
					break
				}
			}
		}
		fmt.Fprintf(f, `    <wp:term>
      <wp:term_id>%d</wp:term_id>
      <wp:term_taxonomy><![CDATA[%s]]></wp:term_taxonomy>
      <wp:term_slug><![CDATA[%s]]></wp:term_slug>
      <wp:term_parent><![CDATA[%s]]></wp:term_parent>
      <wp:term_name><![CDATA[%s]]></wp:term_name>
    </wp:term>
`, t.ID, t.Taxonomy, t.Slug, parent, t.Name)
	}

	// Items. We use encoding/xml here so escaping of CDATA / attrs is correct.
	enc := xml.NewEncoder(f)
	enc.Indent("    ", "  ")
	for _, p := range s.Posts {
		item := wxrItem{
			Title:        p.Title,
			Link:         fmt.Sprintf("%s/?p=%d", s.BaseURL, p.ID),
			PubDate:      p.Date.UTC().Format(time.RFC1123Z),
			Creator:      authorLogin(s, p.AuthorID),
			GUID:         wxrGUID{IsPermalink: "false", Value: fmt.Sprintf("%s/?p=%d", s.BaseURL, p.ID)},
			Desc:         "",
			Content:      wxrCDATA{Value: p.Content},
			Excerpt:      wxrCDATA{Value: p.Excerpt},
			PostID:       p.ID,
			PostDate:     p.Date.UTC().Format("2006-01-02 15:04:05"),
			PostDateGMT:  p.Date.UTC().Format("2006-01-02 15:04:05"),
			CommentStat:  ternaryComment(s.Profile.WithComments),
			PingStat:     "open",
			PostName:     p.Slug,
			Status:       p.Status,
			PostParent:   p.ParentID,
			MenuOrder:    0,
			PostType:     p.Type,
			PostPassword: "",
			IsSticky:     0,
		}
		if p.Attachment != nil {
			item.AttachURL = p.Attachment.URL
		}
		for _, tid := range p.TermIDs {
			if t := findTerm(s, tid); t != nil {
				item.Categories = append(item.Categories, wxrCategoryRef{
					Domain:   t.Taxonomy,
					Nicename: t.Slug,
					Value:    t.Name,
				})
			}
		}
		for _, m := range p.Postmeta {
			item.Postmeta = append(item.Postmeta, wxrPostmeta{
				Key:   wxrCDATA{Value: m.Key},
				Value: wxrCDATA{Value: m.Value},
			})
		}
		for _, c := range p.Comments {
			item.Comments = append(item.Comments, wxrComment{
				ID:       c.ID,
				Author:   c.Author,
				Email:    c.Email,
				Date:     c.Date.UTC().Format("2006-01-02 15:04:05"),
				DateGMT:  c.Date.UTC().Format("2006-01-02 15:04:05"),
				Content:  wxrCDATA{Value: c.Content},
				Approved: c.Approved,
				Type:     "",
				Parent:   c.ParentID,
				UserID:   0,
			})
		}
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	if err := enc.Flush(); err != nil {
		return err
	}

	// Newline + close tags.
	if _, err := f.WriteString("\n  </channel>\n</rss>\n"); err != nil {
		return err
	}
	return nil
}

func ternaryComment(allow bool) string {
	if allow {
		return "open"
	}
	return "closed"
}

func authorLogin(s *site, id int) string {
	for _, a := range s.Authors {
		if a.ID == id {
			return a.Login
		}
	}
	return "unknown"
}

func findTerm(s *site, id int) *term {
	for i := range s.Terms {
		if s.Terms[i].ID == id {
			return &s.Terms[i]
		}
	}
	return nil
}

func xmlEscape(s string) string {
	// encoding/xml.EscapeText is preferable but writes to a writer; for a
	// handful of header strings the manual escape is fine and keeps the
	// header generation a plain string concat.
	replacer := []struct{ old, new string }{
		{"&", "&amp;"},
		{"<", "&lt;"},
		{">", "&gt;"},
		{"\"", "&quot;"},
		{"'", "&apos;"},
	}
	out := s
	for _, r := range replacer {
		out = replaceAll(out, r.old, r.new)
	}
	return out
}

// replaceAll is a hand-rolled strings.ReplaceAll to keep this file's imports
// narrow; the strings package is already pulled into generator.go.
func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
