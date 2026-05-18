package status

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// DefaultSourceTimeout is the per-source budget for one Snapshot call.
// Sized for a healthy stack on the same pod network: a Postgres ping is
// sub-millisecond, an Inspector queue scan is a handful of Redis hops,
// the disk walks complete on the order of a hundred ms for a small
// site. 2s is generous; it bounds the worst case (transient Redis
// blip) without making the page wait the full request timeout to
// surface that one axis is sick.
const DefaultSourceTimeout = 2 * time.Second

// Handler is the HTTP entry point for GET /api/v1/admin/status. It is
// constructed once at boot and reused across requests; the Sources
// values are themselves expected to be concurrency-safe (every
// production adapter wraps a long-lived *pgxpool.Pool / *redis.Client
// / *asynq.Inspector / etc., all of which are thread-safe).
//
// The handler is mounted behind policy.Require(p, policy.CapSystemRead)
// — see Mount — so an unauthenticated caller gets 401 and an
// authenticated caller without system_read gets 403. The handler
// itself does not re-check authorization.
type Handler struct {
	sources Sources
	clock   Clock
	timeout time.Duration
	logger  *slog.Logger
}

// HandlerOptions configures the optional knobs. Zero values pick the
// production defaults: time.Now, DefaultSourceTimeout, slog.Default.
type HandlerOptions struct {
	Clock         Clock
	SourceTimeout time.Duration
	Logger        *slog.Logger
}

// NewHandler returns a Handler that aggregates the report from the
// given sources. Nil sources are tolerated — each section records
// "source not configured" as its Error and the UI renders the axis as
// unknown rather than red. This lets a developer running `make run`
// without (say) Redis still see the rest of the page.
func NewHandler(sources Sources, opts HandlerOptions) *Handler {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	timeout := opts.SourceTimeout
	if timeout <= 0 {
		timeout = DefaultSourceTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		sources: sources,
		clock:   clock,
		timeout: timeout,
		logger:  logger.With(slog.String("component", "admin.status")),
	}
}

// ServeHTTP aggregates every configured source under a per-source
// timeout and writes the report as JSON. The status code is always
// 200 — a sick subsystem is reported in the body, not as an HTTP
// failure, because failing the whole page would erase the operator's
// view of the healthy axes too.
//
// 405 is returned for non-GET requests; the route is read-only.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	report := h.Aggregate(r.Context())
	writeJSON(w, http.StatusOK, report)
}

// Aggregate runs every configured source in parallel under a per-source
// timeout and returns the assembled report. Exposed for tests and for
// in-process consumers (a future "system health" exporter could call
// Aggregate directly without going through HTTP).
//
// Implementation note: each source runs in its own goroutine — slow
// axes don't block the fast ones — but the goroutine count is bounded
// by the number of source slots (eight), so there is no risk of
// runaway fanout.
func (h *Handler) Aggregate(ctx context.Context) StatusReport {
	report := StatusReport{
		Generated: h.clock().UTC().Format(time.RFC3339),
		Queues:    []QueueStatus{}, // never nil — JSON consistency
	}

	// BuildInfo is synchronous and trivially cheap (a struct copy); no
	// reason to spend a goroutine on it.
	if h.sources.BuildInfo != nil {
		bi := h.sources.BuildInfo.Get()
		report.Version = bi.Version
		report.Commit = bi.Commit
		report.BuildDate = bi.Date
		report.GoVersion = bi.GoVersion
		report.OS = bi.OS
		report.Arch = bi.Arch
	}

	var wg sync.WaitGroup
	wg.Add(7)

	go func() {
		defer wg.Done()
		report.Database = h.snapshotDatabase(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Redis = h.snapshotRedis(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Migrations = h.snapshotMigrations(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Queues = h.snapshotQueues(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Theme = h.snapshotTheme(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Plugins = h.snapshotPlugins(ctx)
	}()
	go func() {
		defer wg.Done()
		report.Disk = h.snapshotDisk(ctx)
	}()

	wg.Wait()
	return report
}

func (h *Handler) snapshotDatabase(ctx context.Context) DatabaseStatus {
	if h.sources.DB == nil {
		return DatabaseStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.DB.Snapshot(sctx)
}

func (h *Handler) snapshotRedis(ctx context.Context) RedisStatus {
	if h.sources.Redis == nil {
		return RedisStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.Redis.Snapshot(sctx)
}

func (h *Handler) snapshotMigrations(ctx context.Context) MigrationsStatus {
	if h.sources.Migrations == nil {
		return MigrationsStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.Migrations.Snapshot(sctx)
}

func (h *Handler) snapshotQueues(ctx context.Context) []QueueStatus {
	if h.sources.Queues == nil {
		return []QueueStatus{}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	q := h.sources.Queues.Snapshot(sctx)
	if q == nil {
		return []QueueStatus{}
	}
	return q
}

func (h *Handler) snapshotTheme(ctx context.Context) ThemeStatus {
	if h.sources.Theme == nil {
		return ThemeStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.Theme.Snapshot(sctx)
}

func (h *Handler) snapshotPlugins(ctx context.Context) PluginsStatus {
	if h.sources.Plugins == nil {
		return PluginsStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.Plugins.Snapshot(sctx)
}

func (h *Handler) snapshotDisk(ctx context.Context) DiskStatus {
	if h.sources.Disk == nil {
		return DiskStatus{Error: errSourceNotConfigured.Error()}
	}
	sctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	return h.sources.Disk.Snapshot(sctx)
}

// errSourceNotConfigured is the sentinel surfaced in a section's Error
// when its Source slot was nil at construction. The UI renders this as
// "—" (unknown) rather than red — a nil source is a wiring choice on
// the operator's part, not a runtime failure.
var errSourceNotConfigured = errors.New("source not configured")

// Mount wires the status handler onto mux at base + "/status" behind a
// policy.Require check for system_read. The base is taken verbatim so
// callers control the canonical path (typically "/api/v1/admin"); the
// suffix is fixed because the admin UI's /status route depends on it.
//
// Returns an error if pol is nil; callers must thread the same Policy
// the rest of the API uses so capability resolution is consistent.
func Mount(mux *http.ServeMux, base string, pol policy.Policy, h *Handler) error {
	if pol == nil {
		return errors.New("admin/status.Mount: policy is required")
	}
	if h == nil {
		return errors.New("admin/status.Mount: handler is required")
	}
	mux.Handle("GET "+base+"/status",
		policy.Require(pol, policy.CapSystemRead)(h))
	return nil
}

// writeJSON is the package's local response writer. Identical shape to
// the rest/router/json.go helper but kept local so the admin/status
// package doesn't pull in a transitive dependency on the REST router
// just to serialize one map.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
