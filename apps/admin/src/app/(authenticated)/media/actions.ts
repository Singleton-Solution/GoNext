/**
 * Media admin — server-action wrappers.
 *
 * Each helper wraps a single endpoint on the admin API. Returning the
 * parsed body (or `void` for delete-style actions) keeps the component
 * layer free of `fetch`-shaped detail.
 *
 * Uploads are an exception: they use a raw `fetch` with `FormData`
 * rather than the `api` JSON wrapper, because they need (a) multipart
 * encoding and (b) an XHR-style progress event so the dropzone can
 * render a per-file progress bar. The wrapper still lives in this
 * file so the call sites are uniform.
 */
import { api, apiBaseUrl, ApiError } from '@/lib/api-client';
import type {
  BulkRequest,
  BulkResult,
  CollectionListResponse,
  MediaAsset,
  MediaCollection,
  MediaListResponse,
  MediaTypeFilter,
  MediaUpdateBody,
  MoveMediaBody,
} from './types';

export interface ListParams {
  type?: MediaTypeFilter;
  limit?: number;
  cursor?: string;
  /**
   * Folder narrowing. `undefined` is "no filter"; `'root'` selects
   * assets sitting at the implicit root (no collection_id); a UUID
   * selects one folder. Issue #69.
   */
  collection?: string;
}

/**
 * Fetch a page of media assets. The cursor is opaque — pass back the
 * `next_cursor` from a previous response verbatim. `type="all"` is
 * the default and translates to "no `type` query param".
 */
export function listMedia(params: ListParams = {}): Promise<MediaListResponse> {
  const search = new URLSearchParams();
  if (params.type && params.type !== 'all') {
    search.set('type', params.type);
  }
  if (params.limit !== undefined) {
    search.set('limit', String(params.limit));
  }
  if (params.cursor) {
    search.set('cursor', params.cursor);
  }
  if (params.collection) {
    search.set('collection', params.collection);
  }
  const qs = search.toString();
  const path = qs ? `/api/v1/admin/media?${qs}` : '/api/v1/admin/media';
  return api.get<MediaListResponse>(path);
}

/** Fetch a single asset's full record. */
export function getMedia(id: string): Promise<MediaAsset> {
  return api.get<MediaAsset>(`/api/v1/admin/media/${encodeURIComponent(id)}`);
}

/**
 * Patch alt_text and/or caption. The server requires at least one
 * field to be present; the caller is expected to filter empty patches
 * before calling.
 */
export function updateMedia(
  id: string,
  body: MediaUpdateBody,
): Promise<MediaAsset> {
  return api.patch<MediaAsset>(
    `/api/v1/admin/media/${encodeURIComponent(id)}`,
    body,
  );
}

/**
 * Soft-delete an asset. The server returns 204 on success; this
 * function resolves to void.
 */
export function deleteMedia(id: string): Promise<void> {
  return api
    .delete<unknown>(`/api/v1/admin/media/${encodeURIComponent(id)}`)
    .then(() => undefined);
}

/**
 * Upload a single file via multipart/form-data. Returns the persisted
 * asset record on success. The optional `onProgress` callback receives
 * a fraction in [0, 1] as the upload streams; it fires only when the
 * runtime's XMLHttpRequest exposes `upload.onprogress` (jsdom does not,
 * which is fine — the production browser does).
 *
 * We use XHR rather than fetch because the Fetch Streams API lacks an
 * upload-progress event in most browsers as of writing. When the
 * platform support story improves, we swap to fetch + ReadableStream
 * without changing this function's signature.
 */
export function uploadMedia(
  file: File,
  options: { signal?: AbortSignal; onProgress?: (fraction: number) => void } = {},
): Promise<MediaAsset> {
  const { signal, onProgress } = options;
  const form = new FormData();
  form.append('file', file, file.name);

  // We keep the URL building here (rather than reusing `api`) because
  // the api-client.ts wrapper assumes JSON; FormData needs the browser
  // to set the multipart boundary itself, which means we must NOT
  // touch Content-Type.
  return new Promise<MediaAsset>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${apiBaseUrl()}/api/v1/admin/media`, true);
    xhr.withCredentials = true;

    if (xhr.upload && onProgress) {
      xhr.upload.onprogress = (evt) => {
        if (evt.lengthComputable && evt.total > 0) {
          onProgress(evt.loaded / evt.total);
        }
      };
    }

    xhr.onload = () => {
      // 2xx is success; both 200 (dedupe) and 201 (created) return
      // the asset body. Anything else surfaces as an ApiError so the
      // call site's catch branch matches the rest of the admin code.
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const parsed = JSON.parse(xhr.responseText) as MediaAsset;
          resolve(parsed);
        } catch (err) {
          reject(
            new ApiError(
              xhr.status,
              xhr.statusText,
              undefined,
              `failed to parse upload response: ${(err as Error).message}`,
            ),
          );
        }
        return;
      }
      let payload: unknown;
      try {
        payload = JSON.parse(xhr.responseText);
      } catch {
        payload = xhr.responseText;
      }
      reject(new ApiError(xhr.status, xhr.statusText, payload));
    };

    xhr.onerror = () => {
      reject(new ApiError(0, 'network error', undefined, 'network error'));
    };
    xhr.onabort = () => {
      reject(new ApiError(0, 'aborted', undefined, 'upload aborted'));
    };

    if (signal) {
      signal.addEventListener(
        'abort',
        () => {
          xhr.abort();
        },
        { once: true },
      );
    }
    xhr.send(form);
  });
}

// --- Collections + bulk operations (issues #69, #71) --------------------------

/** List every media folder as a flat list ordered by path. */
export function listCollections(): Promise<CollectionListResponse> {
  return api.get<CollectionListResponse>('/api/v1/admin/media/collections');
}

/** Create a new folder. parentId omitted => root collection. */
export function createCollection(input: {
  slug: string;
  name: string;
  parentId?: string;
}): Promise<MediaCollection> {
  return api.post<MediaCollection>('/api/v1/admin/media/collections', {
    slug: input.slug,
    name: input.name,
    parent_id: input.parentId,
  });
}

/** Rename a folder. Either field can be omitted; both omitted is a 400. */
export function renameCollection(
  id: string,
  input: { slug?: string; name?: string },
): Promise<MediaCollection> {
  return api.put<MediaCollection>(
    `/api/v1/admin/media/collections/${encodeURIComponent(id)}`,
    { slug: input.slug, name: input.name },
  );
}

/**
 * Re-parent a folder. `newParentId === null` moves to the root; the
 * `move_parent` flag is required by the server to distinguish
 * "client did not specify parent_id" from "client wants null".
 */
export function moveCollection(
  id: string,
  newParentId: string | null,
): Promise<MediaCollection> {
  return api.put<MediaCollection>(
    `/api/v1/admin/media/collections/${encodeURIComponent(id)}`,
    { parent_id: newParentId, move_parent: true },
  );
}

/** Delete a folder and every descendant. Cascade on the server. */
export function deleteCollection(id: string): Promise<void> {
  return api
    .delete<unknown>(
      `/api/v1/admin/media/collections/${encodeURIComponent(id)}`,
    )
    .then(() => undefined);
}

/**
 * Move a list of media into a folder. `collectionId === null` (or
 * omitted) puts the assets at the root. Returns `{ moved, failed? }`.
 */
export function moveMediaToCollection(
  body: MoveMediaBody,
): Promise<{ moved: number; failed?: Record<string, string> }> {
  return api.post<{ moved: number; failed?: Record<string, string> }>(
    '/api/v1/admin/media/move',
    body,
  );
}

/**
 * Fan-out bulk operation. The server validates the op and returns a
 * count of successes plus a per-id failure map.
 */
export function bulkMedia(req: BulkRequest): Promise<BulkResult> {
  return api.post<BulkResult>('/api/v1/admin/media/bulk', req);
}
