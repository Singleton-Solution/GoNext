# First-party plugins

Reference plugins shipped in the GoNext monorepo. Each is a separate workspace member with its own manifest and build pipeline.

See [docs/02-plugin-system.md](../docs/02-plugin-system.md) for the plugin architecture and [proposal S8](../docs/proposals/14-proposals-strategic.md) for the first-party plugin slate.

## Planned

- **`gn-seo/`** — sitemap, meta tags, OpenGraph, schema.org, redirects, robots.txt. Replaces Yoast / Rank Math for the common case.
- **`gn-forms/`** — drag-drop form builder, submissions store, spam guard. Replaces CF7 / WPForms for the common case.
- **`gn-shop/`** — lightweight ecommerce (products, cart, Stripe Checkout, basic inventory). *Not* a WooCommerce replacement.

## Status

Empty — these are P4 deliverables (plugin runtime exists first). See [ROADMAP.md](../ROADMAP.md).
