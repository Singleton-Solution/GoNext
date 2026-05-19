/**
 * Renders a single ADR from `apps/docs/content/adr/<slug>.md`.
 *
 * Same shape as the docs route — different content tree, same renderer.
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
  const pages = await listPages('adr');
  return pages
    .filter((p) => p.slug !== '')
    .map((p) => ({ slug: p.slugParts }));
}

export async function generateMetadata({ params }: PageProps): Promise<Metadata> {
  const { slug = [] } = await params;
  const page = await findPage('adr', slug);
  if (!page) return {};
  return {
    title: page.meta.title,
    description: page.meta.description,
  };
}

export default async function AdrPage({ params }: PageProps): Promise<ReactElement> {
  const { slug = [] } = await params;
  const page = await findPage('adr', slug);
  if (!page) notFound();

  const [nodes, allPages, rendered] = await Promise.all([
    buildNav('adr'),
    listPages('adr'),
    renderMarkdown(page.body),
  ]);

  return (
    <div className="docs-shell">
      <Sidebar section="adr" nodes={nodes} activeSlug={page.meta.slug} />
      <div className="docs-shell__main">
        <div className="docs-shell__content">
          <article className="docs-shell__article">
            <header>
              <h1>{page.meta.title}</h1>
            </header>
            <div dangerouslySetInnerHTML={{ __html: rendered.html }} />
            <PageNav pages={allPages} currentSlug={page.meta.slug} section="adr" />
          </article>
          <TocSidebar headings={rendered.headings} />
        </div>
      </div>
    </div>
  );
}
