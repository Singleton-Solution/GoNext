/**
 * RedactDialog — Living-Systems brand snapshot.
 *
 * Pins the visual contract:
 *  1. Dialog surface is cream paper-2 with sh-lg float.
 *  2. Each field row sits on paper (idle) / lavender-soft (selected).
 *  3. The apply button is the emerald variant (affirmative).
 *  4. The cancel button is the ghost variant (neutral).
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { RedactDialog } from './RedactDialog';

describe('RedactDialog — brand snapshot', () => {
  it('renders the dialog on a paper-2 surface with a lg float shadow', () => {
    render(
      <RedactDialog
        open
        fields={['token', 'email']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    // The dialog panel is the inner div inside the testid root.
    const dialog = screen.getByTestId('redact-dialog');
    // The backdrop sits on the forest tone (calm, not pure-black).
    expect(dialog.className).toContain('bg-forest/55');
    const panel = dialog.firstElementChild as HTMLElement;
    expect(panel.className).toContain('bg-paper-2');
    expect(panel.className).toContain('shadow-lg');
  });

  it('apply button lands on the emerald variant', () => {
    render(
      <RedactDialog
        open
        fields={['token']}
        initiallySelected={['token']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const apply = screen.getByTestId('redact-apply');
    expect(apply.className).toContain('bg-emerald');
    expect(apply.className).toContain('text-emerald-ink');
  });

  it('cancel button is a ghost variant', () => {
    render(
      <RedactDialog
        open
        fields={['token']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const cancel = screen.getByTestId('redact-cancel');
    // Ghost variant: transparent background, fg-muted text.
    expect(cancel.className).toContain('bg-transparent');
  });

  it('selected field rows pick up the lavender-soft tint', () => {
    render(
      <RedactDialog
        open
        fields={['token', 'email']}
        initiallySelected={['token']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const tokenLabel = screen
      .getByTestId('redact-field-token')
      .closest('label') as HTMLElement;
    expect(tokenLabel.className).toContain('bg-lavender-soft');

    const emailLabel = screen
      .getByTestId('redact-field-email')
      .closest('label') as HTMLElement;
    expect(emailLabel.className).toContain('bg-paper');
  });

  it('toggling a row updates the lavender highlight class', () => {
    render(
      <RedactDialog
        open
        fields={['token']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const checkbox = screen.getByTestId('redact-field-token');
    const label = checkbox.closest('label') as HTMLElement;
    expect(label.className).toContain('bg-paper');
    fireEvent.click(checkbox);
    expect(label.className).toContain('bg-lavender-soft');
  });
});
