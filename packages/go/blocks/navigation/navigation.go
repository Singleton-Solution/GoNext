// Package navigation is the renderer integration for the core/navigation
// block. Issue #54.
//
// The block stores a menu_id attribute (UUID) and resolves it via the
// menus.Store at render time to produce a flat <ul> of items. The
// resolver is passed through the [render.Context] under the
// [ContextKeyMenuResolver] key so the renderer can stay decoupled from
// the persistence layer — tests inject an inline closure, production
// wires in [menus.Store.GetWithItemsBySlug] / [menus.Store.GetWithItems].
package navigation

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"strings"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/blocks/render"
	"github.com/Singleton-Solution/GoNext/packages/go/menus"
)

// BlockType is the canonical wire name for the navigation block.
const BlockType = "core/navigation"

// ContextKeyMenuResolver is the [render.Context] key under which a
// [MenuResolver] is expected to live. The walker boundary that mounts
// the block tree is responsible for stuffing one into the context.
const ContextKeyMenuResolver = "menuResolver"

// MenuResolver is the closure the navigation renderer uses to fetch a
// menu's items. Implementations typically wrap a [menus.Store].
type MenuResolver func(ctx context.Context, menuID uuid.UUID, slug string) (menus.MenuWithItems, error)

// NewStoreResolver builds a [MenuResolver] that delegates to a
// [menus.Store]. Looks up by UUID when menuID is non-zero; falls back
// to lookup by slug otherwise.
func NewStoreResolver(store menus.Store) MenuResolver {
	return func(ctx context.Context, menuID uuid.UUID, slug string) (menus.MenuWithItems, error) {
		if menuID != uuid.Nil {
			return store.GetWithItems(ctx, menuID)
		}
		if slug != "" {
			return store.GetWithItemsBySlug(ctx, slug)
		}
		return menus.MenuWithItems{}, errors.New("navigation: block has neither menu_id nor menu_slug")
	}
}

// Register installs the navigation block renderer onto reg.
func Register(reg *render.Registry) error {
	return reg.Register(BlockType, render.BlockSpec{
		Render: renderNav,
	})
}

// render is the per-block renderer. It pulls the resolver out of the
// context, hands it the block's menu_id / menu_slug attribute, and
// emits a flat <ul class="gn-block-navigation"> with the items sorted
// by path. If the resolver isn't installed or returns an error, the
// block emits an HTML comment carrying the failure cause — same "loud
// but non-fatal" contract as the rest of the core blocks.
func renderNav(block render.Block, _ template.HTML, ctx render.Context) (template.HTML, error) {
	resolverV, ok := ctx[ContextKeyMenuResolver]
	if !ok {
		return template.HTML("<!-- core/navigation: no menuResolver in context -->"), nil
	}
	resolver, ok := resolverV.(MenuResolver)
	if !ok {
		return template.HTML("<!-- core/navigation: menuResolver type mismatch -->"), nil
	}

	menuIDStr, _ := block.Attributes["menu_id"].(string)
	menuSlug, _ := block.Attributes["menu_slug"].(string)
	var menuID uuid.UUID
	if menuIDStr != "" {
		parsed, err := uuid.Parse(menuIDStr)
		if err == nil {
			menuID = parsed
		}
	}
	if menuID == uuid.Nil && menuSlug == "" {
		return template.HTML("<!-- core/navigation: missing menu_id and menu_slug -->"), nil
	}

	bundle, err := resolver(rootContext(ctx), menuID, menuSlug)
	if err != nil {
		return template.HTML(
			fmt.Sprintf("<!-- core/navigation: resolve failed: %s -->", htmlComment(err.Error())),
		), nil
	}
	if len(bundle.Items) == 0 {
		return template.HTML("<ul class=\"gn-block-navigation gn-block-navigation--empty\"></ul>"), nil
	}

	var b strings.Builder
	b.WriteString(`<ul class="gn-block-navigation">`)
	for _, item := range bundle.Items {
		b.WriteString(`<li class="gn-nav-item">`)
		if item.URL != "" {
			b.WriteString(`<a href="`)
			b.WriteString(template.HTMLEscapeString(item.URL))
			b.WriteString(`">`)
			b.WriteString(template.HTMLEscapeString(item.Label))
			b.WriteString(`</a>`)
		} else {
			b.WriteString(`<span>`)
			b.WriteString(template.HTMLEscapeString(item.Label))
			b.WriteString(`</span>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String()), nil
}

// rootContext extracts a stdlib context.Context from the render context
// when callers stash one under "ctx"; falls back to context.Background.
// The render package's Context is plain map[string]any and doesn't
// carry a cancellation root by default, so the walker boundary stuffs
// one in when it cares about cancellation.
func rootContext(ctx render.Context) context.Context {
	if v, ok := ctx["ctx"]; ok {
		if c, ok := v.(context.Context); ok {
			return c
		}
	}
	return context.Background()
}

// htmlComment scrubs "--" sequences out of a string so the comment
// stays well-formed HTML.
func htmlComment(s string) string {
	return strings.ReplaceAll(s, "--", "- -")
}
