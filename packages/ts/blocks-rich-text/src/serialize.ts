/**
 * Canonical HTML serializer for the rich-text package.
 *
 * Walks an array of `InlineRun`s and produces the byte-stable HTML string
 * that `core/paragraph`'s `save()` would have wrapped in `<p>...</p>`,
 * `core/heading` in `<hN>...</hN>`, etc. The block-level wrappers are the
 * concern of the *block's* save function; this module emits the inner HTML
 * — the part that lives between the open and close tag of the block.
 *
 * Decisions:
 *
 *  - Adjacent text runs with identical marks are collapsed before emission,
 *    so a paragraph typed as a single string survives the round-trip
 *    without sprouting redundant `<strong></strong>` boundaries.
 *  - The order of inline tags is fixed (`strong` outermost, then `em`, then
 *    `code`). The browser doesn't care, but a fixed order makes snapshot
 *    diffs sane and matches what `deserialize` reconstructs.
 *  - Links are emitted as `<a href="…">…</a>` with `rel`/`target` only when
 *    set — empty attributes are dropped. `href` is HTML-escaped, not
 *    URL-encoded; callers are responsible for handing us a string that's
 *    already URL-safe.
 *  - Empty/whitespace-only inputs serialize to an empty string, not to
 *    a `<br>` or a non-breaking space. The Go SSR walker emits nothing
 *    for empty paragraphs and the canvas does the same.
 */
import { escapeHtml } from './escape.ts';
import {
  isLinkRun,
  isLineBreakRun,
  isTextRun,
  type InlineRun,
  type InlineMarks,
} from './inline.ts';

function marksEqual(a: InlineMarks | undefined, b: InlineMarks | undefined): boolean {
  // Both unset → equal; one unset → unequal; otherwise compare each known mark.
  if (!a && !b) {
    return true;
  }
  if (!a || !b) {
    return false;
  }
  return (
    Boolean(a.bold) === Boolean(b.bold) &&
    Boolean(a.italic) === Boolean(b.italic) &&
    Boolean(a.code) === Boolean(b.code)
  );
}

function collapseAdjacentText(runs: InlineRun[]): InlineRun[] {
  const out: InlineRun[] = [];
  for (const run of runs) {
    const prev = out[out.length - 1];
    if (
      isTextRun(run) &&
      prev !== undefined &&
      isTextRun(prev) &&
      marksEqual(prev.marks, run.marks)
    ) {
      out[out.length - 1] = {
        type: 'text',
        text: prev.text + run.text,
        ...(prev.marks ? { marks: prev.marks } : {}),
      };
      continue;
    }
    if (isLinkRun(run)) {
      out.push({ ...run, children: collapseAdjacentText(run.children) });
      continue;
    }
    out.push(run);
  }
  return out;
}

function wrapMarks(html: string, marks: InlineMarks | undefined): string {
  if (!marks) {
    return html;
  }
  let inner = html;
  // Innermost first: code → em → strong. Reversing on the way out yields
  // a deterministic outer-to-inner order in the emitted bytes.
  if (marks.code) {
    inner = `<code>${inner}</code>`;
  }
  if (marks.italic) {
    inner = `<em>${inner}</em>`;
  }
  if (marks.bold) {
    inner = `<strong>${inner}</strong>`;
  }
  return inner;
}

function serializeRun(run: InlineRun): string {
  if (isLineBreakRun(run)) {
    return '<br>';
  }
  if (isLinkRun(run)) {
    const rel = run.rel ? ` rel="${escapeHtml(run.rel)}"` : '';
    const target = run.target ? ` target="${escapeHtml(run.target)}"` : '';
    const inner = run.children.map(serializeRun).join('');
    return `<a href="${escapeHtml(run.href)}"${rel}${target}>${inner}</a>`;
  }
  // text run.
  const escaped = escapeHtml(run.text);
  return wrapMarks(escaped, run.marks);
}

/**
 * Serialize an array of inline runs to canonical HTML. Empty inputs and
 * inputs that collapse to an empty string both return `''`.
 */
export function serializeInline(runs: InlineRun[]): string {
  if (runs.length === 0) {
    return '';
  }
  const collapsed = collapseAdjacentText(runs);
  return collapsed.map(serializeRun).join('');
}

/**
 * Flatten an array of inline runs to a plain string — drops every mark,
 * keeps text and link text, replaces linebreaks with `\n`. Used by the
 * `<RichText/>` wrapper to bridge into block attributes that still type
 * `content` as `string` (every core block today). Once those attributes
 * widen to `InlineRun[]` this helper can be retired.
 */
export function flattenInlineToText(runs: InlineRun[]): string {
  let out = '';
  for (const run of runs) {
    if (isLineBreakRun(run)) {
      out += '\n';
      continue;
    }
    if (isLinkRun(run)) {
      out += flattenInlineToText(run.children);
      continue;
    }
    out += run.text;
  }
  return out;
}
