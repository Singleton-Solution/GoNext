/**
 * Static, typed re-export of every hook JSON Schema shipped with the
 * package.
 *
 * The schemas themselves are the source of truth: this file is the
 * thin bridge that makes them importable as ES modules without each
 * call site re-doing the JSON import dance.
 *
 * If you're adding a new schema:
 *
 *   1. Add the .json file to packages/go/hooks/schemas/schemas/.
 *   2. Run `pnpm --filter @gonext/hooks-schemas sync-schemas` to mirror
 *      it into packages/ts/hooks-schemas/src/schemas/.
 *   3. Add the import and the entry to BUILTIN_SCHEMAS below.
 *   4. Ship.
 *
 * The map is exported as `const` so TS narrows the keys at use sites —
 * consumers can write `BUILTIN_SCHEMAS['the_content']` with full type
 * safety. Use `Object.keys(BUILTIN_SCHEMAS)` to iterate the canonical
 * set.
 */
import admin_enqueue_scripts from './admin_enqueue_scripts.json';
import body_class from './body_class.json';
import comment_post from './comment_post.json';
import comment_text from './comment_text.json';
import delete_post from './delete_post.json';
import get_avatar from './get_avatar.json';
import init from './init.json';
import login_redirect from './login_redirect.json';
import post_class from './post_class.json';
import profile_update from './profile_update.json';
import publish_post from './publish_post.json';
import save_post from './save_post.json';
import template_redirect from './template_redirect.json';
import the_content from './the_content.json';
import the_excerpt from './the_excerpt.json';
import the_permalink from './the_permalink.json';
import the_title from './the_title.json';
import user_register from './user_register.json';
import wp_enqueue_scripts from './wp_enqueue_scripts.json';
import wp_footer from './wp_footer.json';
import wp_head from './wp_head.json';
import wp_loaded from './wp_loaded.json';
import wp_title from './wp_title.json';

/**
 * Map of hook name -> JSON Schema document. Keys are stable hook names
 * matching the Go side (packages/go/hooks/schemas/schemas/*.json).
 */
export const BUILTIN_SCHEMAS = {
  admin_enqueue_scripts,
  body_class,
  comment_post,
  comment_text,
  delete_post,
  get_avatar,
  init,
  login_redirect,
  post_class,
  profile_update,
  publish_post,
  save_post,
  template_redirect,
  the_content,
  the_excerpt,
  the_permalink,
  the_title,
  user_register,
  wp_enqueue_scripts,
  wp_footer,
  wp_head,
  wp_loaded,
  wp_title,
} as const;

/** Hook name union derived from the canonical schema set. */
export type BuiltinHookName = keyof typeof BUILTIN_SCHEMAS;
