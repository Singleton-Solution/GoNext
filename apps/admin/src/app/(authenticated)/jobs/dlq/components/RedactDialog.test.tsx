/**
 * RedactDialog — unit tests.
 *
 * Targets:
 *  - The dialog only renders when `open` is true.
 *  - Each provided field shows a checkbox; pre-selected fields are
 *    checked.
 *  - Toggling a checkbox updates internal state; Apply calls onApply
 *    with the selected set.
 *  - Cancel calls onCancel without invoking onApply.
 *  - Empty fields renders the special "no top-level fields" hint and
 *    keeps the Apply button disabled.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { RedactDialog } from './RedactDialog';

describe('RedactDialog', () => {
  it('renders nothing when closed', () => {
    const { container } = render(
      <RedactDialog
        open={false}
        fields={['a', 'b']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders one checkbox per field', () => {
    render(
      <RedactDialog
        open
        fields={['token', 'email', 'url']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId('redact-field-token')).toBeInTheDocument();
    expect(screen.getByTestId('redact-field-email')).toBeInTheDocument();
    expect(screen.getByTestId('redact-field-url')).toBeInTheDocument();
  });

  it('pre-selects initiallySelected fields', () => {
    render(
      <RedactDialog
        open
        fields={['token', 'email']}
        initiallySelected={['email']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const emailBox = screen.getByTestId('redact-field-email') as HTMLInputElement;
    const tokenBox = screen.getByTestId('redact-field-token') as HTMLInputElement;
    expect(emailBox.checked).toBe(true);
    expect(tokenBox.checked).toBe(false);
  });

  it('Apply emits the selected field set', () => {
    const onApply = vi.fn();
    render(
      <RedactDialog
        open
        fields={['token', 'email']}
        onApply={onApply}
        onCancel={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('redact-field-token'));
    fireEvent.click(screen.getByTestId('redact-apply'));
    expect(onApply).toHaveBeenCalledWith(['token']);
  });

  it('Apply is disabled when nothing is selected', () => {
    render(
      <RedactDialog
        open
        fields={['token']}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const apply = screen.getByTestId('redact-apply') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
  });

  it('Cancel calls onCancel without invoking onApply', () => {
    const onApply = vi.fn();
    const onCancel = vi.fn();
    render(
      <RedactDialog
        open
        fields={['token']}
        onApply={onApply}
        onCancel={onCancel}
      />,
    );
    fireEvent.click(screen.getByTestId('redact-cancel'));
    expect(onCancel).toHaveBeenCalled();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('shows hint when fields list is empty', () => {
    render(
      <RedactDialog
        open
        fields={[]}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId('redact-dialog-empty')).toBeInTheDocument();
    const apply = screen.getByTestId('redact-apply') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
  });

  it('Esc cancels the dialog', () => {
    const onCancel = vi.fn();
    render(
      <RedactDialog
        open
        fields={['x']}
        onApply={vi.fn()}
        onCancel={onCancel}
      />,
    );
    const dialog = screen.getByTestId('redact-dialog');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalled();
  });
});
