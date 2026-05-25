package i18n

import "testing"

func TestNoopTranslator_ReturnsKey(t *testing.T) {
	tr := NoopTranslator{}
	if got := tr.Translate("post.author", "fr-FR"); got != "post.author" {
		t.Errorf("Translate: got %q, want %q", got, "post.author")
	}
	if got := tr.Translate("", "fr-FR"); got != "" {
		t.Errorf("Translate empty key: got %q, want empty", got)
	}
}

func TestMapTranslator_ExactMatch(t *testing.T) {
	tr := NewMapTranslator(map[string]map[string]string{
		"fr-FR": {"post.author": "Auteur"},
	})
	if got := tr.Translate("post.author", "fr-FR"); got != "Auteur" {
		t.Errorf("Translate: got %q, want %q", got, "Auteur")
	}
}

func TestMapTranslator_LanguageFallback(t *testing.T) {
	tr := NewMapTranslator(map[string]map[string]string{
		"fr": {"post.author": "Auteur"},
	})
	// fr-FR misses, but bare fr hits.
	if got := tr.Translate("post.author", "fr-FR"); got != "Auteur" {
		t.Errorf("Translate fallback: got %q, want %q", got, "Auteur")
	}
}

func TestMapTranslator_MissingReturnsKey(t *testing.T) {
	tr := NewMapTranslator(nil)
	if got := tr.Translate("post.author", "de"); got != "post.author" {
		t.Errorf("Translate missing: got %q, want key", got)
	}
}

func TestMapTranslator_Set(t *testing.T) {
	tr := NewMapTranslator(nil)
	tr.Set("en", "post.title", "Title")
	if got := tr.Translate("post.title", "en"); got != "Title" {
		t.Errorf("Translate: got %q, want %q", got, "Title")
	}
}

func TestMapTranslator_EmptyKey(t *testing.T) {
	tr := NewMapTranslator(map[string]map[string]string{"en": {"a": "b"}})
	if got := tr.Translate("", "en"); got != "" {
		t.Errorf("Translate empty: got %q, want empty", got)
	}
}
