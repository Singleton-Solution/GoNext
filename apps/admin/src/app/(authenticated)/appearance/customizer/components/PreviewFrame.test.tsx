/**
 * Tests for the PreviewFrame postMessage bridge (issue #22).
 *
 * Coverage:
 *  - Initial render still wires the encoded-URL src so a fresh load
 *    rebuilds CSS variables from the URL (renderer fallback).
 *  - On mount we publish one overrides:update message tagged with the
 *    documented channel + version envelope.
 *  - Subsequent overrides changes fire another update; the message
 *    body carries the new override map.
 *  - The targetOrigin passed to postMessage is the origin of
 *    publicSiteUrl (same-origin allowlist).
 *  - A malformed publicSiteUrl falls back gracefully — no postMessage,
 *    no thrown error.
 */
import { describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import {
  CUSTOMIZER_PREVIEW_CHANNEL,
  CUSTOMIZER_PREVIEW_VERSION,
  PreviewFrame,
} from './PreviewFrame';
import type { ThemeOverrides } from '../types';

/** Build a fresh override object — distinct identity per call so the
 *  effect dependency cleanly triggers on rerender. */
function overrides(palette?: string): ThemeOverrides {
  return {
    settings: {
      color: {
        palette: palette
          ? [{ slug: 'p', name: 'P', color: palette }]
          : undefined,
      },
    },
  } as ThemeOverrides;
}

/** Install a postMessage spy on the iframe element's contentWindow.
 *  jsdom gives every iframe a contentWindow with its own postMessage;
 *  we replace it with vi.fn() so we can inspect calls. */
function spyOnFrame(): {
  postMessage: ReturnType<typeof vi.fn>;
  restore: () => void;
} {
  // Capture every iframe created in the test and install the spy when
  // src is assigned. Doing it on first render is enough because the
  // PreviewFrame keeps a stable ref across rerenders.
  const calls: ReturnType<typeof vi.fn> = vi.fn();
  const origDescriptor = Object.getOwnPropertyDescriptor(
    HTMLIFrameElement.prototype,
    'contentWindow',
  );
  Object.defineProperty(HTMLIFrameElement.prototype, 'contentWindow', {
    configurable: true,
    get() {
      return { postMessage: calls } as unknown as Window;
    },
  });
  return {
    postMessage: calls,
    restore: () => {
      if (origDescriptor) {
        Object.defineProperty(
          HTMLIFrameElement.prototype,
          'contentWindow',
          origDescriptor,
        );
      } else {
        delete (HTMLIFrameElement.prototype as { contentWindow?: Window })
          .contentWindow;
      }
    },
  };
}

describe('PreviewFrame postMessage bridge', () => {
  it('still wires the encoded-URL fallback on the iframe src', () => {
    const spy = spyOnFrame();
    try {
      const { getByTestId } = render(
        <PreviewFrame
          publicSiteUrl="http://example.test"
          overrides={overrides()}
        />,
      );
      const frame = getByTestId('customizer-preview-frame') as HTMLIFrameElement;
      expect(frame.src).toMatch(/customizer=preview/);
      expect(frame.src).toMatch(/overrides=/);
    } finally {
      spy.restore();
    }
  });

  it('publishes a versioned update message on mount', () => {
    const spy = spyOnFrame();
    try {
      render(
        <PreviewFrame
          publicSiteUrl="http://example.test"
          overrides={overrides('#abc')}
        />,
      );
      expect(spy.postMessage).toHaveBeenCalledTimes(1);
      const [payload, targetOrigin] = spy.postMessage.mock.calls[0] as [
        Record<string, unknown>,
        string,
      ];
      expect(payload.channel).toBe(CUSTOMIZER_PREVIEW_CHANNEL);
      expect(payload.type).toBe('overrides:update');
      expect(payload.version).toBe(CUSTOMIZER_PREVIEW_VERSION);
      expect(targetOrigin).toBe('http://example.test');
    } finally {
      spy.restore();
    }
  });

  it('fires another update when overrides change', () => {
    const spy = spyOnFrame();
    try {
      const { rerender } = render(
        <PreviewFrame
          publicSiteUrl="http://example.test"
          overrides={overrides('#abc')}
        />,
      );
      const initial = spy.postMessage.mock.calls.length;
      rerender(
        <PreviewFrame
          publicSiteUrl="http://example.test"
          overrides={overrides('#def')}
        />,
      );
      expect(spy.postMessage.mock.calls.length).toBeGreaterThan(initial);
      const lastCall =
        spy.postMessage.mock.calls[spy.postMessage.mock.calls.length - 1];
      if (lastCall === undefined) throw new Error('expected last call');
      const lastPayload = lastCall[0] as { overrides: ThemeOverrides };
      expect(
        lastPayload.overrides.settings?.color?.palette?.[0]?.color,
      ).toBe('#def');
    } finally {
      spy.restore();
    }
  });

  it('skips postMessage when publicSiteUrl is unparseable', () => {
    const spy = spyOnFrame();
    try {
      render(
        <PreviewFrame
          publicSiteUrl="not a url"
          overrides={overrides('#abc')}
        />,
      );
      expect(spy.postMessage).not.toHaveBeenCalled();
    } finally {
      spy.restore();
    }
  });
});
