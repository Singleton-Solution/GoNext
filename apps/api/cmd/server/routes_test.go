package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// TestBuildRouter_DocumentedRoutesAreMounted is the K3-verify smoke
// guard: every documented route must respond with SOMETHING other than
// 404 from buildRouter. We do NOT assert success here (the routes
// require auth, valid JSON, a live DB, …); we assert only that the
// route is REACHABLE — i.e. the mux has a handler bound to it.
//
// A 404 from this test means buildRouter forgot to call the package's
// Mount/Routes helper, which is exactly the regression that caused
// the K3 finding (#427) and blocked the K4 e2e (#424).
//
// We pass nil for pool/rdb because every guarded block in buildRouter
// either falls through cleanly on nil or logs a warning and skips the
// mount. The login + sessions blocks both skip on a nil sessions arg;
// the public-comments block runs unconditionally; rest/posts is in-
// memory and pool-independent. The routes covered here are the union
// of "mounts that work without external state".
func TestBuildRouter_DocumentedRoutesAreMounted(t *testing.T) {
	cfg := &config.Config{
		Plugins: config.PluginsConfig{DevMode: false},
		Auth: config.AuthConfig{
			// Non-zero TTLs so login.Deps.validate() doesn't reject the
			// mount. Values themselves don't matter for the mount-time
			// assertion — we never actually mint a session in this test.
			SessionTTL:     24 * time.Hour,
			SessionIdleTTL: time.Hour,
		},
	}

	cases := []struct {
		name, method, path string
	}{
		// Operational endpoint — already wired; baseline that the
		// harness itself works. readyz is omitted because it
		// dereferences the (nil) pool/rdb its checks were built with.
		{"healthz", http.MethodGet, "/healthz"},

		// rest/posts — mounted with MemoryStore, runs without a pool.
		{"posts.list", http.MethodGet, "/api/v1/posts"},
		{"posts.create", http.MethodPost, "/api/v1/posts"},
		{"posts.get", http.MethodGet, "/api/v1/posts/abc"},
		{"posts.update", http.MethodPatch, "/api/v1/posts/abc"},
		{"posts.delete", http.MethodDelete, "/api/v1/posts/abc"},

		// rest/comments — already wired before this PR. Smoke-check.
		{"comments.list", http.MethodGet, "/api/v1/posts/abc/comments"},

		// auth/login — the route this PR's K3 finding (#427) was about.
		// We send POST so the route's "POST /…" pattern matches.
		{"auth.login", http.MethodPost, "/api/v1/auth/login"},

		// auth/sessions — must be reachable under the RequireSession
		// guard. The guard returns 401 (not 404) for an unauth caller,
		// which is exactly the signal we want here.
		{"auth.sessions.list", http.MethodGet, "/api/v1/auth/sessions"},
		{"auth.sessions.delete_one", http.MethodDelete, "/api/v1/auth/sessions/abc"},
		{"auth.sessions.delete_all", http.MethodDelete, "/api/v1/auth/sessions"},
	}

	// Pass a non-nil session manager so the login + sessions blocks
	// register their routes. The manager's nil rdb is fine: handler
	// REGISTRATION never touches Redis, only handler INVOCATION does,
	// and we never invoke past the auth guard in this test.
	sm := session.NewWithClient(nil, testLogger())
	router := buildRouter(cfg, nil, nil, sm, "", testLogger(), nil, nil, nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
			if rr.Code == http.StatusNotFound {
				t.Errorf("%s %s: route not mounted (got 404)", tc.method, tc.path)
			}
		})
	}
}
