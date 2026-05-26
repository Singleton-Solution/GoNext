/**
 * SourceStep — unit tests.
 *
 * Verifies the kind selector switches the input shown, and that the
 * "Continue" button gating matches the isReady helper. The state-
 * machine integration is covered by MigrationWizard.test.tsx.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SourceStep, isReady } from './SourceStep';

describe('SourceStep', () => {
  it('renders three kind radios', () => {
    render(
      <SourceStep
        value={{ kind: 'wxr-upload', wxrFile: null }}
        onChange={vi.fn()}
        onNext={vi.fn()}
      />,
    );
    expect(screen.getByTestId('source-kind-wxr-upload')).toBeInTheDocument();
    expect(screen.getByTestId('source-kind-rest-url')).toBeInTheDocument();
    expect(screen.getByTestId('source-kind-acf-json')).toBeInTheDocument();
  });

  it('switching kind clears file input and shows URL input', () => {
    const onChange = vi.fn();
    render(
      <SourceStep
        value={{ kind: 'wxr-upload', wxrFile: null }}
        onChange={onChange}
        onNext={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByTestId('source-kind-rest-url'));
    // The onChange call uses the new kind and a cleared wxrFile.
    expect(onChange).toHaveBeenCalledWith({ kind: 'rest-url', wxrFile: null });
  });

  it('next is disabled until source is ready', () => {
    const onNext = vi.fn();
    const { rerender } = render(
      <SourceStep
        value={{ kind: 'rest-url', restUrl: '' }}
        onChange={vi.fn()}
        onNext={onNext}
      />,
    );
    expect(screen.getByTestId('source-next')).toBeDisabled();
    rerender(
      <SourceStep
        value={{ kind: 'rest-url', restUrl: 'https://blog.example.com' }}
        onChange={vi.fn()}
        onNext={onNext}
      />,
    );
    expect(screen.getByTestId('source-next')).not.toBeDisabled();
  });

  describe('isReady', () => {
    it('rejects empty wxr file', () => {
      expect(isReady({ kind: 'wxr-upload', wxrFile: null })).toBe(false);
    });
    it('accepts a wxr file', () => {
      const f = new File(['x'], 'x.xml', { type: 'application/xml' });
      expect(isReady({ kind: 'wxr-upload', wxrFile: f })).toBe(true);
    });
    it('rejects invalid URL', () => {
      expect(isReady({ kind: 'rest-url', restUrl: 'not-a-url' })).toBe(false);
      expect(isReady({ kind: 'rest-url', restUrl: 'ftp://example.com' })).toBe(false);
    });
    it('accepts http and https URLs', () => {
      expect(isReady({ kind: 'rest-url', restUrl: 'http://a.b' })).toBe(true);
      expect(isReady({ kind: 'rest-url', restUrl: 'https://a.b' })).toBe(true);
    });
    it('rejects empty acf path', () => {
      expect(isReady({ kind: 'acf-json', acfPath: '' })).toBe(false);
      expect(isReady({ kind: 'acf-json', acfPath: '   ' })).toBe(false);
    });
    it('accepts non-empty acf path', () => {
      expect(isReady({ kind: 'acf-json', acfPath: '/etc/acf.json' })).toBe(true);
    });
  });
});
