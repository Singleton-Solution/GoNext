package wxr

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Parser consumes a WXR document from r and emits typed records.
//
// The decoder walks tokens lazily, so the underlying io.Reader can be
// arbitrarily large — only the currently-decoded element and its
// children are buffered. Parsers are not safe for concurrent use.
//
// Usage:
//
//	p := wxr.NewParser(file)
//	site, err := p.Header()
//	if err != nil { ... }
//	for {
//	    rec, err := p.Next()
//	    if errors.Is(err, io.EOF) { break }
//	    if err != nil { return err }
//	    // type-switch on rec
//	}
type Parser struct {
	dec *xml.Decoder

	headerDone bool  // true after a successful Header()
	site       *Site // captured during Header

	// preamble is a small queue of records (authors, categories, tags)
	// that were lexically present before the first <item>. Header
	// returns them queued up so Next can drain them before walking
	// items.
	preamble []Record

	// pendingItem is the xml.StartElement of the first <item> we saw
	// while walking the channel preamble. Next() decodes it on its
	// first invocation, before continuing to subsequent items.
	pendingItem *xml.StartElement

	// channelClosed is set when Header saw </channel> before any
	// <item>. Next() then returns io.EOF immediately.
	channelClosed bool
}

// NewParser wires an xml.Decoder onto r. The decoder's Strict flag is
// left at its default (true) but Entity is empty — WXR doesn't declare
// custom entities, only HTML-ish content inside CDATA.
func NewParser(r io.Reader) *Parser {
	dec := xml.NewDecoder(r)
	// CharsetReader is left nil. WordPress always emits UTF-8 (the WP
	// export code hard-codes it). If a file declares another encoding
	// in its XML decl the decoder will refuse — that's the right
	// behaviour: silent transcoding has burned every WP migration tool
	// at some point.
	return &Parser{dec: dec}
}

// Header consumes the <channel> preamble up to (but not including) the
// first <item>. It returns the Site and queues any Author / Category /
// Tag records to be drained by Next.
//
// Calling Header twice returns ErrHeaderConsumed. Header must be called
// before Next.
func (p *Parser) Header() (*Site, error) {
	if p.headerDone {
		return nil, ErrHeaderConsumed
	}

	var (
		site        Site
		versionSeen bool
	)

	// We scan tokens until we encounter the first <item> start tag, at
	// which point we put it back via the decoder's token rewind trick:
	// xml.Decoder doesn't support pushback directly, so instead we
	// stash a state flag and let Next() pick up from the current
	// position assuming the caller has moved past <channel>'s opening.
	for {
		tok, err := p.dec.Token()
		if err != nil {
			if err == io.EOF {
				// EOF inside header is unambiguously malformed: a WXR
				// file always closes </channel></rss>.
				return nil, fmt.Errorf("%w: unexpected EOF before first item", ErrMalformedXML)
			}
			return nil, wrapXMLErr(err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			// <rss> and <channel> are pure containers — we descend
			// through them without doing anything else. The real work
			// happens on their direct children, which the next loop
			// iteration will pick up.
			if local == "rss" || local == "channel" {
				continue
			}
			// <item> marks the end of the preamble. We've already
			// passed the channel preamble; stash the start element
			// so Next() decodes this item on its first call.
			if local == "item" {
				if !versionSeen {
					return nil, fmt.Errorf("%w: missing wp:wxr_version", ErrMalformedXML)
				}
				p.site = &site
				p.headerDone = true
				cp := t.Copy()
				p.pendingItem = &cp
				return &site, nil
			}

			if err := p.decodeChannelChild(&site, &t, &versionSeen); err != nil {
				return nil, err
			}

		case xml.EndElement:
			if t.Name.Local == "channel" {
				// Channel closed before any item — file has no posts.
				// Still valid; Header returns Site, Next returns EOF.
				if !versionSeen {
					return nil, fmt.Errorf("%w: missing wp:wxr_version", ErrMalformedXML)
				}
				p.site = &site
				p.headerDone = true
				p.channelClosed = true
				return &site, nil
			}
		}
	}
}

// decodeChannelChild dispatches a single direct child of <channel> in
// the preamble. Recognised elements populate site or append to the
// preamble queue; unknown elements are skipped without error.
func (p *Parser) decodeChannelChild(site *Site, start *xml.StartElement, versionSeen *bool) error {
	switch start.Name.Local {
	case "title":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.Title = s
	case "link":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		// <link> appears for both the site and inside each item; here
		// we're guaranteed to be at channel level. Only set once: the
		// first <link> child of <channel> is the site link.
		if site.Link == "" {
			site.Link = s
		}
	case "description":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.Description = s
	case "pubDate":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.PubDate = s
	case "language":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.Language = s
	case "generator":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.GeneratorVersion = s
	case "base_site_url":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.BaseSiteURL = s
	case "base_blog_url":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.BaseBlogURL = s
	case "wxr_version":
		s, err := p.decodeText(start)
		if err != nil {
			return err
		}
		site.WXRVersion = s
		*versionSeen = true
		if !supportedVersion(s) {
			return fmt.Errorf("%w: %q", ErrUnsupportedVersion, s)
		}
	case "author":
		a, err := p.decodeAuthor(start)
		if err != nil {
			return err
		}
		p.preamble = append(p.preamble, a)
	case "category":
		// Channel-level <wp:category> is the term definition. Item-
		// level <category> is a term *reference* on a post. Same
		// local name, different shape — we're at channel level here.
		c, err := p.decodeCategory(start)
		if err != nil {
			return err
		}
		p.preamble = append(p.preamble, c)
	case "tag":
		t, err := p.decodeTag(start)
		if err != nil {
			return err
		}
		p.preamble = append(p.preamble, t)
	default:
		// Unknown channel-level element: skip its entire subtree so
		// we don't accidentally consume the </channel> closer.
		if err := p.dec.Skip(); err != nil {
			return wrapXMLErr(err)
		}
	}
	return nil
}

// Next returns the next typed record in the stream, or io.EOF when the
// document is exhausted.
//
// Records appear in this order:
//
//  1. Authors / Categories / Tags from the channel preamble, in source
//     order (queued by Header).
//  2. Posts in source order; comments and meta are bundled inside.
//
// Returns ErrHeaderRequired if called before Header.
func (p *Parser) Next() (Record, error) {
	if !p.headerDone {
		return nil, ErrHeaderRequired
	}
	// Drain queued preamble records first.
	if len(p.preamble) > 0 {
		rec := p.preamble[0]
		p.preamble = p.preamble[1:]
		return rec, nil
	}
	if p.channelClosed {
		return nil, io.EOF
	}

	// If Header stashed an item start, decode it first.
	if p.pendingItem != nil {
		start := p.pendingItem
		p.pendingItem = nil
		post, err := p.decodeItem(start)
		if err != nil {
			return nil, err
		}
		return post, nil
	}

	// Otherwise, walk forward looking for the next <item> or end of
	// channel.
	for {
		tok, err := p.dec.Token()
		if err != nil {
			if err == io.EOF {
				// Reached real EOF without a </channel>. That's
				// malformed, but we surface EOF to the caller — every
				// importer prefers a clean termination signal here and
				// any structural damage will already have been caught
				// by an inner decode call.
				return nil, io.EOF
			}
			return nil, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "item" {
				cp := t.Copy()
				return p.decodeItem(&cp)
			}
			// Stray channel-level element after preamble (rare but
			// possible if the export tool emits trailing wp:category
			// definitions). Try to handle it; if unknown, skip.
			var versionSeen = true
			if err := p.decodeChannelChild(p.site, &t, &versionSeen); err != nil {
				return nil, err
			}
			// If that produced a preamble record, return it.
			if len(p.preamble) > 0 {
				rec := p.preamble[0]
				p.preamble = p.preamble[1:]
				return rec, nil
			}
		case xml.EndElement:
			if t.Name.Local == "channel" || t.Name.Local == "rss" {
				return nil, io.EOF
			}
		}
	}
}

// --- internal decoders ---------------------------------------------------

// decodeText reads the character data inside an element and returns it
// as a string. The decoder is positioned past the matching close tag.
// Nested elements are tolerated (their text is concatenated) which
// matches WordPress's habit of occasionally putting <br> inside
// excerpt text.
func (p *Parser) decodeText(start *xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return "", wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return b.String(), nil
			}
		case xml.StartElement:
			// Unexpected nested element — recurse to capture its text
			// then continue (the closing tag of the nested element
			// will be consumed by the recursive call).
			inner, err := p.decodeText(&t)
			if err != nil {
				return "", err
			}
			b.WriteString(inner)
		}
	}
}

// decodeAuthor reads a wp:author element. Each child carries a single
// scalar; we dispatch by local name and ignore unknown children.
func (p *Parser) decodeAuthor(start *xml.StartElement) (*Author, error) {
	a := &Author{}
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s, err := p.decodeText(&t)
			if err != nil {
				return nil, err
			}
			switch t.Name.Local {
			case "author_id":
				a.ID = s
			case "author_login":
				a.Login = s
			case "author_email":
				a.Email = s
			case "author_display_name":
				a.DisplayName = s
			case "author_first_name":
				a.FirstName = s
			case "author_last_name":
				a.LastName = s
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return a, nil
			}
		}
	}
}

// decodeCategory reads a channel-level wp:category (term definition).
func (p *Parser) decodeCategory(start *xml.StartElement) (*Category, error) {
	c := &Category{}
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s, err := p.decodeText(&t)
			if err != nil {
				return nil, err
			}
			switch t.Name.Local {
			case "term_id":
				c.TermID = s
			case "category_nicename":
				c.Nicename = s
			case "category_parent":
				c.Parent = s
			case "cat_name":
				c.Name = s
			case "category_description":
				c.Description = s
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return c, nil
			}
		}
	}
}

// decodeTag reads a channel-level wp:tag.
func (p *Parser) decodeTag(start *xml.StartElement) (*Tag, error) {
	tg := &Tag{}
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s, err := p.decodeText(&t)
			if err != nil {
				return nil, err
			}
			switch t.Name.Local {
			case "term_id":
				tg.TermID = s
			case "tag_slug":
				tg.Slug = s
			case "tag_name":
				tg.Name = s
			case "tag_description":
				tg.Description = s
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return tg, nil
			}
		}
	}
}

// decodeItem reads one <item> element into a *Post, including all
// nested <category> term refs, <wp:postmeta> entries, and
// <wp:comment> children.
func (p *Parser) decodeItem(start *xml.StartElement) (*Post, error) {
	post := &Post{Meta: map[string]string{}}
	var metaList []postMeta

	for {
		tok, err := p.dec.Token()
		if err != nil {
			return nil, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch local {
			case "title":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Title = s
			case "link":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Link = s
			case "pubDate":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PubDate = s
			case "creator":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Creator = s
			case "description":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Description = s
			case "encoded":
				// content:encoded vs excerpt:encoded — same local
				// name, distinguished by namespace URI.
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				if strings.Contains(t.Name.Space, "excerpt") {
					post.Excerpt = s
				} else {
					post.Content = s
				}
			case "post_id":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PostID = s
			case "post_date":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PostDate = s
			case "post_date_gmt":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PostDateGMT = s
			case "status":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Status = s
			case "post_type":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PostType = s
			case "post_name":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Name = s
			case "post_parent":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Parent = s
			case "menu_order":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.MenuOrder = s
			case "post_password":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.Password = s
			case "is_sticky":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.IsSticky = s
			case "attachment_url":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.AttachmentURL = s
			case "comment_status":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.CommentStatus = s
			case "ping_status":
				s, err := p.decodeText(&t)
				if err != nil {
					return nil, err
				}
				post.PingStatus = s
			case "category":
				// Item-level <category> is a term reference. The
				// domain attribute distinguishes category vs tag vs
				// custom taxonomy.
				ref, err := p.decodeTermRef(&t)
				if err != nil {
					return nil, err
				}
				post.Terms = append(post.Terms, ref)
				switch ref.Domain {
				case "category":
					post.Categories = append(post.Categories, ref)
				case "post_tag":
					post.Tags = append(post.Tags, ref)
				}
			case "postmeta":
				m, err := decodePostMeta(p.dec, &t)
				if err != nil {
					return nil, err
				}
				metaList = append(metaList, m)
			case "comment":
				c, err := p.decodeComment(&t)
				if err != nil {
					return nil, err
				}
				post.Comments = append(post.Comments, c)
			default:
				// Unknown child of <item>: skip silently. WP plugins
				// frequently inject extension elements (Yoast, ACF,
				// etc.) here; importers handle those out-of-band.
				if err := p.dec.Skip(); err != nil {
					return nil, wrapXMLErr(err)
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				post.Meta = metaFromList(metaList)
				return post, nil
			}
		}
	}
}

// decodeTermRef reads an item-level <category domain="..." nicename="...">.
func (p *Parser) decodeTermRef(start *xml.StartElement) (TermRef, error) {
	ref := TermRef{}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "domain":
			ref.Domain = a.Value
		case "nicename":
			ref.Nicename = a.Value
		}
	}
	name, err := p.decodeText(start)
	if err != nil {
		return TermRef{}, err
	}
	ref.Name = name
	return ref, nil
}

// decodeComment reads one <wp:comment> child of an item.
func (p *Parser) decodeComment(start *xml.StartElement) (Comment, error) {
	c := Comment{}
	for {
		tok, err := p.dec.Token()
		if err != nil {
			return Comment{}, wrapXMLErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			s, err := p.decodeText(&t)
			if err != nil {
				return Comment{}, err
			}
			switch t.Name.Local {
			case "comment_id":
				c.ID = s
			case "comment_author":
				c.Author = s
			case "comment_author_email":
				c.AuthorEmail = s
			case "comment_author_url":
				c.AuthorURL = s
			case "comment_author_IP":
				c.AuthorIP = s
			case "comment_date":
				c.Date = s
			case "comment_date_gmt":
				c.DateGMT = s
			case "comment_content":
				c.Content = s
			case "comment_approved":
				c.Approved = s
			case "comment_type":
				c.Type = s
			case "comment_parent":
				c.Parent = s
			case "comment_user_id":
				c.UserID = s
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return c, nil
			}
		}
	}
}

// supportedVersion reports whether the declared WXR version is one we
// know how to parse. WordPress has shipped 1.2 since 2010 and 1.3 since
// 6.4; earlier formats are structurally different enough to refuse.
func supportedVersion(v string) bool {
	switch strings.TrimSpace(v) {
	case "1.2", "1.3":
		return true
	default:
		return false
	}
}

// wrapXMLErr wraps a low-level XML decoder error as ErrMalformedXML so
// callers can match with errors.Is. EOF is left unwrapped because
// callers treat it as a clean stream termination.
func wrapXMLErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrMalformedXML, err)
}
