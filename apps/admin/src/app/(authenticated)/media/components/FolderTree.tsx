'use client';

/**
 * FolderTree — the collapsible sidebar that lists every media
 * collection (folder). The grid filters off the selected node;
 * dropping a media row onto a node calls move-media and re-files
 * the asset.
 *
 * Why client-side hierarchy reconstruction? The server returns a
 * flat list sorted by ltree path. Splitting on the dot and stitching
 * children under their parents is fast (O(N), no recursion past the
 * initial pass) and means a single endpoint serves both the tree
 * and the per-node detail views. A nested JSON shape would force
 * the API into a recursive serializer.
 *
 * Drop semantics: each node accepts a drop of one or more media ids
 * (serialized as JSON on the dragstart event by the grid). On drop
 * we POST /admin/media/move and notify the parent via
 * `onMediaMoved` so the grid can refresh.
 */
import { ChevronRight, Folder, FolderOpen, Plus, X } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type DragEvent,
  type ReactElement,
} from 'react';
import {
  createCollection,
  deleteCollection,
  listCollections,
  moveMediaToCollection,
} from '../actions';
import type { MediaCollection } from '../types';

/** Sentinel value for "the implicit root view" (assets without a folder). */
export const ROOT_NODE_ID = '__root__';

/** Sentinel value for "no filter" — every asset across every folder. */
export const ALL_NODE_ID = '__all__';

/**
 * MIME of the drag payload — a JSON array of media ids — that
 * MediaGrid writes onto a dragstart and FolderTree reads on drop.
 * Custom MIMEs avoid colliding with text/plain, which OS browsers
 * sometimes auto-fill from the selected element's text content.
 */
export const MEDIA_DRAG_MIME = 'application/x-gonext-media-ids';

export interface FolderTreeProps {
  /** The currently selected folder; null when nothing is selected. */
  selectedId: string | null;
  /** Fires when the operator clicks a folder. */
  onSelect: (id: string | null) => void;
  /**
   * Fires when at least one media row has been dropped on a folder
   * and the move succeeded. The grid uses this to refresh its list.
   */
  onMediaMoved?: () => void;
  /** Optional initial list to seed the tree (server-prefetch path). */
  initialCollections?: MediaCollection[];
}

/**
 * One node in the reconstructed tree. The store-side row has a
 * pre-order list of paths; we build a parent/children dictionary
 * once so the render loop is a simple recursive walk.
 */
interface TreeNode {
  collection: MediaCollection;
  children: TreeNode[];
}

/** Build the parent->children tree from a flat list of collections. */
function buildTree(rows: MediaCollection[]): TreeNode[] {
  const byID = new Map<string, TreeNode>();
  for (const c of rows) byID.set(c.id, { collection: c, children: [] });
  const roots: TreeNode[] = [];
  for (const c of rows) {
    const node = byID.get(c.id)!;
    if (c.parent_id && byID.has(c.parent_id)) {
      byID.get(c.parent_id)!.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

export function FolderTree(props: FolderTreeProps): ReactElement {
  const { selectedId, onSelect, onMediaMoved, initialCollections } = props;
  const [collections, setCollections] = useState<MediaCollection[]>(
    initialCollections ?? [],
  );
  const [loading, setLoading] = useState<boolean>(!initialCollections);
  const [error, setError] = useState<string | null>(null);
  const [creatingUnder, setCreatingUnder] = useState<string | null | typeof ROOT_NODE_ID>(
    null,
  );
  const [newName, setNewName] = useState<string>('');
  const [dropTarget, setDropTarget] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const res = await listCollections();
      // API returns `data: null` for an empty collections list (the
      // pgx nil-slice JSON round-trip). Coerce to [] so downstream
      // `collections.length` reads don't throw.
      setCollections(Array.isArray(res.data) ? res.data : []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load folders');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!initialCollections) {
      void refresh();
    }
  }, [refresh, initialCollections]);

  const tree = useMemo(() => buildTree(collections), [collections]);

  const onCreate = useCallback(
    async (parentId: string | null) => {
      const name = newName.trim();
      if (!name) {
        setCreatingUnder(null);
        return;
      }
      // Slug derives from the name — lowercased, non-alphanumeric
      // collapsed to "-", trimmed. The server re-validates so any
      // edge case (Unicode, empty after sanitisation) surfaces as a
      // 400 that we surface in `error`.
      const slug = name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '')
        .slice(0, 64);
      try {
        await createCollection({ slug, name, parentId: parentId ?? undefined });
        setNewName('');
        setCreatingUnder(null);
        await refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'create failed');
      }
    },
    [newName, refresh],
  );

  const onDelete = useCallback(
    async (id: string) => {
      if (
        typeof window !== 'undefined' &&
        !window.confirm('Delete this folder and every nested folder?')
      ) {
        return;
      }
      try {
        await deleteCollection(id);
        if (selectedId === id) onSelect(null);
        await refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'delete failed');
      }
    },
    [refresh, selectedId, onSelect],
  );

  const onDrop = useCallback(
    async (e: DragEvent<HTMLLIElement>, targetCollectionId: string | null) => {
      e.preventDefault();
      e.stopPropagation();
      setDropTarget(null);
      const raw = e.dataTransfer.getData(MEDIA_DRAG_MIME);
      if (!raw) return;
      let ids: string[] = [];
      try {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) {
          ids = parsed.filter((v): v is string => typeof v === 'string');
        }
      } catch {
        return;
      }
      if (ids.length === 0) return;
      try {
        await moveMediaToCollection({
          ids,
          collection_id: targetCollectionId ?? null,
        });
        if (onMediaMoved) onMediaMoved();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'move failed');
      }
    },
    [onMediaMoved],
  );

  return (
    <aside
      data-testid="folder-tree"
      aria-label="Folders"
      className="flex flex-col gap-2 min-w-[200px]"
    >
      <header className="flex items-baseline justify-between">
        <h2 className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-fg-muted m-0">
          Folders
        </h2>
        <button
          type="button"
          onClick={() => setCreatingUnder(ROOT_NODE_ID)}
          aria-label="New root folder"
          title="New root folder"
          data-testid="folder-create-root"
          className="inline-flex h-6 w-6 items-center justify-center rounded-sm border border-border bg-paper-2 text-fg-muted hover:border-border-strong hover:text-ink cursor-pointer"
        >
          <Plus width={12} height={12} aria-hidden="true" />
        </button>
      </header>

      {error && (
        <p role="alert" className="font-sans text-xs text-danger m-0">
          {error}
        </p>
      )}

      <ul className="m-0 list-none p-0 flex flex-col gap-[2px]">
        <SidebarLeaf
          label="All media"
          selected={selectedId === ALL_NODE_ID || selectedId === null}
          onClick={() => onSelect(ALL_NODE_ID)}
          testId="folder-leaf-all"
        />
        <SidebarLeaf
          label="Unfiled"
          selected={selectedId === ROOT_NODE_ID}
          onClick={() => onSelect(ROOT_NODE_ID)}
          onDrop={(e) => onDrop(e, null)}
          dropActive={dropTarget === ROOT_NODE_ID}
          onDragOver={(e) => {
            e.preventDefault();
            setDropTarget(ROOT_NODE_ID);
          }}
          onDragLeave={() => setDropTarget(null)}
          testId="folder-leaf-unfiled"
        />
      </ul>

      {creatingUnder === ROOT_NODE_ID && (
        <CreateRow
          onSubmit={() => onCreate(null)}
          onCancel={() => {
            setNewName('');
            setCreatingUnder(null);
          }}
          value={newName}
          onChange={setNewName}
        />
      )}

      {loading && collections.length === 0 ? (
        <p className="font-sans text-xs text-fg-subtle m-0">Loading…</p>
      ) : (
        <ul className="m-0 list-none p-0 flex flex-col gap-[2px]">
          {tree.map((node) => (
            <TreeRow
              key={node.collection.id}
              node={node}
              depth={0}
              selectedId={selectedId}
              onSelect={onSelect}
              onDelete={onDelete}
              creatingUnder={creatingUnder}
              setCreatingUnder={setCreatingUnder}
              newName={newName}
              setNewName={setNewName}
              onCreate={onCreate}
              onDrop={onDrop}
              dropTarget={dropTarget}
              setDropTarget={setDropTarget}
            />
          ))}
        </ul>
      )}
    </aside>
  );
}

interface SidebarLeafProps {
  label: string;
  selected: boolean;
  onClick: () => void;
  onDrop?: (e: DragEvent<HTMLLIElement>) => void;
  onDragOver?: (e: DragEvent<HTMLLIElement>) => void;
  onDragLeave?: () => void;
  dropActive?: boolean;
  testId?: string;
}

function SidebarLeaf(props: SidebarLeafProps): ReactElement {
  const { label, selected, onClick, onDrop, onDragOver, onDragLeave, dropActive, testId } =
    props;
  return (
    <li
      data-testid={testId}
      onDrop={onDrop}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      className={[
        'rounded-sm',
        dropActive ? 'bg-emerald-soft' : '',
      ].join(' ')}
    >
      <button
        type="button"
        onClick={onClick}
        className={[
          'w-full text-left flex items-center gap-2 px-2 py-1 rounded-sm',
          'font-sans text-sm cursor-pointer border-0 bg-transparent',
          'transition-colors duration-[160ms] ease-brand',
          selected ? 'bg-emerald-soft text-emerald-deep font-medium' : 'text-ink hover:bg-paper-2',
        ].join(' ')}
      >
        <Folder width={14} height={14} aria-hidden="true" />
        {label}
      </button>
    </li>
  );
}

interface TreeRowProps {
  node: TreeNode;
  depth: number;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  onDelete: (id: string) => void;
  creatingUnder: string | null | typeof ROOT_NODE_ID;
  setCreatingUnder: (v: string | null) => void;
  newName: string;
  setNewName: (v: string) => void;
  onCreate: (parentId: string | null) => void;
  onDrop: (
    e: DragEvent<HTMLLIElement>,
    targetCollectionId: string | null,
  ) => void;
  dropTarget: string | null;
  setDropTarget: (v: string | null) => void;
}

function TreeRow(props: TreeRowProps): ReactElement {
  const {
    node,
    depth,
    selectedId,
    onSelect,
    onDelete,
    creatingUnder,
    setCreatingUnder,
    newName,
    setNewName,
    onCreate,
    onDrop,
    dropTarget,
    setDropTarget,
  } = props;
  const [expanded, setExpanded] = useState<boolean>(true);
  const selected = selectedId === node.collection.id;
  const isDrop = dropTarget === node.collection.id;
  const hasChildren = node.children.length > 0;

  return (
    <li
      data-testid={`folder-row-${node.collection.slug}`}
      onDrop={(e) => onDrop(e, node.collection.id)}
      onDragOver={(e) => {
        e.preventDefault();
        setDropTarget(node.collection.id);
      }}
      onDragLeave={() => setDropTarget(null)}
      className={[
        'rounded-sm',
        isDrop ? 'bg-emerald-soft' : '',
      ].join(' ')}
    >
      <div
        className={[
          'flex items-center gap-1 px-1 py-1 rounded-sm',
          selected ? 'bg-emerald-soft' : '',
        ].join(' ')}
        style={{ paddingLeft: `${depth * 12 + 4}px` }}
      >
        {hasChildren ? (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            aria-label={expanded ? 'Collapse' : 'Expand'}
            className="inline-flex h-5 w-5 items-center justify-center cursor-pointer bg-transparent border-0 text-fg-subtle hover:text-ink"
          >
            <ChevronRight
              width={12}
              height={12}
              aria-hidden="true"
              className={['transition-transform duration-[160ms] ease-brand', expanded ? 'rotate-90' : ''].join(' ')}
            />
          </button>
        ) : (
          <span className="inline-block w-5" aria-hidden="true" />
        )}
        <button
          type="button"
          onClick={() => onSelect(node.collection.id)}
          data-testid={`folder-select-${node.collection.slug}`}
          className={[
            'flex-1 min-w-0 text-left flex items-center gap-2 rounded-sm',
            'font-sans text-sm cursor-pointer border-0 bg-transparent',
            selected ? 'text-emerald-deep font-medium' : 'text-ink hover:text-ink',
          ].join(' ')}
        >
          {expanded ? (
            <FolderOpen width={14} height={14} aria-hidden="true" />
          ) : (
            <Folder width={14} height={14} aria-hidden="true" />
          )}
          <span className="truncate">{node.collection.name}</span>
        </button>
        <button
          type="button"
          onClick={() => setCreatingUnder(node.collection.id)}
          aria-label={`New folder under ${node.collection.name}`}
          title="New subfolder"
          className="inline-flex h-5 w-5 items-center justify-center cursor-pointer bg-transparent border-0 text-fg-subtle hover:text-ink"
        >
          <Plus width={11} height={11} aria-hidden="true" />
        </button>
        <button
          type="button"
          onClick={() => onDelete(node.collection.id)}
          aria-label={`Delete ${node.collection.name}`}
          title="Delete folder"
          className="inline-flex h-5 w-5 items-center justify-center cursor-pointer bg-transparent border-0 text-fg-subtle hover:text-danger"
        >
          <X width={11} height={11} aria-hidden="true" />
        </button>
      </div>

      {creatingUnder === node.collection.id && (
        <div style={{ paddingLeft: `${(depth + 1) * 12 + 4}px` }}>
          <CreateRow
            onSubmit={() => onCreate(node.collection.id)}
            onCancel={() => {
              setNewName('');
              setCreatingUnder(null);
            }}
            value={newName}
            onChange={setNewName}
          />
        </div>
      )}

      {hasChildren && expanded && (
        <ul className="m-0 list-none p-0">
          {node.children.map((child) => (
            <TreeRow
              key={child.collection.id}
              node={child}
              depth={depth + 1}
              selectedId={selectedId}
              onSelect={onSelect}
              onDelete={onDelete}
              creatingUnder={creatingUnder}
              setCreatingUnder={setCreatingUnder}
              newName={newName}
              setNewName={setNewName}
              onCreate={onCreate}
              onDrop={onDrop}
              dropTarget={dropTarget}
              setDropTarget={setDropTarget}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

interface CreateRowProps {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  onCancel: () => void;
}

function CreateRow(props: CreateRowProps): ReactElement {
  const { value, onChange, onSubmit, onCancel } = props;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
      className="flex items-center gap-1 px-1 py-1"
      data-testid="folder-create-row"
    >
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoFocus
        aria-label="New folder name"
        placeholder="Folder name"
        className="flex-1 min-w-0 rounded-sm border border-border bg-paper px-2 py-1 font-sans text-sm text-ink focus-visible:outline-none focus-visible:shadow-focus"
      />
      <button
        type="submit"
        className="rounded-sm bg-emerald text-emerald-ink px-2 py-1 font-sans text-xs font-medium cursor-pointer border-0"
      >
        Add
      </button>
      <button
        type="button"
        onClick={onCancel}
        aria-label="Cancel"
        className="inline-flex h-5 w-5 items-center justify-center cursor-pointer bg-transparent border-0 text-fg-subtle hover:text-ink"
      >
        <X width={11} height={11} aria-hidden="true" />
      </button>
    </form>
  );
}
