/**
 * gn-sdk-ts-hello — worked TypeScript plugin example.
 *
 * Demonstrates the minimum surface a real plugin uses:
 *   - one action (save_post): KV write + audit emission
 *   - one filter (the_content): pass-through value transform
 *   - explicit host imports rather than the namespaced `host` facade
 *     so the example shows both styles.
 *
 * Build:
 *   pnpm install
 *   pnpm build       # tsc + javy -> dist/plugin.wasm + dist/manifest.json
 *
 * The plugin runs sandboxed inside the wazero host. All side effects
 * go through the typed `gn_*` wrappers — nothing escapes that surface.
 */
import {
  audit,
  kv,
  log,
  nowMs,
  pluginInit,
  registerAction,
  registerFilter,
} from '@gonext/sdk-plugin';

const PLUGIN_SLUG = 'gn-sdk-ts-hello';

// Action: record the last save time and emit an audit row.
registerAction('save_post', async (args) => {
  const ts = nowMs();
  log.info(`${PLUGIN_SLUG}: save_post observed at ${ts}`);
  kv.set('last-save-ms', String(ts));
  audit.emit('plugin.save_post.observed', { args, ts });
});

// Filter: wrap the post content in a marker div. The host's
// `the_content` filter chain composes this with any other plugins
// subscribing to the same hook.
registerFilter('the_content', async (value) => {
  return `<div data-plugin="${PLUGIN_SLUG}">${value}</div>`;
});

pluginInit();
