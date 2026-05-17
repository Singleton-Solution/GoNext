// Package wxr implements a streaming parser for the WordPress eXtended
// RSS (WXR) export format, the file produced by every WordPress site's
// Tools → Export screen.
//
// WXR is RSS 2.0 with a stack of WordPress-specific namespaces glued on
// top. A typical real-world export is anywhere from a few KB (a fresh
// blog) to hundreds of MB (long-lived sites with thousands of posts and
// attachments). The parser here is written for that upper end: it walks
// the document with xml.Decoder.Token so the whole stream is never
// resident in memory, and it emits typed records one at a time.
//
// Two-phase API:
//
//  1. Construct a Parser around an io.Reader.
//  2. Call Header() once to consume the channel preamble (Site, plus
//     authors/categories/tags expressed before the first <item>).
//  3. Loop on Next() until io.EOF. Each call returns a Record: an
//     *Author, *Category, *Tag, or *Post. Posts include comments and
//     meta inline.
//
// The parser only supports WXR versions 1.2 and 1.3 — anything else
// returns ErrUnsupportedVersion before any records are emitted. CDATA
// inside content:encoded is preserved verbatim (raw HTML round-trips).
//
// See P5 phase migration plan and issue #153.
package wxr
