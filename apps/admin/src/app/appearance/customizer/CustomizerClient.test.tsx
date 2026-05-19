/**
 * Tests for the CustomizerClient island.
 *
 * Coverage:
 *  - ColorPicker writes onto the correct JSON-pointer path.
 *  - The preview iframe URL is rebuilt when overrides change.
 *  - Save calls saveAction with the right diff; success refreshes
 *    the local state.
 *  - Reset calls resetAction; success clears overrides and re-renders.
 *  - The Save button is disabled when no fields have changed.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { CustomizerClient } from './CustomizerClient';
import type { ActiveResponse } from './types';

function makeActive(): ActiveResponse {
  return {
    themeSlug: 'gn-hello',
    theme: {
      version: 1,
      title: 'gn-hello',
      settings: {
        color: {
          palette: [
            { slug: 'ink', name: 'Ink', color: '#0f172a' },
            { slug: 'accent', name: 'Accent', color: '#2563eb' },
          ],
        },
        typography: {
          fontFamilies: [
            { slug: 'sans', name: 'Sans', fontFamily: 'system-ui' },
          ],
          fontSizes: [{ slug: 'md', name: 'Medium', size: '1rem' }],
        },
        layout: { contentSize: '720px', wideSize: '1180px' },
      },
    },
    overrides: {},
  };
}

describe('CustomizerClient', () => {
  it('renders palette swatches keyed by slug', () => {
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={vi.fn()}
        resetAction={vi.fn()}
      />,
    );
    expect(screen.getByTestId('color-swatch-ink')).toBeInTheDocument();
    expect(screen.getByTestId('color-swatch-accent')).toBeInTheDocument();
  });

  it('disables the Save button until an edit lands', () => {
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={vi.fn()}
        resetAction={vi.fn()}
      />,
    );
    const save = screen.getByTestId('customizer-save') as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });

  it('enables Save once the palette changes and submits the diff', async () => {
    const saveAction = vi.fn().mockResolvedValue({
      themeSlug: 'gn-hello',
      theme: makeActive().theme,
      overrides: {
        settings: {
          color: {
            palette: [
              { slug: 'ink', name: 'Ink', color: '#0f172a' },
              { slug: 'accent', name: 'Accent', color: '#ff0066' },
            ],
          },
        },
      },
    });
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={saveAction}
        resetAction={vi.fn()}
      />,
    );

    const accentSwatch = screen.getByTestId('color-swatch-accent') as HTMLInputElement;
    fireEvent.change(accentSwatch, { target: { value: '#ff0066' } });

    const save = screen.getByTestId('customizer-save') as HTMLButtonElement;
    expect(save.disabled).toBe(false);

    await act(async () => {
      fireEvent.click(save);
    });

    expect(saveAction).toHaveBeenCalledTimes(1);
    const payload = saveAction.mock.calls[0][0];
    expect(payload.settings.color.palette[1].color).toBe('#ff0066');
    expect(screen.getByTestId('customizer-toast').textContent).toMatch(/saved/i);
  });

  it('passes the JSON-pointer path through to ColorPicker rows', () => {
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={vi.fn()}
        resetAction={vi.fn()}
      />,
    );
    const accentRow = screen
      .getByTestId('color-swatch-accent')
      .closest('[data-path]');
    expect(accentRow).toHaveAttribute('data-path', '/settings/color/palette/1/color');
  });

  it('updates the preview iframe URL when an override is added', () => {
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={vi.fn()}
        resetAction={vi.fn()}
      />,
    );
    const iframe = screen.getByTestId('customizer-preview-frame') as HTMLIFrameElement;
    const initialSrc = iframe.src;
    expect(initialSrc).toContain('customizer=preview');

    const contentSize = screen.getByTestId('layout-content-size') as HTMLInputElement;
    fireEvent.change(contentSize, { target: { value: '800px' } });

    expect(iframe.src).not.toBe(initialSrc);
    expect(iframe.src).toContain('overrides=');
  });

  it('invokes resetAction and clears overrides on Reset', async () => {
    const resetAction = vi.fn().mockResolvedValue(undefined);
    const active = makeActive();
    active.overrides = {
      settings: { layout: { contentSize: '999px' } },
    };
    render(
      <CustomizerClient
        active={active}
        publicSiteUrl="http://localhost:3000"
        saveAction={vi.fn()}
        resetAction={resetAction}
      />,
    );

    const contentSize = screen.getByTestId('layout-content-size') as HTMLInputElement;
    expect(contentSize.value).toBe('999px');

    await act(async () => {
      fireEvent.click(screen.getByTestId('customizer-reset'));
    });
    expect(resetAction).toHaveBeenCalledTimes(1);

    // After reset the form reflects the theme defaults.
    expect((screen.getByTestId('layout-content-size') as HTMLInputElement).value).toBe('720px');
  });

  it('renders the validation error detail when Save fails', async () => {
    const { ApiError } = await import('../../api-client');
    const saveAction = vi.fn().mockRejectedValue(
      new ApiError(400, 'Bad Request', { detail: 'invalid color' }),
    );
    render(
      <CustomizerClient
        active={makeActive()}
        publicSiteUrl="http://localhost:3000"
        saveAction={saveAction}
        resetAction={vi.fn()}
      />,
    );
    const contentSize = screen.getByTestId('layout-content-size') as HTMLInputElement;
    fireEvent.change(contentSize, { target: { value: '800px' } });
    await act(async () => {
      fireEvent.click(screen.getByTestId('customizer-save'));
    });
    expect(screen.getByTestId('customizer-toast').textContent).toContain('invalid color');
  });
});
