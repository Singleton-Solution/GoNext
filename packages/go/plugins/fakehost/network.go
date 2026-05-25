package fakehost

import (
	"fmt"
)

// Network ABI surface (gn_http_fetch, gn_media_read, gn_users_read,
// http.serve).

// HTTPFetch returns the scripted response for url. If no response has
// been seeded via SetHTTPResponse, returns ErrNotFound — plugins are
// expected to use only URLs the scenario has whitelisted.
//
// Capability gating: requires the "http" cap.
func (h *Host) HTTPFetch(method, url string, headers map[string]string, body []byte) (HTTPResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"method":  method,
		"url":     url,
		"headers": cloneStringMap(headers),
		"body":    string(body),
	}
	if err := h.requireCapLocked(EventHTTPFetch, "http", args); err != nil {
		return HTTPResponse{}, err
	}
	resp, ok := h.httpResponses[url]
	if !ok {
		h.recordLocked(EventHTTPFetch, args, map[string]any{"err": "no scripted response"})
		return HTTPResponse{}, fmt.Errorf("%w: %s", ErrNotFound, url)
	}
	h.recordLocked(EventHTTPFetch, args, resp.Status)
	return resp, nil
}

// MediaRead returns the seeded media row for the given ID. Mirrors
// gn_media_read on the real host.
//
// Capability gating: requires "media.read".
func (h *Host) MediaRead(id int64) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"id": id}
	if err := h.requireCapLocked(EventMediaRead, "media.read", args); err != nil {
		return nil, err
	}
	row, ok := h.media[id]
	if !ok {
		h.recordLocked(EventMediaRead, args, nil)
		return nil, fmt.Errorf("%w: media %d", ErrNotFound, id)
	}
	out := cloneFields(row)
	h.recordLocked(EventMediaRead, args, out)
	return out, nil
}

// UsersRead returns the seeded user row by ID.
//
// Capability gating: requires "users.read".
func (h *Host) UsersRead(id int64) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"id": id}
	if err := h.requireCapLocked(EventUsersRead, "users.read", args); err != nil {
		return nil, err
	}
	row, ok := h.users[id]
	if !ok {
		h.recordLocked(EventUsersRead, args, nil)
		return nil, fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	out := cloneFields(row)
	h.recordLocked(EventUsersRead, args, out)
	return out, nil
}

// HTTPServe registers an HTTP route the plugin would expose. The fake
// host does not actually accept inbound HTTP traffic — it records
// the registration so a scenario can assert "the plugin registered
// route /webhook with handler X".
//
// Capability gating: requires "http.serve".
func (h *Host) HTTPServe(method, path, handler string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{
		"method":  method,
		"path":    path,
		"handler": handler,
	}
	if err := h.requireCapLocked(EventHTTPServe, "http.serve", args); err != nil {
		return err
	}
	h.recordLocked(EventHTTPServe, args, nil)
	return nil
}

// PostsRead returns the seeded post row by ID. Convenience for the
// plugin-author SDK; the real host exposes this through the data
// ABI (gn_db_read with relation=posts).
//
// Capability gating: requires "posts.read".
func (h *Host) PostsRead(id int64) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"id": id}
	if err := h.requireCapLocked(EventPostsRead, "posts.read", args); err != nil {
		return nil, err
	}
	row, ok := h.posts[id]
	if !ok {
		h.recordLocked(EventPostsRead, args, nil)
		return nil, fmt.Errorf("%w: post %d", ErrNotFound, id)
	}
	out := cloneFields(row)
	h.recordLocked(EventPostsRead, args, out)
	return out, nil
}

// PostsWrite upserts a post and returns its ID.
//
// Capability gating: requires "posts.write".
func (h *Host) PostsWrite(row map[string]any) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	args := map[string]any{"row": cloneFields(row)}
	if err := h.requireCapLocked(EventPostsWrite, "posts.write", args); err != nil {
		return 0, err
	}
	id := int64(0)
	if raw, ok := row["id"]; ok {
		switch n := raw.(type) {
		case int:
			id = int64(n)
		case int64:
			id = n
		case float64:
			id = int64(n)
		}
	}
	if id == 0 {
		h.nextID++
		id = h.nextID
	}
	row = cloneFields(row)
	row["id"] = id
	h.posts[id] = row
	h.recordLocked(EventPostsWrite, args, id)
	return id, nil
}

// cloneStringMap returns a shallow copy of m. Used to avoid aliasing
// caller-owned header maps into the recorded event.
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
