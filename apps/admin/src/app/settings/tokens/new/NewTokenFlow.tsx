'use client';

/**
 * NewTokenFlow — client component that owns the two-step
 * "form → reveal" interaction.
 *
 * The flow is deliberately stateful here rather than in the page so the
 * page itself can stay a server component. After the user confirms the
 * reveal, we navigate back to /settings/tokens; the list there will
 * fetch fresh and include the newly-minted row.
 */

import type { ReactElement } from 'react';
import { useCallback, useState } from 'react';
import { useRouter } from 'next/navigation';
import { NewTokenForm } from '../components/NewTokenForm';
import { TokenReveal } from '../components/TokenReveal';
import type { IssuedTokenView } from '../types';

export function NewTokenFlow(): ReactElement {
  const router = useRouter();
  const [issued, setIssued] = useState<IssuedTokenView | null>(null);

  const onIssued = useCallback((token: IssuedTokenView) => {
    setIssued(token);
  }, []);

  const onDismiss = useCallback(() => {
    setIssued(null);
    router.push('/settings/tokens');
  }, [router]);

  if (issued) {
    return <TokenReveal token={issued} onDismiss={onDismiss} />;
  }
  return <NewTokenForm onIssued={onIssued} />;
}
