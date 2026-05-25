/**
 * Dispatcher integration test. Exercises registerAction /
 * registerFilter plus the dispatch() helper without touching the
 * Javy bridge — the dispatcher is JSON-in, JSON-out, so we can drive
 * it from a vanilla vitest case.
 */

import { afterEach, describe, expect, it } from 'vitest';

import {
  ResultStatus,
  _resetForTests,
  dispatch,
  marshalActionPayload,
  marshalFilterPayload,
  pluginInit,
  registerAction,
  registerFilter,
} from '../src/index.ts';

describe('dispatch', () => {
  afterEach(() => _resetForTests());

  it('routes to a registered action handler', async () => {
    const seen: unknown[][] = [];
    registerAction('save_post', (args) => {
      seen.push(args);
    });

    const result = await dispatch(
      'save_post',
      marshalActionPayload([{ post_id: 1 }]),
    );

    expect(result.status).toBe(ResultStatus.OK);
    expect(result.body).toBeUndefined();
    expect(seen).toEqual([[{ post_id: 1 }]]);
  });

  it('routes to a registered filter handler and returns the value', async () => {
    registerFilter('the_content', (value) => `<wrap>${value}</wrap>`);

    const result = await dispatch(
      'the_content',
      marshalFilterPayload('hello'),
    );

    expect(result.status).toBe(ResultStatus.OK);
    expect(result.body).toBe('{"value":"<wrap>hello</wrap>"}');
  });

  it('returns UnknownHook for an unregistered hook', async () => {
    const result = await dispatch(
      'never_registered',
      marshalActionPayload(),
    );
    expect(result.status).toBe(ResultStatus.UnknownHook);
  });

  it('returns BadPayload for malformed JSON', async () => {
    registerAction('save_post', () => undefined);
    const result = await dispatch('save_post', 'not json');
    expect(result.status).toBe(ResultStatus.BadPayload);
  });

  it('returns Error when a handler throws', async () => {
    registerAction('save_post', () => {
      throw new Error('boom');
    });
    const result = await dispatch('save_post', marshalActionPayload());
    expect(result.status).toBe(ResultStatus.Error);
  });

  it('awaits an async handler before responding', async () => {
    let resolved = false;
    registerFilter('async_filter', async (value) => {
      await new Promise((r) => setTimeout(r, 1));
      resolved = true;
      return `${value}!`;
    });
    const result = await dispatch(
      'async_filter',
      marshalFilterPayload('hi'),
    );
    expect(resolved).toBe(true);
    expect(result.body).toBe('{"value":"hi!"}');
  });

  it('replaces a handler on re-registration', async () => {
    registerFilter('the_content', () => 'first');
    registerFilter('the_content', () => 'second');
    const result = await dispatch('the_content', marshalFilterPayload(null));
    expect(result.body).toBe('{"value":"second"}');
  });
});

describe('pluginInit', () => {
  afterEach(() => {
    _resetForTests();
    delete (globalThis as Record<string, unknown>)['gn_handle_hook'];
  });

  it('installs the dispatcher onto globalThis', () => {
    pluginInit();
    expect(typeof (globalThis as Record<string, unknown>)['gn_handle_hook']).toBe(
      'function',
    );
  });

  it('lets handlers register either before or after pluginInit', async () => {
    pluginInit();
    registerAction('post-init', () => undefined);
    const result = await dispatch('post-init', marshalActionPayload());
    expect(result.status).toBe(ResultStatus.OK);
  });
});
