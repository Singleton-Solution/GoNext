package site_editor

import (
	"fmt"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

// DefaultBlockValidator is the in-process BlockValidator. It accepts a
// fixed allowlist of canonical block names — the seven shipped by
// html2blocks plus the structural / site-furniture blocks the
// theme parts actually use (group, columns, site-title, navigation,
// etc.).
//
// The allowlist is intentionally NOT pulled from a global registry:
// the editor's registry is a TS-side construct (@gonext/blocks-sdk)
// and we don't want the Go API to need a copy of every block schema
// just to validate "did this name exist when the operator saved".
// A small allowlist here, plus a Register seam for plugins, covers
// the legitimate write paths without pulling in the editor's runtime.
//
// Goroutine-safe — the underlying map is guarded by a mutex.
type DefaultBlockValidator struct {
	mu     sync.RWMutex
	allowed map[string]struct{}
}

// NewDefaultBlockValidator returns a validator pre-loaded with the
// canonical core block names. Plugins extend it via Register.
func NewDefaultBlockValidator() *DefaultBlockValidator {
	v := &DefaultBlockValidator{
		allowed: make(map[string]struct{}, 32),
	}
	for _, name := range defaultAllowedBlocks() {
		v.allowed[name] = struct{}{}
	}
	return v
}

// Register adds a block name to the allowlist. Idempotent — repeated
// calls with the same name are a no-op. Exposed so plugins that ship
// custom blocks (e.g. WPC-SEO's "core/wpcseo-meta") can extend the
// validator at boot.
func (v *DefaultBlockValidator) Register(name string) {
	if name == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.allowed[name] = struct{}{}
}

// Validate walks the tree and returns nil iff every block name is
// registered. The error wraps ErrUnknownBlock and includes the
// offending name + the path through the tree so operators can find
// the broken block in the editor.
func (v *DefaultBlockValidator) Validate(tree BlockTree) error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.walk(tree, "")
}

func (v *DefaultBlockValidator) walk(blocks []Block, path string) error {
	for i, b := range blocks {
		here := fmt.Sprintf("%s[%d]", path, i)
		if b.Name == "" {
			return fmt.Errorf("%w: empty block name at %s", ErrUnknownBlock, here)
		}
		if _, ok := v.allowed[b.Name]; !ok {
			return fmt.Errorf("%w: %q at %s", ErrUnknownBlock, b.Name, here)
		}
		if len(b.InnerBlocks) > 0 {
			if err := v.walk(b.InnerBlocks, here+"."+b.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// defaultAllowedBlocks is the canonical seed list. Kept private so the
// public surface is "construct the validator + Register"; the list
// itself is an implementation detail callers should not depend on.
//
// The list mirrors the block names that:
//
//   - the html2blocks converter emits (see html2blocks.Block* consts),
//   - the gn-hello + gn-pro theme parts use today (site-title,
//     site-tagline, site-logo, navigation, search, group, columns),
//   - the future v0.2 templates will pull in (post-title, post-content,
//     post-excerpt, post-date, post-author, query-loop, template-part).
//
// Adding a block here is cheaper than the registry round-trip and
// keeps the editor + validator agreement explicit.
func defaultAllowedBlocks() []string {
	return []string{
		// html2blocks converter output.
		html2blocks.BlockParagraph,
		html2blocks.BlockHeading,
		html2blocks.BlockList,
		html2blocks.BlockImage,
		html2blocks.BlockQuote,
		html2blocks.BlockCode,
		html2blocks.BlockSeparator,
		// Site furniture blocks the theme parts use.
		"core/group",
		"core/columns",
		"core/column",
		"core/site-title",
		"core/site-tagline",
		"core/site-logo",
		"core/navigation",
		"core/search",
		"core/template-part",
		// Loop / archive blocks reserved for v0.2 but allowed here so
		// an early adopter who wires them up via a plugin doesn't get
		// stuck on the validator.
		"core/post-title",
		"core/post-content",
		"core/post-excerpt",
		"core/post-date",
		"core/post-author",
		"core/query",
		"core/query-pagination",
		"core/spacer",
		"core/html",
	}
}
