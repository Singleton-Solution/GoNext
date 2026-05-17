/**
 * Email masking for the users list.
 *
 * The list view must never render raw email addresses in tabular form
 * (audit-trail / shoulder-surf hygiene — see docs/13-security-baseline.md
 * §PII). We keep the first character of the local part, replace the rest
 * with three stars, and leave the domain intact:
 *
 *   alice@example.com   →   a***@example.com
 *   x@y.io              →   x***@y.io
 *
 * Strings that don't look like emails (no `@`, empty local part) are passed
 * through unchanged so the UI never silently corrupts unexpected data.
 */
export function maskEmail(email: string): string {
  const at = email.indexOf('@');
  if (at <= 0) return email;
  const local = email.slice(0, at);
  const domain = email.slice(at);
  const first = local.charAt(0);
  if (!first) return email;
  return `${first}***${domain}`;
}
