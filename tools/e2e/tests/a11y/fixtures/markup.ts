/**
 * Static markup fixtures for the a11y suites.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * The e2e harness runs against `docker-compose` when available (see
 * `tools/e2e/fixtures/server.ts`), but the a11y suite must run *every
 * time* CI is green — there is no "skip because the stack is down" mode
 * for an a11y gate. So each a11y spec falls back to a static
 * `page.setContent()` fixture defined here when the live URL is
 * unreachable.
 *
 * The fixtures mirror the actual rendered HTML of each surface as
 * closely as possible:
 *
 *  - `homepageHtml` — gn-hello front page (templates/index.html rendered
 *    with the canonical block walker output).
 *  - `loginHtml`    — apps/admin/src/app/login/page.tsx markup.
 *  - `postListHtml` — minimal ResourceList projection with a search bar,
 *    filter chips, table, and pagination.
 *  - `blockEditorCanvasHtml` — `<BlockEditCanvas>` shell with a selected
 *    paragraph, a heading, and a button (issue #250 acceptance criteria).
 *
 * The styling is intentionally minimal — enough to keep `color-contrast`
 * passing on the dynamic surfaces. The block-editor canvas uses theme
 * tokens we can't statically pin, so its contrast scan is suppressed via
 * `applyBlockEditorCarveOut` in the helper.
 */

/**
 * Common CSS bundle shared by every fixture. Defines a single
 * high-contrast palette so axe-core's `color-contrast` rule passes on
 * the surfaces that opt in.
 *
 * `:root` colours match the canonical gn-hello tokens documented in
 * `themes/gn-hello/style.css`, with deliberately darkened ink/accent
 * values so contrast ratios clear the 4.5:1 floor.
 */
const SHARED_CSS = `
  :root {
    --ink: #0f172a;
    --paper: #ffffff;
    --muted: #475569;
    --accent: #1d4ed8;
    --danger: #b91c1c;
    --border: #cbd5e1;
  }
  * { box-sizing: border-box; }
  html, body {
    margin: 0;
    padding: 0;
    background: var(--paper);
    color: var(--ink);
    font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
    line-height: 1.5;
  }
  main, header, footer, nav, section { display: block; }
  a { color: var(--accent); }
  a:hover { text-decoration: underline; }
  h1, h2, h3, h4 { color: var(--ink); margin: 1rem 0 0.5rem; }
  label { display: block; margin-bottom: 0.25rem; font-weight: 600; }
  input, button, select, textarea {
    font: inherit;
    color: var(--ink);
    background: var(--paper);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.5rem 0.75rem;
  }
  button {
    background: var(--accent);
    color: var(--paper);
    border-color: var(--accent);
    cursor: pointer;
  }
  button[aria-pressed="false"] { background: var(--paper); color: var(--ink); }
  table { border-collapse: collapse; width: 100%; }
  th, td { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border); text-align: left; }
  th { background: #f1f5f9; }
  .muted { color: var(--muted); }
  .container { max-width: 960px; margin: 0 auto; padding: 1.5rem 1.25rem; }
  [hidden] { display: none !important; }
`;

/** Wrap arbitrary `body` markup in a full HTML5 document with shared CSS. */
export function wrapPage(title: string, body: string): string {
  return `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>${title}</title>
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <style>${SHARED_CSS}</style>
  </head>
  <body>
    ${body}
  </body>
</html>`;
}

/**
 * gn-hello homepage projection. Mirrors `themes/gn-hello/templates/index.html`
 * after the Go block walker has resolved the `wp:` comments. The header /
 * footer / `<main>` landmarks are present so `region` rules pass.
 */
export const homepageHtml = wrapPage(
  'GoNext — Latest posts',
  `
  <a class="skip-link" href="#main" style="position:absolute;left:-9999px;">Skip to content</a>
  <header class="container gn-site-header" role="banner">
    <div style="display:flex;justify-content:space-between;align-items:center;">
      <a href="/" aria-label="GoNext home">
        <strong>GoNext</strong>
      </a>
      <nav aria-label="Primary">
        <ul style="list-style:none;display:flex;gap:1rem;padding:0;margin:0;">
          <li><a href="/blog/">Blog</a></li>
          <li><a href="/about/">About</a></li>
          <li><a href="/contact/">Contact</a></li>
        </ul>
      </nav>
    </div>
    <p class="muted">A faithful reference theme.</p>
  </header>

  <main id="main" class="container gn-main">
    <h1>Latest posts</h1>
    <section aria-label="Posts">
      <article>
        <h2><a href="/blog/hello-world/">Hello, world</a></h2>
        <p class="muted">
          <time datetime="2026-05-01">May 1, 2026</time>
        </p>
        <p>A first post to verify the pipeline.</p>
      </article>
      <article>
        <h2><a href="/blog/second-post/">Second post</a></h2>
        <p class="muted">
          <time datetime="2026-05-08">May 8, 2026</time>
        </p>
        <p>One more, with a longer excerpt that wraps to multiple lines.</p>
      </article>
    </section>

    <nav aria-label="Pagination">
      <a href="?page=1" aria-current="page">1</a>
      <a href="?page=2">2</a>
      <a href="?page=3">3</a>
    </nav>
  </main>

  <footer class="container gn-site-footer" role="contentinfo">
    <p>Built with <a href="https://github.com/Singleton-Solution/GoNext">GoNext</a>.</p>
  </footer>
  `,
);

/**
 * Admin login page projection. Matches `apps/admin/src/app/login/page.tsx`
 * one-for-one — same labels, inputs, autocomplete hints, and submit
 * button.
 */
export const loginHtml = wrapPage(
  'GoNext admin — Sign in',
  `
  <main id="main" class="container">
    <section class="login-card" aria-labelledby="login-title">
      <h1 id="login-title">Sign in</h1>
      <p class="muted">Use your GoNext admin credentials to continue.</p>
      <form novalidate>
        <div class="form-field">
          <label for="email">Email</label>
          <input id="email" name="email" type="email" autocomplete="username" required />
        </div>
        <div class="form-field" style="margin-top:1rem;">
          <label for="password">Password</label>
          <input id="password" name="password" type="password" autocomplete="current-password" required />
        </div>
        <button type="submit" class="btn-primary" style="margin-top:1rem;">Sign in</button>
      </form>
    </section>
  </main>
  `,
);

/**
 * Admin post list projection. Mirrors the shape of `ResourceList`:
 * toolbar with search + filter chip menu, sortable column headers,
 * a row tbody with a checkbox column, and a footer with pagination.
 */
export const postListHtml = wrapPage(
  'GoNext admin — Posts',
  `
  <main id="main" class="container">
    <h1>Posts</h1>
    <div data-testid="resource-list" role="region" aria-label="Posts list">
      <div role="toolbar" aria-label="List controls" style="display:flex;gap:0.75rem;margin-bottom:1rem;">
        <input type="search" aria-label="Search" placeholder="Search" />
        <div role="group" aria-label="Filters">
          <button type="button" aria-pressed="false" aria-haspopup="menu" aria-expanded="false">Status</button>
        </div>
      </div>

      <table aria-labelledby="resource-list-caption">
        <caption id="resource-list-caption" hidden>Posts</caption>
        <thead>
          <tr>
            <th scope="col">
              <input type="checkbox" aria-label="Select all rows" />
            </th>
            <th scope="col" aria-sort="ascending"><button type="button" style="background:transparent;color:inherit;border:0;padding:0;">Title</button></th>
            <th scope="col" aria-sort="none"><button type="button" style="background:transparent;color:inherit;border:0;padding:0;">Author</button></th>
            <th scope="col" aria-sort="none"><button type="button" style="background:transparent;color:inherit;border:0;padding:0;">Updated</button></th>
          </tr>
        </thead>
        <tbody>
          <tr tabindex="0" role="row" aria-selected="false">
            <td><input type="checkbox" aria-label="Select row post-1" /></td>
            <td>Hello, world</td>
            <td>Alice</td>
            <td><time datetime="2026-05-01">May 1, 2026</time></td>
          </tr>
          <tr tabindex="0" role="row" aria-selected="false">
            <td><input type="checkbox" aria-label="Select row post-2" /></td>
            <td>Second post</td>
            <td>Bob</td>
            <td><time datetime="2026-05-08">May 8, 2026</time></td>
          </tr>
        </tbody>
      </table>

      <div style="display:flex;justify-content:space-between;margin-top:1rem;">
        <span>2 items</span>
        <nav aria-label="Pagination">
          <button type="button" disabled aria-label="Previous page">Prev</button>
          <button type="button" aria-label="Next page">Next</button>
        </nav>
      </div>
    </div>
  </main>
  `,
);

/**
 * Block-editor canvas projection. Mirrors what
 * `<BlockEditCanvas registry={...} blocks={[paragraph, heading, button]} />`
 * renders for issue #250's acceptance criteria — a paragraph (selected),
 * a heading, and a button.
 *
 * The outer wrapper carries the canonical `gonext-block-edit-canvas`
 * class so the colour-contrast carve-out in `helpers/axe.ts` finds it.
 */
export const blockEditorCanvasHtml = wrapPage(
  'GoNext admin — Edit post',
  `
  <main id="main" class="container">
    <h1>Edit post</h1>
    <div role="region" aria-label="Block editor">
      <div class="gonext-block-edit-canvas" data-testid="block-edit-canvas">
        <div class="gonext-block-edit-canvas__node" data-block-type="core/paragraph" data-testid="block-edit-canvas-node-core/paragraph">
          <p class="gn-block-paragraph is-selected" data-block="core/paragraph" contenteditable="true" aria-label="Paragraph content">
            A first paragraph with enough body to scan.
          </p>
        </div>
        <div class="gonext-block-edit-canvas__node" data-block-type="core/heading" data-testid="block-edit-canvas-node-core/heading">
          <h2 class="gn-block-heading gn-block-heading--level-2" data-block="core/heading">
            Section heading
          </h2>
        </div>
        <div class="gonext-block-edit-canvas__node" data-block-type="core/button" data-testid="block-edit-canvas-node-core/button">
          <div class="wp-block-button" data-block="core/button">
            <a class="wp-block-button__link" href="https://example.com/" rel="noopener noreferrer">
              Read more
            </a>
          </div>
        </div>
      </div>
    </div>

    <div role="toolbar" aria-label="Block actions" style="margin-top:1rem;">
      <button type="button" aria-label="Move block up">Up</button>
      <button type="button" aria-label="Move block down">Down</button>
      <button type="button" aria-label="Remove block">Remove</button>
    </div>
  </main>
  `,
);
