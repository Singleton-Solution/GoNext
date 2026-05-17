/**
 * Template-hierarchy helpers for theme authors.
 *
 * Mirrors `packages/go/theme/templates/` (resolver.go + types.go). The
 * Go resolver is the authoritative source — at request time, the host
 * picks the most-specific template name from a candidate list and
 * looks it up against the active theme's file set. This TS port
 * exposes the same precedence logic so theme authors (and editor
 * tooling) can answer "given this query, which template will fire?"
 * without round-tripping through the server.
 *
 * The two surfaces stay aligned by sharing the same fixtures: the
 * test file in `templates.test.ts` reuses the table cases from
 * `resolver_test.go`. If a case is added on the Go side, the TS test
 * gets it too — drift is a test failure, not a silent bug.
 *
 * See `docs/03-theme-system.md` §4 for the full hierarchy.
 */

/**
 * The kinds of requests a router classifies an incoming URL into.
 * The strings mirror `RequestType.String()` in `packages/go/theme/
 * templates/types.go` — they're the lowercase, stable identifiers the
 * Go side logs and that the docs reference by name.
 *
 * `'unknown'` is the zero value; passing it to `templatePath()` returns
 * an error string the caller can surface (rather than silently picking
 * a default hierarchy).
 */
export type RequestType =
  | 'unknown'
  | 'singular'
  | 'archive'
  | 'taxonomy'
  | 'author'
  | 'date'
  | 'search'
  | 'home'
  | 'front-page'
  | '404';

/**
 * The set of context hints the resolver can use to build the
 * precedence list. Mirrors the fields on Go `templates.Request`:
 * each one is optional and only the fields relevant to `type` are
 * read.
 *
 * `postSlug` doubles as the author handle for `type: 'author'` — the
 * Go side exposes the username via the same `PostSlug` field because
 * there's no dedicated `AuthorHandle` yet.
 */
export interface ContextHints {
  /**
   * Post type slug. Used by `singular` (`single-{postType}-…`) and
   * `archive` (`archive-{postType}`).
   */
  postType?: string;

  /**
   * Post slug for `singular` requests, OR the human-readable author
   * handle for `author` requests (matches Go `Request.PostSlug`).
   */
  postSlug?: string;

  /** Stringified numeric post ID for `singular` requests. */
  postID?: string;

  /** Taxonomy slug for `taxonomy` requests (e.g. `"genre"`). */
  taxonomySlug?: string;

  /** Term slug for `taxonomy` requests (e.g. `"cookbooks"`). */
  termSlug?: string;

  /**
   * Stringified term ID. Reserved for symmetry with the Go side;
   * the current hierarchy doesn't consume it.
   */
  termID?: string;

  /** Stringified author ID for `author` requests (e.g. `"42"`). */
  authorID?: string;
}

/**
 * Result of `templatePath()`. The shape mirrors Go's `(string, error)`
 * pair: `name` is the bare basename (no directory prefix, no extension)
 * of the most-specific template the request maps to, and `candidates`
 * is the *full ordered list* the resolver would walk against the
 * theme's files. The Go side stops at the first file present; here
 * we expose every candidate so callers can answer "which templates
 * would I need to ship to handle this request?" up front.
 *
 * If the request type is unknown, `name` is the empty string and
 * `error` describes the problem.
 */
export interface TemplateResolution {
  /**
   * The most-specific candidate. Equal to `candidates[0]`. Empty if
   * `error` is set.
   */
  readonly name: string;
  /** Ordered candidate list, most-specific first, always ending in `index`. */
  readonly candidates: readonly string[];
  /** Human-readable error message; empty when the request type is recognised. */
  readonly error: string;
}

/**
 * The extensions the Go resolver tries against each candidate, in
 * order. `.tsx` wins because GoNext themes are React component
 * packages; `.html` is the classic-theme / static-export fallback.
 *
 * Exported so editor tooling can offer the same precedence when it
 * scans a theme directory.
 */
export const TEMPLATE_EXTENSIONS = ['.tsx', '.html'] as const;

/**
 * Returns the precedence list of template basenames the resolver
 * would walk for `(type, hints)`. The first entry is the most-specific
 * match; the list always ends with `"index"` so callers know which
 * file is the ultimate fallback.
 *
 * The branching is intentionally explicit rather than table-driven so
 * the function reads top-to-bottom against the §4.2 hierarchy without
 * the reader having to mentally execute a template-method pattern —
 * same approach the Go side takes.
 *
 * Returns an empty array (not `null`) for unknown request types; the
 * caller is expected to surface the issue via `TemplateResolution.error`.
 */
export function buildCandidates(type: RequestType, hints: ContextHints = {}): string[] {
  switch (type) {
    case 'singular':
      return singularCandidates(hints);
    case 'archive':
      return archiveCandidates(hints);
    case 'taxonomy':
      return taxonomyCandidates(hints);
    case 'author':
      return authorCandidates(hints);
    case 'date':
      return ['date', 'archive', 'index'];
    case 'search':
      return ['search', 'index'];
    case 'home':
      return ['home', 'index'];
    case 'front-page':
      return ['front-page', 'home', 'index'];
    case '404':
      return ['404', 'index'];
    case 'unknown':
      return [];
  }
}

/**
 * Returns the resolution for `(type, hints)`. The function never
 * throws — failures surface via the `error` field — so it's safe to
 * call from React render paths or editor previews.
 *
 * @example
 *   templatePath('singular', { postType: 'book', postSlug: 'foo' });
 *   // → { name: 'single-book-foo',
 *   //     candidates: ['single-book-foo', 'single-book',
 *   //                  'single', 'singular', 'index'],
 *   //     error: '' }
 *
 * Empty hint fields are skipped (an empty `postSlug` doesn't produce
 * a `single-book-.tsx` candidate). This matches the Go resolver's
 * explicit `if req.PostSlug != ""` guards.
 */
export function templatePath(type: RequestType, hints: ContextHints = {}): TemplateResolution {
  const candidates = buildCandidates(type, hints);
  if (candidates.length === 0) {
    return {
      name: '',
      candidates: [],
      error: `templates: unknown request type "${type}" — router must set a recognised type`,
    };
  }
  return {
    // Non-null because the empty-array branch returned above.
    name: candidates[0] as string,
    candidates,
    error: '',
  };
}

/**
 * Builds the precedence list for a `singular` request. The order is:
 *
 * ```
 *   single-{postType}-{slug}     most specific, only when slug set
 *   single-{postType}-{id}       fallback when only id is known
 *   single-{postType}            any item of that post type
 *   single                       any single item (classic alias)
 *   singular                     any single item (modern name)
 *   index                        ultimate fallback
 * ```
 *
 * `single` is emitted before `singular` to match the Go side, which
 * follows the classic-WordPress expectation that a theme shipping
 * `single.tsx` wants it picked over `singular.tsx`.
 */
function singularCandidates(hints: ContextHints): string[] {
  const out: string[] = [];
  if (hints.postType) {
    if (hints.postSlug) {
      out.push(`single-${hints.postType}-${hints.postSlug}`);
    }
    if (hints.postID) {
      out.push(`single-${hints.postType}-${hints.postID}`);
    }
    out.push(`single-${hints.postType}`);
  }
  out.push('single', 'singular', 'index');
  return out;
}

/**
 * Builds the precedence list for an `archive` request:
 *
 * ```
 *   archive-{postType}
 *   archive
 *   index
 * ```
 *
 * When `postType` is empty the post-type-specific entry is skipped,
 * so a caller that wants the bare `/archive` page can pass `type:
 * 'archive'` with no `postType`.
 */
function archiveCandidates(hints: ContextHints): string[] {
  const out: string[] = [];
  if (hints.postType) {
    out.push(`archive-${hints.postType}`);
  }
  out.push('archive', 'index');
  return out;
}

/**
 * Builds the precedence list for a `taxonomy` term archive:
 *
 * ```
 *   taxonomy-{tax}-{term}
 *   taxonomy-{tax}
 *   taxonomy
 *   archive
 *   index
 * ```
 *
 * Built-in taxonomies (`category`, `post_tag`) have friendlier aliases
 * per §4.2 that are reserved for a follow-up — the MVP surface keeps
 * the generic `taxonomy-*` form only, matching the Go resolver.
 */
function taxonomyCandidates(hints: ContextHints): string[] {
  const out: string[] = [];
  if (hints.taxonomySlug) {
    if (hints.termSlug) {
      out.push(`taxonomy-${hints.taxonomySlug}-${hints.termSlug}`);
    }
    out.push(`taxonomy-${hints.taxonomySlug}`);
  }
  out.push('taxonomy', 'archive', 'index');
  return out;
}

/**
 * Builds the precedence list for an `author` archive:
 *
 * ```
 *   author-{id}
 *   author-{username}
 *   author
 *   archive
 *   index
 * ```
 *
 * The id-first order matches the Go resolver (and the task spec
 * ("`author-42.tsx` → `author-<handle>.tsx`")). The rationale: a
 * numeric permalink is always more specific than the slugged one for
 * a given user.
 */
function authorCandidates(hints: ContextHints): string[] {
  const out: string[] = [];
  if (hints.authorID) {
    out.push(`author-${hints.authorID}`);
  }
  if (hints.postSlug) {
    out.push(`author-${hints.postSlug}`);
  }
  out.push('author', 'archive', 'index');
  return out;
}
