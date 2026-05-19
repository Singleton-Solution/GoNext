package email

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"
)

// templateFS embeds the canonical text+HTML templates. They live next to
// this file in ./templates/*.{html,txt}. Embedding keeps a single binary
// shippable for ops and ensures the templates can never drift away from
// the schema the Go code expects.
//
// Operators who want to override the body of a specific email — say,
// to localize the verification message — can do so by passing their own
// Templates instance built via [ParseTemplatesFS] from a fs.FS that
// supplies the same names. The Send-side API does not care which set
// is in use.
//
//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

// TemplateName identifies a built-in template pair. Each constant
// resolves to two files under templates/: <name>.html and <name>.txt.
//
// Adding a new template:
//
//  1. Drop the .html + .txt files in templates/.
//  2. Declare a TemplateName constant here.
//  3. Add the matching test entry in templates_test.go's name list.
//
// The constants are deliberately strings (not iota ints) so the wire
// representation in audit metadata, queue payloads, and logs is stable
// across reorderings.
type TemplateName string

const (
	// TemplateWelcome is the post-signup confirmation: "your account is
	// ready, here's how to sign in". Sent once per account creation.
	TemplateWelcome TemplateName = "welcome"

	// TemplateVerifyEmail asks the recipient to click a one-time link to
	// prove they own the address. Used by apps/api/internal/auth/verify.
	TemplateVerifyEmail TemplateName = "verify-email"

	// TemplatePasswordReset carries a one-time reset link. The wording
	// emphasises "ignore if you didn't request this" because account
	// recovery flows are abuse-prone.
	TemplatePasswordReset TemplateName = "password-reset"

	// TemplateCommentNotification fires when a thread the recipient
	// subscribed to receives a new reply. Carries an unsubscribe link.
	TemplateCommentNotification TemplateName = "comment-notification"
)

// allTemplateNames is the closed set the constructor walks at parse
// time. Keeping it explicit lets ParseTemplates fail fast if a file
// goes missing from the embed set, rather than at the first render.
var allTemplateNames = []TemplateName{
	TemplateWelcome,
	TemplateVerifyEmail,
	TemplatePasswordReset,
	TemplateCommentNotification,
}

// BrandContext captures the per-deployment branding fields every
// template's layout expects: site name, the URL to the home page, and
// the primary brand color (drawn from theme.json's accent palette in
// production wiring). Callers compose this once at boot and merge it
// into per-render contexts via [Templates.Render].
//
// Note: BrandContext is also where the support email and footer year
// live. The struct stays flat (no nested config) on purpose — every
// field maps 1:1 to a template variable, so additions are reviewable
// without chasing indirection.
type BrandContext struct {
	// SiteName is the human-readable name, e.g. "Example Blog". Used in
	// subject lines and footer text. Required by every template.
	SiteName string

	// SiteURL is the canonical home URL. Used as the "sign in" target in
	// the welcome email and as the brand link in headers.
	SiteURL string

	// SiteBrandColor is a CSS color literal — "#2563eb", "rgb(...)", or
	// any value the template can interpolate into a `style="background:
	// {{.SiteBrandColor}};"` attribute. The wiring populates this from
	// theme.json's accent palette. Defaults to "#2563eb" if empty so a
	// misconfigured deployment still renders a styled message.
	SiteBrandColor string

	// SupportEmail is the user-facing escalation address printed in
	// password-reset bodies. Empty is allowed; the template hides the
	// "contact support" sentence when empty would otherwise produce a
	// dangling mailto:.
	SupportEmail string
}

// WelcomeData is the per-render context for TemplateWelcome. It
// composes BrandContext via embedding so callers fill one struct.
type WelcomeData struct {
	BrandContext

	// UserName is the recipient's display name. Empty falls back to a
	// generic salutation in the template.
	UserName string
}

// VerifyEmailData is the per-render context for TemplateVerifyEmail.
// VerifyURL is the full one-time link; ExpiresIn is a human-formatted
// duration like "24 hours".
type VerifyEmailData struct {
	BrandContext

	UserName  string
	VerifyURL string
	ExpiresIn string
}

// PasswordResetData is the per-render context for TemplatePasswordReset.
type PasswordResetData struct {
	BrandContext

	UserName  string
	ResetURL  string
	ExpiresIn string
}

// CommentNotificationData is the per-render context for
// TemplateCommentNotification. Excerpt is the body snippet (already
// truncated by the caller — the template does not impose a length
// limit). UnsubscribeURL is required by CAN-SPAM / RFC 8058; the
// caller must mint and rotate it.
type CommentNotificationData struct {
	BrandContext

	UserName       string
	CommenterName  string
	PostTitle      string
	PostURL        string
	CommentExcerpt string
	UnsubscribeURL string
}

// Templates is the parsed set of HTML+text template pairs ready to
// render. Construct one at boot via [DefaultTemplates] (uses the
// embedded set) or [ParseTemplatesFS] (custom fs.FS). The result is
// safe for concurrent use; Render does not mutate the parsed trees.
type Templates struct {
	html *htmltemplate.Template
	text *texttemplate.Template
}

// DefaultTemplates returns the Templates parsed from the embedded
// template files. The error is non-nil only if the embed itself is
// corrupted (a build-time problem), so the typical wiring is
// `t, _ := email.DefaultTemplates()` after a smoke test in CI.
func DefaultTemplates() (*Templates, error) {
	sub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("email: open embedded templates: %w", err)
	}
	return ParseTemplatesFS(sub)
}

// ParseTemplatesFS parses HTML + text templates from src. src is
// expected to be flat: <name>.html and <name>.txt at the root of the
// fs. Missing files for any [TemplateName] constant return an error so
// boot fails fast rather than at first send.
//
// The HTML side uses html/template (contextual auto-escaping), the
// text side uses text/template (raw, no escaping). Callers should
// never pass a string that contains template syntax into a data
// field — both engines treat data values as inert text.
func ParseTemplatesFS(src fs.FS) (*Templates, error) {
	htmlRoot := htmltemplate.New("")
	textRoot := texttemplate.New("")

	for _, name := range allTemplateNames {
		htmlPath := string(name) + ".html"
		htmlBytes, err := fs.ReadFile(src, htmlPath)
		if err != nil {
			return nil, fmt.Errorf("email: read %s: %w", htmlPath, err)
		}
		if _, err := htmlRoot.New(htmlPath).Parse(string(htmlBytes)); err != nil {
			return nil, fmt.Errorf("email: parse %s: %w", htmlPath, err)
		}

		txtPath := string(name) + ".txt"
		txtBytes, err := fs.ReadFile(src, txtPath)
		if err != nil {
			return nil, fmt.Errorf("email: read %s: %w", txtPath, err)
		}
		if _, err := textRoot.New(txtPath).Parse(string(txtBytes)); err != nil {
			return nil, fmt.Errorf("email: parse %s: %w", txtPath, err)
		}
	}
	return &Templates{html: htmlRoot, text: textRoot}, nil
}

// ErrUnknownTemplate is returned by Render when name is not one of the
// known [TemplateName] constants. Wrapped with the unknown name so
// log lines pinpoint the miswiring.
var ErrUnknownTemplate = errors.New("email: unknown template")

// Render executes both the text and HTML variants of name against
// data and returns the bodies. data must be one of the *Data structs
// defined above — passing a mismatched type yields a template error
// from the engine.
//
// Render does not allocate beyond the two body buffers. It is safe to
// call concurrently from multiple goroutines.
func (t *Templates) Render(name TemplateName, data any) (text string, html string, err error) {
	if !isKnown(name) {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownTemplate, name)
	}

	var htmlBuf bytes.Buffer
	if err := t.html.ExecuteTemplate(&htmlBuf, string(name)+".html", data); err != nil {
		return "", "", fmt.Errorf("email: render %s.html: %w", name, err)
	}
	var textBuf bytes.Buffer
	if err := t.text.ExecuteTemplate(&textBuf, string(name)+".txt", data); err != nil {
		return "", "", fmt.Errorf("email: render %s.txt: %w", name, err)
	}
	return textBuf.String(), htmlBuf.String(), nil
}

// BuildMessage is the convenience helper that renders name with data
// and stitches the result into a [Message] ready to hand to a Sender.
// To and Subject are the only non-template fields the caller has to
// supply.
//
// The function never inspects data beyond passing it to the template
// engine, so it composes cleanly with custom Data shapes built atop
// BrandContext.
func (t *Templates) BuildMessage(name TemplateName, to, subject string, data any) (Message, error) {
	text, html, err := t.Render(name, data)
	if err != nil {
		return Message{}, err
	}
	return Message{
		To:       to,
		Subject:  subject,
		TextBody: text,
		HTMLBody: html,
		Tags:     map[string]string{"template": string(name)},
	}, nil
}

// isKnown reports whether name is one of the declared constants. We
// don't use a map because the set is closed at four entries and a
// linear scan is faster than a map lookup at that size.
func isKnown(name TemplateName) bool {
	for _, n := range allTemplateNames {
		if n == name {
			return true
		}
	}
	return false
}

// applyBrandDefaults fills in zero-valued BrandContext fields with
// safe defaults so a misconfigured deployment still renders a usable
// message. Callers can use it before Render when their wiring loads
// BrandContext lazily.
//
// Defaults:
//
//	SiteName       -> "GoNext"
//	SiteURL        -> "https://example.invalid"
//	SiteBrandColor -> "#2563eb" (matches gn-hello theme accent)
//	SupportEmail   -> "" (template suppresses the contact line)
func applyBrandDefaults(b BrandContext) BrandContext {
	out := b
	if strings.TrimSpace(out.SiteName) == "" {
		out.SiteName = "GoNext"
	}
	if strings.TrimSpace(out.SiteURL) == "" {
		out.SiteURL = "https://example.invalid"
	}
	if strings.TrimSpace(out.SiteBrandColor) == "" {
		out.SiteBrandColor = "#2563eb"
	}
	return out
}

// WithDefaults is the exported wrapper around applyBrandDefaults so
// wiring code can fill defaults before stamping into a data struct.
// Returns a new BrandContext; the receiver is not mutated.
func (b BrandContext) WithDefaults() BrandContext { return applyBrandDefaults(b) }
