/**
 * Provider detection for `core/embed`.
 *
 * Identifying which embed provider a URL belongs to is the first step in
 * resolving an oEmbed payload. We keep the table tiny and explicit — the
 * full oEmbed lookup arrives in a follow-up; today the renderer falls
 * back to the raw URL when no provider matches, so adding a provider
 * here is the only contract change required.
 *
 * Each regex matches the canonical URL shape we expect to see in user
 * input. The first match wins, so put narrower patterns (e.g. Twitter
 * vs. X) first.
 */

/** A registered embed provider. */
export interface EmbedProvider {
  /** Slug stored on the block as `providerNameSlug`. */
  slug: string;
  /** Display label — used in the inspector. */
  label: string;
  /** Match the canonical URL form. */
  pattern: RegExp;
}

/**
 * Ordered list of recognised providers. Order matters when patterns could
 * overlap (e.g. Twitter and X share a host shape historically).
 */
export const EMBED_PROVIDERS: readonly EmbedProvider[] = [
  {
    slug: 'youtube',
    label: 'YouTube',
    // youtube.com/watch?v=…, youtu.be/…, youtube.com/shorts/…
    pattern: /^https?:\/\/(?:(?:www\.|m\.)?youtube\.com\/(?:watch\?v=|shorts\/|embed\/)|youtu\.be\/)/i,
  },
  {
    slug: 'vimeo',
    label: 'Vimeo',
    pattern: /^https?:\/\/(?:www\.)?vimeo\.com\//i,
  },
  {
    slug: 'twitter',
    label: 'X (Twitter)',
    // x.com/<user>/status/… and the legacy twitter.com host.
    pattern: /^https?:\/\/(?:www\.)?(?:twitter\.com|x\.com)\/[^/]+\/status\//i,
  },
  {
    slug: 'spotify',
    label: 'Spotify',
    pattern: /^https?:\/\/open\.spotify\.com\//i,
  },
] as const;

/**
 * Detect which provider a URL belongs to. Returns `null` when none of the
 * registered patterns match — the caller falls back to a raw embed in
 * that case.
 */
export function detectProvider(url: string): EmbedProvider | null {
  for (const provider of EMBED_PROVIDERS) {
    if (provider.pattern.test(url)) {
      return provider;
    }
  }
  return null;
}
