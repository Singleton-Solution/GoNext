/**
 * Tests for the block context provide / consume channel.
 *
 * The contract this file pins down:
 *
 *  - `useBlockContext(key)` returns `undefined` when nothing's provided.
 *  - `BlockContextProvider` merges values on top of the inherited map;
 *    a nested provider's keys win over the outer one.
 *  - `resolveProvidedContext` reads a block's `providesContext` keys
 *    off its attributes, dropping keys the attributes don't carry.
 *  - `filterConsumedContext` narrows an inherited map down to a
 *    block's `usesContext` declaration.
 *  - The empty-context fast path returns the same frozen reference so
 *    React.memo descendants don't churn.
 *  - The canvas threads providesContext through to descendants and
 *    filters the inherited map through each block's `usesContext`
 *    declaration before passing it to the Edit component.
 */
import { describe, expect, it } from 'vitest';
import { act, render, renderHook, screen } from '@testing-library/react';
import {
  BlockRegistry,
  type BlockEditProps,
  type BlockTypeDefinition,
} from '@gonext/blocks-sdk';
import {
  BlockContextProvider,
  EMPTY_BLOCK_CONTEXT,
  filterConsumedContext,
  resolveProvidedContext,
  useBlockContext,
  useBlockContextMap,
} from './block-context.tsx';
import {
  BlockEditCanvas,
  clearEditModuleCache,
} from './block-edit-canvas.tsx';

describe('useBlockContext', () => {
  it('returns undefined when no provider is in the tree', () => {
    const { result } = renderHook(() => useBlockContext<string>('postId'));
    expect(result.current).toBeUndefined();
  });

  it('returns the provided value', () => {
    const { result } = renderHook(() => useBlockContext<string>('postId'), {
      wrapper: ({ children }) => (
        <BlockContextProvider values={{ postId: 'p-1' }}>
          {children}
        </BlockContextProvider>
      ),
    });
    expect(result.current).toBe('p-1');
  });

  it('preserves outer keys not overridden by an inner provider', () => {
    const { result } = renderHook(() => useBlockContextMap(), {
      wrapper: ({ children }) => (
        <BlockContextProvider values={{ postId: 'p-1', postType: 'post' }}>
          <BlockContextProvider values={{ postId: 'p-2' }}>
            {children}
          </BlockContextProvider>
        </BlockContextProvider>
      ),
    });
    // Inner override wins for postId, outer postType still visible.
    expect(result.current.postId).toBe('p-2');
    expect(result.current.postType).toBe('post');
  });
});

describe('BlockContextProvider', () => {
  it('passes through the inherited identity when values is empty', () => {
    // The provider should not allocate a fresh map for an empty
    // `values` — otherwise React.memo descendants would re-render on
    // every parent re-render.
    const { result, rerender } = renderHook(() => useBlockContextMap(), {
      wrapper: ({ children }) => (
        <BlockContextProvider values={{}}>{children}</BlockContextProvider>
      ),
    });
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
  });
});

describe('resolveProvidedContext', () => {
  it('reads each providesContext key from the block attributes', () => {
    const ctx = resolveProvidedContext(
      { type: 'core/query', attributes: { postId: 'p-9', perPage: 10 } },
      { providesContext: ['postId'] },
    );
    expect(ctx).toEqual({ postId: 'p-9' });
    expect(Object.isFrozen(ctx)).toBe(true);
  });

  it('drops keys that the block does not carry', () => {
    const ctx = resolveProvidedContext(
      { type: 'core/query', attributes: { postId: 'p-9' } },
      { providesContext: ['postId', 'postType'] },
    );
    expect(ctx).toEqual({ postId: 'p-9' });
    expect(Object.prototype.hasOwnProperty.call(ctx, 'postType')).toBe(false);
  });

  it('returns the empty singleton when nothing is declared', () => {
    const ctx = resolveProvidedContext(
      { type: 'core/paragraph', attributes: { content: 'hi' } },
      undefined,
    );
    expect(ctx).toBe(EMPTY_BLOCK_CONTEXT);
  });
});

describe('filterConsumedContext', () => {
  it('keeps only the keys the consumer declared', () => {
    const inherited = Object.freeze({
      postId: 'p-1',
      postType: 'post',
      noise: 42,
    });
    const ctx = filterConsumedContext(inherited, {
      usesContext: ['postId', 'postType'],
    });
    expect(ctx).toEqual({ postId: 'p-1', postType: 'post' });
    expect(Object.prototype.hasOwnProperty.call(ctx, 'noise')).toBe(false);
  });

  it('returns the empty singleton when nothing is declared', () => {
    const inherited = Object.freeze({ postId: 'p-1' });
    expect(filterConsumedContext(inherited, undefined)).toBe(
      EMPTY_BLOCK_CONTEXT,
    );
  });

  it('returns the empty singleton when the consumer list is empty', () => {
    const inherited = Object.freeze({ postId: 'p-1' });
    expect(filterConsumedContext(inherited, { usesContext: [] })).toBe(
      EMPTY_BLOCK_CONTEXT,
    );
  });

  it('omits keys the ancestor never provided', () => {
    const inherited = Object.freeze({ postId: 'p-1' });
    const ctx = filterConsumedContext(inherited, {
      usesContext: ['postId', 'queryId'],
    });
    expect(ctx).toEqual({ postId: 'p-1' });
    expect(Object.prototype.hasOwnProperty.call(ctx, 'queryId')).toBe(false);
  });
});

describe('BlockEditCanvas + context', () => {
  /**
   * Build a registry with a provider container (`test/query`) and a
   * consumer leaf (`test/post-title`). The leaf renders the `postId`
   * out of its `context` prop; the canvas should plumb it through.
   */
  function buildRegistry(): BlockRegistry {
    const r = new BlockRegistry();

    const PostTitle = ({ context }: BlockEditProps) => (
      <p data-testid="post-title">{String(context.postId ?? 'missing')}</p>
    );

    const postTitle: BlockTypeDefinition = {
      name: 'test/post-title',
      title: 'Post Title',
      category: 'text',
      attributes: { type: 'object', additionalProperties: true },
      usesContext: ['postId'],
      edit: async () => ({ default: PostTitle as unknown as never }),
    };

    const Container = () => <section data-testid="container" />;
    const queryBlock: BlockTypeDefinition = {
      name: 'test/query',
      title: 'Query',
      category: 'widgets',
      attributes: { type: 'object', additionalProperties: true },
      providesContext: ['postId'],
      edit: async () => ({ default: Container as unknown as never }),
    };

    // A consumer that opted out of context entirely.
    const Bystander = ({ context }: BlockEditProps) => (
      <p data-testid="bystander">{Object.keys(context).join(',') || 'none'}</p>
    );
    const bystander: BlockTypeDefinition = {
      name: 'test/bystander',
      title: 'Bystander',
      category: 'text',
      attributes: { type: 'object', additionalProperties: true },
      edit: async () => ({ default: Bystander as unknown as never }),
    };

    r.register(postTitle);
    r.register(queryBlock);
    r.register(bystander);
    return r;
  }

  it("threads a parent block's providesContext to a child's context prop", async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          blocks={[
            {
              type: 'test/query',
              attributes: { postId: 'p-42' },
              innerBlocks: [
                {
                  type: 'test/post-title',
                  attributes: {},
                },
              ],
            },
          ]}
        />,
      );
    });

    expect(await screen.findByTestId('post-title')).toHaveTextContent('p-42');
  });

  it('seeds the root context from the canvas `context` prop', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          context={{ postId: 'root-1' }}
          blocks={[
            {
              type: 'test/post-title',
              attributes: {},
            },
          ]}
        />,
      );
    });

    expect(await screen.findByTestId('post-title')).toHaveTextContent(
      'root-1',
    );
  });

  it('hands an empty context to blocks that did not declare usesContext', async () => {
    const registry = buildRegistry();
    clearEditModuleCache(registry);

    await act(async () => {
      render(
        <BlockEditCanvas
          registry={registry}
          context={{ postId: 'root-1' }}
          blocks={[
            {
              type: 'test/bystander',
              attributes: {},
            },
          ]}
        />,
      );
    });

    // The bystander never opted in, so context is `{}` — not the
    // ancestor's full map.
    expect(await screen.findByTestId('bystander')).toHaveTextContent('none');
  });
});
