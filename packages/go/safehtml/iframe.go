package safehtml

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// IframeOptions controls how SanitizeIframe processes an <iframe>
// element.
type IframeOptions struct {
	// AllowedHosts is the set of hostnames the iframe src may point
	// at. An empty list rejects every iframe — the sanitizer is
	// deny-by-default. The compare is case-insensitive and exact;
	// subdomains do NOT match implicitly.
	AllowedHosts []string

	// Sandbox is the sandbox token list to enforce. If empty, the
	// default "allow-scripts allow-same-origin" is applied, which
	// matches the YouTube/Vimeo embed contract.
	//
	// Callers can pass an empty-but-non-nil slice (e.g. []string{})
	// for the strictest possible sandbox — the empty attribute
	// (`sandbox=""`) disables every capability.
	Sandbox []string

	// AllowFullscreen, when true, adds the allowfullscreen attribute.
	// Most video embeds want this; document embeds typically don't.
	AllowFullscreen bool
}

// ErrIframeRejected is returned by SanitizeIframe when the iframe is
// rejected outright (src missing, scheme wrong, host not allowlisted,
// etc). Callers can errors.Is against it to distinguish "this iframe
// is not valid" from a parse error.
var ErrIframeRejected = errors.New("safehtml: iframe rejected")

// SanitizeIframe takes an HTML fragment containing one iframe element
// and returns a sanitized version: src enforced through the URL +
// host allowlist, sandbox attribute always present, srcdoc stripped,
// and only a closed list of layout attributes (width, height, title,
// loading, allow) preserved.
//
// If raw contains zero iframe elements, returns "" + nil (a no-op).
// If it contains more than one, only the FIRST is sanitized and
// returned — the input is expected to be a single embed.
func SanitizeIframe(raw string, opts IframeOptions) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return "", err
	}
	iframe := findIframe(nodes)
	if iframe == nil {
		return "", nil
	}
	out, err := cleanIframe(iframe, opts)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := html.Render(&b, out); err != nil {
		return "", err
	}
	return b.String(), nil
}

// findIframe descends nodes (a forest from ParseFragment) and returns
// the first iframe element it finds, or nil if there is none.
func findIframe(nodes []*html.Node) *html.Node {
	for _, n := range nodes {
		if got := findIframeIn(n); got != nil {
			return got
		}
	}
	return nil
}

func findIframeIn(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "iframe") {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findIframeIn(c); got != nil {
			return got
		}
	}
	return nil
}

// cleanIframe applies the host allowlist, scheme check, srcdoc strip,
// and sandbox enforcement. Returns a brand-new iframe node — the
// input is treated as read-only.
func cleanIframe(in *html.Node, opts IframeOptions) (*html.Node, error) {
	var src string
	keep := map[string]string{}
	for _, a := range in.Attr {
		switch strings.ToLower(a.Key) {
		case "src":
			src = a.Val
		case "srcdoc":
			// srcdoc inlines an arbitrary HTML document into the
			// iframe; it bypasses the host allowlist entirely.
			// Always drop.
			continue
		case "name", "title", "width", "height", "loading", "allow",
			"referrerpolicy", "frameborder", "marginwidth", "marginheight":
			keep[strings.ToLower(a.Key)] = a.Val
		default:
			// Anything not in the allowlist — including the on*
			// event handlers and the legacy `seamless` attribute —
			// is dropped.
		}
	}
	if src == "" {
		return nil, fmt.Errorf("%w: missing src", ErrIframeRejected)
	}
	u, err := url.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: parse src: %v", ErrIframeRejected, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("%w: src scheme must be https, got %q", ErrIframeRejected, u.Scheme)
	}
	if !hostAllowed(u.Hostname(), opts.AllowedHosts) {
		return nil, fmt.Errorf("%w: host %q not in allowlist", ErrIframeRejected, u.Hostname())
	}

	out := &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Iframe,
		Data:     "iframe",
	}
	out.Attr = append(out.Attr, html.Attribute{Key: "src", Val: u.String()})
	// Sandbox is mandatory. Per the spec, an empty sandbox attribute
	// turns OFF every capability; a populated one turns on only the
	// listed ones. The default "allow-scripts allow-same-origin"
	// matches the YouTube embed contract.
	sandboxValue := ""
	if opts.Sandbox != nil {
		sandboxValue = strings.Join(opts.Sandbox, " ")
	} else {
		sandboxValue = "allow-scripts allow-same-origin"
	}
	out.Attr = append(out.Attr, html.Attribute{Key: "sandbox", Val: sandboxValue})
	if opts.AllowFullscreen {
		out.Attr = append(out.Attr, html.Attribute{Key: "allowfullscreen", Val: ""})
	}
	// Replay the kept layout attributes in a stable order so the
	// output is deterministic (Render walks Attr in order).
	for _, k := range []string{"title", "name", "width", "height", "loading", "allow", "referrerpolicy", "frameborder", "marginwidth", "marginheight"} {
		if v, ok := keep[k]; ok {
			out.Attr = append(out.Attr, html.Attribute{Key: k, Val: v})
		}
	}
	return out, nil
}

// hostAllowed reports whether host is in the (case-insensitive,
// exact-match) allow list. An empty list rejects everything.
func hostAllowed(host string, allow []string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, h := range allow {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}
