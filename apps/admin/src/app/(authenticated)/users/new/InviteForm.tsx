'use client';

/**
 * InviteForm — issue an invitation email to a new collaborator.
 *
 * Three fields: email (required), role (required, defaults to author —
 * the lowest-privilege role that can author content), optional message.
 *
 * The submit path is a no-op flash today: `POST /api/v1/users/invite`
 * is being designed alongside the email-template work. The form
 * captures the canonical payload the API will eventually expect so
 * the wiring is trivial when the endpoint lands.
 *
 * Visually the form lives inside a paper-2 card with a Headline carrying
 * the italic-accent rule ("Invite *someone*."). The send button is the
 * emerald CTA — same colour family as Publish, Issue token, and the
 * other "positive forward motion" actions across the admin.
 */
import { useState, type FormEvent, type ReactElement } from 'react';
import Link from 'next/link';
import { ArrowLeft, Check, Send } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

import type { UserRole } from '../types';

const ROLE_LABEL: Record<UserRole, string> = {
  super_admin: 'Super admin',
  admin: 'Admin',
  editor: 'Editor',
  author: 'Author',
  contributor: 'Contributor',
  subscriber: 'Subscriber',
};

const ROLE_ORDER: ReadonlyArray<UserRole> = [
  'admin',
  'editor',
  'author',
  'contributor',
  'subscriber',
];

export function InviteForm(): ReactElement {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<UserRole>('author');
  const [message, setMessage] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const onSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    setError(null);
    const trimmed = email.trim();
    if (!trimmed) {
      setError('Enter the new collaborator’s email.');
      return;
    }
    if (!trimmed.includes('@')) {
      setError('That doesn’t look like a complete email address.');
      return;
    }
    setSubmitting(true);
    window.setTimeout(() => {
      setSent(true);
      setSubmitting(false);
      setEmail('');
      setMessage('');
      window.setTimeout(() => setSent(false), 2400);
    }, 400);
  };

  return (
    <section className="mx-auto flex w-full max-w-[560px] flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/users"
          className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-fg-muted transition-colors hover:text-emerald-deep"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Back to users
        </Link>
        <Headline as="h1" size="sub">
          Invite <em>someone</em>.
        </Headline>
        <p className="font-sans text-sm text-fg-muted">
          We’ll email them a one-time link that lets them set a password
          and join your workspace.
        </p>
      </div>

      <Card className="border-border bg-paper-2 shadow-xs">
        <CardContent className="p-6">
          <form
            onSubmit={onSubmit}
            noValidate
            className="flex flex-col gap-4"
            data-testid="invite-form"
          >
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-email">Email</Label>
              <Input
                id="invite-email"
                type="email"
                autoComplete="off"
                placeholder="new@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                data-testid="invite-email"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-role">Role</Label>
              <Select
                value={role}
                onValueChange={(v) => setRole(v as UserRole)}
              >
                <SelectTrigger id="invite-role" data-testid="invite-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ROLE_ORDER.map((r) => (
                    <SelectItem key={r} value={r}>
                      {ROLE_LABEL[r]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="font-sans text-xs text-fg-subtle">
                Roles can be changed any time from the user’s profile.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-message">Personal note (optional)</Label>
              <textarea
                id="invite-message"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                rows={3}
                maxLength={400}
                placeholder="Welcome to the team. Slack me if anything’s unclear."
                className="resize-y rounded-md border border-border bg-paper-3 px-3 py-2 font-sans text-sm leading-snug text-ink placeholder:text-fg-faint hover:border-border-strong focus-visible:border-emerald focus-visible:shadow-focus focus-visible:outline-none"
                data-testid="invite-message"
              />
              <p className="font-sans text-xs text-fg-subtle">
                Appears at the top of the invitation email.
              </p>
            </div>

            {error ? (
              <p
                role="alert"
                className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
                data-testid="invite-error"
              >
                {error}
              </p>
            ) : null}

            <div className="flex items-center justify-end gap-3 border-t border-border pt-4">
              {sent ? (
                <span
                  className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-emerald-deep"
                  role="status"
                  data-testid="invite-sent"
                >
                  <Check className="h-3.5 w-3.5" aria-hidden="true" />
                  Invitation queued
                </span>
              ) : null}
              <Button
                type="submit"
                variant="emerald"
                disabled={submitting}
                data-testid="invite-submit"
              >
                <Send className="h-4 w-4" aria-hidden="true" />
                {submitting ? 'Sending…' : 'Send invitation'}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </section>
  );
}
