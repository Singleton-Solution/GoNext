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
        <p style={{ marginBottom: 12 }}>
          <Link href="/marketplace">← Back to marketplace</Link>
        </p>
        <div role="alert">Couldn’t load listing ({error ?? 'unknown'}).</div>
      </section>
    );
  }
  const latest = versions[0] ?? null;
  const manifest = latest ? parseManifest(latest.manifest) : null;

  return (
    <section>
      <p style={{ marginBottom: 12 }}>
        <Link href={`/marketplace/${encodeURIComponent(slug)}`}>
          ← Back to {listing.name}
        </Link>
      </p>
      <h1 style={{ marginTop: 0, fontSize: 22, fontWeight: 600 }}>
        Install {listing.name}
      </h1>
      <p
        style={{
          margin: '4px 0 20px',
          color: 'var(--color-text-muted, #6b7280)',
          fontSize: 14,
          maxWidth: 720,
        }}
      >
        Review the capabilities this plugin requests. The host applies
        these grants once the install completes; you can revoke them at
        any time from the{' '}
        <Link href="/plugins">plugins list</Link> by uninstalling.
      </p>
      <InstallConfirm
        slug={listing.slug}
        listingName={listing.name}
        versionLabel={latest?.version ?? ''}
        capabilities={manifest?.capabilities ?? []}
      />
    </section>
  );
}
