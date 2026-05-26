package shortcode

import (
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

// Translator turns a parsed Shortcode into zero or more Blocks.
// Translators are pure functions: the same Shortcode always yields
// the same Blocks. The caller is responsible for downstream effects
// like writing referenced media into storage.
//
// A nil/empty return is legal and means "elide this shortcode"
// (handy when the translator decides the shortcode was a no-op,
// e.g. an empty [gallery] with no ids).
type Translator func(Shortcode) []html2blocks.Block

// Registry maps shortcode names to translator funcs. Names are
// lowercased on register and lookup so case differences in source
// content can't sidestep the map. Construct with NewRegistry; the
// zero value is unusable.
//
// Registry is not safe for concurrent Register* calls; finish setup
// before kicking off translation work in workers.
type Registry struct {
	m map[string]Translator
}

// NewRegistry returns an empty Registry. Use RegisterDefaults to
// preload the six built-in translators.
func NewRegistry() *Registry {
	return &Registry{m: map[string]Translator{}}
}

// Register installs a translator under the given name. A later
// Register call for the same name wins (last-write semantics) so
// callers can override a built-in by re-registering after
// RegisterDefaults.
func (r *Registry) Register(name string, t Translator) {
	if r == nil || r.m == nil || t == nil {
		return
	}
	r.m[strings.ToLower(strings.TrimSpace(name))] = t
}

// Lookup returns the translator for a name and a found flag.
func (r *Registry) Lookup(name string) (Translator, bool) {
	if r == nil || r.m == nil {
		return nil, false
	}
	t, ok := r.m[strings.ToLower(strings.TrimSpace(name))]
	return t, ok
}

// RegisterDefaults installs the six built-in translators listed in
// the issue:
//
//   - caption        → core/image with caption attr
//   - gallery        → core/gallery with image ids
//   - video          → core/video
//   - audio          → core/audio
//   - embed          → core/embed
//   - contact-form-7 → core/shortcode (form id preserved in attrs)
//
// Plugins or operators can call Register to add their own; built-in
// translators can be overridden by a subsequent Register with the
// same name.
func (r *Registry) RegisterDefaults() {
	if r == nil {
		return
	}
	r.Register("caption", translateCaption)
	r.Register("gallery", translateGallery)
	r.Register("video", translateVideo)
	r.Register("audio", translateAudio)
	r.Register("embed", translateEmbed)
	r.Register("contact-form-7", translateContactForm7)
}

// translateCaption maps [caption id="..." align="..."]<img ...>caption text[/caption]
// to a core/image block carrying both src and caption.
//
// WP's [caption] always wraps the <img> tag — we extract the src
// from the inner HTML if present, but fall back to the shortcode's
// own attributes if not (some custom captions use src=).
func translateCaption(sc Shortcode) []html2blocks.Block {
	src := sc.Attrs["src"]
	alt := sc.Attrs["alt"]
	caption := strings.TrimSpace(sc.Inner)
	// Strip the embedded <img …> tag (very common form) so the
	// caption text is just the trailing prose. We don't fully parse
	// HTML here — the html2blocks package will rewalk this content
	// on the host post anyway.
	if idx := strings.Index(caption, "</a>"); idx > 0 {
		caption = strings.TrimSpace(caption[idx+4:])
	} else if idx := strings.Index(caption, "/>"); idx > 0 {
		// Self-closing img.
		caption = strings.TrimSpace(caption[idx+2:])
	} else if idx := strings.Index(caption, ">"); idx > 0 && strings.Contains(caption[:idx+1], "<img") {
		caption = strings.TrimSpace(caption[idx+1:])
	}
	attrs := map[string]any{}
	if src != "" {
		attrs["src"] = src
	}
	if alt != "" {
		attrs["alt"] = alt
	}
	if caption != "" {
		attrs["caption"] = caption
	}
	if a, ok := sc.Attrs["align"]; ok && a != "" {
		attrs["align"] = a
	}
	if w, ok := sc.Attrs["width"]; ok && w != "" {
		attrs["width"] = w
	}
	return []html2blocks.Block{{
		Name:  html2blocks.BlockImage,
		Attrs: attrs,
	}}
}

// translateGallery emits a core/gallery block listing the image
// attachment ids from the WP [gallery ids="1,2,3"] form. When ids
// aren't supplied the result is an empty gallery; the caller can
// detect that and fall back if it wants stricter behaviour.
//
// columns / link / size are passed through as attrs since they map
// 1:1 onto the GoNext gallery block.
func translateGallery(sc Shortcode) []html2blocks.Block {
	attrs := map[string]any{}
	if ids, ok := sc.Attrs["ids"]; ok && ids != "" {
		var parts []string
		for _, p := range strings.Split(ids, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			attrs["ids"] = parts
		}
	}
	if c, ok := sc.Attrs["columns"]; ok && c != "" {
		attrs["columns"] = c
	}
	if s, ok := sc.Attrs["size"]; ok && s != "" {
		attrs["size"] = s
	}
	if l, ok := sc.Attrs["link"]; ok && l != "" {
		attrs["link"] = l
	}
	return []html2blocks.Block{{
		Name:  "core/gallery",
		Attrs: attrs,
	}}
}

// translateVideo maps [video src="…"] to core/video. WP also accepts
// mp4=, m4v=, webm=, ogv=, wmv= per-codec attrs; we prefer src
// but fall back to any of the others in source-priority order.
func translateVideo(sc Shortcode) []html2blocks.Block {
	src := sc.Attrs["src"]
	if src == "" {
		for _, k := range []string{"mp4", "m4v", "webm", "ogv", "wmv"} {
			if v, ok := sc.Attrs[k]; ok && v != "" {
				src = v
				break
			}
		}
	}
	if src == "" {
		return nil
	}
	attrs := map[string]any{"src": src}
	if p, ok := sc.Attrs["poster"]; ok && p != "" {
		attrs["poster"] = p
	}
	if l, ok := sc.Attrs["loop"]; ok && l != "" {
		attrs["loop"] = l == "1" || l == "true" || l == "on"
	}
	if a, ok := sc.Attrs["autoplay"]; ok && a != "" {
		attrs["autoplay"] = a == "1" || a == "true" || a == "on"
	}
	return []html2blocks.Block{{
		Name:  "core/video",
		Attrs: attrs,
	}}
}

// translateAudio maps [audio src="…"] / [audio mp3="…"] to
// core/audio. Same fallback order as video.
func translateAudio(sc Shortcode) []html2blocks.Block {
	src := sc.Attrs["src"]
	if src == "" {
		for _, k := range []string{"mp3", "m4a", "ogg", "wav", "wma"} {
			if v, ok := sc.Attrs[k]; ok && v != "" {
				src = v
				break
			}
		}
	}
	if src == "" {
		return nil
	}
	return []html2blocks.Block{{
		Name:  "core/audio",
		Attrs: map[string]any{"src": src},
	}}
}

// translateEmbed handles WP's [embed] shortcode, which takes the
// embed URL as its inner content (or as src=). We preserve both
// the URL and the optional width/height that WP allows.
func translateEmbed(sc Shortcode) []html2blocks.Block {
	url := strings.TrimSpace(sc.Inner)
	if url == "" {
		url = sc.Attrs["src"]
	}
	if url == "" {
		// Positional value: [embed]https://...[/embed] uses Inner,
		// but [embed https://...] uses positional 0.
		url = sc.Attrs["0"]
	}
	if url == "" {
		return nil
	}
	attrs := map[string]any{"url": url}
	if w, ok := sc.Attrs["width"]; ok && w != "" {
		attrs["width"] = w
	}
	if h, ok := sc.Attrs["height"]; ok && h != "" {
		attrs["height"] = h
	}
	return []html2blocks.Block{{
		Name:  "core/embed",
		Attrs: attrs,
	}}
}

// translateContactForm7 maps [contact-form-7 id="123" title="…"]
// to a generic core/shortcode block that records the form id. The
// GoNext side can pick this up via a plugin once an equivalent
// form is wired up; until then the block carries the original
// shortcode text in raw= so a renderer can fall back to printing it.
func translateContactForm7(sc Shortcode) []html2blocks.Block {
	attrs := map[string]any{
		"plugin": "contact-form-7",
		"raw":    sc.Raw,
	}
	if id, ok := sc.Attrs["id"]; ok && id != "" {
		attrs["formId"] = id
	}
	if t, ok := sc.Attrs["title"]; ok && t != "" {
		attrs["title"] = t
	}
	return []html2blocks.Block{{
		Name:  "core/shortcode",
		Attrs: attrs,
	}}
}
