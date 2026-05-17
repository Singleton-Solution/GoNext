# GoNext Pro (gn-pro)

A production-grade reference block theme for GoNext. Where
[`gn-hello`](../gn-hello) is the minimum-viable starter that proves the
contract, **gn-pro is the "what a real production theme looks like"
reference**: a richer template hierarchy, a fully populated
`theme.json`, and a complete style sheet driven only by the renderer's
emitted CSS custom properties.

## What it ships

| Surface | Count | Files |
| --- | --- | --- |
| `theme.json` | 1 | full schema: 10 colors, 4 gradients, 3 font families, 6 font sizes (one fluid), 5-step spacing scale, dual content widths, 3 shadow presets, full `styles.elements` (h1–h6 + `a` + `button`) |
| Templates   | 9 | `index`, `home`, `single`, `singular`, `page`, `archive`, `category`, `search`, `404` |
| Parts       | 5 | `header`, `footer`, `sidebar`, `comments`, `post-meta` |
| Custom templates | 2 | `page-landing`, `page-wide` |

Every visual decision in `style.css` references an emitted token
(`--wp-preset--color--*`, `--wp-preset--font-size--*`,
`--wp-preset--font-family--*`, `--wp-preset--layout--*`,
`--wp-preset--shadow--*`). There are no hard-coded colors or sizes; if
you want to re-skin gn-pro, edit `theme.json` and the whole interface
follows.

## When to use it

- You want a fully populated theme as the starting point for a serious
  site, or you need a reference for "what should a real block theme
  include?".
- You want to see how the resolver picks between `category.html`,
  `archive.html`, and `index.html` when all three exist — the test
  suite in `packages/go/theme/themes_test.go` pins the exact precedence
  per `docs/03-theme-system.md` §4.2.
- You need a working example of `templates.template-part` references
  for `header`, `footer`, `sidebar`, `comments`, and `post-meta`.

If you want the smallest possible block theme instead, copy
[`gn-hello`](../gn-hello).

## Layout map

```
themes/gn-pro/
├── theme.json         # design-token manifest (v1)
├── style.css          # production-quality styles (token-driven only)
├── screenshot.png     # 600x450 placeholder thumbnail
├── README.md
├── templates/
│   ├── index.html
│   ├── home.html
│   ├── single.html
│   ├── singular.html
│   ├── page.html
│   ├── archive.html
│   ├── category.html
│   ├── search.html
│   └── 404.html
└── parts/
    ├── header.html
    ├── footer.html
    ├── sidebar.html
    ├── comments.html
    └── post-meta.html
```

## Template hierarchy reminders

The Go resolver (`packages/go/theme/templates/resolver.go`) walks the
hierarchy in this order for the three flows gn-pro exercises most:

- **Search** (`?s=…`) → `search.html` → `index.html`. `archive.html`
  is **not** consulted for search requests.
- **Category** (`/category/<term>/`) → `taxonomy-category-<term>.html`
  → `taxonomy-category.html` → `taxonomy.html` → `archive.html` →
  `index.html`. Because gn-pro doesn't ship the taxonomy-specific
  variants, the resolver lands on `category.html` only when classic
  hierarchies are wired in; in the MVP resolver it falls through to
  `archive.html`. gn-pro ships both so the visual contract is
  defined either way.
- **404** → `404.html` → `index.html`.

See `themes_test.go` for the exact precedence assertions.

## License

MIT — see the repository [`LICENSE`](../../LICENSE).
