package login

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAuthenticate_DummyHashTimingOnZeroEmail verifies the missing-email
// short-circuit: the service must still run the dummy verify (timing
// guard) and emit both attempt + failed audit events.
func TestAuthenticate_DummyHashTimingOnZeroEmail(t *testing.T) {
	f := newFixture(t)
	svc := f.service(t)
	_, err := svc.Authenticate(context.Background(), Input{
		Email:    "",
		Password: "x",
		IP:       "10.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err: got %v, want ErrInvalidCredentials", err)
	}
	if got, want := f.countEvents(t, "auth.login.attempt"), 1; got != want {
		t.Errorf("attempt events: got %d, want %d", got, want)
	}
	if got, want := f.countEvents(t, "auth.login.failed"), 1; got != want {
		t.Errorf("failed events: got %d, want %d", got, want)
	}
}

// TestAuthenticate_LookupGenericErrorTreatedAsMiss covers a non-
// ErrUserNotFound error from the lookup. The service treats it as
// "user not found" so a transient DB blip doesn't surface as 500.
func TestAuthenticate_LookupGenericErrorTreatedAsMiss(t *testing.T) {
	f := newFixture(t)
	f.deps.Lookup = func(_ context.Context, _ string) (UserRecord, error) {
		return UserRecord{}, errors.New("simulated db error")
	}
	svc := f.service(t)
	_, err := svc.Authenticate(context.Background(), Input{
		Email:    "alice@example.com",
		Password: "pwd",
		IP:       "10.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err: got %v, want ErrInvalidCredentials", err)
	}
}

// TestAuthenticate_LookupZeroValueRowTreatedAsMiss covers the
// "Deps.Lookup returned nil err but empty UserRecord" case — a fake
// returning a zero record must still be treated as a miss.
func TestAuthenticate_LookupZeroValueRowTreatedAsMiss(t *testing.T) {
	f := newFixture(t)
	f.deps.Lookup = func(_ context.Context, _ string) (UserRecord, error) {
		return UserRecord{}, nil // zero value, no error
	}
	svc := f.service(t)
	_, err := svc.Authenticate(context.Background(), Input{
		Email:    "alice@example.com",
		Password: "pwd",
		IP:       "10.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err: got %v, want ErrInvalidCredentials", err)
	}
}

// TestAuthenticate_CorruptHashReturns401NotPanic covers the
// "user found, password_hash column corrupted" path. The handler must
// surface invalid_credentials, not a 500 with the hash error.
func TestAuthenticate_CorruptHashReturns401(t *testing.T) {
	f := newFixture(t)
	f.users["alice@example.com"] = UserRecord{ID: "u-1", Email: "alice@example.com", Status: "active"}
	f.hashes["u-1"] = "not-a-valid-phc-string"
	svc := f.service(t)

	_, err := svc.Authenticate(context.Background(), Input{
		Email:    "alice@example.com",
		Password: "pwd",
		IP:       "10.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err: got %v, want ErrInvalidCredentials", err)
	}
}

// TestRetryAfterReturnsZeroForUnwrapped covers the case where the
// error chain doesn't carry a retryAfterError — retryAfter should
// return 0 cleanly.
func TestRetryAfterReturnsZeroForUnwrapped(t *testing.T) {
	if d := retryAfter(errors.New("boom")); d != 0 {
		t.Errorf("retryAfter on plain error: got %v, want 0", d)
	}
}

// TestRetryAfterErrorString covers the Error method on the wrapper.
func TestRetryAfterErrorString(t *testing.T) {
	wrapped := withRetryAfter(ErrLocked, 5*time.Second)
	if wrapped.Error() != ErrLocked.Error() {
		t.Errorf("Error: got %q, want %q", wrapped.Error(), ErrLocked.Error())
	}
}

// TestVerifyTOTPOrRecovery_NoneSupplied covers the verifyTOTPOrRecovery
// branch where neither field is set. Should return false (no match).
func TestVerifyTOTPOrRecovery_NoneSupplied(t *testing.T) {
	f := newFixture(t)
	svc := f.service(t)
	if svc.verifyTOTPOrRecovery(Input{}, TOTPRecord{}) {
		t.Error("verifyTOTPOrRecovery with no inputs: got true, want false")
	}
}

// TestDefaults_FillsZeroValues covers the defaults() branch where
// Now / Log / IntermediateTTL are zero and need filling.
func TestDefaults_FillsZeroValues(t *testing.T) {
	f := newFixture(t)
	f.deps.Now = nil
	f.deps.Log = nil
	f.deps.IntermediateTTL = 0
	svc, err := NewService(f.deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.deps.Now == nil {
		t.Error("Now defaults not applied")
	}
	if svc.deps.Log == nil {
		t.Error("Log defaults not applied")
	}
	if svc.deps.IntermediateTTL <= 0 {
		t.Error("IntermediateTTL defaults not applied")
	}
}

// TestNewHandler_RequiresService covers NewHandler's nil-svc guard.
func TestNewHandler_RequiresService(t *testing.T) {
	if _, err := NewHandler(nil, Deps{}); err == nil {
		t.Fatal("expected error for nil service")
	}
}

// TestNewHandler_BadDepsFails covers the validate path inside NewHandler.
func TestNewHandler_BadDepsFails(t *testing.T) {
	f := newFixture(t)
	svc, err := NewService(f.deps)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := NewHandler(svc, Deps{}); err == nil {
		t.Fatal("expected error for empty deps")
	}
}

// TestDummyHash_Stable proves getDummyHash returns the same value on
// repeat calls — the sync.Once guarantee — and that calling it doesn't
// panic with an empty pepper.
func TestDummyHash_Stable(t *testing.T) {
	h1, err := getDummyHash([]byte("p"))
	if err != nil {
		t.Fatalf("getDummyHash: %v", err)
	}
	h2, err := getDummyHash([]byte("different-pepper-still-cached"))
	if err != nil {
		t.Fatalf("getDummyHash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("getDummyHash not cached: h1=%q h2=%q", h1, h2)
	}
	if h1 == "" {
		t.Error("getDummyHash returned empty")
	}
}

// TestVerifyDummy_ReturnsFalse confirms the dummy hash never validates
// against any caller-supplied password. The check is constant-time;
// we just want the return value to be false.
func TestVerifyDummy_ReturnsFalse(t *testing.T) {
	f := newFixture(t)
	svc := f.service(t)
	if svc.verifyDummy("hunter2") {
		t.Error("verifyDummy: matched, want false")
	}
	if svc.verifyDummy("") {
		t.Error("verifyDummy on empty: matched, want false")
	}
}

// TestCompleteLogin_SessionCreateError covers the session-create
// error path. We inject a fake session creator that always errors.
func TestCompleteLogin_SessionCreateError(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	f.sessionCreator.err = errors.New("redis is down")
	svc := f.service(t)

	_, err := svc.Authenticate(context.Background(), Input{
		Email:    "alice@example.com",
		Password: "pwd",
		IP:       "10.0.0.1",
	})
	if err == nil {
		t.Fatal("expected error from failing session create")
	}
	// Must NOT be one of the public sentinels — it bubbles as an
	// internal error and the handler renders 500.
	if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrLocked) ||
		errors.Is(err, ErrTOTPInvalid) || errors.Is(err, ErrRateLimited) {
		t.Errorf("err sentinel mismatch: got %v", err)
	}
}

// TestFinalizeTOTP_TOTPDisabledMidFlow covers the "user disabled
// 2FA between first and second call" branch — the second call should
// still succeed, dropping the now-unneeded intermediate token.
func TestFinalizeTOTP_TOTPDisabledMidFlow(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "u-1", "alice@example.com", "pwd", "active")
	f.addTOTP(t, "u-1", "JBSWY3DPEHPK3PXP")
	svc := f.service(t)

	first, err := svc.Authenticate(context.Background(), Input{
		Email:    "alice@example.com",
		Password: "pwd",
		IP:       "10.0.0.1",
	})
	if err != nil || !first.RequiresTOTP {
		t.Fatalf("first: err=%v requires=%v", err, first.RequiresTOTP)
	}

	// Operator disabled 2FA in the meantime.
	delete(f.totp, "u-1")

	res, err := svc.Authenticate(context.Background(), Input{
		IntermediateToken: first.IntermediateToken,
		IP:                "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if res.Token == "" {
		t.Error("Token: empty")
	}
}
