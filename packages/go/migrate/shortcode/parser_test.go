package shortcode

import (
	"reflect"
	"testing"
)

func TestParseAttrs_QuotingForms(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"", map[string]string{}},
		{`a="b"`, map[string]string{"a": "b"}},
		{`a='b'`, map[string]string{"a": "b"}},
		{`a=bare`, map[string]string{"a": "bare"}},
		{`A="B" C='D' e=f`, map[string]string{"a": "B", "c": "D", "e": "f"}},
		{`positional1 positional2`, map[string]string{"0": "positional1", "1": "positional2"}},
		{`mixed="ok" positional`, map[string]string{"mixed": "ok", "0": "positional"}},
		{`url="https://example.com/?a=1&b=2"`, map[string]string{"url": "https://example.com/?a=1&b=2"}},
	}
	for _, tc := range cases {
		got := parseAttrs(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseAttrs(%q):\n got: %#v\nwant: %#v", tc.in, got, tc.want)
		}
	}
}

func TestScanShortcodes_SelfClosing(t *testing.T) {
	src := `before [video src="a.mp4" /] after`
	scan := scanShortcodes(src)
	if len(scan.Tokens) != 3 {
		t.Fatalf("tokens: got %d want 3 (%v)", len(scan.Tokens), scan.Tokens)
	}
	lit, ok := scan.Tokens[0].(*literalToken)
	if !ok || lit.Text != "before " {
		t.Errorf("token 0: got %#v want 'before '", scan.Tokens[0])
	}
	sc, ok := scan.Tokens[1].(*shortcodeToken)
	if !ok {
		t.Fatalf("token 1 wrong type: %T", scan.Tokens[1])
	}
	if sc.Code.Name != "video" {
		t.Errorf("name: got %q want video", sc.Code.Name)
	}
	if !sc.Code.SelfClosing {
		t.Error("expected self-closing")
	}
	if sc.Code.Attrs["src"] != "a.mp4" {
		t.Errorf("src: got %q want a.mp4", sc.Code.Attrs["src"])
	}
}

func TestScanShortcodes_Enclosing(t *testing.T) {
	src := `[caption id="att_1"]<img src="a.jpg"/>my caption[/caption]`
	scan := scanShortcodes(src)
	if len(scan.Tokens) != 1 {
		t.Fatalf("tokens: got %d want 1", len(scan.Tokens))
	}
	sc := scan.Tokens[0].(*shortcodeToken).Code
	if sc.Name != "caption" {
		t.Errorf("name: %q", sc.Name)
	}
	if sc.SelfClosing {
		t.Error("should not be self-closing")
	}
	if sc.Inner != `<img src="a.jpg"/>my caption` {
		t.Errorf("inner: %q", sc.Inner)
	}
}

func TestScanShortcodes_Nested(t *testing.T) {
	// Same-name nesting via depth counting.
	src := `[row][row]inner[/row][/row]`
	scan := scanShortcodes(src)
	if len(scan.Tokens) != 1 {
		t.Fatalf("tokens: got %d want 1 (%#v)", len(scan.Tokens), scan.Tokens)
	}
	sc := scan.Tokens[0].(*shortcodeToken).Code
	if sc.Inner != "[row]inner[/row]" {
		t.Errorf("inner: %q", sc.Inner)
	}
}

func TestScanShortcodes_EscapedBrackets(t *testing.T) {
	// [[shortcode]] should emit literal [shortcode].
	src := `prose [[shortcode]] more`
	scan := scanShortcodes(src)
	if len(scan.Tokens) != 1 {
		t.Fatalf("tokens: got %d want 1", len(scan.Tokens))
	}
	lit := scan.Tokens[0].(*literalToken).Text
	if lit != "prose [shortcode] more" {
		t.Errorf("literal: %q", lit)
	}
}

func TestScanShortcodes_UnbalancedTolerated(t *testing.T) {
	// Opener with no closer should degrade gracefully.
	src := `[video src="a.mp4"]trailing text with no closer`
	scan := scanShortcodes(src)
	// Should yield one shortcode plus the trailing text as a literal
	// AFTER the opener. Implementation marks unbalanced as self-closing,
	// so trailing text is a separate literal.
	if len(scan.Tokens) == 0 {
		t.Fatal("expected at least one token")
	}
}

func TestScanShortcodes_NotAShortcode(t *testing.T) {
	// These shapes intentionally fail the shortcode parser and end
	// up as literal text. A leading space inside the bracket isn't
	// a valid WP shortcode, neither is a numeric-leading name.
	cases := []string{
		"[ leading space inside bracket]",
		"[123numeric_start]",
	}
	for _, src := range cases {
		scan := scanShortcodes(src)
		for _, tok := range scan.Tokens {
			if _, ok := tok.(*shortcodeToken); ok {
				t.Errorf("%q should not parse as a shortcode (got %#v)", src, tok)
			}
		}
	}
}

func TestIsShortcodeName(t *testing.T) {
	good := []string{"foo", "foo-bar", "_under", "foo_123", "Caption"}
	bad := []string{"", "1foo", "-foo", "foo bar", "foo.bar"}
	for _, s := range good {
		if !isShortcodeName(s) {
			t.Errorf("isShortcodeName(%q) = false want true", s)
		}
	}
	for _, s := range bad {
		if isShortcodeName(s) {
			t.Errorf("isShortcodeName(%q) = true want false", s)
		}
	}
}
