/**
 * General settings page test.
 *
 * Covers the bulk of the schema-driven form behavior because General is the
 * richest group (5 fields across all four control types). The other groups
 * reuse the same `SettingsForm` so we trust this surface as the canonical
 * coverage point.
 *
 * Tests:
 *  1. Renders 5 fields with values pre-filled from a fixture.
 *  2. Happy path: edit + submit → `patchSettings` is called → success toast.
 *  3. Validation: invalid URL surfaces an inline field error and blocks PATCH.
 *
 * `patchSettings` is mocked at the module level so we don't touch `fetch`.
 * The setup file installs a loud fetch stub, so any leak would throw.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor, act } from '@testing-library/react';

// Module-level mock so both the form and the test share the same spy.
const patchMock = vi.fn();
vi.mock('../api', () => ({
  patchSettings: (patch: unknown) => patchMock(patch),
  fetchSettings: vi.fn(),
}));

import { GeneralForm } from './GeneralForm';
import { GENERAL_SCHEMA } from './schema';

const FIXTURE = {
  'core.site.name': 'GoNext Demo',
  'core.site.tagline': 'A tagline',
  'core.site.url': 'https://example.com',
  'core.timezone': 'UTC',
  'core.locale': 'en-US',
};

describe('GeneralForm', () => {
  beforeEach(() => {
    patchMock.mockReset();
  });

  it('renders all 5 fields from the schema with the fixture values', () => {
    render(<GeneralForm initialValues={FIXTURE} />);

    // One input/select per schema entry — assert we have exactly 5.
    expect(GENERAL_SCHEMA).toHaveLength(5);

    expect(screen.getByLabelText(/Site name/i)).toHaveValue('GoNext Demo');
    expect(screen.getByLabelText(/Tagline/i)).toHaveValue('A tagline');
    expect(screen.getByLabelText(/Site URL/i)).toHaveValue('https://example.com');
    expect(screen.getByLabelText(/Timezone/i)).toHaveValue('UTC');
    expect(screen.getByLabelText(/Site language/i)).toHaveValue('en-US');
  });

  it('calls patchSettings and shows a success toast on submit', async () => {
    patchMock.mockResolvedValueOnce({});
    render(<GeneralForm initialValues={FIXTURE} />);

    fireEvent.change(screen.getByLabelText(/Site name/i), {
      target: { value: 'Renamed Site' },
    });
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => {
      expect(patchMock).toHaveBeenCalledTimes(1);
    });
    expect(patchMock.mock.calls[0]?.[0]).toMatchObject({
      'core.site.name': 'Renamed Site',
      'core.site.url': 'https://example.com',
    });

    expect(await screen.findByText(/settings saved/i)).toBeInTheDocument();
  });

  it('blocks submit when the Site URL is invalid', async () => {
    render(<GeneralForm initialValues={FIXTURE} />);

    fireEvent.change(screen.getByLabelText(/Site URL/i), {
      target: { value: 'not-a-url' },
    });
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    // The field-level error appears and the patch was never fired.
    expect(
      await screen.findByText(/Site URL must be a valid URL/i),
    ).toBeInTheDocument();
    expect(patchMock).not.toHaveBeenCalled();

    // The input is flagged invalid for assistive tech.
    expect(screen.getByLabelText(/Site URL/i)).toHaveAttribute(
      'aria-invalid',
      'true',
    );
  });

  it('auto-dismisses the success toast after 4s', async () => {
    vi.useFakeTimers();
    try {
      patchMock.mockResolvedValueOnce({});
      render(<GeneralForm initialValues={FIXTURE} />);

      fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

      // Flush the pending PATCH promise so the toast appears.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByText(/settings saved/i)).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(4_000);
      });
      expect(screen.queryByText(/settings saved/i)).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
