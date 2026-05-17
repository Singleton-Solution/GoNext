# First-party themes

Reference themes shipped in the GoNext monorepo. Each is a separate workspace member.

See [docs/03-theme-system.md](../docs/03-theme-system.md) and [proposal S8](../docs/proposals/14-proposals-strategic.md).

## Shipped

- **[`gn-hello/`](./gn-hello)** — classic theme, minimal blog reference. Exercises the `theme.json` v1 manifest and the template hierarchy resolver with the smallest theme that's still useful. Default new-site theme until `gn-pro` lands.

## Planned

- **`gn-pro/`** — block theme demonstrating full-site editing, patterns, block style variations, dark-mode override, and Customizer surfaces. The end-to-end SDK showcase.

## Status

`gn-hello` ships with #28 (this PR). `gn-pro` is a P3 deliverable that follows the theme installer.
