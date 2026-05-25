package sign

import (
	"errors"
	"strings"
	"testing"
)

func TestParseIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		want    Identity
		wantErr bool
	}{
		{name: "github org", input: "github.com/Singleton-Solution", want: Identity{Provider: "github.com", Value: "Singleton-Solution"}},
		{name: "github org/repo", input: "github.com/Singleton-Solution/seo", want: Identity{Provider: "github.com", Value: "Singleton-Solution/seo"}},
		{name: "gitlab", input: "gitlab.com/group/proj", want: Identity{Provider: "gitlab.com", Value: "group/proj"}},
		{name: "mailto", input: "mailto:dev@example.com", want: Identity{Provider: "mailto", Value: "dev@example.com"}},
		{name: "sha256", input: "sha256:" + strings.Repeat("ab", 32), want: Identity{Provider: "sha256", Value: strings.Repeat("ab", 32)}},
		{name: "empty", input: "", wantErr: true},
		{name: "unknown provider", input: "bitbucket.org/foo", wantErr: true},
		{name: "github empty path", input: "github.com/", wantErr: true},
		{name: "github traversal", input: "github.com/foo/../bar", wantErr: true},
		{name: "mailto without @", input: "mailto:notanemail", wantErr: true},
		{name: "sha256 short", input: "sha256:abcd", wantErr: true},
		{name: "sha256 non-hex", input: "sha256:" + strings.Repeat("zz", 32), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseIdentity(c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseIdentity(%q) = %+v, want error", c.input, got)
				}
				if !errors.Is(err, ErrInvalidIdentity) {
					t.Fatalf("error %v doesn't wrap ErrInvalidIdentity", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseIdentity(%q) unexpected error: %v", c.input, err)
			}
			if got != c.want {
				t.Fatalf("ParseIdentity(%q) = %+v, want %+v", c.input, got, c.want)
			}
		})
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"github.com/Singleton-Solution",
		"github.com/Singleton-Solution/seo",
		"gitlab.com/foo/bar",
		"mailto:dev@example.com",
		"sha256:" + strings.Repeat("ab", 32),
	}
	for _, in := range inputs {
		id, err := ParseIdentity(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if id.String() != in {
			t.Fatalf("round-trip: parse(%q).String() = %q", in, id.String())
		}
	}
}

func TestVerifyIdentity(t *testing.T) {
	t.Parallel()
	parse := func(s string) Identity {
		id, err := ParseIdentity(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return id
	}
	cases := []struct {
		name     string
		declared Identity
		observed Identity
		wantErr  bool
	}{
		{name: "exact org match", declared: parse("github.com/Singleton-Solution"), observed: parse("github.com/Singleton-Solution"), wantErr: false},
		{name: "org prefix match", declared: parse("github.com/Singleton-Solution"), observed: parse("github.com/Singleton-Solution/seo"), wantErr: false},
		{name: "repo pin matches itself", declared: parse("github.com/Singleton-Solution/seo"), observed: parse("github.com/Singleton-Solution/seo"), wantErr: false},
		{name: "repo pin rejects sibling", declared: parse("github.com/Singleton-Solution/seo"), observed: parse("github.com/Singleton-Solution/other"), wantErr: true},
		{name: "different org", declared: parse("github.com/foo"), observed: parse("github.com/bar"), wantErr: true},
		{name: "different provider", declared: parse("github.com/foo"), observed: parse("gitlab.com/foo"), wantErr: true},
		{name: "mailto exact", declared: parse("mailto:a@b.com"), observed: parse("mailto:a@b.com"), wantErr: false},
		{name: "mailto case-insensitive", declared: parse("mailto:A@B.com"), observed: parse("mailto:a@b.com"), wantErr: false},
		{name: "sha256 match", declared: parse("sha256:" + strings.Repeat("ab", 32)), observed: parse("sha256:" + strings.Repeat("ab", 32)), wantErr: false},
		{name: "sha256 mismatch", declared: parse("sha256:" + strings.Repeat("ab", 32)), observed: parse("sha256:" + strings.Repeat("cd", 32)), wantErr: true},
		{name: "empty declared", declared: Identity{}, observed: parse("github.com/foo"), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := VerifyIdentity(c.declared, c.observed)
			if c.wantErr {
				if err == nil {
					t.Fatalf("VerifyIdentity(%v, %v) = nil, want error", c.declared, c.observed)
				}
				if !errors.Is(err, ErrIdentityMismatch) {
					t.Fatalf("error %v doesn't wrap ErrIdentityMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("VerifyIdentity(%v, %v) = %v, want nil", c.declared, c.observed, err)
			}
		})
	}
}
