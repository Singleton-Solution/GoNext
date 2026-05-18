// Package wpcompat is the WordPress-compatibility shim for the GoNext hook
// bus. It provides an alias table that bridges familiar WP hook names
// (the_content, the_title, wp_head, init, save_post, ...) to the GoNext
// canonical, dotted, namespaced names the bus actually dispatches on
// (core.filter.the_content, core.post.saved, core.render.head, ...).
//
// # Why this package exists
//
// docs/02-plugin-system.md §5.2 commits us to dotted, namespaced hook names
// — core.filter.the_content rather than the_content — for the platform's
// own dispatch. But the whole point of WP plugin compatibility is that
// authors who learned add_filter("the_content", ...) on the WordPress side
// can keep using the same identifiers when they port to GoNext.
//
// wpcompat is the thin shim that makes both names route to the same chain.
// It is intentionally NOT a parallel hook system: there is one Bus, and the
// canonical names are what live in the Bus. The shim installs forwarding
// registrations under the WP names so that whichever side a subscriber
// listens on, they hear the event.
//
// # What the shim does
//
//   - Aliases is a static, exported map of WP hook name -> Alias. Each Alias
//     describes the native (canonical) name, whether the hook is an action
//     or a filter, and an optional PayloadAdapter that translates between
//     the WP-shaped payload (often a bare string or scalar) and the
//     GoNext-shaped payload (typed structs). Only ~20 high-value WP hooks
//     are aliased; this is a curated list, not the entire WP surface.
//
//   - Bridge.Register installs forwarding handlers on the Bus. For every
//     Alias, a registration on the canonical name re-emits the event under
//     the WP name (after running the PayloadAdapter), so plugins that
//     subscribed via the WP name receive the event with WP-shaped payload.
//     The bridge is idempotent — calling Register twice on the same bus
//     deregisters the prior forwarders before installing the new set.
//
//   - Subscribe(bus, wpName, fn) is the plugin-author-facing entry point.
//     It resolves the WP name to its canonical, registers the handler on
//     the canonical name (so it participates in the native chain at the
//     specified priority), and returns the unsubscribe closure. Unknown WP
//     names return ErrUnknownAlias rather than registering on an unknown
//     name — this catches typos at registration time rather than letting
//     the handler silently never fire.
//
// # What the shim explicitly does NOT do
//
//   - It does not invent new dispatch semantics. The Bus is authoritative;
//     wpcompat is registration sugar plus payload translation.
//
//   - It does not chase the entire WordPress hook surface. We curate ~20
//     high-value names; obscure WP hooks (publish_phone, the_excerpt_rss,
//     etc.) are not in the table and never will be — the long tail is what
//     made WP unforwards-compatible in the first place.
//
//   - It does not rewrite handler signatures dynamically. PayloadAdapter
//     converts a single payload value; if a WP hook fires with three
//     positional args and the GoNext native fires with one struct, the
//     adapter is the one place that unpacks/packs them. Subscribers are
//     expected to know the WP shape (the docs/02-plugin-system.md appendix
//     lists every mapping and its expected payload).
//
// See docs/02-plugin-system.md §5.2 for the broader naming rationale, and
// the WP Hook Aliases appendix in that file for the full per-alias mapping
// table.
package wpcompat
