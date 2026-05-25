package sign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// EnvPublicKey is the environment variable name the production host
// reads to discover the trusted cosign public key in the air-gapped
// (keyed) fallback path.
const EnvPublicKey = "COSIGN_PUBLIC_KEY"

// EnvPublicKeyFile is the env var for path-based key configuration.
// When both EnvPublicKey and EnvPublicKeyFile are set, the inline PEM
// in EnvPublicKey wins.
const EnvPublicKeyFile = "COSIGN_PUBLIC_KEY_FILE"

// EnvKeySource is the KeySource implementation that reads the trusted
// public key from EnvPublicKey or EnvPublicKeyFile at every call.
//
// We re-read on every call so a SIGHUP-style rotation just works
// without restarting the host.
type EnvKeySource struct {
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
}

// PublicKeyPEM implements KeySource. Returns the PEM bytes, or
// (nil, nil) when no key is configured.
func (e *EnvKeySource) PublicKeyPEM(ctx context.Context) ([]byte, error) {
	lookup := e.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	read := e.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	if v, ok := lookup(EnvPublicKey); ok && strings.TrimSpace(v) != "" {
		return []byte(v), nil
	}
	if path, ok := lookup(EnvPublicKeyFile); ok && strings.TrimSpace(path) != "" {
		body, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("sign: read %s=%q: %w", EnvPublicKeyFile, path, err)
		}
		return body, nil
	}
	return nil, nil
}

// FingerprintPublicKey returns the lowercase-hex SHA-256 of the
// PEM-encoded public key. Whitespace and trailing newlines are
// normalised before hashing so a PEM with a Windows CRLF and one with a
// bare LF fingerprint identically.
func FingerprintPublicKey(pem []byte) string {
	s := strings.ReplaceAll(string(pem), "\r\n", "\n")
	s = strings.TrimSpace(s)
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// MakeKeyedIdentity is a convenience that builds the Identity a host
// expects to see in a cosign bundle for a given trusted public key.
func MakeKeyedIdentity(pem []byte) Identity {
	return Identity{Provider: "sha256", Value: FingerprintPublicKey(pem)}
}
