package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestManager_AbsoluteTTLEnforced is the regression test for issue #1.
//
// Pre-fix: Create's ttl argument was documented as the absolute lifetime
// but never enforced — sessions could live indefinitely under continuous
// use because Get refreshed the key with idleTTL alone.
//
// This test creates a session with ttl=2s, idleTTL=1s and polls Get
// every 200 ms (well under the idle window). Without the fix every Get
// keeps bumping the idle TTL back to 1s and the session stays live
// indefinitely; with the fix Get returns ErrNotFound once the absolute
// deadline passes.
func TestManager_AbsoluteTTLEnforced(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	const ttl = 2 * time.Second
	const idleTTL = 1 * time.Second

	tok, err := m.Create(ctx, "user-abs", nil, ttl, idleTTL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var sawExpired bool
	for time.Now().Before(deadline) {
		_, gErr := m.Get(ctx, tok, idleTTL)
		if errors.Is(gErr, ErrNotFound) {
			sawExpired = true
			break
		}
		if gErr != nil {
			t.Fatalf("Get during liveness window: %v", gErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !sawExpired {
		t.Fatalf("session survived past its absolute TTL — Get never returned ErrNotFound within %v", 3*time.Second)
	}
}

// TestManager_UserSessionsTTLPreservedOnGet is the regression test for
// issue #2.
//
// Pre-fix: Get's refresh ran pipe.Expire(userSessionsKey, idleTTL),
// which OVERRODE the existing (longer) absolute TTL with the
// (typically much shorter) idle window. This silently broke
// DeleteAllForUser and List for long-running sessions.
//
// This test creates a session with ttl=24h, idleTTL=2s; calls Get
// once; and asserts the user_sessions:{uid} set's TTL is still
// near 24h (i.e. ≥ 1 hour, never the 2-second idle window).
func TestManager_UserSessionsTTLPreservedOnGet(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	const ttl = 24 * time.Hour
	const idleTTL = 2 * time.Second

	tok, err := m.Create(ctx, "user-set-ttl", nil, ttl, idleTTL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sleep briefly so we'd notice if the TTL was overwritten back to
	// idleTTL (it'd be ~2s remaining).
	time.Sleep(500 * time.Millisecond)

	if _, err := m.Get(ctx, tok, idleTTL); err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := m.rdb.TTL(ctx, userSessionsKey("user-set-ttl")).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if got < time.Hour {
		t.Fatalf("user_sessions TTL collapsed: got %v, expected near %v (idleTTL=%v)",
			got, ttl, idleTTL)
	}
}

// TestManager_GetDeleteRaceNoResurrection is the regression test for
// issue #3.
//
// Pre-fix: Get's GET-then-SET (the GET runs outside the TxPipeline)
// allowed a Delete arriving between the read and the write to be
// silently undone — the SET writes the blob back even though Delete
// already SREM'd from user_sessions:{uid}, leaving an orphaned
// session blob undetectable by DeleteAllForUser.
//
// The test repeatedly races Get against Delete and asserts no
// orphans accumulate (every live session:* key must have a
// corresponding entry in user_sessions:*).
func TestManager_GetDeleteRaceNoResurrection(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	const iterations = 200
	const userID = "user-race"
	const idleTTL = time.Hour

	var orphans int
	for i := 0; i < iterations; i++ {
		tok, err := m.Create(ctx, userID, nil, time.Hour, idleTTL)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Best-effort Get — may succeed or hit ErrNotFound; we don't
			// care about its return value, only the side effects.
			_, _ = m.Get(ctx, tok, idleTTL)
		}()
		go func() {
			defer wg.Done()
			_ = m.Delete(ctx, tok)
		}()
		wg.Wait()

		// After both finish, the session blob must NOT exist unless
		// it's still tracked by the user_sessions set. An orphan is the
		// resurrection bug: blob exists, set member doesn't.
		blobExists, err := m.rdb.Exists(ctx, sessionKey(tok)).Result()
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if blobExists == 0 {
			continue
		}
		isMember, err := m.rdb.SIsMember(ctx, userSessionsKey(userID), tok).Result()
		if err != nil {
			t.Fatalf("SIsMember: %v", err)
		}
		if !isMember {
			orphans++
			// Tidy up so the next iteration starts clean.
			_ = m.rdb.Del(ctx, sessionKey(tok)).Err()
		}
		// Clean up the (legitimately) live session too.
		_ = m.Delete(ctx, tok)
	}

	if orphans > 0 {
		t.Fatalf("session-resurrection TOCTOU race produced %d orphaned blobs across %d iterations",
			orphans, iterations)
	}
}

// TestManager_Regenerate covers the new token-rotation primitive used
// after privilege escalation (post-login, post-2FA, after role change)
// to defeat session fixation.
func TestManager_Regenerate(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	oldTok, err := m.Create(ctx, "user-regen", map[string]any{"role": "viewer"},
		time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTok, err := m.Regenerate(ctx, oldTok, 30*time.Minute)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if newTok == oldTok {
		t.Fatal("Regenerate must return a different token")
	}
	if !validToken(newTok) {
		t.Fatalf("Regenerate returned malformed token: %q", newTok)
	}

	// Old token is dead.
	if _, err := m.Get(ctx, oldTok, 30*time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token: err = %v, want ErrNotFound", err)
	}
	// New token works and carries the same data.
	sess, err := m.Get(ctx, newTok, 30*time.Minute)
	if err != nil {
		t.Fatalf("Get new: %v", err)
	}
	if sess.UserID != "user-regen" {
		t.Errorf("UserID: got %q want user-regen", sess.UserID)
	}
	if sess.Data["role"] != "viewer" {
		t.Errorf("Data[role]: got %v want viewer", sess.Data["role"])
	}

	// The user_sessions set must point at the new token, not the old.
	mems, err := m.rdb.SMembers(ctx, userSessionsKey("user-regen")).Result()
	if err != nil {
		t.Fatalf("SMembers: %v", err)
	}
	var sawNew, sawOld bool
	for _, t2 := range mems {
		if t2 == newTok {
			sawNew = true
		}
		if t2 == oldTok {
			sawOld = true
		}
	}
	if !sawNew {
		t.Errorf("new token missing from user_sessions set: %v", mems)
	}
	if sawOld {
		t.Errorf("old token lingering in user_sessions set: %v", mems)
	}
}

// TestManager_MaxDataSizeRejected guards the cheap insurance ceiling
// against runaway payloads.
func TestManager_MaxDataSizeRejected(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	m.SetMaxDataSize(256) // small for testability

	huge := strings.Repeat("a", 1024)
	_, err := m.Create(ctx, "user-big", map[string]any{"k": huge},
		time.Hour, 30*time.Minute)
	if err == nil {
		t.Fatal("Create with oversize Data should fail")
	}
	if !errors.Is(err, ErrDataTooLarge) {
		t.Fatalf("err = %v, want ErrDataTooLarge", err)
	}
}
