/**
 * defineBlock tests.
 *
 * Two modes: forwarding to a registry the editor already
 * published, and capturing into the local map when the registry
 * has not booted yet.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __drainCapturedBlocks,
  __getCapturedBlocks,
  defineBlock,
} from './blocks';

beforeEach(() => {
  delete (globalThis as { __GN_BLOCK_REGISTRY__?: unknown }).__GN_BLOCK_REGISTRY__;
  delete (globalThis as { BLOCK_REGISTRY?: unknown }).BLOCK_REGISTRY;
  __drainCapturedBlocks();
});

afterEach(() => {
  __drainCapturedBlocks();
});

describe('defineBlock', () => {
  it('forwards to __GN_BLOCK_REGISTRY__ when present', () => {
    const register = vi.fn();
    (globalThis as { __GN_BLOCK_REGISTRY__?: unknown }).__GN_BLOCK_REGISTRY__ = {
      register,
    };
    defineBlock({
      name: 'acme/hello',
      title: 'Hello',
      attributes: { greeting: 'Hi' },
    });
    expect(register).toHaveBeenCalledTimes(1);
    expect(register.mock.calls[0]![0]).toMatchObject({ name: 'acme/hello' });
    expect(__getCapturedBlocks()).toHaveLength(0);
  });

  it('falls back to BLOCK_REGISTRY when only that is present', () => {
    const register = vi.fn();
    (globalThis as { BLOCK_REGISTRY?: unknown }).BLOCK_REGISTRY = { register };
    defineBlock({ name: 'acme/old', attributes: {} });
    expect(register).toHaveBeenCalledTimes(1);
  });

  it('captures locally when no registry is installed yet', () => {
    defineBlock({ name: 'acme/captured', attributes: {} });
    expect(__getCapturedBlocks()).toHaveLength(1);
    expect(__getCapturedBlocks()[0]!.name).toBe('acme/captured');
  });

  it('drain returns and clears the capture map', () => {
    defineBlock({ name: 'acme/a', attributes: {} });
    defineBlock({ name: 'acme/b', attributes: {} });
    const drained = __drainCapturedBlocks();
    expect(drained.map((s) => s.name)).toEqual(['acme/a', 'acme/b']);
    expect(__getCapturedBlocks()).toHaveLength(0);
  });

  it('replaces a captured block on re-registration', () => {
    defineBlock({ name: 'acme/dupe', title: 'First', attributes: {} });
    defineBlock({ name: 'acme/dupe', title: 'Second', attributes: {} });
    const captured = __getCapturedBlocks();
    expect(captured).toHaveLength(1);
    expect(captured[0]!.title).toBe('Second');
  });

  it('rejects an invalid name', () => {
    expect(() =>
      defineBlock({ name: 'BadName', attributes: {} } as never),
    ).toThrow(TypeError);
    expect(() =>
      defineBlock({ name: 'no-slash', attributes: {} } as never),
    ).toThrow(TypeError);
    expect(() =>
      defineBlock({ name: 'acme/Bad Slug', attributes: {} } as never),
    ).toThrow(TypeError);
  });

  it('ignores a registry that lacks register()', () => {
    (globalThis as { __GN_BLOCK_REGISTRY__?: unknown }).__GN_BLOCK_REGISTRY__ = {};
    defineBlock({ name: 'acme/ok', attributes: {} });
    expect(__getCapturedBlocks()).toHaveLength(1);
  });
});
