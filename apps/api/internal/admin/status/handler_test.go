package status

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// fixedClock returns a constant time.Time so report JSON is byte-stable
// across runs. The value chosen is arbitrary; we just need it to round-
// trip through RFC3339 unchanged.
func fixedClock(t time.Time) Clock { return func() time.Time { return t } }

// stubSources is a fully populated Sources with deterministic stubs.
// Tests build one of these and tweak the field they want to assert on.
func stubSources() Sources {
	return Sources{
		BuildInfo: stubBuildInfo{snap: BuildInfoSnapshot{
			Version: "v1.2.3", Commit: "abc123", Date: "2026-05-17T00:00:00Z",
			GoVersion: "go1.25.0", OS: "linux", Arch: "amd64",
		}},
		DB: stubDBValue{snap: DatabaseStatus{
			OK: true, Version: "PostgreSQL 16.2", MaxConns: 25, InUse: 4, Idle: 21,
			ResponseTimeMS: 1,
		}},
		Redis: stubRedis{snap: RedisStatus{
			OK: true, Version: "7.2.4", ResponseTimeMS: 1,
		}},
		Migrations: stubMigrations{snap: MigrationsStatus{
			CurrentVersion: 42, Dirty: false, TotalCount: 42,
		}},
		Queues: stubQueues{snap: []QueueStatus{
			{Name: "critical", Pending: 2, Active: 1, Processed24H: 100, Failed24H: 0},
			{Name: "default", Pending: 0, Active: 0, Processed24H: 50, Failed24H: 0},
		}},
		Theme: stubTheme{snap: ThemeStatus{
			ActiveName: "gn-pro", Version: "v1", PartsCount: 4, TemplatesCount: 3,
		}},
		Plugins: stubPlugins{snap: PluginsStatus{
			Installed: 5, Active: 4, Errored: 0,
			LastInstall: "2026-05-10T00:00:00Z",
		}},
		Disk: stubDisk{snap: DiskStatus{
			ThemeDirBytes: 12_345, MediaDirBytes: 67_890_123,
		}},
	}
}

type stubBuildInfo struct{ snap BuildInfoSnapshot }

func (s stubBuildInfo) Get() BuildInfoSnapshot { return s.snap }

// stubDBValue is the value-receiver DatabaseSource stub used by every
// test in this file. We don't need a pointer-receiver variant that
// counts invocations — the contract under test is "the report carries
// the value the source returned", not "the handler called the source
// exactly once".
type stubDBValue struct{ snap DatabaseStatus }

func (s stubDBValue) Snapshot(context.Context) DatabaseStatus { return s.snap }

// Replace the &-receiver stubDB above with stubDBValue in stubSources;
// the pointer form is only used by the contextDeadline test below.

type stubRedis struct{ snap RedisStatus }

func (s stubRedis) Snapshot(context.Context) RedisStatus { return s.snap }

type stubMigrations struct{ snap MigrationsStatus }

func (s stubMigrations) Snapshot(context.Context) MigrationsStatus { return s.snap }

type stubQueues struct{ snap []QueueStatus }

func (s stubQueues) Snapshot(context.Context) []QueueStatus { return s.snap }

type stubTheme struct{ snap ThemeStatus }

func (s stubTheme) Snapshot(context.Context) ThemeStatus { return s.snap }

type stubPlugins struct{ snap PluginsStatus }

func (s stubPlugins) Snapshot(context.Context) PluginsStatus { return s.snap }

type stubDisk struct{ snap DiskStatus }

func (s stubDisk) Snapshot(context.Context) DiskStatus { return s.snap }

// TestAggregate_AllSourcesPresent asserts every section of the report
// carries the values the stubs returned. The test is the primary
// shape-of-the-wire guard; renaming a JSON tag breaks the comparison.
func TestAggregate_AllSourcesPresent(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	h := NewHandler(stubSources(), HandlerOptions{Clock: fixedClock(at)})

	got := h.Aggregate(context.Background())

	if got.Generated != "2026-05-17T12:00:00Z" {
		t.Errorf("generated = %q, want 2026-05-17T12:00:00Z", got.Generated)
	}
	if got.Version != "v1.2.3" || got.Commit != "abc123" {
		t.Errorf("buildinfo = %+v", got)
	}
	if got.GoVersion != "go1.25.0" || got.OS != "linux" || got.Arch != "amd64" {
		t.Errorf("buildinfo platform = %+v", got)
	}
	if !got.Database.OK || got.Database.MaxConns != 25 || got.Database.InUse != 4 || got.Database.Idle != 21 {
		t.Errorf("database = %+v", got.Database)
	}
	if got.Database.Version != "PostgreSQL 16.2" {
		t.Errorf("database version = %q", got.Database.Version)
	}
	if !got.Redis.OK || got.Redis.Version != "7.2.4" {
		t.Errorf("redis = %+v", got.Redis)
	}
	if got.Migrations.CurrentVersion != 42 || got.Migrations.TotalCount != 42 || got.Migrations.Dirty {
		t.Errorf("migrations = %+v", got.Migrations)
	}
	if len(got.Queues) != 2 || got.Queues[0].Name != "critical" || got.Queues[0].Pending != 2 {
		t.Errorf("queues = %+v", got.Queues)
	}
	if got.Theme.ActiveName != "gn-pro" || got.Theme.PartsCount != 4 || got.Theme.TemplatesCount != 3 {
		t.Errorf("theme = %+v", got.Theme)
	}
	if got.Plugins.Installed != 5 || got.Plugins.Active != 4 || got.Plugins.LastInstall == "" {
		t.Errorf("plugins = %+v", got.Plugins)
	}
	if got.Disk.MediaDirBytes != 67_890_123 || got.Disk.ThemeDirBytes != 12_345 {
		t.Errorf("disk = %+v", got.Disk)
	}
}

// TestAggregate_NilSourcesMarkUnknown asserts that a Sources with every
// slot empty produces a report where each section is flagged with the
// "source not configured" error and no panic occurs. This is the
// developer-laptop scenario: Redis isn't running, the report still
// renders for the configured axes.
func TestAggregate_NilSourcesMarkUnknown(t *testing.T) {
	t.Parallel()

	h := NewHandler(Sources{}, HandlerOptions{Clock: fixedClock(time.Unix(0, 0))})

	got := h.Aggregate(context.Background())

	for name, errStr := range map[string]string{
		"database":   got.Database.Error,
		"redis":      got.Redis.Error,
		"migrations": got.Migrations.Error,
		"theme":      got.Theme.Error,
		"plugins":    got.Plugins.Error,
		"disk":       got.Disk.Error,
	} {
		if !strings.Contains(errStr, "not configured") {
			t.Errorf("%s.Error = %q, want substring %q", name, errStr, "not configured")
		}
	}
	if got.Queues == nil {
		t.Error("queues should be a non-nil slice for JSON consistency")
	}
	if len(got.Queues) != 0 {
		t.Errorf("queues len = %d, want 0", len(got.Queues))
	}
}

// TestServeHTTP_OKWritesJSONReport asserts the HTTP wrapper writes a
// 200 with application/json and a body parseable into StatusReport.
// The Mount-time policy gate is exercised separately in TestMount_Auth.
func TestServeHTTP_OKWritesJSONReport(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubSources(), HandlerOptions{
		Clock: fixedClock(time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var r StatusReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}
	if r.Version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", r.Version)
	}
	if r.Database.MaxConns != 25 {
		t.Errorf("database.max_conns = %d, want 25", r.Database.MaxConns)
	}
}

// TestServeHTTP_MethodNotAllowed asserts non-GET requests get 405 with
// an Allow header pointing at GET. The route is read-only.
func TestServeHTTP_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubSources(), HandlerOptions{Clock: fixedClock(time.Unix(0, 0))})

	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		req := httptest.NewRequest(m, "/api/v1/admin/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status = %d, want 405", m, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("method %s: Allow = %q, want GET", m, got)
		}
	}
}

// TestMount_Auth covers the capability gate that Mount installs around
// the handler:
//
//   - no principal     -> 401 unauthorized.
//   - subscriber       -> 403 (lacks system_read).
//   - admin            -> 200 (system_read is in the admin role bundle).
//   - super_admin      -> 200 (super_admin inherits admin's caps).
func TestMount_Auth(t *testing.T) {
	t.Parallel()

	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	h := NewHandler(stubSources(), HandlerOptions{Clock: fixedClock(time.Unix(0, 0))})

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin", pol, h); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	cases := []struct {
		name      string
		principal *policy.Principal // nil = no principal
		wantCode  int
	}{
		{"no principal", nil, http.StatusUnauthorized},
		{"subscriber", &policy.Principal{UserID: "user:1", Roles: []policy.Role{policy.RoleSubscriber}}, http.StatusForbidden},
		{"editor", &policy.Principal{UserID: "user:2", Roles: []policy.Role{policy.RoleEditor}}, http.StatusForbidden},
		{"admin", &policy.Principal{UserID: "user:3", Roles: []policy.Role{policy.RoleAdmin}}, http.StatusOK},
		{"super_admin", &policy.Principal{UserID: "user:4", Roles: []policy.Role{policy.RoleSuperAdmin}}, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
			if tc.principal != nil {
				req = req.WithContext(policy.WithPrincipal(req.Context(), *tc.principal))
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body=%q)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// TestMount_RequiresPolicyAndHandler asserts the constructor validates
// its inputs; passing nil is a programming error and Mount surfaces it
// rather than waiting until the first request blows up with a panic.
func TestMount_RequiresPolicyAndHandler(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/admin", nil, &Handler{}); err == nil {
		t.Error("Mount with nil policy: want error, got nil")
	}
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	if err := Mount(mux, "/api/v1/admin", pol, nil); err == nil {
		t.Error("Mount with nil handler: want error, got nil")
	}
}

// TestAggregate_SourceErrorIsolated asserts a single sick axis does not
// poison the rest of the report. We replace just the DB stub with one
// that returns an Error; every other section keeps its values.
func TestAggregate_SourceErrorIsolated(t *testing.T) {
	t.Parallel()

	src := stubSources()
	src.DB = stubDBValue{snap: DatabaseStatus{Error: "ping: connection refused"}}

	h := NewHandler(src, HandlerOptions{Clock: fixedClock(time.Unix(0, 0))})
	got := h.Aggregate(context.Background())

	if !strings.Contains(got.Database.Error, "ping: connection refused") {
		t.Errorf("database.Error = %q, want substring %q", got.Database.Error, "ping: connection refused")
	}
	if !got.Redis.OK {
		t.Errorf("redis.OK = false, expected unaffected by db failure: %+v", got.Redis)
	}
	if got.Migrations.CurrentVersion != 42 {
		t.Errorf("migrations.current = %d, want 42 (db failure must not poison other axes)", got.Migrations.CurrentVersion)
	}
}

// TestAggregate_NilClockDefaultsToTimeNow asserts the constructor's
// zero-value handling: a HandlerOptions with no Clock falls back to
// time.Now and yields a non-empty Generated field.
func TestAggregate_NilClockDefaultsToTimeNow(t *testing.T) {
	t.Parallel()

	h := NewHandler(stubSources(), HandlerOptions{})
	got := h.Aggregate(context.Background())
	if got.Generated == "" {
		t.Error("Generated is empty, want non-empty RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, got.Generated); err != nil {
		t.Errorf("Generated = %q is not RFC3339: %v", got.Generated, err)
	}
}
