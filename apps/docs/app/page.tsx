/**
 * Landing page.
 *
 * Three pieces, ordered top-down on the page:
 *  1. A hero with the project name, one-line value prop, and a Getting
 *     Started CTA that links into the first doc.
 *  2. A feature grid summarising what the docs cover.
 *  3. A direct path into the ADR list for readers who came here looking
 *     for design decisions, not how-to.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';

const FEATURES = [
  {
    title: 'Architecture',
    body: 'The shared mental model: Go backend, Next.js frontend, Postgres data layer, WASM plugins.',
    href: '/docs/00-architecture-overview',
  },
  {
    title: 'Core CMS',
    body: 'Content model, taxonomies, custom fields, revisions, comments, permalinks.',
    href: '/docs/01-core-cms',
  },
  {
    title: 'Plugin system',
    body: 'WASM host ABI, capability scopes, hook/filter dispatch, lifecycle.',
    href: '/docs/02-plugin-system',
  },
  {
    title: 'Theme system',
    body: 'Theme packaging, slot composition, template hierarchy, asset pipeline.',
    href: '/docs/03-theme-system',
  },
  {
    title: 'Block editor',
    body: 'Lexical-based authoring, block tree storage, custom blocks via SDK.',
    href: '/docs/04-block-editor',
  },
  {
    title: 'Admin API',
    body: 'REST + GraphQL surface, authentication, rate limits, OpenAPI schema.',
    href: '/docs/05-admin-api',
  },
];

export default function LandingPage(): ReactElement {
  return (
    <main className="landing">
      <section className="landing__hero">
        <h1 className="landing__title">GoNext Documentation</h1>
        <p className="landing__subtitle">
          A modern, modular CMS built on Go and Next.js. Familiar mental model,
          modern stack, plugin and theme ecosystems engineered for safety.
        </p>
        <Link href="/docs/00-architecture-overview" className="landing__cta">
          Start with the architecture overview &rarr;
        </Link>
      </section>

      <section aria-label="Documentation areas">
        <div className="feature-grid">
          {FEATURES.map((f) => (
            <Link key={f.href} href={f.href} className="feature-card">
              <h2 className="feature-card__title">{f.title}</h2>
              <p className="feature-card__body">{f.body}</p>
            </Link>
          ))}
        </div>
      </section>

      <section style={{ marginTop: '48px', textAlign: 'center' }}>
        <p style={{ color: 'var(--color-text-muted)', fontSize: '14px' }}>
          Looking for design decisions? Read the{' '}
          <Link href="/adr">Architecture Decision Records</Link>.
        </p>
      </section>
    </main>
  );
}
