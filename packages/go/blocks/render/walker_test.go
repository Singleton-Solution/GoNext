package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strings"
	"testing"
)

// Tests for the Walker — depth-first recursion, error collection,
// unknown-block fallback, context provide / consume.

// helperRegistry builds a registry with a couple of test-only block
// types so the walker tests don't depend on the core renderers.
func helperRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	r.MustRegister("test/leaf", BlockSpec{
		Render: func(b Block, _ template.HTML, _ Context) (template.HTML, error) {
			content, _ := b.Attributes["content"].(string)
			return template.HTML(fmt.Sprintf("<p>%s</p>", content)), nil
		},
	})
	r.MustRegister("test/container", BlockSpec{
		Render: func(_ Block, inner template.HTML, _ Context) (template.HTML, error) {
			return template.HTML("<section>" + string(inner) + "</section>"), nil
		},
	})
	r.MustRegister("test/erroring", BlockSpec{
		Render: func(_ Block, _ template.HTML, _ Context) (template.HTML, error) {
			return "", errors.New("boom")
		},
	})
	return r
}

func TestWalker_RendersLeafBlock(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(BlockTree{
		{Type: "test/leaf", Attributes: map[string]any{"content": "hello"}},
	}, nil)
	if got, want := string(res.HTML), "<p>hello</p>"; got != want {
		t.Fatalf("html = %q, want %q", got, want)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v, want none", res.Errors)
	}
}

func TestWalker_RecursesInnerBlocksDepthFirst(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(BlockTree{
		{
			Type:       "test/container",
			Attributes: map[string]any{},
			InnerBlocks: []Block{
				{Type: "test/leaf", Attributes: map[string]any{"content": "a"}},
				{Type: "test/leaf", Attributes: map[string]any{"content": "b"}},
			},
		},
	}, nil)
	want := "<section><p>a</p><p>b</p></section>"
	if got := string(res.HTML); got != want {
		t.Fatalf("html = %q, want %q", got, want)
	}
}

func TestWalker_UnknownBlockEmitsPlaceholder(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(BlockTree{
		{Type: "test/ghost", Attributes: map[string]any{}},
	}, nil)
	if !strings.Contains(string(res.HTML), "gn-block-unknown") {
		t.Fatalf("expected unknown placeholder, got %q", res.HTML)
	}
	if !strings.Contains(string(res.HTML), "test/ghost") {
		t.Fatalf("expected block type in placeholder, got %q", res.HTML)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(res.Errors))
	}
	if !errors.Is(res.Errors[0], ErrUnknownBlockType) {
		t.Fatalf("expected ErrUnknownBlockType, got %v", res.Errors[0].Err)
	}
	if res.Errors[0].Path != "/0" {
		t.Fatalf("path = %q, want /0", res.Errors[0].Path)
	}
}

func TestWalker_RendererErrorEmitsPlaceholderAndCollectsError(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(BlockTree{
		{Type: "test/erroring", Attributes: map[string]any{}},
	}, nil)
	if !strings.Contains(string(res.HTML), "gn-block-error") {
		t.Fatalf("expected error placeholder, got %q", res.HTML)
	}
	if len(res.Errors) != 1 || res.Errors[0].Err.Error() != "boom" {
		t.Fatalf("expected single boom error, got %+v", res.Errors)
	}
}

func TestWalker_NestedErrorPathReportsInnerLocation(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(BlockTree{
		{
			Type:       "test/container",
			Attributes: map[string]any{},
			InnerBlocks: []Block{
				{Type: "test/leaf", Attributes: map[string]any{"content": "ok"}},
				{Type: "test/erroring", Attributes: map[string]any{}},
			},
		},
	}, nil)
	if len(res.Errors) != 1 {
		t.Fatalf("len(errors) = %d, want 1", len(res.Errors))
	}
	if res.Errors[0].Path != "/0/innerBlocks/1" {
		t.Fatalf("path = %q, want /0/innerBlocks/1", res.Errors[0].Path)
	}
	// The container still rendered (with the error placeholder
	// inlined for the failing child).
	if !strings.Contains(string(res.HTML), "<section>") {
		t.Fatalf("expected container to still render: %q", res.HTML)
	}
}

func TestWalker_NilTreeRendersEmpty(t *testing.T) {
	t.Parallel()
	w := New(helperRegistry(t))
	res := w.Walk(nil, nil)
	if string(res.HTML) != "" {
		t.Fatalf("html = %q, want empty", res.HTML)
	}
}

func TestWalker_ContextProvidesAndConsumes(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("test/query", BlockSpec{
		Render: func(_ Block, inner template.HTML, _ Context) (template.HTML, error) {
			return template.HTML("<ul>" + string(inner) + "</ul>"), nil
		},
		ProvidesContext: []string{"postId"},
	})
	r.MustRegister("test/title", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			id, _ := ctx["postId"].(string)
			return template.HTML("<li>" + id + "</li>"), nil
		},
		UsesContext: []string{"postId"},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{
			Type:       "test/query",
			Attributes: map[string]any{"postId": "p-42"},
			InnerBlocks: []Block{
				{Type: "test/title", Attributes: map[string]any{}},
			},
		},
	}, nil)
	want := "<ul><li>p-42</li></ul>"
	if got := string(res.HTML); got != want {
		t.Fatalf("html = %q, want %q", got, want)
	}
}

func TestWalker_RootContextThreadedThrough(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("test/title", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			id, _ := ctx["postId"].(string)
			return template.HTML("<h1>" + id + "</h1>"), nil
		},
		UsesContext: []string{"postId"},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{Type: "test/title", Attributes: map[string]any{}},
	}, Context{"postId": "root-1"})
	if got, want := string(res.HTML), "<h1>root-1</h1>"; got != want {
		t.Fatalf("html = %q, want %q", got, want)
	}
}

func TestWalker_BystanderSeesNoContext(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("test/bystander", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			if len(ctx) != 0 {
				return "", fmt.Errorf("bystander saw context keys: %v", keysOf(ctx))
			}
			return "<p>bystander</p>", nil
		},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{Type: "test/bystander", Attributes: map[string]any{}},
	}, Context{"postId": "p-1", "postType": "post"})
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v, want none", res.Errors)
	}
	if string(res.HTML) != "<p>bystander</p>" {
		t.Fatalf("html = %q", res.HTML)
	}
}

func TestWalker_ConsumerFilteredToDeclaredKeys(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("test/consumer", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			// Should only see postId, not postType or extra.
			keys := keysOf(ctx)
			if len(keys) != 1 || keys[0] != "postId" {
				return "", fmt.Errorf("unexpected ctx keys %v", keys)
			}
			id, _ := ctx["postId"].(string)
			return template.HTML(id), nil
		},
		UsesContext: []string{"postId"},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{Type: "test/consumer", Attributes: map[string]any{}},
	}, Context{"postId": "p-1", "postType": "post", "extra": 42})
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v", res.Errors)
	}
	if string(res.HTML) != "p-1" {
		t.Fatalf("html = %q", res.HTML)
	}
}

func TestWalker_DescendantContextLayers(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Outer provides postType, inner provides postId. The leaf
	// reads both — proving the walker layers context as it
	// recurses.
	r.MustRegister("test/outer", BlockSpec{
		Render: func(_ Block, inner template.HTML, _ Context) (template.HTML, error) {
			return template.HTML("<div>" + string(inner) + "</div>"), nil
		},
		ProvidesContext: []string{"postType"},
	})
	r.MustRegister("test/inner", BlockSpec{
		Render: func(_ Block, inner template.HTML, _ Context) (template.HTML, error) {
			return template.HTML("<ul>" + string(inner) + "</ul>"), nil
		},
		ProvidesContext: []string{"postId"},
	})
	r.MustRegister("test/leaf", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			pt, _ := ctx["postType"].(string)
			id, _ := ctx["postId"].(string)
			return template.HTML(fmt.Sprintf("<li>%s/%s</li>", pt, id)), nil
		},
		UsesContext: []string{"postType", "postId"},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{
			Type:       "test/outer",
			Attributes: map[string]any{"postType": "post"},
			InnerBlocks: []Block{
				{
					Type:       "test/inner",
					Attributes: map[string]any{"postId": "p-9"},
					InnerBlocks: []Block{
						{Type: "test/leaf", Attributes: map[string]any{}},
					},
				},
			},
		},
	}, nil)
	want := "<div><ul><li>post/p-9</li></ul></div>"
	if got := string(res.HTML); got != want {
		t.Fatalf("html = %q, want %q", got, want)
	}
}

func TestWalker_ProvidedKeyMissingFromAttributesIsDropped(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister("test/provider", BlockSpec{
		Render: func(_ Block, inner template.HTML, _ Context) (template.HTML, error) {
			return template.HTML(inner), nil
		},
		ProvidesContext: []string{"postId", "postType"},
	})
	r.MustRegister("test/consumer", BlockSpec{
		Render: func(_ Block, _ template.HTML, ctx Context) (template.HTML, error) {
			if _, ok := ctx["postType"]; ok {
				return "", fmt.Errorf("postType should have been absent: %v", ctx)
			}
			id, _ := ctx["postId"].(string)
			return template.HTML(id), nil
		},
		UsesContext: []string{"postId", "postType"},
	})
	w := New(r)
	res := w.Walk(BlockTree{
		{
			Type:       "test/provider",
			Attributes: map[string]any{"postId": "p-1"}, // postType deliberately missing
			InnerBlocks: []Block{
				{Type: "test/consumer", Attributes: map[string]any{}},
			},
		},
	}, nil)
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v", res.Errors)
	}
	if string(res.HTML) != "p-1" {
		t.Fatalf("html = %q", res.HTML)
	}
}

func TestWalker_NewPanicsOnNilRegistry(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic, got none")
		}
	}()
	New(nil)
}

func TestDecodeTree_RoundTripsJSON(t *testing.T) {
	t.Parallel()
	payload := `[{"type":"core/heading","attributes":{"content":"Hi","level":2}}]`
	tree, err := DecodeTree([]byte(payload))
	if err != nil {
		t.Fatalf("DecodeTree: %v", err)
	}
	if len(tree) != 1 || tree[0].Type != "core/heading" {
		t.Fatalf("unexpected tree: %+v", tree)
	}
	if tree[0].Attributes["content"] != "Hi" {
		t.Fatalf("attribute content = %v", tree[0].Attributes["content"])
	}
}

func TestDecodeTree_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "  ", "\n\t  "} {
		tree, err := DecodeTree([]byte(in))
		if err != nil {
			t.Fatalf("DecodeTree(%q): %v", in, err)
		}
		if tree != nil {
			t.Fatalf("DecodeTree(%q) = %v, want nil", in, tree)
		}
	}
}

func TestDecodeTree_MalformedReturnsError(t *testing.T) {
	t.Parallel()
	_, err := DecodeTree([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWalkError_FormatAndUnwrap(t *testing.T) {
	t.Parallel()
	we := WalkError{
		Path:      "/0/innerBlocks/2",
		BlockType: "core/heading",
		Err:       fmt.Errorf("kaboom"),
	}
	s := we.Error()
	if !strings.Contains(s, "/0/innerBlocks/2") {
		t.Fatalf("missing path: %q", s)
	}
	if !strings.Contains(s, "core/heading") {
		t.Fatalf("missing block type: %q", s)
	}
	if !errors.Is(we, we.Err) {
		t.Fatal("Unwrap broken")
	}
}

// keysOf returns the keys of a Context as a sorted slice for stable
// comparisons in tests.
func keysOf(ctx Context) []string {
	out := make([]string, 0, len(ctx))
	for k := range ctx {
		out = append(out, k)
	}
	// Sort by hand to avoid pulling in sort.Strings in helper code.
	// Stable insertion sort is fine for small N.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// quick sanity: BlockTree decodes the same JSON shape the TS save
// pipeline emits.
func TestBlock_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := Block{
		Type: "core/paragraph",
		Attributes: map[string]any{
			"content": "hi",
		},
		InnerBlocks: []Block{
			{Type: "core/heading", Attributes: map[string]any{"level": float64(2)}},
		},
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Block
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != in.Type {
		t.Fatalf("type = %q", out.Type)
	}
	if len(out.InnerBlocks) != 1 {
		t.Fatalf("inner = %+v", out.InnerBlocks)
	}
}
