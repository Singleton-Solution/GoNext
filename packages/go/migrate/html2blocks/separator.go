package html2blocks

// separatorBlock returns a core/separator block. It is a leaf with no
// attributes today — the @gonext/blocks-core definition exposes a
// style toggle on the editor side that we cannot infer from a bare
// `<hr/>`, so we leave attrs empty and let the renderer apply its
// default.
func separatorBlock() Block {
	return Block{Name: BlockSeparator}
}
