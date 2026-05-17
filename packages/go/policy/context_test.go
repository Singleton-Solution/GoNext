package policy

import (
	"context"
	"testing"
)

// TestContext_RoundTrip is the basic "stash + read" check: a Principal
// placed on a context with WithPrincipal is recovered intact via
// FromContext.
func TestContext_RoundTrip(t *testing.T) {
	want := Principal{
		UserID: "user:42",
		Roles:  []Role{RoleEditor, RoleAuthor},
	}

	ctx := WithPrincipal(context.Background(), want)
	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false after WithPrincipal")
	}
	if got.UserID != want.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, want.UserID)
	}
	if len(got.Roles) != len(want.Roles) {
		t.Fatalf("len(Roles) = %d, want %d", len(got.Roles), len(want.Roles))
	}
	for i := range want.Roles {
		if got.Roles[i] != want.Roles[i] {
			t.Errorf("Roles[%d] = %q, want %q", i, got.Roles[i], want.Roles[i])
		}
	}
}

// TestContext_AbsentReturnsZero asserts that a context without a stashed
// Principal returns the zero Principal and ok=false. Callers (notably
// the Require middleware) treat this as "unauthenticated".
func TestContext_AbsentReturnsZero(t *testing.T) {
	p, ok := FromContext(context.Background())
	if ok {
		t.Errorf("FromContext on bare context returned ok=true (%+v)", p)
	}
	if p.UserID != "" || len(p.Roles) != 0 {
		t.Errorf("zero Principal expected, got %+v", p)
	}
}

// TestContext_NilSafe ensures FromContext(nil) does not panic. A defensive
// guard — handlers that wire up the context chain incorrectly should get
// a clean "no principal" result rather than a nil-deref.
func TestContext_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FromContext(nil) panicked: %v", r)
		}
	}()
	if _, ok := FromContext(nil); ok {
		t.Error("FromContext(nil) should return ok=false")
	}
}

// TestContext_ChildOverrides asserts that nesting WithPrincipal honors
// the inner value — needed for hypothetical impersonation flows
// (docs/06-auth-permissions.md §15) that swap the principal mid-chain.
func TestContext_ChildOverrides(t *testing.T) {
	outer := Principal{UserID: "user:1", Roles: []Role{RoleSubscriber}}
	inner := Principal{UserID: "user:2", Roles: []Role{RoleAdmin}}

	ctx := WithPrincipal(WithPrincipal(context.Background(), outer), inner)
	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("ok=false")
	}
	if got.UserID != inner.UserID {
		t.Errorf("nested override failed: got UserID %q, want %q", got.UserID, inner.UserID)
	}
}
