# Fonts

All three families used by GoNext are free, web-licensed, and loaded **live from Google Fonts**. No font files are vendored in this repo.

| Family | Use | Weights | Source |
|---|---|---|---|
| Space Grotesk | Display, headlines | 500, 700 | https://fonts.google.com/specimen/Space+Grotesk |
| Geist | UI, body | 400, 500, 600, 700 | https://fonts.google.com/specimen/Geist |
| JetBrains Mono | Mono, kickers, code | 400, 500 | https://fonts.google.com/specimen/JetBrains+Mono |

The import URL is at the top of `colors_and_type.css`:

```
https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@500;700&family=Geist:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap
```

If you ever need to self-host (offline build, GDPR concern), download the static `.woff2` files from Google Fonts and add a local `@font-face` block — the variable names in `colors_and_type.css` don't need to change.
