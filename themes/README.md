# First-party themes

Reference themes shipped in the GoNext monorepo.

See [docs/03-theme-system.md](../docs/03-theme-system.md) and [proposal S8](../docs/proposals/14-proposals-strategic.md).

## Themes

- **[`gn-hello/`](./gn-hello)** — classic minimal reference theme. Exercises the `theme.json` v1 manifest and the four template-hierarchy fallbacks (`index`, `single`, `archive`, `404`) with the smallest theme that's still useful. Default new-site theme.
- **[`gn-pro/`](./gn-pro)** — full block theme demonstrating production conventions: 9 templates, 5 template parts, full `theme.json` schema (10 colors, 4 gradients, 3 font families, 6 font sizes, multi-step spacing scale, dual content widths, `styles.elements` for h1-h6 + a + button).

## Status

Both themes are P3 deliverables. `gn-hello` (#28) keeps the bar low for theme authoring; `gn-pro` (#32) demonstrates the breadth of supported features.
