/**
 * Landing page — the docs site front door.
 *
 * Three sections, ordered top-down:
 *  1. A hero with a giant Archivo headline ("Docs that *grow* with you.")
 *     where the italic emerald `grow` ties to the brand's
 *     italic-accent rule. Beneath it sits a Geist subtitle and two
 *     prominent CTAs — emerald primary (Read the docs) and a cream
 *     secondary (API reference).
 *  2. A forest "alive band" with organic radial-glow gradients in
 *     emerald + lavender. Inside the band lives the feature grid of
 *     subsystem entry points (each card jumps into a specific doc).
 *  3. A direct path into the ADR list for readers who came here looking
 *     for design decisions, not how-to.
 *
 * The italic emphasis (`<em>grow</em>`) follows HANDOFF.md "The italic
 * accent rule": one italic word per headline, max two. It is emphasis
 * not decoration.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { ArrowRight, BookOpen } from 'lucide-react';

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
        <span className="landing__eyebrow">Living documentation</span>
        <h1 className="landing__title">
          Docs that <em>grow</em> with you.
        </h1>
        <p className="landing__subtitle">
          Subsystem guides, architectural decisions, and the API reference for the
          GoNext platform. One coherent surface for the whole stack — Go backend,
          Next.js frontend, plugin and theme ecosystems.
        </p>
        <div className="landing__ctas">
          <Link href="/docs/00-architecture-overview" className="landing__cta landing__cta--primary">
            <BookOpen className="landing__cta-icon" aria-hidden="true" />
            Read the docs
          </Link>
          <Link href="/api" className="landing__cta landing__cta--secondary">
            API reference
            <ArrowRight className="landing__cta-icon" aria-hidden="true" />
          </Link>
        </div>
      </section>

      <section className="landing__band" aria-label="Documentation areas">
        <div className="landing__band-eyebrow">By subsystem</div>
        <h2 className="landing__band-title">
          One product for everything you used <em>five</em> for.
        </h2>
        <p className="landing__band-sub">
          Each guide is self-contained — the architecture overview is the entry
          point, but you can drop into any subsystem on its own.
        </p>
        <div className="feature-grid">
          {FEATURES.map((f) => (
            <Link key={f.href} href={f.href} className="feature-card">
              <h3 className="feature-card__title">{f.title}</h3>
              <p className="feature-card__body">{f.body}</p>
            </Link>
          ))}
        </div>
      </section>

      <p className="landing__footnote">
        Looking for design decisions? Read the{' '}
        <Link href="/adr">Architecture Decision Records</Link>.
      </p>
    </main>
  );
}
