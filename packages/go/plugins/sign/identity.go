package sign

import (
	"errors"
	"fmt"
	"strings"
)

// Identity is the publisher's stable identity claim, extracted from the
// bundle's signatures/publisher.json.
//
// For keyless cosign signatures, Identity matches the SAN URI emitted in
// the Fulcio-issued certificate. The format is provider-prefixed:
//
//	github.com/<org>            — any commit in the org
//	github.com/<org>/<repo>     — commits in a specific repo
//	gitlab.com/<group>          — GitLab group
//	mailto:<user@example.com>   — explicit email identity
//
// For keyed signatures, Identity carries the SHA-256 fingerprint of the
// publisher's cosign public key, with a "sha256:" prefix.
//
// The zero value is invalid — use ParseIdentity.
type Identity struct {
	// Provider is the issuer half ("github.com", "gitlab.com",
	// "mailto", "sha256"). It selects the verification mode and the
	// expected SAN scheme.
	Provider string

	// Value is the issuer-specific value (org/repo, email, key fp).
	Value string
}

// ErrInvalidIdentity is returned by ParseIdentity for empty or malformed
// inputs.
var ErrInvalidIdentity = errors.New("sign: invalid publisher identity")

// ErrIdentityMismatch is returned by VerifyIdentity when the verified
// cosign cert subject (or key fingerprint) doesn't equal the bundle's
// declared identity. Activation is refused.
var ErrIdentityMismatch = errors.New("sign: publisher identity mismatch")

// ParseIdentity decodes a publisher.identity string into a structured
// Identity. Returns ErrInvalidIdentity (wrapped) for any unrecognised
// shape. The check is strict on purpose: ambiguity in the identity
// scheme would let a malicious publisher pass under a sibling org's
// name.
func ParseIdentity(s string) (Identity, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Identity{}, fmt.Errorf("%w: empty", ErrInvalidIdentity)
	}
	if strings.HasPrefix(s, "mailto:") {
		v := strings.TrimPrefix(s, "mailto:")
		if !strings.Contains(v, "@") || strings.ContainsAny(v, " \t\n") {
			return Identity{}, fmt.Errorf("%w: mailto value %q is not an email", ErrInvalidIdentity, v)
		}
		return Identity{Provider: "mailto", Value: v}, nil
	}
	if strings.HasPrefix(s, "sha256:") {
		v := strings.TrimPrefix(s, "sha256:")
		if len(v) != 64 || !isHex(v) {
			return Identity{}, fmt.Errorf("%w: sha256 fingerprint must be 64 hex chars", ErrInvalidIdentity)
		}
		return Identity{Provider: "sha256", Value: strings.ToLower(v)}, nil
	}
	for _, host := range []string{"github.com", "gitlab.com"} {
		if strings.HasPrefix(s, host+"/") {
			v := strings.TrimPrefix(s, host+"/")
			if v == "" || strings.Contains(v, " ") {
				return Identity{}, fmt.Errorf("%w: %s identity has empty path", ErrInvalidIdentity, host)
			}
			if strings.Contains(v, "..") {
				return Identity{}, fmt.Errorf("%w: %s identity contains parent traversal", ErrInvalidIdentity, host)
			}
			return Identity{Provider: host, Value: v}, nil
		}
	}
	return Identity{}, fmt.Errorf("%w: unknown provider in %q", ErrInvalidIdentity, s)
}

// String renders the identity in the same form ParseIdentity accepts.
func (i Identity) String() string {
	if i.Provider == "" {
		return ""
	}
	switch i.Provider {
	case "mailto", "sha256":
		return i.Provider + ":" + i.Value
	default:
		return i.Provider + "/" + i.Value
	}
}

// VerifyIdentity compares a declared (bundle) identity against the
// identity observed in a cosign verification result. Returns nil on
// match, ErrIdentityMismatch (wrapped with both sides) on mismatch.
//
// Matching is exact for mailto and sha256. For github.com/gitlab.com,
// an org-only declared identity ("github.com/Singleton-Solution") matches
// any repo under that org in the observed identity. A declared identity
// that pins a repo will refuse a signature from a sibling repo under
// the same org.
func VerifyIdentity(declared, observed Identity) error {
	if declared.Provider == "" || observed.Provider == "" {
		return fmt.Errorf("%w: declared=%q observed=%q",
			ErrIdentityMismatch, declared.String(), observed.String())
	}
	if declared.Provider != observed.Provider {
		return fmt.Errorf("%w: provider differs (declared=%s observed=%s)",
			ErrIdentityMismatch, declared.Provider, observed.Provider)
	}
	switch declared.Provider {
	case "mailto", "sha256":
		if !strings.EqualFold(declared.Value, observed.Value) {
			return fmt.Errorf("%w: declared=%q observed=%q",
				ErrIdentityMismatch, declared.String(), observed.String())
		}
		return nil
	case "github.com", "gitlab.com":
		dv, ov := declared.Value, observed.Value
		if dv == ov {
			return nil
		}
		if !strings.Contains(dv, "/") && strings.HasPrefix(ov, dv+"/") {
			return nil
		}
		return fmt.Errorf("%w: declared=%q observed=%q",
			ErrIdentityMismatch, declared.String(), observed.String())
	default:
		return fmt.Errorf("%w: unknown provider %q", ErrIdentityMismatch, declared.Provider)
	}
}

// isHex returns true iff s is non-empty and every byte is a lowercase or
// uppercase hex digit.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
