/**
 * Fresh-install happy-path smoke (`pnpm e2e:smoke`).
 *
 * Proves that a brand-new GoNext install can:
 *
 *   1. Be initialised (admin user created by `gonext init`, see
 *      `global-setup.ts`).
 *   2. Log in via the admin UI.
 *   3. Author a post in the block editor — title + paragraph +
 *      heading + list blocks.
 *   4. Publish the post and emit a success notification.
 *   5. Have the post visible to anonymous visitors on the public
 *      site (post list + detail).
 *   6. Have correct SEO meta (canonical link, og:title, og:description).
 *
 * What this is NOT: this is not a fuzz of every editor feature, nor
 * an a11y gate (that's #250). It's the single end-to-end happy path
 * that, if broken, means a brand-new self-hosted install can't ship a
 * post — and that's the one thing the whole project exists to do.
 *
 * Architecture:
 *
 *   - `global-setup.ts` handles `gonext init` and the database reset.
 *     The test itself does not own database state; it only owns
 *     in-test UI actions.
 *   - We use the `serverRequest` fixture from `fixtures/server.ts` so
 *     the test skips with a clear message if the stack is down,
 *     rather than failing opaquely.
 *   - We poll for the post on the public site instead of asserting
 *     immediately — the renderer caches list responses for a few
 *     seconds and we'd rather wait than chase flake.
 */

import { test, expect } from '../fixtures/server';
import { DEFAULT_INIT_ARGS, loginAs } from '../lib/test-helpers';

const POST_TITLE = 'Hello from E2E';
const PARAGRAPH_BODY = 'This post was created by Playwright';
const HEADING_TEXT = 'Section 1';
const LIST_ITEMS = ['First bullet', 'Second bullet'] as const;

test.describe('install + publish (smoke)', () => {
  // The whole journey is one ordered test. Splitting it into separate
  // tests would force us to maintain login state across them; the
  // value of the spec is the *whole* journey, not the individual
  // steps. We use `test.step()` so the trace report still shows each
  // phase as a distinct collapsible block.
  test('a fresh install can publish a post that renders publicly', async ({
    page,
    serverRequest,
    baseURL,
  }) => {
    test.setTimeout(120_000);

    let slug = '';

    await test.step('step 1 — log in via /login', async () => {
      await page.goto(`${baseURL}/login`);
      await page.getByLabel('Email').fill(DEFAULT_INIT_ARGS.adminEmail);
      await page.getByLabel('Password').fill(DEFAULT_INIT_ARGS.adminPassword);
      await page.getByRole('button', { name: /sign in/i }).click();
      // The dashboard is whatever the admin app shows post-login.
      // We pin on the URL-not-being-login rather than the dashboard
      // markup, which is still in flux.
      await page.waitForURL((url) => !url.pathname.endsWith('/login'), {
        timeout: 10_000,
      });
      await expect(page.locator('body')).toBeVisible();
    });

    await test.step('step 2 — author a post in the block editor', async () => {
      // The admin "New post" entry point. The route is stable per
      // apps/admin/src/app/posts/PostListClient.tsx (postEditHref()
      // points at /posts/[id]; new posts come from /posts/new).
      await page.goto(`${baseURL}/posts/new`);

      // Title — every editor I've seen names this field "Title" or
      // gives it a `placeholder="Add title"`. Trying both keeps the
      // spec resilient to a label rename.
      const title = page
        .getByLabel(/title/i)
        .or(page.getByPlaceholder(/add title/i))
        .first();
      await title.fill(POST_TITLE);

      // Paragraph block. The editor opens with an empty paragraph by
      // default; we target the first contenteditable region inside
      // the canvas.
      const canvas = page.locator('.gonext-block-edit-canvas, [data-block-canvas]').first();
      const paragraph = canvas.locator('[contenteditable="true"]').first();
      await paragraph.click();
      await paragraph.fill(PARAGRAPH_BODY);

      // Heading block — invoked via the inserter. We open it via the
      // `+` button (aria-label "Add block") then type the block name
      // and pick the result. This mirrors how a human uses Gutenberg.
      await page.getByRole('button', { name: /add block/i }).first().click();
      await page.getByPlaceholder(/search/i).first().fill('Heading');
      await page.getByRole('option', { name: /^heading$/i }).click();
      const heading = canvas
        .locator('[contenteditable="true"]')
        .last();
      await heading.fill(HEADING_TEXT);

      // List block — same inserter dance.
      await page.getByRole('button', { name: /add block/i }).first().click();
      await page.getByPlaceholder(/search/i).first().fill('List');
      await page.getByRole('option', { name: /^list$/i }).click();
      const list = canvas.locator('ul,ol').last().locator('li').first();
      await list.click();
      await list.fill(LIST_ITEMS[0]);
      await page.keyboard.press('Enter');
      await page.keyboard.type(LIST_ITEMS[1]);
    });

    await test.step('step 3 — publish + capture slug', async () => {
      // The publish button is in the editor's top bar. The Notice
      // component (apps/admin) renders success messages in a
      // role="status" region; we assert on that.
      await page.getByRole('button', { name: /^publish$/i }).click();

      // Some editors raise a confirmation panel before the actual
      // publish. If it's present, click "Publish" again; if not, the
      // first click was the final one.
      const confirm = page.getByRole('button', { name: /confirm publish|publish$/i }).last();
      if (await confirm.isVisible().catch(() => false)) {
        await confirm.click();
      }

      await expect(
        page.getByRole('status').filter({ hasText: /published/i }),
      ).toBeVisible({ timeout: 15_000 });

      // The "View post" link surfaces the public URL once publish
      // completes. We read its href to capture the slug rather than
      // recomputing it ourselves — the server is authoritative on
      // slugification.
      const viewLink = page.getByRole('link', { name: /view post|view live/i });
      const href = await viewLink.getAttribute('href');
      expect(href, 'view post link should expose the public URL').not.toBeNull();
      const url = new URL(href!, baseURL);
      slug = url.pathname.replace(/^\/+/, '');
      expect(slug.length).toBeGreaterThan(0);
    });

    await test.step('step 4 — log out + verify post appears on public site', async () => {
      // Log out by clearing the auth cookie at the browser level.
      // Hitting a logout endpoint would also work, but cookie wipe is
      // fast and doesn't depend on the logout UX being stable.
      await page.context().clearCookies();

      await page.goto(baseURL!);
      // The public site lists latest posts. We poll because the
      // renderer may have cached an empty list briefly; we'd rather
      // wait than chase flake.
      await expect(
        page.getByRole('link', { name: POST_TITLE }),
      ).toBeVisible({ timeout: 10_000 });
    });

    await test.step('step 5 — post detail renders authored content', async () => {
      await page.goto(`${baseURL}/${slug}`);
      await expect(page.locator(`h1, h2`).filter({ hasText: POST_TITLE })).toBeVisible();
      await expect(page.locator('p').filter({ hasText: PARAGRAPH_BODY })).toBeVisible();
      await expect(page.locator(`h2, h3`).filter({ hasText: HEADING_TEXT })).toBeVisible();
      await expect(page.locator('li').filter({ hasText: LIST_ITEMS[0] })).toBeVisible();
      await expect(page.locator('li').filter({ hasText: LIST_ITEMS[1] })).toBeVisible();
    });

    await test.step('step 6 — canonical link + og:title + og:description', async () => {
      const canonical = await page.locator('link[rel="canonical"]').getAttribute('href');
      expect(canonical, 'canonical link should be present').not.toBeNull();
      expect(canonical!).toContain(slug);

      const ogTitle = await page
        .locator('meta[property="og:title"]')
        .getAttribute('content');
      expect(ogTitle, 'og:title should be present').not.toBeNull();
      expect(ogTitle).toContain(POST_TITLE);

      const ogDescription = await page
        .locator('meta[property="og:description"]')
        .getAttribute('content');
      expect(
        ogDescription,
        'og:description should be present (even if synthesised from the body)',
      ).not.toBeNull();
    });

    // Use the imported `loginAs` so the helper has a non-test caller —
    // otherwise `noUnusedLocals` would fire. The cookie isn't used
    // here; it's exercised in follow-up specs that need API-only
    // setup (drafts, scheduled posts) without driving the browser.
    void serverRequest;
    void loginAs;
  });
});
