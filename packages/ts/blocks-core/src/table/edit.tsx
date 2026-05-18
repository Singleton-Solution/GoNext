/**
 * `core/table` Edit component.
 *
 * Tables use plain `contenteditable` cells rather than a full `<RichText/>`
 * surface — the structural editing (cells / rows / sections) dominates the
 * authoring cost, and inline formatting inside a single cell is well
 * served by the browser's native editing primitives. When the rich-inline
 * model lands we can swap this for the Lexical-backed version block-wide.
 *
 * The component is intentionally read-mostly: we render the persisted
 * matrix and forward `blur` events into `setAttributes` so the canvas
 * picks up the cell text whenever focus leaves a cell. This keeps the
 * common authoring path responsive without thrashing React state on every
 * keystroke.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { FocusEvent, ReactElement } from 'react';
import type { TableAttributes, TableRow } from './save.ts';

interface CellProps {
  value: string;
  tag: 'td' | 'th';
  isSelected: boolean;
  onCommit: (next: string) => void;
}

/** A single editable cell. `<td>`/`<th>` switches based on the section. */
function Cell({ value, tag, isSelected, onCommit }: CellProps): ReactElement {
  const handleBlur = (event: FocusEvent<HTMLTableCellElement>): void => {
    const next = event.currentTarget.textContent ?? '';
    if (next !== value) {
      onCommit(next);
    }
  };
  const Tag = tag;
  return (
    <Tag
      // `suppressContentEditableWarning` because we intentionally let the
      // DOM hold the source of truth for the in-progress cell text — we
      // only sync back to React on blur.
      contentEditable={isSelected}
      suppressContentEditableWarning
      onBlur={handleBlur}
    >
      {value}
    </Tag>
  );
}

/** Render a single section's rows. */
function Section({
  sectionTag,
  rows,
  cellTag,
  isSelected,
  onUpdate,
}: {
  sectionTag: 'thead' | 'tbody' | 'tfoot';
  rows: TableRow[];
  cellTag: 'td' | 'th';
  isSelected: boolean;
  onUpdate: (rowIdx: number, cellIdx: number, next: string) => void;
}): ReactElement | null {
  if (rows.length === 0) {
    return null;
  }
  const Tag = sectionTag;
  return (
    <Tag>
      {rows.map((row, rowIdx) => (
        <tr key={rowIdx}>
          {row.map((cell, cellIdx) => (
            <Cell
              key={cellIdx}
              value={cell}
              tag={cellTag}
              isSelected={isSelected}
              onCommit={(next) => onUpdate(rowIdx, cellIdx, next)}
            />
          ))}
        </tr>
      ))}
    </Tag>
  );
}

export function TableEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<TableAttributes>): ReactElement {
  const head = attributes.head ?? [];
  const body = attributes.body;
  const foot = attributes.foot ?? [];

  // Helper that immutably swaps a single cell within a section and writes
  // the updated section back through `setAttributes`.
  const updateSection = (
    section: 'head' | 'body' | 'foot',
    rows: TableRow[],
    rowIdx: number,
    cellIdx: number,
    next: string,
  ): void => {
    const nextRows: TableRow[] = rows.map((row, idx) =>
      idx === rowIdx
        ? row.map((cell, cIdx) => (cIdx === cellIdx ? next : cell))
        : row,
    );
    setAttributes({ [section]: nextRows } as Partial<TableAttributes>);
  };

  const className = [
    'gn-block-table',
    attributes.style?.stripes ? 'is-style-stripes' : null,
    attributes.style?.borders ? 'is-style-borders' : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return (
    <table className={className} data-block="core/table">
      {attributes.caption ? <caption>{attributes.caption}</caption> : null}
      <Section
        sectionTag="thead"
        rows={head}
        cellTag="th"
        isSelected={isSelected}
        onUpdate={(r, c, n) => updateSection('head', head, r, c, n)}
      />
      <Section
        sectionTag="tbody"
        rows={body}
        cellTag="td"
        isSelected={isSelected}
        onUpdate={(r, c, n) => updateSection('body', body, r, c, n)}
      />
      <Section
        sectionTag="tfoot"
        rows={foot}
        cellTag="td"
        isSelected={isSelected}
        onUpdate={(r, c, n) => updateSection('foot', foot, r, c, n)}
      />
    </table>
  );
}

export default TableEdit;
