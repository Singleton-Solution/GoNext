'use client';

/**
 * Navigation menus admin client — issue #54.
 *
 * Two-pane layout: the left rail lists menus and offers a "New menu"
 * form; the right pane shows the selected menu's items in drag-to-
 * reorder order. Items can be edited inline (label/url) or removed;
 * dragging an item up or down rewrites its `path` and the local order
 * optimistically, then PATCHes the new ordering via
 * POST /api/v1/admin/menus/{id}/items/reorder.
 *
 * The drag-and-drop uses native HTML5 drag events — keeps the bundle
 * lean and avoids pulling in @dnd-kit just for this surface.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type DragEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
import { api, ApiError } from '@/lib/api-client';
import type { Menu, MenuItem, MenuWithItems } from './types';

interface Props {
  initialMenus: Menu[];
}

export function MenusClient({ initialMenus }: Props): ReactElement {
  const [menus, setMenus] = useState<Menu[]>(initialMenus);
  const [selectedId, setSelectedId] = useState<string>(initialMenus[0]?.id ?? '');
  const [items, setItems] = useState<MenuItem[]>([]);
  const [loadingItems, setLoadingItems] = useState(false);
  const [error, setError] = useState<string>('');

  // Load the items for the selected menu whenever it changes.
  useEffect(() => {
    if (!selectedId) {
      setItems([]);
      return;
    }
    let cancelled = false;
    setLoadingItems(true);
    setError('');
    api
      .get<MenuWithItems>(`/api/v1/admin/menus/${selectedId}`)
      .then((data) => {
        if (cancelled) return;
        setItems(data.items ?? []);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(e instanceof ApiError ? `Load failed (${e.status})` : 'Load failed');
      })
      .finally(() => {
        if (!cancelled) setLoadingItems(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId]);

  const sortedItems = useMemo(
    () => [...items].sort((a, b) => a.path.localeCompare(b.path)),
    [items],
  );

  const onCreateMenu = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      const form = e.currentTarget;
      const slug = (form.elements.namedItem('slug') as HTMLInputElement).value.trim();
      const name = (form.elements.namedItem('name') as HTMLInputElement).value.trim();
      if (!slug || !name) return;
      try {
        const created = await api.post<Menu>('/api/v1/admin/menus', { slug, name });
        setMenus((prev) => [...prev, created].sort((a, b) => a.name.localeCompare(b.name)));
        setSelectedId(created.id);
        form.reset();
      } catch (err) {
        setError(err instanceof ApiError ? `Create failed (${err.status})` : 'Create failed');
      }
    },
    [],
  );

  const onAddItem = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      const form = e.currentTarget;
      const label = (form.elements.namedItem('label') as HTMLInputElement).value.trim();
      const url = (form.elements.namedItem('url') as HTMLInputElement).value.trim();
      if (!label) return;
      // Path is the next root slot.
      const nextIdx = sortedItems.filter((it) => !it.path.includes('.')).length + 1;
      const path = String(nextIdx).padStart(3, '0');
      try {
        const created = await api.post<MenuItem>(
          `/api/v1/admin/menus/${selectedId}/items`,
          { path, label, url },
        );
        setItems((prev) => [...prev, created]);
        form.reset();
      } catch (err) {
        setError(err instanceof ApiError ? `Add failed (${err.status})` : 'Add failed');
      }
    },
    [selectedId, sortedItems],
  );

  const onDeleteItem = useCallback(
    async (itemId: string) => {
      try {
        await api.delete(`/api/v1/admin/menus/${selectedId}/items/${itemId}`);
        setItems((prev) => prev.filter((it) => it.id !== itemId));
      } catch (err) {
        setError(err instanceof ApiError ? `Delete failed (${err.status})` : 'Delete failed');
      }
    },
    [selectedId],
  );

  // Drag state: which item is being dragged.
  const [dragId, setDragId] = useState<string>('');

  const onDragStart = useCallback(
    (id: string) => (e: DragEvent<HTMLLIElement>) => {
      setDragId(id);
      e.dataTransfer.effectAllowed = 'move';
    },
    [],
  );

  const onDragOver = useCallback((e: DragEvent<HTMLLIElement>) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback(
    (targetId: string) => async (e: DragEvent<HTMLLIElement>) => {
      e.preventDefault();
      if (!dragId || dragId === targetId) {
        setDragId('');
        return;
      }
      const ordered = [...sortedItems];
      const fromIdx = ordered.findIndex((it) => it.id === dragId);
      const toIdx = ordered.findIndex((it) => it.id === targetId);
      if (fromIdx < 0 || toIdx < 0) {
        setDragId('');
        return;
      }
      const [moved] = ordered.splice(fromIdx, 1);
      // `splice` returns `T[]` so the destructure is typed `T |
      // undefined` under `noUncheckedIndexedAccess`. fromIdx ≥ 0 was
      // checked above, so moved is in practice always defined — but
      // the type-checker doesn't know that. Guard explicitly so the
      // re-insert doesn't push `undefined` into the array.
      if (!moved) {
        setDragId('');
        return;
      }
      ordered.splice(toIdx, 0, moved);
      // Re-stamp paths as flat root-level slots — the drag-drop surface
      // here only supports a single nesting level for now.
      const renumbered = ordered.map((it, i) => ({
        ...it,
        path: String(i + 1).padStart(3, '0'),
      }));
      setItems(renumbered);
      setDragId('');
      try {
        await api.post(`/api/v1/admin/menus/${selectedId}/items/reorder`, {
          items: renumbered.map((it) => ({ id: it.id, path: it.path })),
        });
      } catch (err) {
        setError(err instanceof ApiError ? `Reorder failed (${err.status})` : 'Reorder failed');
      }
    },
    [dragId, selectedId, sortedItems],
  );

  return (
    <div className="menus-admin">
      <h1>Navigation menus</h1>
      {error && (
        <div role="alert" className="menus-admin__error">
          {error}
        </div>
      )}

      <div className="menus-admin__grid">
        {/* Left rail: menus list + new-menu form. */}
        <aside className="menus-admin__rail">
          <h2>Menus</h2>
          <ul className="menus-admin__menu-list">
            {menus.map((m) => (
              <li key={m.id}>
                <button
                  type="button"
                  onClick={() => setSelectedId(m.id)}
                  aria-current={m.id === selectedId ? 'true' : undefined}
                >
                  <strong>{m.name}</strong>
                  <span className="menus-admin__slug">/{m.slug}</span>
                </button>
              </li>
            ))}
          </ul>
          <form onSubmit={onCreateMenu} className="menus-admin__new-menu">
            <h3>New menu</h3>
            <label>
              <span>Slug</span>
              <input
                name="slug"
                pattern="[a-z0-9][a-z0-9_-]*"
                placeholder="primary"
                required
              />
            </label>
            <label>
              <span>Name</span>
              <input name="name" placeholder="Primary" required />
            </label>
            <button type="submit">Create</button>
          </form>
        </aside>

        {/* Right pane: items for the selected menu. */}
        <section className="menus-admin__pane">
          {!selectedId && <p>Select a menu to edit its items.</p>}
          {selectedId && (
            <>
              <h2>Items</h2>
              {loadingItems && <p>Loading…</p>}
              <ol className="menus-admin__items">
                {sortedItems.map((it) => (
                  <li
                    key={it.id}
                    draggable
                    onDragStart={onDragStart(it.id)}
                    onDragOver={onDragOver}
                    onDrop={onDrop(it.id)}
                    className={dragId === it.id ? 'is-dragging' : undefined}
                    aria-label={`Drag to reorder ${it.label}`}
                  >
                    <span className="menus-admin__handle" aria-hidden>
                      ⋮⋮
                    </span>
                    <strong>{it.label}</strong>
                    <code>{it.url || '(no url)'}</code>
                    <button
                      type="button"
                      onClick={() => void onDeleteItem(it.id)}
                      aria-label={`Remove ${it.label}`}
                    >
                      Remove
                    </button>
                  </li>
                ))}
              </ol>
              <form onSubmit={onAddItem} className="menus-admin__new-item">
                <h3>Add item</h3>
                <label>
                  <span>Label</span>
                  <input name="label" placeholder="About" required />
                </label>
                <label>
                  <span>URL</span>
                  <input name="url" placeholder="/about" />
                </label>
                <button type="submit">Add</button>
              </form>
            </>
          )}
        </section>
      </div>
    </div>
  );
}
