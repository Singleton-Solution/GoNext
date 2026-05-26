/**
 * PublicShell — the React envelope that turns a `RenderResult` into a
 * paint-ready node.
 *
 * Two responsibilities:
 *
 *  1. Inject the trusted `bodyHtml` (theme header + main + footer)
 *     verbatim via `dangerouslySetInnerHTML`. The Go-side block walker
 *     escapes user input on the way in, so the strings reaching this
 *     boundary are safe by construction. Wrapping the injection in a
 *     React component keeps the route handlers small and lets us
 *     snapshot the structure from tests.
 *  2. Wrap the themed body in the brand site chrome (sticky nav at top,
 *     forest footer at bottom) when `withChrome` is set. The chrome
 *     uses the Living-Systems wordmark and the same nav links as the
 *     marketing landing, so single-post pages and category archives
 *     keep the brand surface around the theme's content rather than
 *     dropping the visitor into bare-theme chrome.
 *
 *  The "wrap with chrome" mode is opt-in because not every consumer
 *  wants it — e.g. a future preview iframe will paint a barebones
 *  shell without the marketing chrome.
 *
 *  The two `dangerouslySetInnerHTML` calls are intentional:
 *
 *   - `bodyHtml` is the assembled header + main + footer string. The
 *     main region was produced by our block walker (which HTML-escapes
 *     user input via @gonext/blocks-core's `escapeHtml`). The header /
 *     footer parts came from the Go-side walker over template-part
 *     HTML the theme ships — trusted at install time.
 *
 *   - `cssCustomProperties` is the `:root { ... }` block emitted by
 *     the Go-side `EmitCSSCustomProperties`. The function whitelists
 *     token slugs and values during parse, so the output is safe to
 *     drop into a `<style>` element.
 */
import type { ReactElement, ReactNode } from 'react';

import { MarketingFooter } from '@/components/marketing/Footer';
import { MarketingNav } from '@/components/marketing/Nav';

interface PublicShellProps {
  /** Already-assembled HTML body. Trusted — see file header. */
  bodyHtml: string;
  /** `:root { ... }` CSS block from the active theme. Trusted. */
  cssCustomProperties: string;
  /** Template basename — surfaced for e2e assertions. */
  templateBasename: string;
  /**
   * Wrap the themed body in the brand site chrome (sticky nav, forest
   * footer). Defaults to true on routes that render through the public
   * shell — the legacy "bare" mode is reserved for preview surfaces.
   */
  withChrome?: boolean;
  /**
   * Optional slot rendered between the themed body and the brand
   * footer. The catch-all route uses this to drop the comments
   * thread directly under the post content but inside the chrome.
   */
  children?: ReactNode;
}

export function PublicShell({
  bodyHtml,
  cssCustomProperties,
  templateBasename,
  withChrome = true,
  children,
}: PublicShellProps): ReactElement {
  const site = (
    <>
      <div
        className="gn-site"
        data-gn-template={templateBasename}
        // eslint-disable-next-line react/no-danger
        dangerouslySetInnerHTML={{ __html: bodyHtml }}
      />
      {children}
    </>
  );

  return (
    <>
      {/* Theme tokens. The Go side already brace-validated the CSS. */}
      <style
        data-gn-theme="custom-properties"
        // eslint-disable-next-line react/no-danger
        dangerouslySetInnerHTML={{ __html: cssCustomProperties }}
      />
      {/* Hidden metadata marker so e2e tests can assert template choice
          without parsing X-* headers. */}
      <meta
        name="gn:template"
        content={templateBasename}
        data-gn-template={templateBasename}
      />
      {withChrome ? (
        <div className="min-h-screen bg-paper text-ink">
          <MarketingNav />
          <main>{site}</main>
          <MarketingFooter />
        </div>
      ) : (
        site
      )}
    </>
  );
}
