# @gonext/sdk

Browser-side TypeScript SDK for GoNext plugins.

`@gonext/sdk` is what a plugin's browser bundle imports to talk to the GoNext
host. It auto-detects the plugin's slug from the import-map URL, exposes
namespaced REST shims, lets a plugin register client-side blocks, and wraps
the host's translation endpoint.

The SDK is served by the host (typically at `/_/runtime/sdk.mjs`) and pinned
in the admin's import map under the `@gonext/sdk` specifier. Plugin authors
never install it from npm — they import it as a bare specifier and the
import map resolves to the host-served bundle:

```ts
import { defineBlock, host, i18n, setHTML } from '@gonext/sdk';

defineBlock({
  name: 'acme/quote',
  title: 'Acme Quote',
  edit: ({ attributes, setAttributes }) => {
    const me = await host.users.me();
    return <blockquote>{i18n.t('quote.lead', { name: me.name })}</blockquote>;
  },
});
```

That's the whole hello-world. The SDK figures out the plugin slug from
the URL the host loaded the bundle from, and `host.users.me()` calls the
same-origin REST surface with the operator's session cookie.

## Module surface

| Export | Purpose |
| --- | --- |
| `getSlug()` | Returns the auto-detected slug, or `null`. |
| `setSlug(slug)` | Explicit override for tests / SSR. |
| `host.posts.{list,get}` | Reads from `/wp-json/wp/v2/posts`. |
| `host.users.{list,get,me}` | Reads from `/wp-json/wp/v2/users`. |
| `host.media.{list,get}` | Reads from `/wp-json/wp/v2/media`. |
| `host.cache.invalidate(tags)` | Plugin-scoped cache invalidation. |
| `defineBlock(spec)` | Forwards to the editor's `BLOCK_REGISTRY`. |
| `i18n.t(key, args)` | Translation lookup with `{placeholder}` interp. |
| `i18n.load(locale)` | Preloads a catalogue. |
| `setHTML(el, html)` | Trusted-Types-safe `innerHTML` setter. |

## Trusted Types

The admin enforces `require-trusted-types-for 'script'`. Every assignment
to `innerHTML` / `outerHTML` / `script.src` would throw at runtime in
plugin code that didn't go through a registered policy. The SDK ships a
`setHTML(el, html)` helper that routes through the host-installed
`gn-plugin` policy, so plugin code stays portable to environments with or
without TT enforcement.

## Build outputs

`pnpm --filter @gonext/sdk run build` (via `tsup`) produces:

- `dist/index.mjs` — primary ESM bundle (what the import map loads)
- `dist/index.cjs` — CJS fallback for Node test harnesses
- `dist/index.d.ts` — rolled-up TypeScript declarations

## License

Apache-2.0. Plugin authors are explicitly free to ship plugins under any
license — the SDK does not impose viral terms.
