package email

import (
	"strings"
	"testing"
)

// TestDefaultTemplates_LoadsAllNames asserts the embedded set covers
// every declared TemplateName. A missing file would only show up at
// first render in production — running this in CI catches the
// regression at build time.
func TestDefaultTemplates_LoadsAllNames(t *testing.T) {
	tpl, err := DefaultTemplates()
	if err != nil {
		t.Fatalf("DefaultTemplates: %v", err)
	}
	for _, name := range allTemplateNames {
		t.Run(string(name), func(t *testing.T) {
			data := sampleDataFor(name)
			text, html, err := tpl.Render(name, data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if text == "" {
				t.Errorf("text body empty for %s", name)
			}
			if html == "" {
				t.Errorf("html body empty for %s", name)
			}
		})
	}
}

func TestRender_WelcomeContents(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := WelcomeData{
		BrandContext: BrandContext{
			SiteName:       "ExampleApp",
			SiteURL:        "https://example.test/",
			SiteBrandColor: "#aa00ff",
		},
		UserName: "Alice",
	}
	text, html, err := tpl.Render(TemplateWelcome, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Alice", "ExampleApp", "https://example.test/"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q:\n%s", want, html)
		}
	}
	if !strings.Contains(html, "#aa00ff") {
		t.Errorf("html missing brand color")
	}
}

// TestRender_HTMLEscaping confirms html/template's contextual
// auto-escaping kicks in on data fields. A bug here lets a "Hello
// <script>" username body inject script tags into the rendered HTML —
// the exact regression we'd never want.
func TestRender_HTMLEscaping(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := WelcomeData{
		BrandContext: BrandContext{SiteName: "S", SiteURL: "https://x.test/", SiteBrandColor: "#000"},
		UserName:     "Mallory<script>alert('x')</script>",
	}
	_, html, err := tpl.Render(TemplateWelcome, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script>alert('x')</script>") {
		t.Errorf("html/template failed to escape script tag:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("html/template did not produce escaped variant:\n%s", html)
	}
}

func TestRender_VerifyEmailHasLink(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := VerifyEmailData{
		BrandContext: BrandContext{SiteName: "S", SiteURL: "https://x.test/", SiteBrandColor: "#000"}.WithDefaults(),
		UserName:     "Bob",
		VerifyURL:    "https://app.test/verify?token=abc.def",
		ExpiresIn:    "24 hours",
	}
	text, html, err := tpl.Render(TemplateVerifyEmail, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	link := "https://app.test/verify?token=abc.def"
	if !strings.Contains(text, link) {
		t.Errorf("text missing verify link:\n%s", text)
	}
	if !strings.Contains(html, link) {
		t.Errorf("html missing verify link:\n%s", html)
	}
}

func TestRender_PasswordResetCarriesSupport(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := PasswordResetData{
		BrandContext: BrandContext{
			SiteName: "S", SiteURL: "https://x.test/", SiteBrandColor: "#000",
			SupportEmail: "help@x.test",
		},
		UserName:  "Bob",
		ResetURL:  "https://app.test/reset?t=z",
		ExpiresIn: "1 hour",
	}
	text, html, err := tpl.Render(TemplatePasswordReset, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(text, "help@x.test") {
		t.Errorf("text missing support email:\n%s", text)
	}
	if !strings.Contains(html, "mailto:help@x.test") {
		t.Errorf("html missing mailto: link:\n%s", html)
	}
}

func TestRender_CommentNotification(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := CommentNotificationData{
		BrandContext:   BrandContext{SiteName: "S", SiteURL: "https://x.test/", SiteBrandColor: "#000"},
		UserName:       "Author",
		CommenterName:  "Reader",
		PostTitle:      "Hello world",
		PostURL:        "https://x.test/posts/hello#c1",
		CommentExcerpt: "Great post.",
		UnsubscribeURL: "https://x.test/unsubscribe?t=z",
	}
	text, html, err := tpl.Render(TemplateCommentNotification, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Reader", "Hello world", "Great post.", "https://x.test/unsubscribe?t=z"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}
}

func TestRender_UnknownTemplate(t *testing.T) {
	tpl, _ := DefaultTemplates()
	if _, _, err := tpl.Render("does-not-exist", WelcomeData{}); err == nil {
		t.Fatal("expected error for unknown template")
	}
}

func TestBrandContext_DefaultsApplied(t *testing.T) {
	b := BrandContext{}.WithDefaults()
	if b.SiteName == "" || b.SiteURL == "" || b.SiteBrandColor == "" {
		t.Errorf("WithDefaults left zero values: %+v", b)
	}
}

func TestBuildMessage_StampsTagAndSubject(t *testing.T) {
	tpl, _ := DefaultTemplates()
	data := WelcomeData{
		BrandContext: BrandContext{}.WithDefaults(),
		UserName:     "x",
	}
	msg, err := tpl.BuildMessage(TemplateWelcome, "to@x.test", "Hi", data)
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	if msg.To != "to@x.test" || msg.Subject != "Hi" {
		t.Errorf("BuildMessage did not stamp envelope: %+v", msg)
	}
	if msg.Tags["template"] != string(TemplateWelcome) {
		t.Errorf("BuildMessage did not tag template: %+v", msg.Tags)
	}
	if msg.TextBody == "" || msg.HTMLBody == "" {
		t.Errorf("BuildMessage did not populate bodies")
	}
}

// sampleDataFor returns a populated data struct for name. Centralised
// here so adding a new template + constant + test in one PR is a
// three-line patch.
func sampleDataFor(name TemplateName) any {
	brand := BrandContext{
		SiteName: "S", SiteURL: "https://x.test/", SiteBrandColor: "#000",
		SupportEmail: "help@x.test",
	}
	switch name {
	case TemplateWelcome:
		return WelcomeData{BrandContext: brand, UserName: "Alice"}
	case TemplateVerifyEmail:
		return VerifyEmailData{
			BrandContext: brand, UserName: "Alice",
			VerifyURL: "https://x.test/v?t=1", ExpiresIn: "24 hours",
		}
	case TemplatePasswordReset:
		return PasswordResetData{
			BrandContext: brand, UserName: "Alice",
			ResetURL: "https://x.test/r?t=1", ExpiresIn: "1 hour",
		}
	case TemplateCommentNotification:
		return CommentNotificationData{
			BrandContext: brand, UserName: "Alice",
			CommenterName: "Bob", PostTitle: "Title",
			PostURL: "https://x.test/p#c1", CommentExcerpt: "Body",
			UnsubscribeURL: "https://x.test/u",
		}
	}
	return nil
}
