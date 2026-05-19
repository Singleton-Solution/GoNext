'use client';

/**
 * ListingDetailView — client island for the listing detail screen.
 *
 * Renders the listing header (icon + name + rating + install button),
 * version history table with compatibility ranges, ratings aggregate,
 * and the rating-submission form.
 *
 * Server-fetched data is passed down as props; the client owns the
 * rating submission form state and the submission router refresh
 * after a successful POST.
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
import { RatingStars } from '../components/RatingStars';
import { submitMarketplaceRating } from '../actions';
import type {
  ListingDetail,
  RatingsResponse,
  VersionRow,
} from '../types';

const styles: Record<string, CSSProperties> = {
  header: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 16,
    marginBottom: 20,
  },
  icon: {
    width: 60,
    height: 60,
    borderRadius: 12,
    background: 'linear-gradient(135deg, #f0f4ff, #e0e7ff)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 24,
    fontWeight: 600,
    color: '#3730a3',
    flex: '0 0 auto',
  },
  titleBlock: { flex: 1, minWidth: 0 },
  title: { margin: 0, fontSize: 24, fontWeight: 600 },
  slug: {
    margin: '2px 0 8px',
    fontSize: 13,
    color: 'var(--color-text-muted, #6b7280)',
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  },
  installCta: {
    display: 'inline-block',
    padding: '10px 18px',
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    borderRadius: 6,
    textDecoration: 'none',
    fontWeight: 600,
    fontSize: 14,
  },
  metaRow: {
    display: 'flex',
    gap: 16,
    flexWrap: 'wrap',
    fontSize: 13,
    color: 'var(--color-text-muted, #6b7280)',
  },
  card: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 16,
    marginBottom: 16,
  },
  cardTitle: {
    margin: 0,
    fontSize: 14,
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--color-text-muted, #6b7280)',
    marginBottom: 12,
  },
  screenshotPlaceholder: {
    height: 160,
    background: '#f5f5f5',
    border: '1px dashed var(--color-border, #e4e6ea)',
    borderRadius: 6,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    color: 'var(--color-text-muted, #6b7280)',
    fontSize: 13,
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: 13,
  },
  th: {
    textAlign: 'left',
    padding: '6px 8px',
    borderBottom: '1px solid var(--color-border, #e4e6ea)',
    fontWeight: 600,
  },
  td: { padding: '6px 8px', verticalAlign: 'top' },
  deprecated: {
    display: 'inline-block',
    padding: '0 6px',
    background: '#fef3c7',
    color: '#92400e',
    borderRadius: 4,
    fontSize: 11,
    marginLeft: 6,
  },
  ratingForm: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    marginTop: 12,
  },
  textarea: {
    width: '100%',
    minHeight: 80,
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 8,
    fontFamily: 'inherit',
    fontSize: 13,
    resize: 'vertical',
  },
  submit: {
    alignSelf: 'flex-start',
    padding: '6px 14px',
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
  },
  errorBox: {
    padding: 10,
    background: '#fef2f2',
    color: '#991b1b',
    border: '1px solid #fecaca',
    borderRadius: 6,
    fontSize: 13,
  },
  successBox: {
    padding: 10,
    background: '#dcfce7',
    color: '#166534',
    border: '1px solid #86efac',
    borderRadius: 6,
    fontSize: 13,
  },
};

export interface ListingDetailViewProps {
  listing: ListingDetail;
  versions: VersionRow[];
  ratings: RatingsResponse;
}

export function ListingDetailView({
  listing,
  versions,
  ratings,
}: ListingDetailViewProps): ReactElement {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [stars, setStars] = useState(0);
  const [reviewText, setReviewText] = useState('');
  const [submitErr, setSubmitErr] = useState<string | null>(null);
  const [submitOk, setSubmitOk] = useState(false);

  const initial = (listing.name?.[0] ?? listing.slug[0] ?? '?').toUpperCase();
  const latestVersion = versions[0] ?? listing.latest_version ?? null;

  const handleSubmitRating = useCallback(
    async (e: React.FormEvent<HTMLFormElement>): Promise<void> => {
      e.preventDefault();
      setSubmitErr(null);
      setSubmitOk(false);
      if (stars < 1 || stars > 5) {
        setSubmitErr('Pick a star rating from 1 to 5 first.');
        return;
      }
      const result = await submitMarketplaceRating(
        listing.slug,
        stars,
        reviewText.trim() || undefined,
        latestVersion?.id,
      );
      if (result.ok) {
        setSubmitOk(true);
        setReviewText('');
        startTransition(() => router.refresh());
      } else {
        setSubmitErr(result.error);
      }
    },
    [listing.slug, stars, reviewText, latestVersion, router],
  );

  return (
    <div data-testid="listing-detail">
      <div style={styles.header}>
        <span style={styles.icon} aria-hidden="true">
          {initial}
        </span>
        <div style={styles.titleBlock}>
          <h1 style={styles.title}>{listing.name}</h1>
          <p style={styles.slug}>{listing.slug}</p>
          <div style={styles.metaRow}>
            <RatingStars value={listing.stars} count={listing.rating_count} />
            <span>{listing.install_count.toLocaleString()} installs</span>
            {listing.primary_category ? (
              <span>Category: {listing.primary_category}</span>
            ) : null}
            {listing.license_spdx ? (
              <span>Licence: {listing.license_spdx}</span>
            ) : null}
            {listing.homepage_url ? (
              <a href={listing.homepage_url} target="_blank" rel="noreferrer">
                Homepage
              </a>
            ) : null}
          </div>
        </div>
        <Link
          href={`/marketplace/${encodeURIComponent(listing.slug)}/install`}
          style={styles.installCta}
          aria-label={`Install ${listing.name}`}
        >
          Install
        </Link>
      </div>

      <div style={styles.card} data-section="description">
        <h2 style={styles.cardTitle}>About</h2>
        <p style={{ margin: 0, lineHeight: 1.5, fontSize: 14 }}>
          {listing.summary || 'No description provided yet.'}
        </p>
      </div>

      <div style={styles.card} data-section="screenshots">
        <h2 style={styles.cardTitle}>Screenshots</h2>
        <div
          style={styles.screenshotPlaceholder}
          role="img"
          aria-label="Screenshots will appear here once the publisher uploads them."
        >
          Screenshots coming soon
        </div>
      </div>

      <div style={styles.card} data-section="versions">
        <h2 style={styles.cardTitle}>Version history</h2>
        {versions.length === 0 ? (
          <p style={{ margin: 0, fontSize: 13, color: '#6b7280' }}>
            No published versions yet.
          </p>
        ) : (
          <table style={styles.table}>
            <thead>
              <tr>
                <th style={styles.th}>Version</th>
                <th style={styles.th}>Published</th>
                <th style={styles.th}>SHA-256</th>
              </tr>
            </thead>
            <tbody>
              {versions.map((v) => (
                <tr key={v.id}>
                  <td style={styles.td}>
                    <code>{v.version}</code>
                    {v.deprecated ? (
                      <span style={styles.deprecated}>deprecated</span>
                    ) : null}
                  </td>
                  <td style={styles.td}>
                    {new Date(v.published_at).toLocaleString()}
                  </td>
                  <td
                    style={{
                      ...styles.td,
                      fontFamily:
                        'ui-monospace, SFMono-Regular, Menlo, monospace',
                      fontSize: 11,
                      wordBreak: 'break-all',
                    }}
                  >
                    {v.wasm_sha256_hex.slice(0, 16)}…
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div style={styles.card} data-section="compat">
        <h2 style={styles.cardTitle}>Compatibility matrix</h2>
        {versions[0]?.compat && versions[0].compat.length > 0 ? (
          <table style={styles.table}>
            <thead>
              <tr>
                <th style={styles.th}>Host min</th>
                <th style={styles.th}>Host max</th>
                <th style={styles.th}>Tested</th>
              </tr>
            </thead>
            <tbody>
              {versions[0].compat.map((c) => (
                <tr key={`${c.host_min}-${c.host_max}`}>
                  <td style={styles.td}>
                    <code>{c.host_min}</code>
                  </td>
                  <td style={styles.td}>
                    <code>{c.host_max}</code>
                  </td>
                  <td style={styles.td}>{c.tested ? 'Yes' : 'No'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p style={{ margin: 0, fontSize: 13, color: '#6b7280' }}>
            No compatibility ranges declared for the latest version.
          </p>
        )}
      </div>

      <div style={styles.card} data-section="ratings">
        <h2 style={styles.cardTitle}>Ratings &amp; reviews</h2>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <RatingStars value={ratings.aggregate.average} />
          <span style={{ fontWeight: 600, fontSize: 15 }}>
            {ratings.aggregate.average.toFixed(1)}
          </span>
          <span style={{ color: '#6b7280', fontSize: 13 }}>
            ({ratings.aggregate.count} ratings)
          </span>
        </div>
        {ratings.ratings.length > 0 ? (
          <ul
            style={{
              listStyle: 'none',
              margin: '12px 0 0',
              padding: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
            }}
          >
            {ratings.ratings.map((r) => (
              <li
                key={`${r.user_id}-${r.created_at}`}
                style={{
                  borderTop: '1px solid #f3f4f6',
                  paddingTop: 8,
                  fontSize: 13,
                }}
              >
                <RatingStars value={r.stars} />
                {r.review_text ? (
                  <p style={{ margin: '4px 0 0' }}>{r.review_text}</p>
                ) : null}
              </li>
            ))}
          </ul>
        ) : null}

        <form onSubmit={handleSubmitRating} style={styles.ratingForm}>
          <strong style={{ fontSize: 13 }}>Leave a rating</strong>
          <RatingStars value={stars} interactive onChange={setStars} />
          <label htmlFor="review-text" style={{ fontSize: 13 }}>
            Optional written review
          </label>
          <textarea
            id="review-text"
            value={reviewText}
            onChange={(e) => setReviewText(e.target.value)}
            style={styles.textarea}
            placeholder="What did you like or dislike?"
          />
          {submitErr ? (
            <div role="alert" style={styles.errorBox}>
              {submitErr}
            </div>
          ) : null}
          {submitOk ? (
            <div role="status" style={styles.successBox}>
              Thanks — your rating was recorded.
            </div>
          ) : null}
          <button type="submit" disabled={pending} style={styles.submit}>
            {pending ? 'Submitting…' : 'Submit rating'}
          </button>
        </form>
      </div>
    </div>
  );
}
