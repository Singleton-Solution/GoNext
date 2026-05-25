package safehtml

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeSVG_StripsScript(t *testing.T) {
	t.Parallel()
	raw := `<svg><script>alert(1)</script><circle r="5"/></svg>`
	got, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "alert") {
		t.Errorf("script survived sanitization: %q", got)
	}
	if !strings.Contains(got, "<circle") {
		t.Errorf("legitimate child stripped: %q", got)
	}
}

func TestSanitizeSVG_StripsEventHandlers(t *testing.T) {
	t.Parallel()
	raw := `<svg onload="alert(1)"><rect onclick="x()" width="10"/></svg>`
	got, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "onload") ||
		strings.Contains(strings.ToLower(got), "onclick") ||
		strings.Contains(got, "alert") {
		t.Errorf("event handlers survived: %q", got)
	}
	if !strings.Contains(got, `width="10"`) {
		t.Errorf("legitimate attr stripped: %q", got)
	}
}

func TestSanitizeSVG_StripsJavaScriptURL(t *testing.T) {
	t.Parallel()
	raw := `<svg><a href="javascript:alert(1)"><text>click</text></a></svg>`
	got, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("javascript: URL survived: %q", got)
	}
}

func TestSanitizeSVG_PreservesLegitimateContent(t *testing.T) {
	t.Parallel()
	raw := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40" fill="red"/></svg>`
	got, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	for _, want := range []string{"circle", `r="40"`, `fill="red"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output: %q", want, got)
		}
	}
}

func TestSanitizeSVG_DropsForeignObject(t *testing.T) {
	t.Parallel()
	// foreignObject can embed arbitrary HTML inside SVG — a classic
	// XSS vector. Must be dropped entirely.
	raw := `<svg><foreignObject><iframe src="javascript:alert(1)"></iframe></foreignObject><circle r="5"/></svg>`
	got, err := SanitizeSVG(raw)
	if err != nil {
		t.Fatalf("SanitizeSVG: %v", err)
	}
	for _, bad := range []string{"foreignObject", "foreignobject", "iframe", "javascript"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(bad)) {
			t.Errorf("bad token %q present: %q", bad, got)
		}
	}
}

func TestSanitizeMathML_StripsScript(t *testing.T) {
	t.Parallel()
	raw := `<math><script>alert(1)</script><mi>x</mi></math>`
	got, err := SanitizeMathML(raw)
	if err != nil {
		t.Fatalf("SanitizeMathML: %v", err)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "alert") {
		t.Errorf("script survived: %q", got)
	}
	if !strings.Contains(got, "<mi") {
		t.Errorf("mi element stripped: %q", got)
	}
}

func TestSanitizeMathML_StripsEventHandlers(t *testing.T) {
	t.Parallel()
	raw := `<math onerror="alert(1)"><mi onclick="x()">y</mi></math>`
	got, err := SanitizeMathML(raw)
	if err != nil {
		t.Fatalf("SanitizeMathML: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "onerror") ||
		strings.Contains(strings.ToLower(got), "onclick") {
		t.Errorf("handlers survived: %q", got)
	}
}

func TestSanitizeIframe_RequiresHTTPS(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="http://example.com/embed"></iframe>`
	_, err := SanitizeIframe(raw, IframeOptions{AllowedHosts: []string{"example.com"}})
	if err == nil {
		t.Fatalf("expected ErrIframeRejected for http://")
	}
	if !errors.Is(err, ErrIframeRejected) {
		t.Errorf("err=%v want ErrIframeRejected", err)
	}
}

func TestSanitizeIframe_EnforcesHostAllowlist(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="https://evil.example.com/embed"></iframe>`
	_, err := SanitizeIframe(raw, IframeOptions{AllowedHosts: []string{"www.youtube.com"}})
	if err == nil {
		t.Fatalf("expected ErrIframeRejected for non-allowlisted host")
	}
	if !errors.Is(err, ErrIframeRejected) {
		t.Errorf("err=%v want ErrIframeRejected", err)
	}
}

func TestSanitizeIframe_StripsSrcdoc(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="https://www.youtube.com/embed/abc" srcdoc="<script>alert(1)</script>"></iframe>`
	got, err := SanitizeIframe(raw, IframeOptions{AllowedHosts: []string{"www.youtube.com"}})
	if err != nil {
		t.Fatalf("SanitizeIframe: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "srcdoc") {
		t.Errorf("srcdoc survived: %q", got)
	}
}

func TestSanitizeIframe_AddsSandbox(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="https://www.youtube.com/embed/abc"></iframe>`
	got, err := SanitizeIframe(raw, IframeOptions{AllowedHosts: []string{"www.youtube.com"}})
	if err != nil {
		t.Fatalf("SanitizeIframe: %v", err)
	}
	if !strings.Contains(got, "sandbox=") {
		t.Errorf("sandbox missing: %q", got)
	}
	if !strings.Contains(got, "allow-scripts") {
		t.Errorf("default sandbox missing allow-scripts: %q", got)
	}
}

func TestSanitizeIframe_AddsEmptySandboxWhenRequested(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="https://www.youtube.com/embed/abc"></iframe>`
	got, err := SanitizeIframe(raw, IframeOptions{
		AllowedHosts: []string{"www.youtube.com"},
		Sandbox:      []string{}, // empty but non-nil = strictest
	})
	if err != nil {
		t.Fatalf("SanitizeIframe: %v", err)
	}
	if !strings.Contains(got, `sandbox=""`) {
		t.Errorf("empty sandbox attribute missing: %q", got)
	}
}

func TestSanitizeIframe_StripsEventHandlers(t *testing.T) {
	t.Parallel()
	raw := `<iframe src="https://www.youtube.com/embed/abc" onload="alert(1)"></iframe>`
	got, err := SanitizeIframe(raw, IframeOptions{AllowedHosts: []string{"www.youtube.com"}})
	if err != nil {
		t.Fatalf("SanitizeIframe: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "onload") {
		t.Errorf("onload survived: %q", got)
	}
}

func TestSanitizeIframe_NoIframeReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := SanitizeIframe(`<p>hello</p>`, IframeOptions{AllowedHosts: []string{"x"}})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != "" {
		t.Errorf("want empty, got %q", got)
	}
}
