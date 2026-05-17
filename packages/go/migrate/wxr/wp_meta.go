package wxr

import (
	"encoding/xml"
	"fmt"
)

// postMeta mirrors <wp:postmeta> with its <wp:meta_key>/<wp:meta_value>
// child pair. It is used as a temporary unmarshal target by the
// streaming parser; the public Post.Meta map is built from a slice of
// these.
//
// Real WXR exports use a wp: namespace prefix, but xml.Decoder rewrites
// the element names with the namespace URI rather than the prefix. We
// match on local name only — see parser.go for the helper.
type postMeta struct {
	Key   string `xml:"meta_key"`
	Value string `xml:"meta_value"`
}

// decodePostMeta consumes a wp:postmeta element starting from its
// StartElement token (caller already saw the opener) and returns the
// resulting key/value pair. The decoder is advanced past the matching
// </wp:postmeta>.
//
// Wrapping xml.Decoder.DecodeElement gives us automatic CDATA handling
// and avoids re-implementing the inner-text walk by hand.
func decodePostMeta(dec *xml.Decoder, start *xml.StartElement) (postMeta, error) {
	var m postMeta
	if err := dec.DecodeElement(&m, start); err != nil {
		return postMeta{}, fmt.Errorf("decode postmeta: %w", err)
	}
	return m, nil
}

// metaFromList flattens a slice of postMeta into a map keyed by Key.
// On duplicate keys (rare; allowed by WordPress) the last write wins,
// matching wp_postmeta's runtime get_post_meta($single=true) semantics.
// A nil/empty input returns an empty (non-nil) map so callers can
// always range freely.
func metaFromList(list []postMeta) map[string]string {
	out := make(map[string]string, len(list))
	for _, m := range list {
		out[m.Key] = m.Value
	}
	return out
}
