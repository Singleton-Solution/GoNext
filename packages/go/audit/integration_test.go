package audit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// schemaSQL is the slice of migration 000029 the audit store needs:
// the audit_log table plus the retention-sweep index. We mirror the
// migration here (rather than running the full migrator) so the
// integration test doesn't depend on the binary that owns migrate up;
// keep this in sync with migrations/000029_audit_log.up.sql.
//
// Two divergences from the migration file are intentional:
//
//   1. We omit the actor / event / target indexes — the tests below
//      exercise the contract, not the planner, and the retention
//      sweep is the only path that benefits from a partial index.
//   2. We drop the COMMENT statement — it adds nothing to a one-off
//      test container and would only run once per container boot.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS audit_log (
    id              BIGSERIAL PRIMARY KEY,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_user_id   UUID,
    actor_kind      TEXT NOT NULL
                    CHECK (actor_kind IN ('user', 'plugin', 'system')),
    actor_label     TEXT,
    event           TEXT NOT NULL
                    CHECK (length(event) > 0 AND length(event) <= 128),
    target_kind     TEXT,
    target_id       TEXT,
    ip              INET,
    user_agent      TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}'::JSONB,
    severity        TEXT NOT NULL DEFAULT 'info'
                    CHECK (severity IN ('info', 'warning', 'critical')),
    prev_hash       BYTEA
);
CREATE INDEX IF NOT EXISTS audit_occurred_sweep_idx
    ON audit_log (occurred_at)
    WHERE severity = 'info';
`

// setupPostgres boots a Postgres container, applies just enough of
// 000029_audit_log.up.sql to back the store, and returns a pool ready
// for use. Tests are skipped on no-Docker hosts — the unit tests in
// postgres_test.go carry the SQL contract; this file is the
// belt-and-suspenders against the real query planner.
func setupPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestIntegration_PostgresStore_EmitThenList writes a couple of events,
// reads them back, and checks the filter columns work end-to-end.
// This is the contract test for the SELECT path against a real
// planner — the unit tests can't catch a planner-rejected query
// shape.
func TestIntegration_PostgresStore_EmitThenList(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store := audit.NewPostgresStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	alice := uuid.Must(uuid.NewV7()).String()
	bob := uuid.Must(uuid.NewV7()).String()

	events := []audit.Event{
		{
			EventType:    "auth.login.success",
			ActorUserID:  alice,
			IP:           "192.0.2.1",
			UserAgent:    "ua-alice",
			ResourceType: "user",
			ResourceID:   alice,
			Metadata:     map[string]any{"method": "password"},
			Severity:     audit.SeverityInfo,
		},
		{
			EventType:    "auth.login.failed",
			ActorUserID:  "",
			IP:           "192.0.2.2",
			UserAgent:    "ua-attacker",
			Metadata:     map[string]any{"reason": "wrong_password"},
			Severity:     audit.SeverityWarning,
		},
		{
			EventType:       "gn-forms.submission.exported",
			ActorPluginSlug: "gn-forms",
			ActorUserID:     bob,
			Severity:        audit.SeverityInfo,
		},
	}
	for _, e := range events {
		if err := store.Emit(ctx, e); err != nil {
			t.Fatalf("Emit(%s): %v", e.EventType, err)
		}
	}

	// Read everything back; expect 3 rows newest-first.
	all, err := store.List(ctx, audit.Filter{})
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(all): got %d rows, want 3", len(all))
	}

	// Filter by event type.
	failures, err := store.List(ctx, audit.Filter{EventType: "auth.login.failed"})
	if err != nil {
		t.Fatalf("List(failed): %v", err)
	}
	if len(failures) != 1 || failures[0].EventType != "auth.login.failed" {
		t.Errorf("List(failed): got %v", failures)
	}
	if failures[0].Metadata["reason"] != "wrong_password" {
		t.Errorf("List(failed): metadata=%v", failures[0].Metadata)
	}
	if failures[0].Severity != audit.SeverityWarning {
		t.Errorf("List(failed): severity=%q want warning", failures[0].Severity)
	}

	// Filter by actor UUID.
	byAlice, err := store.List(ctx, audit.Filter{ActorUserID: alice})
	if err != nil {
		t.Fatalf("List(alice): %v", err)
	}
	if len(byAlice) != 1 || byAlice[0].ActorUserID != alice {
		t.Errorf("List(alice): got %v", byAlice)
	}

	// Filter by plugin slug.
	byPlugin, err := store.List(ctx, audit.Filter{PluginSlug: "gn-forms"})
	if err != nil {
		t.Fatalf("List(plugin): %v", err)
	}
	if len(byPlugin) != 1 || byPlugin[0].ActorPluginSlug != "gn-forms" {
		t.Errorf("List(plugin): got %v", byPlugin)
	}

	// Filter by severity.
	bySev, err := store.List(ctx, audit.Filter{Severity: audit.SeverityWarning})
	if err != nil {
		t.Fatalf("List(severity): %v", err)
	}
	if len(bySev) != 1 {
		t.Errorf("List(severity): got %d want 1", len(bySev))
	}
}

// TestIntegration_PostgresStore_Sweep writes old + new events, runs
// Sweep, and checks that only info-severity rows older than the
// horizon were deleted. Critical/warning rows must survive
// indefinitely per docs/06-auth-permissions.md §13.2.
func TestIntegration_PostgresStore_Sweep(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store := audit.NewPostgresStore(pool)
	store.NowFunc = func() time.Time { return now }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// One old info row (eligible for sweep), one old critical row
	// (must survive), one fresh info row (must survive).
	mustEmit := func(e audit.Event) {
		if err := store.Emit(ctx, e); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	mustEmit(audit.Event{
		EventType: "auth.login.success",
		Time:      now.Add(-100 * 24 * time.Hour),
		Severity:  audit.SeverityInfo,
	})
	mustEmit(audit.Event{
		EventType: "auth.impersonation.start",
		Time:      now.Add(-100 * 24 * time.Hour),
		Severity:  audit.SeverityCritical,
	})
	mustEmit(audit.Event{
		EventType: "auth.login.success",
		Time:      now.Add(-1 * 24 * time.Hour),
		Severity:  audit.SeverityInfo,
	})

	// Keep 90 days → the 100-day-old info row should go; the 100-day-
	// old critical row should stay; the 1-day-old info row should stay.
	deleted, err := store.Sweep(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Sweep: deleted=%d want 1", deleted)
	}

	rows, err := store.List(ctx, audit.Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List after sweep: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("post-sweep row count: got %d want 2", len(rows))
	}
	var sawCritical, sawFreshInfo bool
	for _, r := range rows {
		switch r.Severity {
		case audit.SeverityCritical:
			sawCritical = true
		case audit.SeverityInfo:
			sawFreshInfo = true
		}
	}
	if !sawCritical || !sawFreshInfo {
		t.Errorf("post-sweep: missing rows (critical=%v fresh_info=%v)", sawCritical, sawFreshInfo)
	}
}

// TestIntegration_PostgresStore_ConcurrentWrites fires N writers in
// parallel and asserts every event ends up in the table. The audit
// log is the kind of resource where "we silently dropped one row
// under load" is a real-world disaster (we'd miss the very thing the
// audit trail is supposed to record), so this test is the contract
// against the BIGSERIAL + INSERT path under contention.
func TestIntegration_PostgresStore_ConcurrentWrites(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store := audit.NewPostgresStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		writers     = 16
		perWriter   = 25
		totalEvents = writers * perWriter
	)

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				e := audit.Event{
					EventType: fmt.Sprintf("test.worker.%d", workerID),
					Metadata:  map[string]any{"i": j, "w": workerID},
					Severity:  audit.SeverityInfo,
				}
				if err := store.Emit(ctx, e); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Emit: %v", err)
		}
	}

	// Verify the row count head-on. Use a List with the max limit
	// (1000 > totalEvents=400) so we don't have to paginate.
	rows, err := store.List(ctx, audit.Filter{Limit: 1000})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != totalEvents {
		t.Errorf("row count: got %d want %d (lost an event under contention)", len(rows), totalEvents)
	}
}

// TestIntegration_Sweeper_StartStopRunsFinalSweep checks that the
// background Sweeper drains cleanly and ALSO runs one final Sweep on
// shutdown — the property that makes it safe to register a single
// closer with the shutdown orchestrator and trust the audit log to
// be pruned across a deploy.
func TestIntegration_Sweeper_StartStopRunsFinalSweep(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	store := audit.NewPostgresStore(pool)
	store.NowFunc = func() time.Time { return now }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Plant one ancient info row.
	if err := store.Emit(ctx, audit.Event{
		EventType: "auth.login.success",
		Time:      now.Add(-200 * 24 * time.Hour),
		Severity:  audit.SeverityInfo,
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Sweeper with a long interval (60s) so the background tick can't
	// fire during the test — Stop() must do the cleanup itself.
	sw := audit.NewSweeper(store, 90*24*time.Hour, &audit.SweeperOptions{
		Interval: 60 * time.Second,
	})
	sw.Start()
	if err := sw.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The final-sweep guarantee: the ancient row should be gone even
	// though the ticker never fired.
	rows, err := store.List(ctx, audit.Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("post-Stop row count: got %d want 0 (final sweep did not run)", len(rows))
	}
}
