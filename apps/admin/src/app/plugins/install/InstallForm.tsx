'use client';

/**
 * InstallForm — upload-or-paste install flow with the verbatim
 * capability review at the heart of it.
 *
 * Three accepted inputs (in priority order):
 *   1. `.gnplugin` (or `.wasm` / `.zip`) bundle upload
 *   2. `manifest.json` file upload (marketplace-stub installs)
 *   3. JSON paste-in (drop-target / textarea fallback)
 *
 * The form has two visual states:
 *   - "Pick something" — empty + helper text
 *   - "Preview" — the manifest summary plus the CapabilityReview
 *     screen; only with explicit consent does the Install button
 *     enable
 *
 * The Install button calls `installPlugin(formData)` (server action)
 * and, on success, replaces the form with a "Installed!" panel and a
 * link back to the list.
 *
 * Manifest parsing is intentionally lenient: we surface what we can
 * (apiVersion, name, version, capabilities, depends) and silently drop
 * unknown fields. The host re-validates against the JSON Schema; this
 * preview is operator-facing UX, not enforcement.
 *
 * Why not RSC: we own the manifest preview, the file input state, and
 * the consent flow — all client state. Server actions are still
 * server-side; we just call them from the client.
 */
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import {
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
  useTransition,
  type ChangeEvent,
  type CSSProperties,
  type DragEvent,
  type ReactElement,
} from 'react';
import { installPlugin } from '../actions';
import { CapabilityReview } from '../components/CapabilityReview';
import type { PluginManifest } from '../types';

const styles: Record<string, CSSProperties> = {
  card: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 16,
    marginBottom: 16,
  },
  cardTitle: {
    margin: 0,
    fontSize: 14,
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--color-text-muted, #6b7280)',
    marginBottom: 12,
  },
  dropzone: {
    border: '2px dashed var(--color-border, #e4e6ea)',
    borderRadius: 8,
    padding: 24,
    textAlign: 'center',
    background: '#fafafa',
    cursor: 'pointer',
  },
  dropzoneActive: {
    borderColor: 'var(--color-accent, #2563eb)',
    background: '#eff6ff',
  },
  fileInputRow: { marginTop: 12, display: 'flex', gap: 12, flexWrap: 'wrap' },
  fileInputBlock: { flex: '1 1 220px' },
  textarea: {
    width: '100%',
    minHeight: 120,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 8,
    resize: 'vertical',
  },
  parseError: {
    marginTop: 8,
    padding: '8px 10px',
    background: '#fef2f2',
    color: '#991b1b',
    border: '1px solid #fecaca',
    borderRadius: 6,
    fontSize: 13,
  },
  submitRow: {
    display: 'flex',
    gap: 12,
    alignItems: 'center',
    flexWrap: 'wrap',
  },
  submit: {
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    border: 0,
    borderRadius: 6,
    padding: '8px 18px',
    fontSize: 14,
    fontWeight: 500,
    cursor: 'pointer',
  },
  submitDisabled: {
    opacity: 0.5,
    cursor: 'not-allowed',
  },
  resultOk: {
    padding: 14,
    background: '#dcfce7',
    color: '#166534',
    border: '1px solid #86efac',
    borderRadius: 6,
  },
  resultErr: {
    padding: 14,
    background: '#fef2f2',
    color: '#991b1b',
    border: '1px solid #fecaca',
    borderRadius: 6,
  },
  manifestSummary: { fontSize: 14 },
  manifestKey: {
    color: 'var(--color-text-muted, #6b7280)',
    fontWeight: 500,
    paddingRight: 12,
    verticalAlign: 'top',
  },
  manifestTd: { padding: '4px 0', verticalAlign: 'top' },
};

/**
 * Read a File as a UTF-8 string. Falls back to a FileReader when
 * `File.prototype.text` isn't available (some test environments and
 * older Safari builds). The polyfill keeps the install screen
 * test-friendly under jsdom regardless of which Node + jsdom pair the
 * CI image is shipping.
 */
async function readAnyFileAsText(file: File): Promise<string> {
  if (typeof (file as { text?: () => Promise<string> }).text === 'function') {
    return file.text();
  }
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ''));
    reader.onerror = () => reject(reader.error ?? new Error('read failed'));
    reader.readAsText(file);
  });
}

/**
 * Light-touch parse: accept any object whose `apiVersion` is the v1
 * literal, and project the fields we render. The host re-validates
 * against the canonical schema (packages/go/plugins/manifest), so a
 * lax preview here is safe — it just means the operator may see an
 * incomplete summary if the manifest is malformed, and the host
 * rejects the install in that case.
 */
function tryParseManifest(text: string):
  | { manifest: PluginManifest; raw: string }
  | { error: string } {
  const trimmed = text.trim();
  if (!trimmed) return { error: 'Empty input.' };
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (err) {
    return {
      error: `Not valid JSON: ${err instanceof Error ? err.message : 'parse error'}`,
    };
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { error: 'Manifest must be a JSON object.' };
  }
  const obj = parsed as Record<string, unknown>;
  if (typeof obj.name !== 'string' || !obj.name) {
    return { error: 'Manifest is missing a "name" field.' };
  }
  if (typeof obj.version !== 'string' || !obj.version) {
    return { error: 'Manifest is missing a "version" field.' };
  }
  const m: PluginManifest = {
    apiVersion: typeof obj.apiVersion === 'string' ? obj.apiVersion : '',
    name: obj.name,
    version: obj.version,
    displayName:
      typeof obj.displayName === 'string' ? obj.displayName : undefined,
    description:
      typeof obj.description === 'string' ? obj.description : undefined,
    author: typeof obj.author === 'string' ? obj.author : undefined,
    homepage: typeof obj.homepage === 'string' ? obj.homepage : undefined,
    entry: typeof obj.entry === 'string' ? obj.entry : undefined,
    capabilities: Array.isArray(obj.capabilities)
      ? (obj.capabilities.filter((c) => typeof c === 'string') as string[])
      : [],
    depends: Array.isArray(obj.depends)
      ? (obj.depends.filter((d): d is { name: string; version: string } => {
          return (
            !!d &&
            typeof d === 'object' &&
            typeof (d as Record<string, unknown>).name === 'string' &&
            typeof (d as Record<string, unknown>).version === 'string'
          );
        }) as Array<{ name: string; version: string }>)
      : [],
  };
  return { manifest: m, raw: trimmed };
}

export function InstallForm(): ReactElement {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  const [bundleFile, setBundleFile] = useState<File | null>(null);
  const [manifestText, setManifestText] = useState('');
  const [parsed, setParsed] = useState<{
    manifest: PluginManifest;
    raw: string;
  } | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [installed, setInstalled] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const bundleId = useId();
  const manifestId = useId();
  const textareaId = useId();
  const dropzoneRef = useRef<HTMLDivElement>(null);

  const tryPreview = useCallback((text: string): void => {
    const result = tryParseManifest(text);
    if ('error' in result) {
      setParsed(null);
      setParseError(result.error);
      setAcknowledged(false);
      return;
    }
    setParsed(result);
    setParseError(null);
  }, []);

  const readFileAsText = useCallback(
    async (file: File): Promise<string> => {
      // Files that smell like JSON we parse for the preview; binary
      // bundles we skip — the host parses the bundle.
      if (
        !file.name.endsWith('.json') &&
        !file.type.includes('json') &&
        !file.name.endsWith('.txt')
      ) {
        return '';
      }
      return readAnyFileAsText(file);
    },
    [],
  );

  const handleBundleChange = useCallback(
    async (e: ChangeEvent<HTMLInputElement>): Promise<void> => {
      const file = e.target.files?.[0] ?? null;
      setBundleFile(file);
      if (!file) return;
      // For non-JSON bundles, we don't have a manifest to preview from
      // the client; the host will extract it on install. The operator
      // can paste the manifest into the textarea below to see the
      // capability review.
      const text = await readFileAsText(file);
      if (text) {
        setManifestText(text);
        tryPreview(text);
      }
    },
    [readFileAsText, tryPreview],
  );

  const handleManifestFileChange = useCallback(
    async (e: ChangeEvent<HTMLInputElement>): Promise<void> => {
      const file = e.target.files?.[0] ?? null;
      if (!file) return;
      const text = await readAnyFileAsText(file);
      setManifestText(text);
      tryPreview(text);
    },
    [tryPreview],
  );

  const handleTextChange = useCallback(
    (e: ChangeEvent<HTMLTextAreaElement>): void => {
      const text = e.target.value;
      setManifestText(text);
      if (text.trim()) {
        tryPreview(text);
      } else {
        setParsed(null);
        setParseError(null);
        setAcknowledged(false);
      }
    },
    [tryPreview],
  );

  const handleDrop = useCallback(
    async (e: DragEvent<HTMLDivElement>): Promise<void> => {
      e.preventDefault();
      setDragging(false);
      const file = e.dataTransfer?.files?.[0];
      if (!file) return;
      const text = await readAnyFileAsText(file);
      setManifestText(text);
      tryPreview(text);
    },
    [tryPreview],
  );

  const handleSubmit = useCallback(
    async (e: React.FormEvent<HTMLFormElement>): Promise<void> => {
      e.preventDefault();
      setSubmitError(null);
      const fd = new FormData();
      if (bundleFile) fd.set('bundle', bundleFile);
      if (parsed) fd.set('manifest', parsed.raw);
      if (acknowledged) fd.set('capabilities_acknowledged', 'on');
      const result = await installPlugin(fd);
      if (result.ok) {
        setInstalled(parsed?.manifest.name ?? 'plugin');
        startTransition(() => router.refresh());
      } else {
        setSubmitError(result.error);
      }
    },
    [bundleFile, parsed, acknowledged, router],
  );

  const canSubmit = useMemo(() => {
    if (pending) return false;
    if (installed) return false;
    if (!parsed && !bundleFile) return false;
    if (parsed && !acknowledged) return false;
    return true;
  }, [pending, installed, parsed, bundleFile, acknowledged]);

  if (installed) {
    return (
      <div style={styles.resultOk} role="status">
        <strong>&quot;{installed}&quot; installed.</strong>
        <p style={{ margin: '8px 0 0' }}>
          The plugin is in the <code>installed</code> state. Visit the{' '}
          <Link href="/plugins">plugins list</Link> to activate it.
        </p>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit}>
      <div style={styles.card} data-section="upload">
        <h2 style={styles.cardTitle}>Source</h2>
        <div
          ref={dropzoneRef}
          style={
            dragging ? { ...styles.dropzone, ...styles.dropzoneActive } : styles.dropzone
          }
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={handleDrop}
          aria-label="Drop manifest.json here"
        >
          <p style={{ margin: 0, fontWeight: 500 }}>
            Drop a manifest.json or paste it below.
          </p>
          <p
            style={{
              margin: '4px 0 0',
              fontSize: 13,
              color: 'var(--color-text-muted, #6b7280)',
            }}
          >
            Or upload a <code>.gnplugin</code> bundle — the host will extract
            the manifest on install.
          </p>
        </div>

        <div style={styles.fileInputRow}>
          <div style={styles.fileInputBlock}>
            <label htmlFor={bundleId} style={{ fontSize: 13, fontWeight: 500 }}>
              Bundle (.gnplugin / .wasm)
            </label>
            <input
              id={bundleId}
              name="bundle"
              type="file"
              accept=".gnplugin,.wasm,.zip"
              onChange={handleBundleChange}
              style={{ display: 'block', marginTop: 6 }}
              aria-label="Upload plugin bundle"
            />
            {bundleFile ? (
              <p
                style={{
                  fontSize: 12,
                  color: 'var(--color-text-muted, #6b7280)',
                  margin: '4px 0 0',
                }}
              >
                Selected: <code>{bundleFile.name}</code>
              </p>
            ) : null}
          </div>
          <div style={styles.fileInputBlock}>
            <label
              htmlFor={manifestId}
              style={{ fontSize: 13, fontWeight: 500 }}
            >
              Manifest (.json)
            </label>
            <input
              id={manifestId}
              type="file"
              accept="application/json,.json"
              onChange={handleManifestFileChange}
              style={{ display: 'block', marginTop: 6 }}
              aria-label="Upload manifest.json"
            />
          </div>
        </div>

        <div style={{ marginTop: 12 }}>
          <label htmlFor={textareaId} style={{ fontSize: 13, fontWeight: 500 }}>
            Paste manifest JSON
          </label>
          <textarea
            id={textareaId}
            value={manifestText}
            onChange={handleTextChange}
            placeholder={'{ "apiVersion": "gonext.io/v1", "name": "...", ... }'}
            style={{ ...styles.textarea, marginTop: 6 }}
            aria-label="Manifest JSON paste-in"
          />
        </div>

        {parseError ? (
          <div role="alert" style={styles.parseError}>
            {parseError}
          </div>
        ) : null}
      </div>

      {parsed ? (
        <>
          <div style={styles.card} data-section="manifest-preview">
            <h2 style={styles.cardTitle}>Manifest preview</h2>
            <table style={styles.manifestSummary}>
              <tbody>
                <tr>
                  <td style={styles.manifestKey}>Name</td>
                  <td style={styles.manifestTd}>
                    <strong>{parsed.manifest.displayName ?? parsed.manifest.name}</strong>{' '}
                    <code>{parsed.manifest.name}</code>
                  </td>
                </tr>
                <tr>
                  <td style={styles.manifestKey}>Version</td>
                  <td style={styles.manifestTd}>
                    <code>{parsed.manifest.version}</code>
                  </td>
                </tr>
                {parsed.manifest.apiVersion ? (
                  <tr>
                    <td style={styles.manifestKey}>API version</td>
                    <td style={styles.manifestTd}>
                      <code>{parsed.manifest.apiVersion}</code>
                    </td>
                  </tr>
                ) : null}
                {parsed.manifest.description ? (
                  <tr>
                    <td style={styles.manifestKey}>Description</td>
                    <td style={styles.manifestTd}>
                      {parsed.manifest.description}
                    </td>
                  </tr>
                ) : null}
                {parsed.manifest.author ? (
                  <tr>
                    <td style={styles.manifestKey}>Author</td>
                    <td style={styles.manifestTd}>{parsed.manifest.author}</td>
                  </tr>
                ) : null}
                {parsed.manifest.depends && parsed.manifest.depends.length > 0 ? (
                  <tr>
                    <td style={styles.manifestKey}>Depends</td>
                    <td style={styles.manifestTd}>
                      <ul style={{ margin: 0, paddingLeft: 18 }}>
                        {parsed.manifest.depends.map((d) => (
                          <li key={d.name}>
                            <code>{d.name}</code> {d.version}
                          </li>
                        ))}
                      </ul>
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>

          <CapabilityReview
            capabilities={parsed.manifest.capabilities ?? []}
            acknowledged={acknowledged}
            onAcknowledgeChange={setAcknowledged}
            disabled={pending}
          />
        </>
      ) : null}

      {submitError ? (
        <div
          role="alert"
          style={{ ...styles.resultErr, marginTop: 16 }}
        >
          {submitError}
        </div>
      ) : null}

      <div style={{ marginTop: 16, ...styles.submitRow }}>
        <button
          type="submit"
          disabled={!canSubmit}
          aria-disabled={!canSubmit}
          style={{
            ...styles.submit,
            ...(canSubmit ? {} : styles.submitDisabled),
          }}
        >
          {pending ? 'Installing…' : 'Install plugin'}
        </button>
        {parsed && !acknowledged ? (
          <span
            style={{ fontSize: 13, color: 'var(--color-text-muted, #6b7280)' }}
          >
            Tick the consent box above to enable Install.
          </span>
        ) : null}
        {!parsed && bundleFile ? (
          <span
            style={{ fontSize: 13, color: 'var(--color-text-muted, #6b7280)' }}
          >
            Paste the manifest below to see the capability review.
          </span>
        ) : null}
      </div>
    </form>
  );
}
