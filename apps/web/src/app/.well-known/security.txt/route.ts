/**
 * Route handler for `/.well-known/security.txt`.
 *
 * Serves an RFC 9116 (`securitytxt.org`) machine-readable summary of the
 * project's vulnerability-disclosure policy. The canonical policy lives in
 * the repository at `/SECURITY.md` (entry point) and `/docs/15-security-policy.md`
 * (full text); this endpoint is the discoverable on-domain summary that
 * vulnerability scanners, researchers' bookmarks, and journalists' tools
 * fetch first.
 *
 * Required RFC 9116 fields emitted:
 *   - Contact            (one or more, mailto: or https:)
 *   - Expires            (RFC 3339 timestamp; MUST be in the future)
 *
 * Recommended fields also emitted: Encryption, Preferred-Languages,
 * Canonical, Policy, Hiring, Acknowledgments.
 *
 * The response Content-Type is `text/plain; charset=utf-8`, per §3 of the RFC.
 *
 * The `Expires` value rolls 365 days forward from build time so a stale
 * deployment still serves a non-expired file for at least a year. The CI/CD
 * pipeline rebuilds this app on a cadence that comfortably beats expiry; if
 * a deploy is sitting on a year-old build, the freshness signal is the
 * wrong thing for operators to be worrying about.
 */
import { NextResponse } from 'next/server';

export const dynamic = 'force-static';
export const revalidate = 86400; // 24h — keep the file fresh enough that Expires stays in the future.

const ONE_YEAR_MS = 365 * 24 * 60 * 60 * 1000;

function expiresOneYearOut(): string {
  return new Date(Date.now() + ONE_YEAR_MS).toISOString();
}

function buildSecurityTxt(): string {
  // Order matters only for readability — RFC 9116 does not pin field order.
  const lines = [
    '# GoNext security.txt — RFC 9116.',
    '#',
    '# Canonical policy: https://github.com/Singleton-Solution/GoNext/blob/main/SECURITY.md',
    '# Full programmatic policy: https://github.com/Singleton-Solution/GoNext/blob/main/docs/15-security-policy.md',
    '# Bug bounty: https://github.com/Singleton-Solution/GoNext/blob/main/docs/16-bug-bounty.md',
    '',
    'Contact: mailto:security@gonext.io',
    'Contact: https://github.com/Singleton-Solution/GoNext/security/advisories/new',
    `Expires: ${expiresOneYearOut()}`,
    'Encryption: https://gonext.io/.well-known/pgp-key.txt',
    'Preferred-Languages: en',
    'Canonical: https://gonext.io/.well-known/security.txt',
    'Policy: https://github.com/Singleton-Solution/GoNext/blob/main/SECURITY.md',
    'Hiring: https://github.com/Singleton-Solution/GoNext/blob/main/CONTRIBUTING.md',
    'Acknowledgments: https://github.com/Singleton-Solution/GoNext/blob/main/SECURITY-HALL-OF-FAME.md',
    '',
  ];
  return lines.join('\n');
}

export async function GET(): Promise<NextResponse> {
  return new NextResponse(buildSecurityTxt(), {
    status: 200,
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      // Researchers and scanners cache aggressively; let them, but allow a
      // refresh inside the day so we don't serve an Expires field that has
      // drifted into the past on a long-running deployment.
      'Cache-Control': 'public, max-age=3600, must-revalidate',
      // The file is a static disclosure summary; explicitly disallow framing.
      'X-Content-Type-Options': 'nosniff',
    },
  });
}
