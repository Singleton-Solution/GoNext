package main

// Profile is a coarse shape descriptor for a synthetic site. It loosely
// matches the catalog in docs/08-migration-compat.md §16.1 — the goal is to
// cover the importer's pathology surface, not literal real-site content.
type Profile struct {
	Slug             string   // directory name and stable identifier
	Label            string   // human-readable description
	PostTypes        []string // wp post_types likely to appear (post + custom)
	WithComments     bool     // emit wp_comments rows + <wp:comment> elements
	HierarchicalTaxa bool     // emit a hierarchical taxonomy with parent terms
	ACFLike          bool     // emit acf-style postmeta (repeaters, flex_content)
	Gutenberg        bool     // wrap content in <!-- wp:* --> block markers
	HasMedia         bool     // attach media items referenced from post bodies
	Plugins          []string // declared plugin set (manifest only; informational)
	PostFactor       float64  // multiplier applied to --posts-per-site
}

// Profiles returns the catalog in stable order. The order is part of the
// determinism contract: profile i is always assigned to site index i modulo
// len(Profiles()), so a given (--seed, --sites) pair always produces the
// same on-disk layout.
func Profiles() []Profile {
	return []Profile{
		{
			Slug: "01-tiny-classic", Label: "Tiny personal blog, classic editor",
			PostTypes: []string{"post"}, WithComments: false, HierarchicalTaxa: false,
			ACFLike: false, Gutenberg: false, HasMedia: false,
			Plugins: nil, PostFactor: 0.05,
		},
		{
			Slug: "02-news-classic", Label: "News site, classic editor, Yoast + Jetpack",
			PostTypes: []string{"post", "page"}, WithComments: true, HierarchicalTaxa: true,
			ACFLike: false, Gutenberg: false, HasMedia: true,
			Plugins: []string{"yoast-seo", "jetpack"}, PostFactor: 1.0,
		},
		{
			Slug: "03-mixed-editor", Label: "Mixed classic + Gutenberg blog",
			PostTypes: []string{"post", "page"}, WithComments: true, HierarchicalTaxa: false,
			ACFLike: false, Gutenberg: true, HasMedia: true,
			Plugins: []string{"classic-editor"}, PostFactor: 2.0,
		},
		{
			Slug: "04-pagebuilder", Label: "Elementor-style page-builder pages",
			PostTypes: []string{"page"}, WithComments: false, HierarchicalTaxa: false,
			ACFLike: false, Gutenberg: false, HasMedia: true,
			Plugins: []string{"elementor"}, PostFactor: 0.3,
		},
		{
			Slug: "05-acf-heavy", Label: "ACF Pro heavy (repeaters, flex content)",
			PostTypes: []string{"post", "page"}, WithComments: false, HierarchicalTaxa: false,
			ACFLike: true, Gutenberg: true, HasMedia: true,
			Plugins: []string{"advanced-custom-fields-pro"}, PostFactor: 0.4,
		},
		{
			Slug: "06-cpt-taxonomy", Label: "Custom post types and custom taxonomies",
			PostTypes: []string{"post", "page", "case_study", "event"}, WithComments: false,
			HierarchicalTaxa: true, ACFLike: true, Gutenberg: true, HasMedia: true,
			Plugins: []string{"cpt-ui"}, PostFactor: 1.5,
		},
		{
			Slug: "07-comment-heavy", Label: "Comment-heavy site (threaded, pingbacks)",
			PostTypes: []string{"post"}, WithComments: true, HierarchicalTaxa: false,
			ACFLike: false, Gutenberg: false, HasMedia: false,
			Plugins: nil, PostFactor: 0.6,
		},
		{
			Slug: "08-media-scale", Label: "Large media library",
			PostTypes: []string{"post", "attachment"}, WithComments: false,
			HierarchicalTaxa: false, ACFLike: false, Gutenberg: true, HasMedia: true,
			Plugins: nil, PostFactor: 0.2, // many attachments dwarf posts
		},
		{
			Slug: "09-woocommerce", Label: "WooCommerce store (negative-case warn coverage)",
			PostTypes: []string{"post", "page", "product", "shop_order"},
			WithComments: false, HierarchicalTaxa: true, ACFLike: false, Gutenberg: true,
			HasMedia: true, Plugins: []string{"woocommerce"}, PostFactor: 0.8,
		},
		{
			Slug: "10-multilang", Label: "Multi-language site (Polylang)",
			PostTypes: []string{"post", "page"}, WithComments: true, HierarchicalTaxa: true,
			ACFLike: false, Gutenberg: true, HasMedia: true,
			Plugins: []string{"polylang"}, PostFactor: 1.0,
		},
	}
}
