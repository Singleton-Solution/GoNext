/**
 * Create a new redirect rule — Living-Systems brand surface.
 *
 * Brand mapping: italic-accent <Headline>, cream-paper body, the
 * brand-tokenized <RedirectForm> with mono inputs + regex
 * playground. Breadcrumb link uses the brand's emerald-deep link
 * tone.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { ArrowLeft } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import { RedirectForm } from '../RedirectForm';

export const dynamic = 'force-dynamic';

export default function NewRedirectPage(): ReactElement {
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/redirects"
          className="inline-flex w-fit items-center gap-1 text-xs text-fg-muted hover:text-emerald-deep"
        >
          <ArrowLeft size={14} aria-hidden="true" /> Redirects
        </Link>
        <span className="eyebrow">Routing &middot; new rule</span>
        <Headline as="h1" size="page">
          Catch a path before it <em>404s</em>.
        </Headline>
        <p className="lead max-w-[58ch]">
          Decide whether the source is a literal path (default) or a
          regular expression, pick the HTTP status, and save. Literal
          rules match in O(1); regex rules cost slightly more per request
          but support capture-group substitution.
        </p>
      </div>
      <RedirectForm />
    </section>
  );
}
