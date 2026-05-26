package render

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/hooks"
)

// fakeDispatcher records every Dispatch invocation and returns a
// canned response.
type fakeDispatcher struct {
	calls []PluginBlockRequest
	resp  template.HTML
	err   error
}

func (f *fakeDispatcher) Dispatch(slug, handler string, req PluginBlockRequest) (template.HTML, error) {
	f.calls = append(f.calls, req)
	return f.resp, f.err
}

// TestWalker_PluginBlockDispatched verifies a `plugin/<slug>/<handler>`
// block routes through the dispatcher instead of the local registry.
func TestWalker_PluginBlockDispatched(t *testing.T) {
	reg := NewRegistry()
	disp := &fakeDispatcher{resp: template.HTML("<p>from plugin</p>")}
	w := New(reg).WithPluginDispatcher(disp)

	tree := BlockTree{
		{Type: "plugin/seo/sitemap-link", Attributes: map[string]any{"slug": "home"}},
	}
	res := w.Walk(tree, Context{"postId": 42})

	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	if !strings.Contains(string(res.HTML), "from plugin") {
		t.Errorf("html: got %q", res.HTML)
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatch calls: got %d want 1", len(disp.calls))
	}
	got := disp.calls[0]
	if got.BlockType != "plugin/seo/sitemap-link" {
		t.Errorf("block type: got %q", got.BlockType)
	}
	if got.Attributes["slug"] != "home" {
		t.Errorf("attrs: got %v", got.Attributes)
	}
	if got.Context["postId"] != 42 {
		t.Errorf("context: got %v", got.Context)
	}
}

// TestWalker_PluginBlockInnerRendered verifies the dispatcher receives
// already-rendered children HTML.
func TestWalker_PluginBlockInnerRendered(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister("core/paragraph", BlockSpec{
		Render: func(b Block, inner template.HTML, ctx Context) (template.HTML, error) {
			return template.HTML("<p>" + attrString(b.Attributes, "text", "") + "</p>"), nil
		},
	})
	disp := &fakeDispatcher{resp: template.HTML("<section/>")}
	w := New(reg).WithPluginDispatcher(disp)

	tree := BlockTree{
		{
			Type:       "plugin/blog/featured",
			Attributes: map[string]any{},
			InnerBlocks: []Block{
				{Type: "core/paragraph", Attributes: map[string]any{"text": "child"}},
			},
		},
	}
	w.Walk(tree, nil)
	if len(disp.calls) != 1 {
		t.Fatalf("calls: %d", len(disp.calls))
	}
	if !strings.Contains(disp.calls[0].Inner, "<p>child</p>") {
		t.Errorf("inner: got %q", disp.calls[0].Inner)
	}
}

// TestWalker_PluginBlockErrorDegrades shows that a dispatcher error
// degrades to the render-error placeholder without taking the whole
// page down.
func TestWalker_PluginBlockErrorDegrades(t *testing.T) {
	reg := NewRegistry()
	disp := &fakeDispatcher{err: errors.New("plugin blew up")}
	w := New(reg).WithPluginDispatcher(disp)

	tree := BlockTree{
		{Type: "plugin/foo/bar"},
	}
	res := w.Walk(tree, nil)
	if len(res.Errors) != 1 {
		t.Fatalf("errors: got %d want 1", len(res.Errors))
	}
	if !strings.Contains(string(res.HTML), "gn-block-error") {
		t.Errorf("html: got %q", res.HTML)
	}
}

// TestWalker_PluginBlockUnknownFallbackWhenNoDispatcher confirms a
// plugin block falls through to the standard "unknown" placeholder
// when no dispatcher is wired.
func TestWalker_PluginBlockUnknownFallbackWhenNoDispatcher(t *testing.T) {
	reg := NewRegistry()
	w := New(reg) // no dispatcher

	tree := BlockTree{{Type: "plugin/foo/bar"}}
	res := w.Walk(tree, nil)
	if len(res.Errors) != 1 {
		t.Fatalf("errors: got %d want 1", len(res.Errors))
	}
	if !errors.Is(res.Errors[0].Err, ErrUnknownBlockType) {
		t.Errorf("err: %v", res.Errors[0].Err)
	}
}

// TestParsePluginBlockType handles edge cases.
func TestParsePluginBlockType(t *testing.T) {
	cases := []struct {
		in            string
		slug, handler string
		ok            bool
	}{
		{"plugin/seo/sitemap", "seo", "sitemap", true},
		{"plugin/foo/bar/baz", "foo", "bar/baz", true},
		{"core/paragraph", "", "", false},
		{"plugin/seo", "", "", false},
		{"plugin//bar", "", "", false},
		{"plugin/seo/", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		s, h, ok := parsePluginBlockType(tc.in)
		if ok != tc.ok || s != tc.slug || h != tc.handler {
			t.Errorf("%q: got (%q,%q,%v) want (%q,%q,%v)",
				tc.in, s, h, ok, tc.slug, tc.handler, tc.ok)
		}
	}
}

// TestHookBusDispatcher_RoundTrip wires the real hook bus to a plugin
// stub that returns marshalled HTML; the dispatcher must unwrap it
// and return template.HTML.
func TestHookBusDispatcher_RoundTrip(t *testing.T) {
	bus := hooks.NewBus()
	// Plugin "myplug" subscribes to its sitemap block.
	bus.RegisterFilter("block.render:myplug/card", 10,
		func(ctx context.Context, value any, args ...any) (any, error) {
			// Read the incoming request to assert wiring.
			raw, ok := value.(json.RawMessage)
			if !ok {
				t.Errorf("plugin: value type %T", value)
			}
			var req PluginBlockRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("plugin: unmarshal: %v", err)
			}
			if req.BlockType != "plugin/myplug/card" {
				t.Errorf("block type: %q", req.BlockType)
			}
			// Return HTML as a json-encoded string.
			out, _ := json.Marshal("<article>hello</article>")
			return json.RawMessage(out), nil
		})

	disp := NewHookBusDispatcher(context.Background(), bus)
	w := New(NewRegistry()).WithPluginDispatcher(disp)
	tree := BlockTree{{Type: "plugin/myplug/card", Attributes: map[string]any{}}}
	res := w.Walk(tree, nil)
	if len(res.Errors) != 0 {
		t.Fatalf("errs: %v", res.Errors)
	}
	if !strings.Contains(string(res.HTML), "<article>hello</article>") {
		t.Errorf("html: %q", res.HTML)
	}
}

// TestHookBusDispatcher_BusError surfaces the bus error.
func TestHookBusDispatcher_BusError(t *testing.T) {
	bus := hooks.NewBus()
	want := errors.New("plugin trap")
	bus.RegisterFilter("block.render:p/h", 10,
		func(ctx context.Context, value any, args ...any) (any, error) {
			return value, want
		})
	disp := NewHookBusDispatcher(context.Background(), bus)
	_, err := disp.Dispatch("p", "h", PluginBlockRequest{})
	if !errors.Is(err, want) {
		t.Errorf("err: %v", err)
	}
}
