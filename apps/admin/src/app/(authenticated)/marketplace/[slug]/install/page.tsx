/**
 * Marketplace install — confirmation page.
 *
 * Server component that loads the listing detail and its latest
 * version's manifest, then renders the install confirmation client
 * island. The capability review screen here is the SAME component
 * the manual install path uses (apps/admin/src/app/plugins/components/
 * CapabilityReview.tsx) — we deliberately don't duplicate the
 * consent surface so a manifest's capability description reads the
 * same regardless of how the operator reached this screen.
 *
 * Brand
 * =====
 * Headline uses the brand's italic-accent pattern: "Install <name>." —
 * the listing name swaps to Instrument Serif italic. Crumb + lead
 * share the emerald-deep underline and Geist muted lead used across
 * the marketplace surface.
 */
import Link from 'next/link';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import {
  getMarketplaceListing,
  getMarketplaceVersions,
} from '../../actions';
import { InstallConfirm } from './InstallConfirm';
import type { PluginManifest } from '../../../plugins/types';

export const dynamic = 'force-dynamic';

const backLinkStyle = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  marginBottom: 18,
  fontFamily: 'var(--font-sans)',
  fontSize: 'var(--t-sm)',
  color: 'var(--emerald-deep)',
  textDecoration: 'underline',
  textDecorationColor: 'var(--emerald-soft)',
  textUnderlineOffset: 3,
} as const;

const leadLinkStyle = {
  color: 'var(--emerald-deep)',
  textDecoration: 'underline',
  textDecorationColor: 'var(--emerald-soft)',
  textUnderlineOffset: 3,
} as const;

/**
 * Light-touch parse of the version's manifest JSON into the shape the
 * shared CapabilityReview understands. Mirrors the parsing in the
 * manual install form — lenient on the way in, the host re-validates
 * the canonical schema on the way back through.
 */
function parseManifest(raw: string): PluginManifest | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (!parsed || typeof parsed !== 'object') return null;
    const name =
      typeof parsed.name === 'string' ? parsed.name : undefined;
    const version =
      typeof parsed.version === 'string' ? parsed.version : undefined;
    if (!name || !version) return null;
    return {
      apiVersion:
        typeof parsed.apiVersion === 'string' ? parsed.apiVersion : '',
      name,
      version,
      displayName:
        typeof parsed.displayName === 'string'
          ? parsed.displayName
          : undefined,
      description:
        typeof parsed.description === 'string'
          ? parsed.description
          : undefined,
      author:
        typeof parsed.author === 'string' ? parsed.author : undefined,
      homepage:
        typeof parsed.homepage === 'string' ? parsed.homepage : undefined,
      capabilities: Array.isArray(parsed.capabilities)
        ? (parsed.capabilities.filter(
            (c) => typeof c === 'string',
          ) as string[])
        : [],
    };
  } catch {
    return null;
  }
}

export default async function MarketplaceInstallPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<ReactElement> {
  const { slug } = await params;
  const [{ listing, error }, { versions }] = await Promise.all([
    getMarketplaceListing(slug),
    getMarketplaceVersions(slug),
  ]);
  if (!listing) {
    if (error === 'not_found') notFound();
    return (
      <section>
        <Link href="/marketplace" style={backLinkStyle}>
          ← Back to marketplace
        </Link>
        <div
          role="alert"
          style={{
            padding: '12px 14px',
            background: 'var(--warning-soft)',
            color: 'var(--warning)',
            border: '1px solid var(--warning-soft)',
            borderRadius: 'var(--r-md)',
            fontFamily: 'var(--font-sans)',
            fontSize: 'var(--t-sm)',
          }}
        >
          Couldn&apos;t load listing ({error ?? 'unknown'}).
        </div>
      </section>
    );
  }
  const latest = versions[0] ?? null;
  const manifest = latest ? parseManifest(latest.manifest) : null;

  return (
    <section>
      <Link
        href={`/marketplace/${encodeURIComponent(slug)}`}
        style={backLinkStyle}
      >
        ← Back to {listing.name}
      </Link>
      <header style={{ marginBottom: 28 }}>
        <span className="eyebrow">Capability review</span>
        <h1
          className="h1"
          style={{
            margin: '8px 0 0',
            fontSize: 'clamp(36px, 4.5vw, 52px)',
            lineHeight: 0.95,
          }}
        >
          Install <em className="italic-accent">{listing.name}</em>.
        </h1>
        <p
          className="lead"
          style={{
            margin: '12px 0 0',
            maxWidth: 640,
          }}
        >
          Review the capabilities this plugin requests. The host applies
          these grants once the install completes; you can revoke them at
          any time from the{' '}
          <Link href="/plugins" style={leadLinkStyle}>
            plugins list
          </Link>{' '}
          by uninstalling.
        </p>
      </header>
      <InstallConfirm
        slug={listing.slug}
        listingName={listing.name}
        versionLabel={latest?.version ?? ''}
        capabilities={manifest?.capabilities ?? []}
      />
    </section>
  );
}
