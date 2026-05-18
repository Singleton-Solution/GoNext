/**
 * Tests for `<RecoveryDialog>`.
 *
 * The component has three observable behaviours:
 *
 *  - 204 from the server → render nothing.
 *  - Autosave older than canonical → render nothing.
 *  - Autosave newer than canonical → render dialog with Restore /
 *    Discard buttons that fire the callbacks.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RecoveryDialog } from './RecoveryDialog.tsx';
import type { BlockTree } from '@gonext/blocks-sdk';

const autosaveBlocks: BlockTree = [
  { type: 'core/paragraph', attributes: { text: 'restored' }, innerBlocks: [] },
];

function fetchOk(body: unknown) {
  return vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => body,
  })) as unknown as typeof fetch;
}

function fetchStatus(status: number) {
  return vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json: async () => ({}),
  })) as unknown as typeof fetch;
}

describe('<RecoveryDialog>', () => {
  it('renders nothing when server returns 204', async () => {
    const fetchImpl = fetchStatus(204);
    const { container } = render(
      <RecoveryDialog
        postId="post-1"
        canonicalUpdatedAt={new Date().toISOString()}
        onRestore={() => {}}
        onDiscard={() => {}}
        fetchImpl={fetchImpl}
      />,
    );
    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalled();
    });
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });

  it('renders nothing when autosave is older than canonical', async () => {
    const canonical = new Date('2026-05-17T12:00:00Z').toISOString();
    const autosave = new Date('2026-05-17T11:00:00Z').toISOString();
    const fetchImpl = fetchOk({
      post_id: 'post-1',
      user_id: 'u1',
      blocks: autosaveBlocks,
      updated_at: autosave,
    });
    const { container } = render(
      <RecoveryDialog
        postId="post-1"
        canonicalUpdatedAt={canonical}
        onRestore={() => {}}
        onDiscard={() => {}}
        fetchImpl={fetchImpl}
      />,
    );
    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalled();
    });
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });

  it('renders dialog when autosave is newer than canonical', async () => {
    const canonical = new Date('2026-05-17T11:00:00Z').toISOString();
    const autosave = new Date('2026-05-17T12:00:00Z').toISOString();
    const fetchImpl = fetchOk({
      post_id: 'post-1',
      user_id: 'u1',
      blocks: autosaveBlocks,
      updated_at: autosave,
    });
    render(
      <RecoveryDialog
        postId="post-1"
        canonicalUpdatedAt={canonical}
        onRestore={() => {}}
        onDiscard={() => {}}
        fetchImpl={fetchImpl}
      />,
    );
    await waitFor(() => {
      expect(screen.queryByTestId('autosave-recovery-dialog')).not.toBeNull();
    });
  });

  it('calls onRestore with the autosaved blocks when Restore clicked', async () => {
    const onRestore = vi.fn();
    const canonical = new Date('2026-05-17T11:00:00Z').toISOString();
    const autosave = new Date('2026-05-17T12:00:00Z').toISOString();
    const fetchImpl = fetchOk({
      post_id: 'post-1',
      user_id: 'u1',
      blocks: autosaveBlocks,
      updated_at: autosave,
    });
    render(
      <RecoveryDialog
        postId="post-1"
        canonicalUpdatedAt={canonical}
        onRestore={onRestore}
        onDiscard={() => {}}
        fetchImpl={fetchImpl}
      />,
    );
    const btn = await screen.findByTestId('autosave-recovery-restore');
    const user = userEvent.setup();
    await user.click(btn);
    expect(onRestore).toHaveBeenCalledWith(autosaveBlocks);
    // After restore, the dialog dismisses itself.
    await waitFor(() => {
      expect(screen.queryByTestId('autosave-recovery-dialog')).toBeNull();
    });
  });

  it('calls onDiscard when Discard clicked', async () => {
    const onDiscard = vi.fn();
    const canonical = new Date('2026-05-17T11:00:00Z').toISOString();
    const autosave = new Date('2026-05-17T12:00:00Z').toISOString();
    const fetchImpl = fetchOk({
      post_id: 'post-1',
      user_id: 'u1',
      blocks: autosaveBlocks,
      updated_at: autosave,
    });
    render(
      <RecoveryDialog
        postId="post-1"
        canonicalUpdatedAt={canonical}
        onRestore={() => {}}
        onDiscard={onDiscard}
        fetchImpl={fetchImpl}
      />,
    );
    const btn = await screen.findByTestId('autosave-recovery-discard');
    const user = userEvent.setup();
    await user.click(btn);
    expect(onDiscard).toHaveBeenCalled();
  });
});
