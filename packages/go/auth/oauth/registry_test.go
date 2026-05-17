package oauth

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeProvider is a minimal Provider used only by these tests. We do not
// reuse GenericOIDCProvider because that would couple registry tests to
// the network-fetching constructor; the registry should work with any
// implementation, fake or real.
type fakeProvider struct {
	id, name string
}

func (f *fakeProvider) ID() string   { return f.id }
func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) AuthURL(state, redirectURI string) string {
	return "https://idp.example/authorize?state=" + state + "&redirect_uri=" + redirectURI
}
func (f *fakeProvider) Exchange(_ context.Context, _, _ string) (*Token, error) {
	return &Token{AccessToken: "fake"}, nil
}
func (f *fakeProvider) UserInfo(_ context.Context, _ *Token) (*UserInfo, error) {
	return &UserInfo{Sub: "fake-sub"}, nil
}

func TestRegistry_RegisterGetList(t *testing.T) {
	r := NewRegistry()

	g := &fakeProvider{id: "google", name: "Google"}
	gh := &fakeProvider{id: "github", name: "GitHub"}
	okta := &fakeProvider{id: "okta", name: "Okta"}

	for _, p := range []Provider{gh, g, okta} { // intentionally not sorted
		if err := r.Register(p); err != nil {
			t.Fatalf("Register(%q): %v", p.ID(), err)
		}
	}

	got, err := r.Get("github")
	if err != nil {
		t.Fatalf("Get(github): %v", err)
	}
	if got.Name() != "GitHub" {
		t.Errorf("Get(github).Name() = %q, want %q", got.Name(), "GitHub")
	}

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List() len = %d, want 3", len(list))
	}
	wantOrder := []string{"github", "google", "okta"}
	for i, p := range list {
		if p.ID() != wantOrder[i] {
			t.Errorf("List()[%d].ID() = %q, want %q (full = %v)", i, p.ID(), wantOrder[i], wantOrder)
		}
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nope")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Get(nope): err = %v, want errors.Is ErrProviderNotFound", err)
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{id: "google", name: "Google"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(&fakeProvider{id: "google", name: "Google Workspace"})
	if !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("second Register: err = %v, want errors.Is ErrDuplicateProvider", err)
	}
	// Confirm the original survives (no silent overwrite).
	p, _ := r.Get("google")
	if p.Name() != "Google" {
		t.Errorf("Get(google).Name() = %q, want %q (no overwrite)", p.Name(), "Google")
	}
}

func TestRegistry_ZeroValueUsable(t *testing.T) {
	// The zero-value Registry should be usable (map allocated lazily).
	var r Registry
	if err := r.Register(&fakeProvider{id: "google", name: "Google"}); err != nil {
		t.Fatalf("Register on zero-value: %v", err)
	}
	if _, err := r.Get("google"); err != nil {
		t.Fatalf("Get on zero-value-registered: %v", err)
	}
}

func TestRegistry_NilProviderRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); !errors.Is(err, ErrInvalidProviderID) {
		t.Fatalf("Register(nil): err = %v, want errors.Is ErrInvalidProviderID", err)
	}
}

func TestRegistry_ValidateID(t *testing.T) {
	cases := []struct {
		id      string
		wantErr bool
	}{
		{"google", false},
		{"github", false},
		{"generic-oidc", false},
		{"okta_corp", false},
		{"o", false},  // single char ok
		{"a1", false}, // alnum ok
		{"", true},
		{"Google", true},        // uppercase
		{"google.com", true},    // dot
		{"google idp", true},    // space
		{"google!", true},       // bang
		{"-leading-dash", true}, // bad leading char
		{"é", true},             // non-ASCII
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			r := NewRegistry()
			err := r.Register(&fakeProvider{id: c.id, name: "X"})
			gotErr := err != nil
			if gotErr != c.wantErr {
				t.Fatalf("Register(id=%q): err = %v, wantErr = %v", c.id, err, c.wantErr)
			}
			if c.wantErr && !errors.Is(err, ErrInvalidProviderID) {
				t.Errorf("err not ErrInvalidProviderID: %v", err)
			}
		})
	}
}

func TestRegistry_ListReturnsCopy(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&fakeProvider{id: "a", name: "A"}); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	// Mutate the returned slice — should not affect the registry.
	list[0] = &fakeProvider{id: "b", name: "B"}
	again := r.List()
	if again[0].ID() != "a" {
		t.Errorf("List() returned shared slice; got id = %q, want %q", again[0].ID(), "a")
	}
}

func TestRegistry_ConcurrentRegisterGet(t *testing.T) {
	// Race detector will yell if Register and Get share state without
	// synchronisation. We run with -race in CI; this test mostly exists
	// so the race detector has something to chew on.
	r := NewRegistry()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "p"
			if i%2 == 0 {
				id = "q"
			}
			// Ignore duplicate errors — half the goroutines will lose.
			_ = r.Register(&fakeProvider{id: id, name: id})
			_, _ = r.Get(id)
			_ = r.List()
		}(i)
	}
	wg.Wait()

	if _, err := r.Get("p"); err != nil {
		t.Errorf("Get(p) after concurrent ops: %v", err)
	}
	if _, err := r.Get("q"); err != nil {
		t.Errorf("Get(q) after concurrent ops: %v", err)
	}
}
