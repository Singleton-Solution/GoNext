# gn-hello

The first GoNext theme — a deliberately minimal **classic blog** that
exists to exercise (and document, by example) the two contracts the
theme system is built on:

1. The `theme.json` v1 manifest parsed and validated by
   [`packages/go/theme`](../../packages/go/theme).
2. The template hierarchy resolved by
   [`packages/go/theme/templates`](../../packages/go/theme/templates).

It ships no fancy blocks, no patterns, no Customizer surface, no
dark-mode override. Look at [`gn-pro`](../gn-pro) (when it lands) for
that. `gn-hello` exists so contributors can read the smallest valid
theme end-to-end in five minutes.

## What it proves

| Contract | How |
|---|---|
| `theme.json` validates | `version: 1`, 4-token palette, 2 font families, 4-step type scale, layout sizes — all valid CSS. |
| Token emission round-trips | `EmitCSSCustomProperties()` produces `--wp-preset--color--*`, `--wp-preset--font-family--*`, `--wp-preset--font-size--*`, `--wp-preset--layout--*`. `style.css` consumes them via `var()`. |
| Template hierarchy resolves | `index.html`, `single.html`, `archive.html`, `404.html` are the canonical fallbacks; the `templates` package resolves the matching `Request.Type` to each. |
| Template parts wire up | `parts/header.html` + `parts/footer.html` are referenced from every template, exercising the `TemplatePartDef` declarations in `theme.json`. |

## Files

```
gn-hello/
  theme.json              # tokens + template-part declarations
  style.css               # consumes the emitted CSS custom props
  README.md
  templates/
    index.html            # post list (RequestTypeHome / RequestTypeArchive fallback)
    single.html           # single post (RequestTypeSingular)
    archive.html          # generic archive (RequestTypeArchive)
    404.html              # not-found (RequestTypeNotFound)
  parts/
    header.html
    footer.html
```

There are intentionally no `.tsx` files. The resolver prefers `.tsx`
over `.html`; by shipping only `.html`, `gn-hello` exercises the
classic-theme fallback path and any theme that later adds a `.tsx`
sibling will visibly take precedence.

## Loading locally

The theme installer is still landing (see issue #41). Until then, the
authoritative way to use this theme is via the Go theme package
directly:

```go
data, _ := os.ReadFile("themes/gn-hello/theme.json")
tj, err := theme.Parse(data)
if err != nil { /* parse error */ }
if errs := tj.Validate(); len(errs) > 0 { /* surface to admin */ }

css := tj.EmitCSSCustomProperties()
// inject css into <head>

files := osThemeFiles{root: "themes/gn-hello"} // implements templates.ThemeFiles
resolver := templates.NewDefaultResolver()
name, err := resolver.Resolve(
    templates.Request{Type: templates.RequestTypeSingular},
    files,
)
// name == "single.html"
```

The package test
[`themes/gn-hello/themes_test.go`](./themes_test.go) wires exactly this
up against the real on-disk files and is the cleanest reference for
how the install path will work.
