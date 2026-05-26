'use client';

/**
 * PluginPageBridge — client bridge that lazy-loads the plugin frontend
 * host and asks it for the (plugin, slug) page module.
 *
 * The host module is expected to expose a `resolveAdminPage(plugin,
 * slug)` function returning either a React component or null. When the
 * host module isn't bundled (development without active plugins), the
 * bridge falls back to a static "no module registered" placeholder so
 * the route still renders something useful.
 *
 * Why dynamic import: keeps the admin bundle slim when no plugin is
 * active; the host module pulls in WASM glue + plugin-side React
 * runtimes that we don't want in every admin payload.
 */
import { useEffect, useState, type ReactElement, type ComponentType } from 'react';

interface Props {
  plugin: string;
  slug: string;
}

interface PluginPageHost {
  resolveAdminPage: (
    plugin: string,
    slug: string,
  ) => ComponentType<{ plugin: string; slug: string }> | null;
}

type LoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; Page: ComponentType<{ plugin: string; slug: string }> }
  | { kind: 'missing' };

export function PluginPageBridge({ plugin, slug }: Props): ReactElement {
  const [state, setState] = useState<LoadState>({ kind: 'loading' });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Dynamic import so the bundle stays slim when the host is
        // absent. The path resolves to the plugin frontend host
        // entry; production builds inject the real module via a path
        // alias.
        const host = (await import(
          /* webpackIgnore: true */ '@gonext/plugin-frontend-host' as string
        ).catch(() => null)) as PluginPageHost | null;
        if (cancelled) return;
        if (!host || typeof host.resolveAdminPage !== 'function') {
          setState({ kind: 'missing' });
          return;
        }
        const Page = host.resolveAdminPage(plugin, slug);
        if (!Page) {
          setState({ kind: 'missing' });
          return;
        }
        setState({ kind: 'ready', Page });
      } catch {
        if (!cancelled) setState({ kind: 'missing' });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [plugin, slug]);

  if (state.kind === 'loading') return <p>Loading plugin page…</p>;
  if (state.kind === 'missing') {
    return (
      <div className="plugin-page__missing">
        <h1>No module registered</h1>
        <p>
          The plugin <code>{plugin}</code> declared an admin page{' '}
          <code>{slug}</code> in its manifest, but no frontend module is
          registered for it. The plugin host bundle may not be loaded in
          this environment.
        </p>
      </div>
    );
  }
  const { Page } = state;
  return <Page plugin={plugin} slug={slug} />;
}
