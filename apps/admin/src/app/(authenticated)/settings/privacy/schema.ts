/**
 * Schema for the Privacy settings form — issue #225.
 *
 * Mirrors the keys registered by [packages/go/settings/privacy.go].
 * The settings registry endpoint at /api/v1/settings?group=privacy
 * returns the same key set; the form schema is what drives the input
 * controls.
 */
import type { Setting } from '../types';

export const PRIVACY_SCHEMA: readonly Setting[] = [
  {
    key: 'core.privacy.cookie_policy_url',
    label: 'Cookie policy URL',
    type: 'url',
    placeholder: 'https://example.com/cookies',
    help: 'Linked from the cookie consent banner and the footer.',
  },
  {
    key: 'core.privacy.cookie_policy_text',
    label: 'Cookie banner text',
    type: 'text',
    placeholder: 'This site uses cookies to keep you signed in.',
    help: 'Plain text shown in the consent banner.',
  },
  {
    key: 'core.privacy.retention.audit_days',
    label: 'Audit-log retention (days)',
    type: 'number',
    help: 'How long to keep audit entries. Use 0 to retain indefinitely.',
  },
  {
    key: 'core.privacy.retention.sessions_days',
    label: 'Sessions retention (days)',
    type: 'number',
    help: 'How long to keep expired session records. 0 to retain indefinitely.',
  },
  {
    key: 'core.privacy.retention.login_attempts_days',
    label: 'Login attempts retention (days)',
    type: 'number',
    help: 'How long to keep failed-login records. 0 to retain indefinitely.',
  },
  {
    key: 'core.privacy.allow_gdpr_self_service',
    label: 'Allow GDPR self-service data export',
    type: 'boolean',
    help: 'When enabled, signed-in users may export their personal data via /api/v1/account/data/export.',
  },
];
