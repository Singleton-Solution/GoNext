/**
 * Renders a single doc page from `apps/docs/content/docs/<slug>.md`.
 *
 * Statically generated at build time via `generateStaticParams`. The page
 * never executes at request time on the deployed site — every URL has a
 * matching HTML file in `.next/server/app/docs/...`.
 */
import { notFound } from 'next/navigation';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import { Sidebar } from '@/components/Sidebar';
import { TocSidebar } from '@/components/TocSidebar';
import { PageNav } from '@/components/PageNav';
import { buildNav, findPage, listPages } from '@/lib/content';
import { renderMarkdown } from '@/lib/mdx';

interface PageProps {
  params: Promise<{ slug?: string[] }>;
}

export async function generateStaticParams(): Promise<{ slug: string[] }[]> {
  const pages = await listPages('docs');
  return pages
    .filter((p) => p.slug !== '')
    .map((p) => ({ slug: p.slugParts }));
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { slug = [] } = await params;
  const page = await findPage('docs', slug);
  if (!page) return {};
  return {
    title: page.meta.title,
    description: page.meta.description,
  };
}

export default async function DocPage({ params }: PageProps): Promise<ReactElement> {
  const { slug = [] } = await params;
  const page = await findPage('docs', slug);
  if (!page) notFound();

  const [nodes, allPages, rendered] = await Promise.all([
    buildNav('docs'),
    listPages('docs'),
    renderMarkdown(page.body),
  ]);

  return (
    <div className="docs-shell">
      <Sidebar section="docs" nodes={nodes} activeSlug={page.meta.slug} />
      <div className="docs-shell__main">
        <div className="docs-shell__content">
          <article className="docs-shell__article">
            <header>
              <h1>{page.meta.title}</h1>
              {page.meta.description && (
                <p style={{ color: 'var(--color-text-muted)', fontSize: '17px' }}>
                  {page.meta.description}
                </p>
              )}
            </header>
            <div dangerouslySetInnerHTML={{ __html: rendered.html }} />
            <PageNav pages={allPages} currentSlug={page.meta.slug} section="docs" />
          </article>
          <TocSidebar headings={rendered.headings} />
        </div>
      </div>
    </div>
  );
}
