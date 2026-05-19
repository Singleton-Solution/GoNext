/**
 * PublicShell — the React envelope that turns a `RenderResult` into a
 * paint-ready node.
 *
 * Why a component (vs. emitting raw HTML from the route): React's
 * `dangerouslySetInnerHTML` is the one supported escape hatch for
 * server-rendered HTML in App Router. Wrapping it in a component
 * keeps the route handlers small and lets us snapshot the structure
 * from tests.
 *
 * The two `dangerouslySetInnerHTML` calls are intentional:
 *
 *  - `bodyHtml` is the assembled header + main + footer string. The
 *    main region was produced by our block walker (which HTML-escapes
 *    user input via @gonext/blocks-core's `escapeHtml`). The header /
 *    footer parts came from the Go-side walker over template-part
 *    HTML the theme ships — trusted at install time.
 *
 *  - `cssCustomProperties` is the `:root { ... }` block emitted by
 *    the Go-side `EmitCSSCustomProperties`. The function whitelists
 *    token slugs and values during parse, so the output is safe to
 *    drop into a `<style>` element.
 */
import type { ReactElement } from 'react';

interface PublicShellProps {
  /** Already-assembled HTML body. Trusted — see file header. */
  bodyHtml: string;
  /** `:root { ... }` CSS block from the active theme. Trusted. */
  cssCustomProperties: string;
  /** Template basename — surfaced for e2e assertions. */
  templateBasename: string;
}

export function PublicShell({
  bodyHtml,
  cssCustomProperties,
  templateBasename,
}: PublicShellProps): ReactElement {
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
      <div
        className="gn-site"
        data-gn-template={templateBasename}
        // eslint-disable-next-line react/no-danger
        dangerouslySetInnerHTML={{ __html: bodyHtml }}
      />
    </>
  );
}
