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
 *
 * Brand
 * =====
 * Headline title uses the Archivo display face with an italic-serif
 * accent on the listing author — the "by" line reads as part of the
 * h1. Version IDs and SHA digests stay in Geist Mono so the operator
 * can scan them at a glance. The compatibility matrix sits on a
 * `--paper-3` "sunken" surface to mark it as reference data rather
 * than an interactive panel. Section panels are `--paper-2` cards
 * with eyebrow titles.
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
    gap: 20,
    marginBottom: 28,
    paddingBottom: 24,
    borderBottom: '1px solid var(--border)',
  },
  icon: {
    width: 72,
    height: 72,
    borderRadius: 'var(--r-lg)',
    background: 'var(--emerald-soft)',
    border: '1px solid var(--border)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontFamily: 'var(--font-display)',
    fontSize: 32,
    fontWeight: 800,
    color: 'var(--emerald-deep)',
    flex: '0 0 auto',
  },
  titleBlock: { flex: 1, minWidth: 0 },
  eyebrow: {
    display: 'inline-block',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    letterSpacing: '0.12em',
    textTransform: 'uppercase',
    color: 'var(--emerald-deep)',
    fontWeight: 500,
    marginBottom: 4,
  },
  title: {
    margin: 0,
    fontFamily: 'var(--font-display)',
    fontWeight: 800,
    fontSize: 'clamp(36px, 4.5vw, 52px)',
    lineHeight: 0.95,
    letterSpacing: '-0.03em',
    color: 'var(--ink)',
  },
  authorLine: {
    margin: '8px 0 0',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-md)',
    color: 'var(--fg-muted)',
  },
  authorAccent: {
    fontFamily: 'var(--font-serif)',
    fontStyle: 'italic',
    fontWeight: 400,
    color: 'var(--emerald-deep)',
    fontSize: '1.08em',
    marginLeft: 4,
  },
  slug: {
    margin: '4px 0 0',
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-sm)',
    color: 'var(--fg-subtle)',
  },
  installCta: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '12px 22px',
    background: 'var(--emerald)',
    color: 'var(--emerald-ink)',
    border: '1px solid var(--emerald)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-base)',
    textDecoration: 'none',
    boxShadow: 'var(--sh-xs)',
    transition:
      'background var(--dur) var(--ease), border-color var(--dur) var(--ease)',
  },
  metaRow: {
    display: 'flex',
    gap: 16,
    flexWrap: 'wrap',
    alignItems: 'center',
    marginTop: 14,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--fg-muted)',
  },
  metaItem: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 4,
  },
  card: {
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    padding: 24,
    marginBottom: 18,
    boxShadow: 'var(--sh-xs)',
  },
  cardPaper3: {
    background: 'var(--paper-3)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-lg)',
    padding: 24,
    marginBottom: 18,
  },
  cardEyebrow: {
    display: 'inline-block',
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-xs)',
    letterSpacing: '0.12em',
    textTransform: 'uppercase',
    color: 'var(--fg-subtle)',
    marginBottom: 14,
  },
  about: {
    margin: 0,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-md)',
    lineHeight: 1.55,
    color: 'var(--ink-soft)',
  },
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  th: {
    textAlign: 'left',
    padding: '8px 12px',
    borderBottom: '1px solid var(--border)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 600,
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
    color: 'var(--fg-subtle)',
  },
  td: {
    padding: '10px 12px',
    verticalAlign: 'top',
    borderBottom: '1px solid var(--border-subtle)',
  },
  tdMono: {
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-xs)',
    color: 'var(--ink-soft)',
  },
  deprecated: {
    display: 'inline-block',
    marginLeft: 6,
    padding: '1px 8px',
    background: 'var(--warning-soft)',
    color: 'var(--warning)',
    borderRadius: 'var(--r-sm)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-2xs)',
    fontWeight: 500,
  },
  ratingsHeader: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    flexWrap: 'wrap',
  },
  ratingsAverage: {
    fontFamily: 'var(--font-display)',
    fontWeight: 800,
    fontSize: 'var(--t-2xl)',
    color: 'var(--ink)',
    letterSpacing: '-0.02em',
  },
  ratingsCount: {
    color: 'var(--fg-subtle)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
  reviewsCarousel: {
    listStyle: 'none',
    margin: '16px 0 0',
    padding: 0,
    display: 'flex',
    gap: 14,
    overflowX: 'auto',
    scrollSnapType: 'x mandatory',
    paddingBottom: 8,
  },
  reviewCard: {
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    padding: 16,
    minWidth: 260,
    maxWidth: 320,
    flex: '0 0 280px',
    scrollSnapAlign: 'start',
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
  },
  reviewText: {
    margin: 0,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    lineHeight: 1.5,
    color: 'var(--ink-soft)',
  },
  reviewMeta: {
    marginTop: 4,
    fontFamily: 'var(--font-mono)',
    fontSize: 'var(--t-2xs)',
    color: 'var(--fg-subtle)',
  },
  ratingForm: {
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
    marginTop: 20,
    paddingTop: 18,
    borderTop: '1px solid var(--border)',
  },
  ratingFormLabel: {
    fontFamily: 'var(--font-sans)',
    fontWeight: 500,
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
  },
  textarea: {
    width: '100%',
    minHeight: 88,
    background: 'var(--paper)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    padding: 12,
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    resize: 'vertical',
    outline: 'none',
  },
  submit: {
    alignSelf: 'flex-start',
    background: 'var(--emerald)',
    color: 'var(--emerald-ink)',
    border: '1px solid var(--emerald)',
    borderRadius: 'var(--r-md)',
    padding: '8px 16px',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    fontWeight: 500,
    cursor: 'pointer',
    boxShadow: 'var(--sh-xs)',
  },
  errorBox: {
    padding: '10px 12px',
    background: 'var(--danger-soft)',
    color: 'var(--danger)',
    border: '1px solid var(--danger-soft)',
    borderRadius: 'var(--r-md)',
    fontSize: 'var(--t-sm)',
    fontFamily: 'var(--font-sans)',
  },
  successBox: {
    padding: '10px 12px',
    background: 'var(--success-soft)',
    color: 'var(--success)',
    border: '1px solid var(--success-soft)',
    borderRadius: 'var(--r-md)',
    fontSize: 'var(--t-sm)',
    fontFamily: 'var(--font-sans)',
  },
  homepageLink: {
    color: 'var(--emerald-deep)',
    textDecoration: 'underline',
    textDecorationColor: 'var(--emerald-soft)',
    textUnderlineOffset: 3,
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
          <span style={styles.eyebrow}>
            {listing.primary_category ?? 'Marketplace listing'}
          </span>
          <h1 style={styles.title}>{listing.name}</h1>
          {listing.author_id ? (
            <p style={styles.authorLine}>
              by<span style={styles.authorAccent}>{listing.author_id}</span>
            </p>
          ) : null}
          <p style={styles.slug}>{listing.slug}</p>
          <div style={styles.metaRow}>
            <span style={styles.metaItem}>
              <RatingStars
                value={listing.stars}
                count={listing.rating_count}
              />
            </span>
            <span style={styles.metaItem}>
              <span
                style={{
                  fontFamily: 'var(--font-mono)',
                  color: 'var(--ink)',
                }}
              >
                {listing.install_count.toLocaleString()}
              </span>
              <span style={{ color: 'var(--fg-subtle)' }}>installs</span>
            </span>
            {listing.license_spdx ? (
              <span style={styles.metaItem}>
                <span style={{ color: 'var(--fg-subtle)' }}>Licence:</span>
                <code
                  style={{
                    fontFamily: 'var(--font-mono)',
                    color: 'var(--ink)',
                  }}
                >
                  {listing.license_spdx}
                </code>
              </span>
            ) : null}
            {listing.homepage_url ? (
              <a
                href={listing.homepage_url}
                target="_blank"
                rel="noreferrer"
                style={styles.homepageLink}
              >
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
        <span style={styles.cardEyebrow}>About</span>
        <p style={styles.about}>
          {listing.summary || 'No description provided yet.'}
        </p>
      </div>

      <div style={styles.card} data-section="versions">
        <span style={styles.cardEyebrow}>Version history</span>
        {versions.length === 0 ? (
          <p
            style={{
              margin: 0,
              fontFamily: 'var(--font-sans)',
              fontSize: 'var(--t-sm)',
              color: 'var(--fg-muted)',
            }}
          >
            No published versions yet.
          </p>
        ) : (
          <table style={styles.table}>
            <thead>
              <tr>
                <th style={styles.th}>ID</th>
                <th style={styles.th}>Version</th>
                <th style={styles.th}>Published</th>
                <th style={styles.th}>SHA-256</th>
              </tr>
            </thead>
            <tbody>
              {versions.map((v) => (
                <tr key={v.id}>
                  <td style={{ ...styles.td, ...styles.tdMono }}>
                    {v.id.slice(0, 8)}
                  </td>
                  <td style={styles.td}>
                    <code
                      style={{
                        fontFamily: 'var(--font-mono)',
                        color: 'var(--ink)',
                      }}
                    >
                      {v.version}
                    </code>
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
                      ...styles.tdMono,
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

      <div style={styles.cardPaper3} data-section="compat">
        <span style={styles.cardEyebrow}>Compatibility matrix</span>
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
                  <td style={{ ...styles.td, ...styles.tdMono }}>
                    {c.host_min}
                  </td>
                  <td style={{ ...styles.td, ...styles.tdMono }}>
                    {c.host_max}
                  </td>
                  <td style={styles.td}>
                    <span
                      style={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: 6,
                        color: c.tested
                          ? 'var(--emerald-deep)'
                          : 'var(--fg-muted)',
                      }}
                    >
                      {c.tested ? '✓' : '·'} {c.tested ? 'Yes' : 'No'}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p
            style={{
              margin: 0,
              fontFamily: 'var(--font-sans)',
              fontSize: 'var(--t-sm)',
              color: 'var(--fg-muted)',
            }}
          >
            No compatibility ranges declared for the latest version.
          </p>
        )}
      </div>

      <div style={styles.card} data-section="ratings">
        <span style={styles.cardEyebrow}>Ratings &amp; reviews</span>
        <div style={styles.ratingsHeader}>
          <RatingStars value={ratings.aggregate.average} />
          <span style={styles.ratingsAverage}>
            {ratings.aggregate.average.toFixed(1)}
          </span>
          <span style={styles.ratingsCount}>
            ({ratings.aggregate.count} ratings)
          </span>
        </div>
        {ratings.ratings.length > 0 ? (
          <ul
            style={styles.reviewsCarousel}
            data-testid="reviews-carousel"
            aria-label="Recent reviews"
          >
            {ratings.ratings.map((r) => (
              <li
                key={`${r.user_id}-${r.created_at}`}
                style={styles.reviewCard}
              >
                <RatingStars value={r.stars} />
                {r.review_text ? (
                  <p style={styles.reviewText}>{r.review_text}</p>
                ) : (
                  <p
                    style={{
                      ...styles.reviewText,
                      color: 'var(--fg-faint)',
                      fontStyle: 'italic',
                    }}
                  >
                    No written review.
                  </p>
                )}
                <span style={styles.reviewMeta}>
                  {new Date(r.created_at).toLocaleDateString()}
                </span>
              </li>
            ))}
          </ul>
        ) : null}

        <form onSubmit={handleSubmitRating} style={styles.ratingForm}>
          <strong style={styles.ratingFormLabel}>Leave a rating</strong>
          <RatingStars value={stars} interactive onChange={setStars} />
          <label htmlFor="review-text" style={styles.ratingFormLabel}>
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
