/**
 * InstallForm tests.
 *
 * Issue acceptance criteria coverage:
 *  - File upload triggers manifest preview
 *  - Manifest preview shows the capability review with every cap from
 *    the manifest, in order
 *  - The Install button is disabled until the consent checkbox is ticked
 *  - On submit, the server action receives the form data with the
 *    bundle, manifest, and acknowledgement flag
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';

const installSpy = vi.fn(async (_fd: FormData) => ({ ok: true as const }));
const refreshSpy = vi.fn();

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    refresh: refreshSpy,
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

vi.mock('../actions', () => ({
  installPlugin: (fd: FormData) => installSpy(fd),
  activatePlugin: vi.fn(),
  deactivatePlugin: vi.fn(),
  uninstallPlugin: vi.fn(),
}));

import { InstallForm } from './InstallForm';

const VALID_MANIFEST = JSON.stringify({
  apiVersion: 'gonext.io/v1',
  name: 'mailchimp-sync',
  version: '2.3.1',
  description: 'Sync to Mailchimp.',
  capabilities: ['posts.read', 'email.send'],
});

describe('InstallForm', () => {
  beforeEach(() => {
    installSpy.mockClear();
    refreshSpy.mockReset();
  });

  it('shows the manifest preview after pasting JSON', async () => {
    render(<InstallForm />);
    const textarea = screen.getByLabelText(/manifest json paste-in/i);
    fireEvent.change(textarea, { target: { value: VALID_MANIFEST } });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { level: 2, name: /manifest preview/i }),
      ).toBeInTheDocument();
    });

    // The capability review list appears alongside the preview.
    const list = screen.getByTestId('capability-review-list');
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveAttribute('data-capability-id', 'posts.read');
    expect(items[1]).toHaveAttribute('data-capability-id', 'email.send');
  });

  it('triggers the manifest preview when a manifest.json file is uploaded', async () => {
    render(<InstallForm />);
    const file = new File([VALID_MANIFEST], 'manifest.json', {
      type: 'application/json',
    });
    const fileInput = screen.getByLabelText(
      /upload manifest\.json/i,
    ) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(fileInput, { target: { files: [file] } });
    });

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { level: 2, name: /manifest preview/i }),
      ).toBeInTheDocument();
    });
  });

  it('disables the Install button until the consent checkbox is ticked', async () => {
    render(<InstallForm />);
    fireEvent.change(screen.getByLabelText(/manifest json paste-in/i), {
      target: { value: VALID_MANIFEST },
    });

    const submitBtn = await waitFor(() =>
      screen.getByRole('button', { name: /install plugin/i }),
    );
    expect(submitBtn).toBeDisabled();

    // Tick the consent checkbox.
    fireEvent.click(screen.getByRole('checkbox'));
    expect(submitBtn).not.toBeDisabled();
  });

  it('submits the bundled FormData with the acknowledgement flag set', async () => {
    render(<InstallForm />);
    fireEvent.change(screen.getByLabelText(/manifest json paste-in/i), {
      target: { value: VALID_MANIFEST },
    });
    await waitFor(() =>
      screen.getByRole('heading', { level: 2, name: /manifest preview/i }),
    );
    fireEvent.click(screen.getByRole('checkbox'));

    const submitBtn = screen.getByRole('button', { name: /install plugin/i });
    await act(async () => {
      fireEvent.click(submitBtn);
    });

    expect(installSpy).toHaveBeenCalledOnce();
    const fd = installSpy.mock.calls[0]![0] as FormData;
    expect(fd.get('capabilities_acknowledged')).toBe('on');
    expect(fd.get('manifest')).toContain('mailchimp-sync');
  });

  it('shows a friendly success panel after install', async () => {
    render(<InstallForm />);
    fireEvent.change(screen.getByLabelText(/manifest json paste-in/i), {
      target: { value: VALID_MANIFEST },
    });
    await waitFor(() =>
      screen.getByRole('heading', { level: 2, name: /manifest preview/i }),
    );
    fireEvent.click(screen.getByRole('checkbox'));
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /install plugin/i }));
    });

    expect(
      await screen.findByText(/"mailchimp-sync" installed/i),
    ).toBeInTheDocument();
  });

  it('renders a parse error when the pasted JSON is malformed', () => {
    render(<InstallForm />);
    fireEvent.change(screen.getByLabelText(/manifest json paste-in/i), {
      target: { value: '{ this is not json' },
    });
    expect(screen.getByRole('alert')).toHaveTextContent(/not valid json/i);
    // No preview is shown.
    expect(
      screen.queryByRole('heading', { level: 2, name: /manifest preview/i }),
    ).not.toBeInTheDocument();
  });

  it('refuses to submit without a source picked', () => {
    render(<InstallForm />);
    const submitBtn = screen.getByRole('button', { name: /install plugin/i });
    expect(submitBtn).toBeDisabled();
  });
});
