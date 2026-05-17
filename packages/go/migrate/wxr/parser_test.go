package wxr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// openFixture returns an *os.File for a file under testdata/. The
// caller is responsible for closing it.
func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// drain pulls every record from p and returns them. Stops on io.EOF.
func drain(t *testing.T, p *Parser) []Record {
	t.Helper()
	var out []Record
	for {
		rec, err := p.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, rec)
	}
}

func TestParser_Minimal(t *testing.T) {
	f := openFixture(t, "minimal.xml")
	p := NewParser(f)

	site, err := p.Header()
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	if site.Title != "Tiny Blog" {
		t.Errorf("Title = %q, want %q", site.Title, "Tiny Blog")
	}
	if site.Link != "https://tiny.example.com" {
		t.Errorf("Link = %q", site.Link)
	}
	if site.WXRVersion != "1.2" {
		t.Errorf("WXRVersion = %q", site.WXRVersion)
	}
	if site.Language != "en-US" {
		t.Errorf("Language = %q", site.Language)
	}

	recs := drain(t, p)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (1 author + 1 post)", len(recs))
	}

	a, ok := recs[0].(*Author)
	if !ok {
		t.Fatalf("rec[0] is %T, want *Author", recs[0])
	}
	if a.Login != "admin" || a.Email != "admin@tiny.example.com" {
		t.Errorf("author = %+v", a)
	}
	if a.Kind() != KindAuthor {
		t.Errorf("Kind = %q", a.Kind())
	}

	post, ok := recs[1].(*Post)
	if !ok {
		t.Fatalf("rec[1] is %T, want *Post", recs[1])
	}
	if post.Title != "Hello World" {
		t.Errorf("post.Title = %q", post.Title)
	}
	if post.Creator != "admin" {
		t.Errorf("post.Creator = %q", post.Creator)
	}
	if post.PostType != "post" {
		t.Errorf("post.PostType = %q", post.PostType)
	}
	if post.Status != "publish" {
		t.Errorf("post.Status = %q", post.Status)
	}
	// content:encoded should round-trip raw HTML
	want := `<p>Welcome to <strong>WordPress</strong>.</p>`
	if post.Content != want {
		t.Errorf("post.Content = %q, want %q", post.Content, want)
	}

	// Another Next must return EOF.
	_, err = p.Next()
	if !errors.Is(err, io.EOF) {
		t.Errorf("trailing Next: err = %v, want io.EOF", err)
	}
}

func TestParser_Rich(t *testing.T) {
	f := openFixture(t, "rich.xml")
	p := NewParser(f)

	site, err := p.Header()
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	if site.WXRVersion != "1.2" {
		t.Errorf("WXRVersion = %q", site.WXRVersion)
	}

	recs := drain(t, p)
	// Expect: 1 author + 2 categories + 1 tag + 2 posts = 6 records.
	if len(recs) != 6 {
		t.Fatalf("got %d records, want 6", len(recs))
	}

	if _, ok := recs[0].(*Author); !ok {
		t.Errorf("recs[0] = %T, want *Author", recs[0])
	}
	c1, ok := recs[1].(*Category)
	if !ok {
		t.Fatalf("recs[1] = %T, want *Category", recs[1])
	}
	if c1.Nicename != "news" {
		t.Errorf("c1.Nicename = %q", c1.Nicename)
	}
	c2, ok := recs[2].(*Category)
	if !ok {
		t.Fatalf("recs[2] = %T, want *Category", recs[2])
	}
	if c2.Parent != "news" {
		t.Errorf("c2.Parent = %q", c2.Parent)
	}
	tg, ok := recs[3].(*Tag)
	if !ok {
		t.Fatalf("recs[3] = %T, want *Tag", recs[3])
	}
	if tg.Slug != "golang" {
		t.Errorf("tg.Slug = %q", tg.Slug)
	}

	post, ok := recs[4].(*Post)
	if !ok {
		t.Fatalf("recs[4] = %T, want *Post", recs[4])
	}

	if got, want := len(post.Comments), 10; got != want {
		t.Errorf("comments = %d, want %d", got, want)
	}
	if got, want := len(post.Categories), 5; got != want {
		t.Errorf("categories = %d, want %d", got, want)
	}
	if got, want := len(post.Tags), 1; got != want {
		t.Errorf("tags = %d, want %d", got, want)
	}
	if got, want := len(post.Meta), 20; got != want {
		t.Errorf("meta entries = %d, want %d", got, want)
	}

	// Verify a couple of meta values.
	if post.Meta["_thumbnail_id"] != "42" {
		t.Errorf("Meta[_thumbnail_id] = %q", post.Meta["_thumbnail_id"])
	}
	if !strings.Contains(post.Meta["_yoast_seo_desc"], "<strong>HTML</strong>") {
		t.Errorf("Meta[_yoast_seo_desc] = %q (HTML not preserved)", post.Meta["_yoast_seo_desc"])
	}

	// HTML entities inside CDATA should round-trip bit-for-bit.
	wantContent := `<p>This is a <em>rich</em> post with <code>&lt;script&gt;</code> tags &amp; entities preserved.</p>`
	if post.Content != wantContent {
		t.Errorf("content mismatch:\n got: %q\nwant: %q", post.Content, wantContent)
	}

	// Comment 2 is threaded under comment 1.
	if post.Comments[1].Parent != "1" {
		t.Errorf("comment[1].Parent = %q, want 1", post.Comments[1].Parent)
	}
	// Comment 5 is spam; comment 6 is a pingback.
	if post.Comments[4].Approved != "spam" {
		t.Errorf("comment[4].Approved = %q, want spam", post.Comments[4].Approved)
	}
	if post.Comments[5].Type != "pingback" {
		t.Errorf("comment[5].Type = %q, want pingback", post.Comments[5].Type)
	}

	// Attachment is the second post.
	att, ok := recs[5].(*Post)
	if !ok {
		t.Fatalf("recs[5] = %T, want *Post", recs[5])
	}
	if att.PostType != "attachment" {
		t.Errorf("att.PostType = %q", att.PostType)
	}
	if att.AttachmentURL != "https://rich.example.com/wp-content/uploads/2024/03/photo.jpg" {
		t.Errorf("att.AttachmentURL = %q", att.AttachmentURL)
	}
	if att.Parent != "100" {
		t.Errorf("att.Parent = %q, want 100", att.Parent)
	}
}

func TestParser_UnsupportedVersion(t *testing.T) {
	f := openFixture(t, "bad_version.xml")
	p := NewParser(f)
	_, err := p.Header()
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("Header: err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestParser_Malformed(t *testing.T) {
	f := openFixture(t, "malformed.xml")
	p := NewParser(f)
	// The malformed token appears mid-item, so Header might succeed
	// (it scans up to the first <item>) and Next fails — or Header
	// itself catches the issue. Either is acceptable as long as we
	// observe an ErrMalformedXML somewhere and never panic.
	var sawMalformed bool
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
		if !sawMalformed {
			t.Errorf("expected ErrMalformedXML during parse")
		}
	}()

	_, err := p.Header()
	if err != nil {
		if errors.Is(err, ErrMalformedXML) {
			sawMalformed = true
			return
		}
		t.Fatalf("unexpected Header error: %v", err)
	}
	for {
		_, err := p.Next()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if errors.Is(err, ErrMalformedXML) {
			sawMalformed = true
			return
		}
		t.Fatalf("unexpected Next error: %v", err)
	}
}

func TestParser_HeaderTwice(t *testing.T) {
	f := openFixture(t, "minimal.xml")
	p := NewParser(f)
	if _, err := p.Header(); err != nil {
		t.Fatalf("Header: %v", err)
	}
	if _, err := p.Header(); !errors.Is(err, ErrHeaderConsumed) {
		t.Errorf("second Header: err = %v, want ErrHeaderConsumed", err)
	}
}

func TestParser_NextBeforeHeader(t *testing.T) {
	f := openFixture(t, "minimal.xml")
	p := NewParser(f)
	if _, err := p.Next(); !errors.Is(err, ErrHeaderRequired) {
		t.Errorf("Next without Header: err = %v, want ErrHeaderRequired", err)
	}
}

// TestParser_100Posts builds a 100-post WXR document in-memory and
// asserts the parser completes in under one second. The runtime
// budget is generous; this is more of a smoke test against quadratic
// regressions than a microbench.
func TestParser_100Posts(t *testing.T) {
	buf := synthesizeWXR(100, 32) // 100 posts, ~32 chars of content each
	start := time.Now()
	p := NewParser(bytes.NewReader(buf))
	if _, err := p.Header(); err != nil {
		t.Fatalf("Header: %v", err)
	}
	var posts int
	for {
		rec, err := p.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if _, ok := rec.(*Post); ok {
			posts++
		}
	}
	took := time.Since(start)
	if posts != 100 {
		t.Errorf("got %d posts, want 100", posts)
	}
	if took > time.Second {
		t.Errorf("parse took %v, want < 1s", took)
	}
}

// TestParser_StreamingMemory streams a ~50MB synthetic WXR through
// the parser via an io.Pipe (so the document is never resident in
// memory all at once on the reader side) and asserts that the parser's
// own working set stays well under the file size.
//
// We measure HeapAlloc deltas with runtime.ReadMemStats — RSS is not
// portable, but HeapAlloc tracks live Go-side allocations, which is
// the property the streaming design is meant to bound. Any regression
// that buffers the whole stream would push the delta into the tens of
// MB; we cap at 20MB.
//
// The test is skipped under -race because race detection inflates
// allocations by 5-10x and would invalidate the threshold; the
// race-free path still exercises every code path the racy one does.
func TestParser_StreamingMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	if raceEnabled {
		t.Skip("skipping memory threshold check under -race (overhead inflates HeapAlloc)")
	}

	const (
		nPosts       = 2000
		contentBytes = 24 * 1024 // ~50MB total stream
	)

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		streamSynthWXR(pw, nPosts, contentBytes)
	}()

	var before, peak runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	p := NewParser(pr)
	if _, err := p.Header(); err != nil {
		t.Fatalf("Header: %v", err)
	}
	var posts int
	for {
		rec, err := p.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if _, ok := rec.(*Post); ok {
			posts++
		}
		// Sample peak heap every 50 posts. Calling ReadMemStats every
		// iteration would itself perturb the measurement.
		if posts%50 == 0 {
			var s runtime.MemStats
			runtime.ReadMemStats(&s)
			if s.HeapAlloc > peak.HeapAlloc {
				peak = s
			}
		}
	}
	if posts != nPosts {
		t.Errorf("got %d posts, want %d", posts, nPosts)
	}

	const cap = 20 << 20
	delta := int64(peak.HeapAlloc) - int64(before.HeapAlloc)
	if delta > cap {
		t.Errorf("peak heap delta = %d bytes (%.1f MB); want under %d (%.1f MB) — parser may be buffering the whole stream",
			delta, float64(delta)/(1<<20), cap, float64(cap)/(1<<20))
	}
}

// streamSynthWXR writes a valid WXR document directly to w post by
// post. Unlike synthesizeWXR (which returns a []byte), this lets the
// memory test feed the parser without materialising the whole input
// in memory first.
func streamSynthWXR(w io.Writer, n, contentBytes int) {
	io.WriteString(w, `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"
  xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/"
  xmlns:content="http://purl.org/rss/1.0/modules/content/"
  xmlns:dc="http://purl.org/dc/elements/1.1/"
  xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>Synth</title>
<link>https://synth.example.com</link>
<description>Synthetic</description>
<wp:wxr_version>1.2</wp:wxr_version>
<wp:base_site_url>https://synth.example.com</wp:base_site_url>
<wp:base_blog_url>https://synth.example.com</wp:base_blog_url>
<wp:author>
  <wp:author_id>1</wp:author_id>
  <wp:author_login><![CDATA[synth]]></wp:author_login>
  <wp:author_email><![CDATA[s@synth.example.com]]></wp:author_email>
  <wp:author_display_name><![CDATA[Synth]]></wp:author_display_name>
  <wp:author_first_name><![CDATA[Syn]]></wp:author_first_name>
  <wp:author_last_name><![CDATA[Th]]></wp:author_last_name>
</wp:author>
`)
	pattern := []byte("abcdefghijklmnop")
	filler := bytes.Repeat(pattern, (contentBytes+len(pattern)-1)/len(pattern))[:contentBytes]
	for i := 1; i <= n; i++ {
		fmt.Fprintf(w, `<item>
<title>Post %d</title>
<link>https://synth.example.com/p/%d/</link>
<dc:creator><![CDATA[synth]]></dc:creator>
<content:encoded><![CDATA[`, i, i)
		w.Write(filler)
		fmt.Fprintf(w, `]]></content:encoded>
<wp:post_id>%d</wp:post_id>
<wp:post_type><![CDATA[post]]></wp:post_type>
<wp:status><![CDATA[publish]]></wp:status>
</item>
`, i)
	}
	io.WriteString(w, `</channel>
</rss>`)
}

// synthesizeWXR builds a valid WXR 1.2 document with n posts whose
// content:encoded payload is filler bytes of size contentBytes. Used
// for performance and memory tests; the output is parseable but not
// otherwise interesting.
func synthesizeWXR(n, contentBytes int) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"
  xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/"
  xmlns:content="http://purl.org/rss/1.0/modules/content/"
  xmlns:dc="http://purl.org/dc/elements/1.1/"
  xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>Synth</title>
<link>https://synth.example.com</link>
<description>Synthetic</description>
<wp:wxr_version>1.2</wp:wxr_version>
<wp:base_site_url>https://synth.example.com</wp:base_site_url>
<wp:base_blog_url>https://synth.example.com</wp:base_blog_url>
<wp:author>
  <wp:author_id>1</wp:author_id>
  <wp:author_login><![CDATA[synth]]></wp:author_login>
  <wp:author_email><![CDATA[s@synth.example.com]]></wp:author_email>
  <wp:author_display_name><![CDATA[Synth]]></wp:author_display_name>
  <wp:author_first_name><![CDATA[Syn]]></wp:author_first_name>
  <wp:author_last_name><![CDATA[Th]]></wp:author_last_name>
</wp:author>
`)
	// Repeat a 16-byte pattern. The pattern intentionally avoids
	// characters that need XML escaping inside CDATA (i.e. nothing
	// containing `]]>`).
	pattern := []byte("abcdefghijklmnop")
	filler := bytes.Repeat(pattern, (contentBytes+len(pattern)-1)/len(pattern))[:contentBytes]
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, `<item>
<title>Post %d</title>
<link>https://synth.example.com/p/%d/</link>
<dc:creator><![CDATA[synth]]></dc:creator>
<content:encoded><![CDATA[`, i, i)
		b.Write(filler)
		fmt.Fprintf(&b, `]]></content:encoded>
<wp:post_id>%d</wp:post_id>
<wp:post_type><![CDATA[post]]></wp:post_type>
<wp:status><![CDATA[publish]]></wp:status>
</item>
`, i)
	}
	b.WriteString(`</channel>
</rss>`)
	return b.Bytes()
}

// TestParser_EmptyChannel verifies a WXR with no items returns EOF
// cleanly after the preamble drains.
func TestParser_EmptyChannel(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>Empty</title>
<link>https://empty.example.com</link>
<description>None</description>
<wp:wxr_version>1.2</wp:wxr_version>
</channel>
</rss>`
	p := NewParser(strings.NewReader(doc))
	site, err := p.Header()
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	if site.Title != "Empty" {
		t.Errorf("Title = %q", site.Title)
	}
	if _, err := p.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next: err = %v, want io.EOF", err)
	}
}

// TestParser_WXR13 verifies that the 1.3 namespace is accepted.
func TestParser_WXR13(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0" xmlns:wp="http://wordpress.org/export/1.3/">
<channel>
<title>v13</title>
<link>https://v13.example.com</link>
<description>13</description>
<wp:wxr_version>1.3</wp:wxr_version>
</channel>
</rss>`
	p := NewParser(strings.NewReader(doc))
	site, err := p.Header()
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	if site.WXRVersion != "1.3" {
		t.Errorf("WXRVersion = %q", site.WXRVersion)
	}
}

// TestParser_PanicSafety wraps every public call in a recover. None of
// them should ever panic, even on absurd input.
func TestParser_PanicSafety(t *testing.T) {
	inputs := [][]byte{
		nil,
		[]byte(""),
		[]byte("not xml"),
		[]byte("<rss"),
		[]byte("<rss></rss>"),
	}
	for i, in := range inputs {
		t.Run(fmt.Sprintf("input_%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on input %d: %v", i, r)
				}
			}()
			p := NewParser(bytes.NewReader(in))
			_, _ = p.Header()
			_, _ = p.Next()
		})
	}
}
