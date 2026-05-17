/**
 * Canonical HTML → `InlineRun[]` deserializer.
 *
 * The package's pivot model is the inline run array (see `inline.ts`).
 * `serialize` emits HTML from runs; this module is the inverse, used in
 * three places:
 *
 *  - `<RichText/>` translates the `value` prop (plain string today,
 *    `string | InlineRun[]` once the schema widens) into a Lexical initial
 *    state so the editor mounts already populated.
 *  - Tests assert round-trip stability (HTML → runs → HTML).
 *  - The future paste-from-Google-Docs sanitiser will hand untrusted HTML
 *    in here to land it as runs the editor can store.
 *
 * The parser intentionally accepts only the subset of HTML that the
 * serializer emits: `<strong>`, `<em>`, `<code>`, `<a>`, `<br>`, and raw
 * text. Anything outside that allowlist (tables, scripts, custom tags,
 * inline styles, classes, `style="…"`) is dropped — its text content
 * survives, the wrapper does not. This keeps the surface area small and
 * the round-trip property total.
 *
 * Implementation notes:
 *
 *  - We use a hand-rolled tokenising scanner instead of `DOMParser`.
 *    jsdom exposes `DOMParser` in tests, but our consumer matrix includes
 *    the Go SSR walker's TypeScript counterpart in Node where a DOM is not
 *    cheap. A scanner keeps the package portable.
 *  - Mismatched / unclosed tags are tolerated: the parser closes any tag
 *    left open at end-of-input. This mirrors what every battle-tested
 *    HTML5 parser does and makes the function total.
 *  - `&amp;` etc. are decoded on raw text only — attribute values use the
 *    same decoder via `decodeHtml`.
 */
import { decodeHtml } from './escape.ts';
import type { InlineMarks, InlineRun, InlineTextRun, InlineLinkRun } from './inline.ts';

interface OpenTag {
  /** Tag name lowercased — `'strong'` | `'em'` | `'code'` | `'a'`. */
  name: string;
  /** Attribute map for `<a>` tags; empty for marks. */
  attrs: Record<string, string>;
}

const MARK_TAGS = new Set(['strong', 'b', 'em', 'i', 'code']);

function tagNameToMark(name: string): keyof InlineMarks | null {
  if (name === 'strong' || name === 'b') {
    return 'bold';
  }
  if (name === 'em' || name === 'i') {
    return 'italic';
  }
  if (name === 'code') {
    return 'code';
  }
  return null;
}

function marksFromStack(stack: OpenTag[]): InlineMarks | undefined {
  const marks: InlineMarks = {};
  for (const tag of stack) {
    const mark = tagNameToMark(tag.name);
    if (mark) {
      marks[mark] = true;
    }
  }
  return Object.keys(marks).length > 0 ? marks : undefined;
}

/** Find the nearest open `<a>` in the stack, scanning from inside out. */
function findOpenLink(stack: OpenTag[]): OpenTag | undefined {
  for (let i = stack.length - 1; i >= 0; i -= 1) {
    const tag = stack[i];
    if (tag !== undefined && tag.name === 'a') {
      return tag;
    }
  }
  return undefined;
}

function parseAttrs(raw: string): Record<string, string> {
  const out: Record<string, string> = {};
  // Match `key="value"` or `key='value'` or bare `key`. Tolerant of weird
  // whitespace and double-encoded entities.
  const re = /([a-zA-Z_:][\w:.-]*)\s*(?:=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(raw)) !== null) {
    const [, key, dq, sq, bare] = match;
    if (key === undefined) {
      continue;
    }
    const value = dq ?? sq ?? bare ?? '';
    out[key.toLowerCase()] = decodeHtml(value);
  }
  return out;
}

/**
 * Append a text fragment to the current output context, honouring any
 * open `<a>` ancestor (links accumulate into their own children array)
 * and applying the current mark stack.
 */
function emitText(
  textContent: string,
  topLevelOut: InlineRun[],
  stack: OpenTag[],
): void {
  if (textContent === '') {
    return;
  }
  const link = findOpenLink(stack);
  const marks = marksFromStack(stack);
  const run: InlineTextRun = marks
    ? { type: 'text', text: textContent, marks }
    : { type: 'text', text: textContent };
  if (link) {
    // The link's children live on `link.attrs.__children` — we stash a
    // pointer to keep the recursion tidy below.
    (link as OpenTag & { children?: InlineRun[] }).children =
      (link as OpenTag & { children?: InlineRun[] }).children ?? [];
    (link as OpenTag & { children?: InlineRun[] }).children!.push(run);
    return;
  }
  topLevelOut.push(run);
}

/**
 * Deserialize a fragment of canonical HTML into an array of inline runs.
 * Total — any input produces a (possibly empty) array.
 */
export function deserializeInline(html: string): InlineRun[] {
  if (html === '') {
    return [];
  }

  const out: InlineRun[] = [];
  const stack: OpenTag[] = [];
  let cursor = 0;

  while (cursor < html.length) {
    const next = html.indexOf('<', cursor);
    if (next === -1) {
      const tail = html.slice(cursor);
      emitText(decodeHtml(tail), out, stack);
      break;
    }
    if (next > cursor) {
      emitText(decodeHtml(html.slice(cursor, next)), out, stack);
    }
    const close = html.indexOf('>', next);
    if (close === -1) {
      // Unterminated tag — treat the rest as text and bail.
      emitText(decodeHtml(html.slice(next)), out, stack);
      break;
    }
    const rawTag = html.slice(next + 1, close);
    cursor = close + 1;

    if (rawTag.startsWith('!')) {
      // Comment or doctype — skip silently.
      continue;
    }

    const isClose = rawTag.startsWith('/');
    const body = isClose ? rawTag.slice(1) : rawTag;
    const isSelfClosing = body.endsWith('/');
    const trimmedBody = isSelfClosing ? body.slice(0, -1) : body;

    const spaceIdx = trimmedBody.search(/\s/);
    const tagName = (spaceIdx === -1 ? trimmedBody : trimmedBody.slice(0, spaceIdx))
      .toLowerCase()
      .trim();
    const attrRaw = spaceIdx === -1 ? '' : trimmedBody.slice(spaceIdx + 1);

    if (tagName === 'br') {
      // Treat `<br>` and `<br/>` identically — inside or outside links.
      const link = findOpenLink(stack);
      if (link) {
        (link as OpenTag & { children?: InlineRun[] }).children =
          (link as OpenTag & { children?: InlineRun[] }).children ?? [];
        (link as OpenTag & { children?: InlineRun[] }).children!.push({
          type: 'linebreak',
        });
      } else {
        out.push({ type: 'linebreak' });
      }
      continue;
    }

    if (isClose) {
      // Pop matching tag from the stack. Ignore unmatched closes.
      for (let i = stack.length - 1; i >= 0; i -= 1) {
        const tag = stack[i];
        if (tag !== undefined && tag.name === tagName) {
          // Close this entry. If it's an `<a>`, finalise it into the
          // appropriate parent context.
          const closed = stack.splice(i, 1)[0];
          if (closed !== undefined && closed.name === 'a') {
            const link: InlineLinkRun = {
              type: 'link',
              href: closed.attrs['href'] ?? '',
              children:
                (closed as OpenTag & { children?: InlineRun[] }).children ?? [],
            };
            if (closed.attrs['rel']) {
              link.rel = closed.attrs['rel'];
            }
            if (closed.attrs['target']) {
              link.target = closed.attrs['target'];
            }
            // Re-route into the new top-of-stack context.
            const outerLink = findOpenLink(stack);
            if (outerLink) {
              (outerLink as OpenTag & { children?: InlineRun[] }).children =
                (outerLink as OpenTag & { children?: InlineRun[] }).children ?? [];
              (outerLink as OpenTag & { children?: InlineRun[] }).children!.push(link);
            } else {
              out.push(link);
            }
          }
          break;
        }
      }
      continue;
    }

    if (MARK_TAGS.has(tagName) || tagName === 'a') {
      stack.push({ name: tagName, attrs: parseAttrs(attrRaw) });
      if (isSelfClosing) {
        // Pop immediately if the tag self-closes (degenerate case).
        stack.pop();
      }
      continue;
    }

    // Unknown tag — emit nothing for the wrapper, fall through. Text
    // inside the unknown tag will still be picked up on the next loop.
  }

  // Close any tags left on the stack — link → emit, marks → drop.
  while (stack.length > 0) {
    const open = stack.pop();
    if (open !== undefined && open.name === 'a') {
      const link: InlineLinkRun = {
        type: 'link',
        href: open.attrs['href'] ?? '',
        children: (open as OpenTag & { children?: InlineRun[] }).children ?? [],
      };
      if (open.attrs['rel']) {
        link.rel = open.attrs['rel'];
      }
      if (open.attrs['target']) {
        link.target = open.attrs['target'];
      }
      const outerLink = findOpenLink(stack);
      if (outerLink) {
        (outerLink as OpenTag & { children?: InlineRun[] }).children =
          (outerLink as OpenTag & { children?: InlineRun[] }).children ?? [];
        (outerLink as OpenTag & { children?: InlineRun[] }).children!.push(link);
      } else {
        out.push(link);
      }
    }
  }

  return out;
}

/**
 * Lift a plain string into a single-text-run array. Bridges the
 * `<RichText value=string>` path until block attributes widen to
 * `string | InlineRun[]`.
 */
export function stringToInline(value: string): InlineRun[] {
  if (value === '') {
    return [];
  }
  return [{ type: 'text', text: value }];
}
