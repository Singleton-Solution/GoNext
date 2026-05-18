/**
 * Tests for the TS-side hook schema validator.
 *
 * Cover the same scenarios as the Go-side schemas package so a
 * regression on either side surfaces in a parallel test:
 *
 *   - Register + validate happy path.
 *   - Malformed payload rejection.
 *   - Loose mode passes unregistered hooks.
 *   - Strict mode rejects unregistered hooks.
 *   - Wrong-dialect schemas are rejected at register time.
 *   - The built-in registry covers every WP-compat hook listed in the
 *     Go-side wpcompat aliases table.
 *
 * We avoid importing the Go side at runtime (cross-language); instead
 * the parity check uses the canonical schema directory listing.
 */
import { describe, it, expect } from 'vitest';
import {
  SchemaRegistry,
  createBuiltinRegistry,
  builtinHookNames,
  HooksValidationError,
  HooksUnregisteredError,
  HooksUnsupportedDialectError,
  SCHEMA_DIALECT,
  BUILTIN_SCHEMAS,
} from './index.ts';

describe('SchemaRegistry: register + validate', () => {
  it('accepts a well-formed payload after register', () => {
    const reg = new SchemaRegistry();
    reg.register('plg.demo.payload', {
      $schema: SCHEMA_DIALECT,
      type: 'object',
      required: ['id'],
      properties: { id: { type: 'string' } },
    });
    expect(() => reg.validate('plg.demo.payload', { id: 'x' })).not.toThrow();
  });

  it('rejects a malformed payload', () => {
    const reg = new SchemaRegistry();
    reg.register('plg.demo.payload', {
      $schema: SCHEMA_DIALECT,
      type: 'object',
      required: ['id'],
      properties: { id: { type: 'string' } },
    });
    expect(() =>
      reg.validate('plg.demo.payload', { name: 'x' }),
    ).toThrow(HooksValidationError);
    expect(() =>
      reg.validate('plg.demo.payload', { id: 42 }),
    ).toThrow(HooksValidationError);
  });

  it('loose mode passes through unregistered hooks', () => {
    const reg = new SchemaRegistry();
    expect(() => reg.validate('unknown.hook', 'anything')).not.toThrow();
  });

  it('strict mode rejects unregistered hooks', () => {
    const reg = new SchemaRegistry();
    expect(() => reg.validate('unknown.hook', 'x', 'strict')).toThrow(
      HooksUnregisteredError,
    );
  });

  it('rejects duplicate registrations', () => {
    const reg = new SchemaRegistry();
    reg.register('dup', { type: 'string' });
    expect(() => reg.register('dup', { type: 'string' })).toThrow(
      /already registered/,
    );
  });

  it('rejects schemas declaring the wrong dialect', () => {
    const reg = new SchemaRegistry();
    expect(() =>
      reg.register('bad', {
        $schema: 'http://json-schema.org/draft-07/schema#',
        type: 'string',
      }),
    ).toThrow(HooksUnsupportedDialectError);
  });

  it('rejects empty hook names', () => {
    const reg = new SchemaRegistry();
    expect(() => reg.register('', { type: 'string' })).toThrow(/empty hook name/);
  });

  it('rejects non-object schemas', () => {
    const reg = new SchemaRegistry();
    expect(() => reg.register('x', 'not an object' as unknown)).toThrow(
      /must be an object/,
    );
    expect(() => reg.register('x', null as unknown)).toThrow(
      /must be an object/,
    );
    expect(() => reg.register('x', [] as unknown)).toThrow(
      /must be an object/,
    );
  });

  it('has/names accurately reflect the registry state', () => {
    const reg = new SchemaRegistry();
    expect(reg.has('a')).toBe(false);
    reg.register('a', { type: 'string' });
    reg.register('b', { type: 'string' });
    expect(reg.has('a')).toBe(true);
    expect(reg.names()).toEqual(['a', 'b']);
  });
});

describe('createBuiltinRegistry: WP-compat parity', () => {
  it('covers every documented WP-compat alias', () => {
    const reg = createBuiltinRegistry();
    // The canonical list of WP aliases lives in
    // packages/go/hooks/wpcompat/aliases.go. To avoid cross-language
    // imports in tests we mirror the list here; the Go-side test
    // (TestBuiltinRegistry_CoversEveryWPAlias) asserts the same
    // invariant from the Go direction. If you add a new WP alias on
    // the Go side, add it to BOTH this list AND
    // packages/go/hooks/schemas/schemas, then run sync-schemas.
    const wpAliases = [
      'the_content',
      'the_title',
      'the_excerpt',
      'the_permalink',
      'wp_title',
      'body_class',
      'post_class',
      'comment_text',
      'get_avatar',
      'login_redirect',
      'init',
      'wp_loaded',
      'wp_head',
      'wp_footer',
      'wp_enqueue_scripts',
      'admin_enqueue_scripts',
      'template_redirect',
      'save_post',
      'publish_post',
      'delete_post',
      'user_register',
      'profile_update',
      'comment_post',
    ];
    const missing = wpAliases.filter((name) => !reg.has(name));
    expect(missing).toEqual([]);
  });

  it('the_content accepts a string and rejects an object', () => {
    const reg = createBuiltinRegistry();
    expect(() => reg.validate('the_content', 'Hello.')).not.toThrow();
    expect(() => reg.validate('the_content', { v: 1 })).toThrow(
      HooksValidationError,
    );
  });

  it('save_post accepts the WPPost shape and rejects bad types', () => {
    const reg = createBuiltinRegistry();
    expect(() =>
      reg.validate('save_post', { ID: 'p1', Post: null, Update: true }),
    ).not.toThrow();
    expect(() =>
      reg.validate('save_post', { ID: 'p1', Update: 'yes' }),
    ).toThrow(HooksValidationError);
  });

  it('user_register rejects missing required ID', () => {
    const reg = createBuiltinRegistry();
    expect(() => reg.validate('user_register', { ID: 'u1' })).not.toThrow();
    expect(() => reg.validate('user_register', {})).toThrow(HooksValidationError);
  });

  it('zero-arg actions reject extra payload', () => {
    const reg = createBuiltinRegistry();
    // init schema is type=array maxItems=0; the variadic args slice
    // becomes an array on the wire.
    expect(() => reg.validate('init', [])).not.toThrow();
    expect(() => reg.validate('init', ['extra'])).toThrow(HooksValidationError);
  });

  it('exposes the built-in hook names list', () => {
    const names = builtinHookNames();
    expect(names.length).toBeGreaterThan(0);
    expect(names).toContain('the_content');
    expect(names).toContain('save_post');
  });

  it('BUILTIN_SCHEMAS exports raw documents', () => {
    expect(BUILTIN_SCHEMAS.the_content).toMatchObject({
      type: 'string',
    });
  });
});

describe('HooksValidationError', () => {
  it('carries the hook name and ajv errors', () => {
    const reg = new SchemaRegistry();
    reg.register('x', { $schema: SCHEMA_DIALECT, type: 'string' });
    try {
      reg.validate('x', 42);
      expect.fail('expected throw');
    } catch (err) {
      expect(err).toBeInstanceOf(HooksValidationError);
      const ve = err as HooksValidationError;
      expect(ve.hookName).toBe('x');
      expect(ve.errors.length).toBeGreaterThan(0);
      expect(ve.message).toContain('"x"');
    }
  });
});

describe('HooksUnregisteredError', () => {
  it('carries the hook name in strict mode rejections', () => {
    const reg = new SchemaRegistry();
    try {
      reg.validate('plg.unknown', null, 'strict');
      expect.fail('expected throw');
    } catch (err) {
      expect(err).toBeInstanceOf(HooksUnregisteredError);
      expect((err as HooksUnregisteredError).hookName).toBe('plg.unknown');
    }
  });
});
