'use client';

/**
 * InstallConfirm — capability review + install confirmation for a
 * marketplace listing.
 *
 * Reuses the CapabilityReview component shipped with the manual
 * plugin install flow (PR #385). The wording and treatment for each
 * capability id is identical regardless of whether the operator is
 * dropping a `.gnplugin` bundle on the manual install screen or
 * installing from the catalogue — that's the whole point of the
 * shared component.
 *
 * After the operator ticks the consent checkbox, the Install button
 * calls the server action which dispatches to the API's
 * POST /listings/{slug}/install. The API resolves the latest
 * compatible version, pulls the bundle, and walks the plugin
 * lifecycle.
 *
 * Brand
 * =====
 * The meta strip uses the brand's paper-2 card chrome with mono
 * version IDs. The Install button is the emerald CTA (alive/positive
 * action). The success state is a paper-2 panel with an emerald
 * checkmark; the error state borrows the danger-soft palette.
 */

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useState,
  useTransition,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { CapabilityReview } from '../../../plugins/components/CapabilityReview';
import { installMarketplacePlugin } from '../../actions';

const styles: Record<string, CSSProperties> = {
  meta: {
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    padding: '16px 20px',
    marginBottom: 18,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink-soft)',
    boxShadow: 'var(--sh-xs)',
    display: 'flex',
    flexDirection: 'column',
    gap: 6,
  },
  metaRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
  },
  metaLabel: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    color: 'var(--fg-subtle)',
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
    minWidth: 76,
  },
  metaValue: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
  },
  metaMono: {
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
  },
  submitRow: {
    display: 'flex',
    gap: 12,
    alignItems: 'center',
    flexWrap: 'wrap',
    marginTop: 20,
  },
  submit: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    background: 'var(--emerald)',
    color: 'var(--emerald-ink)',
    border: '1px solid var(--emerald)',
    borderRadius: 'var(--r-md)',
    padding: '10px 22px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-base)',
    fontWeight: 500,
    cursor: 'pointer',
    boxShadow: 'var(--sh-xs)',
    transition:
      'background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease)',
  },
  submitDisabled: {
    opacity: 0.5,
    cursor: 'not-allowed',
    boxShadow: 'none',
  },
  consentHint: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--fg-muted)',
  },
  resultOk: {
    padding: 20,
    background: 'var(--paper-2)',
    color: 'var(--ink)',
    border: '1px solid var(--emerald-soft)',
    borderLeft: '3px solid var(--emerald)',
    borderRadius: 'var(--r-lg)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-md)',
    boxShadow: 'var(--sh-xs)',
  },
  resultOkTitle: {
    fontFamily: 'var(--font-display)',
    fontWeight: 700,
    fontSize: 'var(--t-xl)',
    color: 'var(--ink)',
    margin: 0,
    letterSpacing: '-0.01em',
  },
  resultErr: {
    padding: '12px 14px',
    background: 'var(--danger-soft)',
    color: 'var(--danger)',
    border: '1px solid var(--danger-soft)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  pluginsLink: {
    color: 'var(--emerald-deep)',
    textDecoration: 'underline',
    textDecorationColor: 'var(--emerald-soft)',
    textUnderlineOffset: 3,
  },
};

export interface InstallConfirmProps {
  slug: string;
  listingName: string;
  versionLabel: string;
  capabilities: string[];
}

export function InstallConfirm({
  slug,
  listingName,
  versionLabel,
  capabilities,
}: InstallConfirmProps): ReactElement {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [acknowledged, setAcknowledged] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [installed, setInstalled] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const handleInstall = useCallback(async (): Promise<void> => {
    setError(null);
    setSubmitting(true);
    const result = await installMarketplacePlugin(slug, acknowledged);
    setSubmitting(false);
    if (result.ok) {
      setInstalled(result.data?.plugin_slug ?? slug);
      startTransition(() => router.refresh());
    } else {
      setError(result.error);
    }
  }, [slug, acknowledged, router]);

  if (installed) {
    return (
      <div role="status" style={styles.resultOk}>
        <p style={styles.resultOkTitle}>
          “{listingName}” <em className="italic-accent">installed</em>.
        </p>
        <p style={{ margin: '12px 0 0' }}>
          The plugin is in the <code style={styles.metaMono}>installed</code>{' '}
          state under the slug{' '}
          <code style={styles.metaMono}>{installed}</code>. Visit the{' '}
          <Link href="/plugins" style={styles.pluginsLink}>
            plugins list
          </Link>{' '}
          to activate it.
        </p>
      </div>
    );
  }

  const canSubmit = acknowledged && !submitting && !pending;

  return (
    <div data-testid="install-confirm">
      <div style={styles.meta}>
        <div style={styles.metaRow}>
          <span style={styles.metaLabel}>Listing</span>
          <span style={styles.metaValue}>{listingName}</span>
          <code style={styles.metaMono}>({slug})</code>
        </div>
        {versionLabel ? (
          <div style={styles.metaRow}>
            <span style={styles.metaLabel}>Version</span>
            <code style={styles.metaMono}>{versionLabel}</code>
          </div>
        ) : null}
      </div>

      <CapabilityReview
        capabilities={capabilities}
        acknowledged={acknowledged}
        onAcknowledgeChange={setAcknowledged}
        disabled={submitting}
      />

      {error ? (
        <div role="alert" style={{ ...styles.resultErr, marginTop: 16 }}>
          {error}
        </div>
      ) : null}

      <div style={styles.submitRow}>
        <button
          type="button"
          disabled={!canSubmit}
          aria-disabled={!canSubmit}
          onClick={handleInstall}
          style={{
            ...styles.submit,
            ...(canSubmit ? {} : styles.submitDisabled),
          }}
        >
          {submitting ? 'Installing…' : 'Install plugin'}
        </button>
        {!acknowledged ? (
          <span style={styles.consentHint}>
            Tick the consent box above to enable Install.
          </span>
        ) : null}
      </div>
    </div>
  );
}
