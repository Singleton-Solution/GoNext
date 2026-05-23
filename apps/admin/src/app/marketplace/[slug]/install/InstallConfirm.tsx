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
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 16,
    marginBottom: 16,
    fontSize: 14,
  },
  metaRow: { padding: '4px 0' },
  submitRow: {
    display: 'flex',
    gap: 12,
    alignItems: 'center',
    flexWrap: 'wrap',
    marginTop: 16,
  },
  submit: {
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    padding: '8px 18px',
    fontSize: 14,
    fontWeight: 500,
    cursor: 'pointer',
  },
  submitDisabled: {
    opacity: 0.5,
    cursor: 'not-allowed',
  },
  resultOk: {
    padding: 14,
    background: '#dcfce7',
    color: '#166534',
    border: '1px solid #86efac',
    borderRadius: 6,
  },
  resultErr: {
    padding: 14,
    background: '#fef2f2',
    color: '#991b1b',
    border: '1px solid #fecaca',
    borderRadius: 6,
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
        <strong>“{listingName}” installed.</strong>
        <p style={{ margin: '8px 0 0' }}>
          The plugin is in the <code>installed</code> state under the
          slug <code>{installed}</code>. Visit the{' '}
          <Link href="/plugins">plugins list</Link> to activate it.
        </p>
      </div>
    );
  }

  const canSubmit = acknowledged && !submitting && !pending;

  return (
    <div data-testid="install-confirm">
      <div style={styles.meta}>
        <div style={styles.metaRow}>
          <strong>Listing:</strong> {listingName} ({slug})
        </div>
        {versionLabel ? (
          <div style={styles.metaRow}>
            <strong>Version:</strong> <code>{versionLabel}</code>
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
          <span
            style={{ fontSize: 13, color: 'var(--color-text-muted, #6b7280)' }}
          >
            Tick the consent box above to enable Install.
          </span>
        ) : null}
      </div>
    </div>
  );
}
