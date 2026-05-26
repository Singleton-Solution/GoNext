// Package revalidate implements the outbound HTTP webhook fired by the
// REST surface when a post or page is published or updated. It's the
// "tell apps/web that an ISR cache entry just went stale" hook.
//
// # Contract
//
// On a publish or update event, the REST handler calls Client.Notify
// with the path that should be revalidated (typically "/" for the home
// feed, "/posts/{slug}" for a single post). The client issues a POST
// to:
//
//	{NEXT_REVALIDATE_URL}/api/revalidate?path={path}&secret={secret}
//
// where {NEXT_REVALIDATE_URL} is the apps/web origin (e.g.
// "https://example.com") and {secret} is a shared HMAC-equivalent token
// the Next.js route handler validates before clearing the cache.
//
// Configuration:
//
//   - GONEXT_NEXT_REVALIDATE_URL  — apps/web origin
//   - GONEXT_NEXT_REVALIDATE_SECRET — shared secret
//
// When EITHER is empty the client is a no-op — the chassis runs without
// the renderer (or against a non-ISR static host) and the REST handler
// shouldn't break in that mode.
//
// # Why not the existing webhooks/delivery framework
//
// packages/go/webhooks/delivery is the user-facing webhook fan-out
// system (operators register N webhooks per event, signed bodies,
// retries, dead-letter queue). That's the right shape for "tell the
// operator's Zapier integration about new posts".
//
// The ISR revalidation hook is the OPPOSITE shape: it's a single,
// chassis-internal endpoint that lives in apps/web by convention; the
// failure mode is "stale cache for a few seconds until the next ISR
// revalidation kicks in", not "lost integration event". A retry queue
// + signed body + DLQ would be overkill — a fire-and-forget POST with
// a short timeout is exactly the right surface.
//
// # Failure mode
//
// Notify returns errors so callers can decide whether to surface them.
// In practice the REST handlers log-and-swallow: a failed revalidation
// is a cache-staleness issue, not a reason to fail a successful POST
// of an article.
package revalidate
