package runtime

// platform_secrets.go — AES-256-GCM-backed plugin secret read path
// underpinning gn_secrets_get. The wiring into the WASM ABI lives in
// host_platform.go; this file owns the cryptography and storage seams.
//
// Threat model: a plugin must be able to read ONLY its own secrets,
// and only after host-side AAD validation succeeds. The host KEK never
// crosses the WASM boundary; the per-plugin DEK is unwrapped in-process,
// used for one Open call, then zeroed.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrSecretNotFound is returned by SecretsStore.Get when the (slug, key)
// pair has no row. Callers should match with errors.Is. The host's
// gn_secrets_get returns -1 to the guest on this — a missing secret is
// the plugin author's problem (manifest typo, install path skipped) and
// distinguishing "not found" from "decrypt failed" via separate trap
// codes would only leak attack surface to a hostile guest.
var ErrSecretNotFound = errors.New("runtime: secret not found")

// ErrSecretDecrypt is returned on any AEAD failure (corruption, wrong
// DEK, AAD mismatch). Collapsed to a single sentinel so timing or branch
// behaviour can't disclose which AAD bit mismatched — the standard
// AEAD opacity rule.
var ErrSecretDecrypt = errors.New("runtime: secret decrypt failed")

// dekSize is the byte length of a per-plugin Data Encryption Key.
// AES-256 means 32 bytes; hardcoded so a misconfigured boot can't
// silently use a shorter key.
const dekSize = 32

// gcmNonceSize is the standard 96-bit nonce length used by AES-GCM.
// The plugin_secrets.nonce column has a matching CHECK length = 12,
// keeping the schema and Go side aligned at compile time.
const gcmNonceSize = 12

// SecretsStore is the persistence seam — the runtime asks for an
// encrypted row by (slug, key) and gets back ciphertext + nonce + AAD.
// The store has no access to the DEK. Split from the cryptography for
// testability and so a future store backend (e.g. KMS-side decrypt)
// can be swapped in without touching the runtime call site.
type SecretsStore interface {
	FetchEncrypted(ctx context.Context, pluginSlug, key string) (ciphertext, nonce, aad []byte, err error)
}

// SecretsCrypto does the two-step decrypt: unwrap the plugin's DEK
// under the host KEK, then open the secret blob under the DEK.
//
// The reference implementation (NewAESGCMCrypto) uses crypto/cipher's
// constant-time AES-GCM; alternative implementations (HSM, KMS) must
// preserve that property — branching on plaintext content would defeat
// the whole point of AEAD secrecy.
type SecretsCrypto interface {
	UnwrapDEK(ctx context.Context, pluginSlug string) ([]byte, error)
	OpenSecret(dek, ciphertext, nonce, aad []byte) ([]byte, error)
}

// DEKProvider is the seam that supplies the wrapped DEK for a plugin.
// The reference wiring pulls it from the lifecycle plugin row, where
// the DEK was stored encrypted-under-KEK at install time.
type DEKProvider interface {
	WrappedDEK(ctx context.Context, pluginSlug string) ([]byte, error)
}

// SecretsService is the bundled view host_platform.go consumes. One
// service per process, wired at boot. Safe for concurrent use because
// both halves of the pair are read-only after construction.
type SecretsService struct {
	store  SecretsStore
	crypto SecretsCrypto
}

// NewSecretsService bundles a store + crypto pair. Both args are
// required; nil panics — this is boot-time wiring and a nil here is
// a build error, not a runtime condition.
func NewSecretsService(store SecretsStore, crypto SecretsCrypto) *SecretsService {
	if store == nil {
		panic("runtime: NewSecretsService: store is required")
	}
	if crypto == nil {
		panic("runtime: NewSecretsService: crypto is required")
	}
	return &SecretsService{store: store, crypto: crypto}
}

// Get returns the plaintext for (pluginSlug, key) or an error.
//
// Flow:
//
//  1. Fetch the encrypted row.
//  2. Cross-check stored AAD against host-recomputed AAD
//     (slug || NUL || key). Catches a row copied between plugins
//     before any cipher work — defense in depth.
//  3. Unwrap the plugin's DEK under the host KEK.
//  4. AEAD-open the ciphertext under the DEK + stored AAD.
//
// The DEK is zeroed via defer immediately after the Open call, so its
// plaintext lifetime on the host heap is bounded to a single decrypt.
func (s *SecretsService) Get(ctx context.Context, pluginSlug, key string) ([]byte, error) {
	if pluginSlug == "" {
		return nil, errors.New("runtime: secrets: pluginSlug is required")
	}
	if key == "" {
		return nil, errors.New("runtime: secrets: key is required")
	}

	ciphertext, nonce, aad, err := s.store.FetchEncrypted(ctx, pluginSlug, key)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("runtime: secrets: fetch: %w", err)
	}

	expectedAAD := aadFor(pluginSlug, key)
	if !constantTimeEqual(aad, expectedAAD) {
		return nil, ErrSecretDecrypt
	}

	dek, err := s.crypto.UnwrapDEK(ctx, pluginSlug)
	if err != nil {
		return nil, fmt.Errorf("runtime: secrets: unwrap DEK: %w", err)
	}
	defer zero(dek)

	plain, err := s.crypto.OpenSecret(dek, ciphertext, nonce, aad)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

// aadFor produces the canonical AAD for a (slug, key) row: the slug,
// a NUL separator, then the key. The NUL prevents silent re-splicing
// (slug="foo", key="bar" cannot produce the same AAD as slug="foob",
// key="ar"). Plugin slugs and keys are text-encoded and contain no
// literal NULs in practice.
func aadFor(pluginSlug, key string) []byte {
	out := make([]byte, 0, len(pluginSlug)+1+len(key))
	out = append(out, pluginSlug...)
	out = append(out, 0)
	out = append(out, key...)
	return out
}

// constantTimeEqual is a non-allocating constant-time byte slice
// compare. We inline rather than import subtle to keep the
// cross-package dependency tree minimal. The math is the standard
// XOR-OR fold.
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// zero overwrites b with zeros. Used in a defer right after UnwrapDEK
// so the DEK is wiped before any return path. The Go compiler keeps
// this loop because the writes go through a slice header opaque to
// escape analysis; if a future toolchain elides it we'd need to switch
// to runtime.KeepAlive or assembly.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// aesGCMCrypto is the production SecretsCrypto: AES-256-GCM for both
// KEK unwrap and per-secret open.
//
// The KEK is the app-level secret (issue #114 — a 32-byte random value
// held in env/file/KMS). It NEVER crosses the WASM boundary; the
// WASM-visible payload is only the requested plaintext value.
type aesGCMCrypto struct {
	kek      []byte
	provider DEKProvider
}

// NewAESGCMCrypto returns a SecretsCrypto backed by AES-256-GCM.
//
// kek MUST be exactly 32 bytes — anything shorter weakens the cipher,
// anything other length is not a valid AES key. provider supplies the
// wrapped DEK blob from storage.
//
// Both args are required; a misconfigured boot fails with a clear
// startup error rather than at first secret fetch.
func NewAESGCMCrypto(kek []byte, provider DEKProvider) (SecretsCrypto, error) {
	if len(kek) != dekSize {
		return nil, fmt.Errorf("runtime: NewAESGCMCrypto: KEK must be %d bytes, got %d", dekSize, len(kek))
	}
	if provider == nil {
		return nil, errors.New("runtime: NewAESGCMCrypto: provider is required")
	}
	kekCopy := make([]byte, dekSize)
	copy(kekCopy, kek)
	return &aesGCMCrypto{kek: kekCopy, provider: provider}, nil
}

// UnwrapDEK fetches the wrapped DEK and AES-GCM-decrypts it under the
// KEK.
//
// On-disk layout: [ 12-byte nonce ] [ ciphertext || 16-byte tag ].
// AAD: "plugin-dek:" || pluginSlug — a stolen wrapped-DEK blob can't
// be replayed under a different plugin's slug.
//
// AEAD-uniqueness: the DEK is generated once per plugin at install
// with a fresh random nonce, so there's no nonce-reuse risk.
func (c *aesGCMCrypto) UnwrapDEK(ctx context.Context, pluginSlug string) ([]byte, error) {
	wrapped, err := c.provider.WrappedDEK(ctx, pluginSlug)
	if err != nil {
		return nil, fmt.Errorf("runtime: unwrap DEK: %w", err)
	}
	if len(wrapped) < gcmNonceSize+16 {
		// 16 = GCM tag size. Anything shorter than nonce+tag can't
		// be a valid blob — fail fast before handing garbage to the
		// cipher.
		return nil, ErrSecretDecrypt
	}
	nonce := wrapped[:gcmNonceSize]
	ct := wrapped[gcmNonceSize:]

	block, err := aes.NewCipher(c.kek)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	aad := []byte("plugin-dek:" + pluginSlug)
	dek, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	if len(dek) != dekSize {
		// Wrong-length DEK means KEK corruption or a wrap from a
		// different scheme. Refuse rather than guess.
		return nil, ErrSecretDecrypt
	}
	return dek, nil
}

// OpenSecret decrypts one secret blob under the supplied DEK.
// ciphertext is the GCM ciphertext including the appended 16-byte tag
// (the standard crypto/cipher convention). nonce is the 12-byte
// per-secret nonce. aad is aadFor(slug, key).
//
// Any AEAD failure collapses to ErrSecretDecrypt — callers must not
// branch on which bit failed.
func (c *aesGCMCrypto) OpenSecret(dek, ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(dek) != dekSize {
		return nil, ErrSecretDecrypt
	}
	if len(nonce) != gcmNonceSize {
		return nil, ErrSecretDecrypt
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	plain, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	return plain, nil
}

// Compile-time guard that aesGCMCrypto satisfies SecretsCrypto.
var _ SecretsCrypto = (*aesGCMCrypto)(nil)

// SealSecret is the host-side helper used by the admin install path to
// produce the (ciphertext, nonce, aad) triple for a plugin_secrets row.
// Exported so the operator CLI (and a future admin REST) can write new
// rows under the same scheme the WASM read path uses, without
// reimplementing the AAD layout.
//
// A fresh random nonce is generated per call.
func SealSecret(dek []byte, pluginSlug, key string, plaintext []byte) (ciphertext, nonce, aad []byte, err error) {
	if len(dek) != dekSize {
		return nil, nil, nil, fmt.Errorf("runtime: SealSecret: DEK must be %d bytes", dekSize)
	}
	if pluginSlug == "" {
		return nil, nil, nil, errors.New("runtime: SealSecret: pluginSlug is required")
	}
	if key == "" {
		return nil, nil, nil, errors.New("runtime: SealSecret: key is required")
	}

	nonce = make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, nil, fmt.Errorf("runtime: SealSecret: nonce: %w", err)
	}
	aad = aadFor(pluginSlug, key)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("runtime: SealSecret: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("runtime: SealSecret: GCM: %w", err)
	}
	ciphertext = aead.Seal(nil, nonce, plaintext, aad)
	return ciphertext, nonce, aad, nil
}
