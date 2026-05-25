package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// chainKey returns a deterministic 32-byte test key. We don't import
// crypto/rand into a unit test; production keys live in env vars
// rather than hardcoded values.
func chainKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// newChainedEmitter wires an Emitter to a MemoryStore with the chain
// enabled, using the store's MostRecent as the PrevFetcher.
func newChainedEmitter(t *testing.T) (*Emitter, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	emitter := NewEmitter(store).WithChain(&ChainConfig{
		Key:         chainKey(),
		PrevFetcher: func() (Event, error) { return store.MostRecent() },
	})
	return emitter, store
}

func TestChain_EmitPopulatesPrevHash(t *testing.T) {
	t.Parallel()
	emitter, store := newChainedEmitter(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := emitter.Emit(ctx, "test.event"); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}

	events, err := store.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events)=%d want 3", len(events))
	}

	// List returns most-recent first. The OLDEST row (events[2]) is
	// the chain root and should have a nil PrevHash. The newer rows
	// should each have a non-empty PrevHash.
	if len(events[2].PrevHash) != 0 {
		t.Errorf("root row has prev_hash=%x; want nil", events[2].PrevHash)
	}
	if len(events[1].PrevHash) == 0 {
		t.Errorf("mid row has nil prev_hash; want non-empty")
	}
	if len(events[0].PrevHash) == 0 {
		t.Errorf("newest row has nil prev_hash; want non-empty")
	}
}

func TestChain_VerifyHappyPath(t *testing.T) {
	t.Parallel()
	emitter, store := newChainedEmitter(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := emitter.Emit(ctx, "test.event"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	if err := VerifyChain(ctx, store, chainKey(), "", ""); err != nil {
		t.Errorf("VerifyChain: %v", err)
	}
}

func TestChain_VerifyDetectsTampering(t *testing.T) {
	t.Parallel()
	emitter, store := newChainedEmitter(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := emitter.Emit(ctx, "test.event"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	// Tamper with the middle row's content (which is the
	// canonical-bytes input for the newest row's prev_hash). The
	// verifier should detect that the newest row's prev_hash no
	// longer matches the tampered predecessor.
	store.mu.Lock()
	store.events[1].EventType = "tampered.event"
	store.mu.Unlock()

	err := VerifyChain(ctx, store, chainKey(), "", "")
	if err == nil {
		t.Fatalf("expected chain-broken error after tampering")
	}
	if !errors.Is(err, ErrChainBroken) {
		t.Errorf("err=%v, want ErrChainBroken", err)
	}
}

func TestChain_WrongKeyFailsVerify(t *testing.T) {
	t.Parallel()
	emitter, store := newChainedEmitter(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := emitter.Emit(ctx, "test.event"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = 0xff
	}
	err := VerifyChain(ctx, store, wrongKey, "", "")
	if err == nil || !errors.Is(err, ErrChainBroken) {
		t.Errorf("want ErrChainBroken, got %v", err)
	}
}

func TestCanonicalBytes_Deterministic(t *testing.T) {
	t.Parallel()
	e := Event{
		EventType:   "test",
		ActorUserID: "u",
		Time:        time.Date(2025, 5, 25, 12, 0, 0, 0, time.UTC),
		Metadata:    map[string]any{"b": 2, "a": 1, "c": "x"},
	}
	b1 := CanonicalBytes(e)
	b2 := CanonicalBytes(e)
	if string(b1) != string(b2) {
		t.Errorf("CanonicalBytes is not deterministic")
	}
	// Sanity check that metadata keys are sorted.
	if !containsBytes(b1, "a=1;b=2;c=x;") {
		t.Errorf("metadata not sorted: %q", b1)
	}
}

func TestHMACKeyFromEnv_RejectsShortKey(t *testing.T) {
	t.Setenv(EnvAuditHMACKey, "too-short")
	_, err := HMACKeyFromEnv()
	if err == nil || !errors.Is(err, ErrInvalidHMACKey) {
		t.Errorf("want ErrInvalidHMACKey, got %v", err)
	}
}

func TestHMACKeyFromEnv_AcceptsHex(t *testing.T) {
	// 32 zero bytes hex-encoded.
	t.Setenv(EnvAuditHMACKey, "0000000000000000000000000000000000000000000000000000000000000000")
	key, err := HMACKeyFromEnv()
	if err != nil {
		t.Fatalf("HMACKeyFromEnv: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key len=%d want 32", len(key))
	}
}

func TestHMACKeyFromEnv_AcceptsRawAscii(t *testing.T) {
	// 40-byte passphrase, not hex.
	t.Setenv(EnvAuditHMACKey, "this-is-a-sufficiently-long-raw-passphrase-123")
	key, err := HMACKeyFromEnv()
	if err != nil {
		t.Fatalf("HMACKeyFromEnv: %v", err)
	}
	if len(key) < 32 {
		t.Errorf("key too short: %d", len(key))
	}
}

func containsBytes(haystack []byte, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
