/**
 * Plugin admin page host — issue #228.
 *
 * Catch-all route at /plugins/{plugin}/{slug}. The actual page module
 * is provided by the plugin's frontend bundle; this route is a thin
 * shell that lazy-imports the plugin host's resolver and hands it the
 * (plugin, slug) tuple. If no plugin module is registered for the
 * tuple, the host renders a "not found" placeholder.
 *
 * The plugin frontend host module (@gonext/plugin-frontend-host) is
 * not yet on the workspace dependency graph; the import below is a
 * dynamic specifier so a build-time absence falls back to the inline
 * placeholder. Production deployments swap the placeholder out by
 * shipping the host module on the admin's path alias.
 */
import type { ReactElement } from 'react';
import { PluginPageBridge } from './PluginPageBridge';

interface Props {
  params: Promise<{ plugin: string; slug: string }>;
}

export const dynamic = 'force-dynamic';

export default async function PluginAdminPage({ params }: Props): Promise<ReactElement> {
  const { plugin, slug } = await params;
  return <PluginPageBridge plugin={plugin} slug={slug} />;
}
