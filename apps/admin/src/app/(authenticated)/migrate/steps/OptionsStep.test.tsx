/**
 * OptionsStep — unit tests.
 *
 * Pins the radio defaults and the role-override editor's add/remove
 * flow. The wizard-level navigation is covered by the parent test.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { OptionsStep } from './OptionsStep';
import { DEFAULT_OPTIONS } from '../types';

describe('OptionsStep', () => {
  it('renders the radio groups with defaults selected', () => {
    render(
      <OptionsStep
        value={DEFAULT_OPTIONS}
        onChange={vi.fn()}
        onBack={vi.fn()}
        onNext={vi.fn()}
      />,
    );
    const copy = screen.getByTestId('media-copy') as HTMLInputElement;
    expect(copy.checked).toBe(true);
    const convert = screen.getByTestId('shortcode-convert') as HTMLInputElement;
    expect(convert.checked).toBe(true);
  });

  it('changing media mode calls onChange', () => {
    const onChange = vi.fn();
    render(
      <OptionsStep
        value={DEFAULT_OPTIONS}
        onChange={onChange}
        onBack={vi.fn()}
        onNext={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('media-proxy'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ mediaMode: 'proxy' }),
    );
  });

  it('add then remove a role override', () => {
    const onChange = vi.fn();
    let value = DEFAULT_OPTIONS;
    const { rerender } = render(
      <OptionsStep
        value={value}
        onChange={(next) => {
          value = next;
          onChange(next);
        }}
        onBack={vi.fn()}
        onNext={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByTestId('role-key-input'), {
      target: { value: 'contributor' },
    });
    fireEvent.change(screen.getByTestId('role-val-input'), {
      target: { value: 'author' },
    });
    fireEvent.click(screen.getByTestId('role-add'));
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        roleOverrides: { contributor: 'author' },
      }),
    );

    rerender(
      <OptionsStep
        value={value}
        onChange={onChange}
        onBack={vi.fn()}
        onNext={vi.fn()}
      />,
    );

    expect(screen.getByTestId('role-override-list')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('role-remove-contributor'));
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        roleOverrides: {},
      }),
    );
  });

  it('add is a no-op when key or value is empty', () => {
    const onChange = vi.fn();
    render(
      <OptionsStep
        value={DEFAULT_OPTIONS}
        onChange={onChange}
        onBack={vi.fn()}
        onNext={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('role-add'));
    expect(onChange).not.toHaveBeenCalled();
  });
});
