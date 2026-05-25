package runtime

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

// fakeStore is a minimal SecretsStore double. We don't need the
// full pgx machinery for testing the crypto + AAD plumbing.
type fakeStore struct {
	rows map[string]storedRow
	err  error
}

type storedRow struct {
	ciphertext []byte
	nonce      []byte
	aad        []byte
}

func (s *fakeStore) FetchEncrypted(_ context.Context, slug, key string) ([]byte, []byte, []byte, error) {
	if s.err != nil {
		return nil, nil, nil, s.err
	}
	r, ok := s.rows[slug+"|"+key]
	if !ok {
		return nil, nil, nil, ErrSecretNotFound
	}
	return r.ciphertext, r.nonce, r.aad, nil
}

// fakeDEK is a one-DEK-per-plugin provider for tests.
type fakeDEK struct {
	deks map[string][]byte // slug -> plaintext DEK
}

func (f *fakeDEK) WrappedDEK(_ context.Context, slug string) ([]byte, error) {
	dek, ok := f.deks[slug]
	if !ok {
		return nil, errors.New("no DEK for slug")
	}
	// Tests bypass KEK wrapping by handing back the plaintext DEK
	// pre-formatted as if it were already unwrapped. The aesGCMCrypto
	// path is separately tested below.
	return dek, nil
}

// directDEK is a SecretsCrypto that returns the supplied DEK as-is.
// Lets us test SecretsService.Get without exercising the AES KEK
// unwrap (which has its own test).
type directDEK struct {
	dek []byte
}

func (d *directDEK) UnwrapDEK(_ context.Context, _ string) ([]byte, error) {
	out := make([]byte, len(d.dek))
	copy(out, d.dek)
	return out, nil
}

func (d *directDEK) OpenSecret(dek, ct, nonce, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	plain, err := aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrSecretDecrypt
	}
	return plain, nil
}

func TestSecretsService_Get_RoundTrip(t *testing.T) {
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		t.Fatalf("dek gen: %v", err)
	}

	ct, nonce, aad, err := SealSecret(dek, "seo", "api-token", []byte("s3cret"))
	if err != nil {
		t.Fatalf("SealSecret: %v", err)
	}

	store := &fakeStore{rows: map[string]storedRow{
		"seo|api-token": {ciphertext: ct, nonce: nonce, aad: aad},
	}}
	svc := NewSecretsService(store, &directDEK{dek: dek})

	plain, err := svc.Get(context.Background(), "seo", "api-token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(plain) != "s3cret" {
		t.Errorf("plain = %q, want %q", plain, "s3cret")
	}
}

func TestSecretsService_Get_NotFound(t *testing.T) {
	store := &fakeStore{rows: map[string]storedRow{}}
	svc := NewSecretsService(store, &directDEK{dek: make([]byte, dekSize)})
	_, err := svc.Get(context.Background(), "seo", "missing")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Errorf("want ErrSecretNotFound, got %v", err)
	}
}

func TestSecretsService_Get_AAD_Mismatch(t *testing.T) {
	// Seal a row for (seo, key1) then store it under (seo, key2).
	// The cross-check against expected AAD must reject before any
	// AEAD work happens.
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		t.Fatalf("dek gen: %v", err)
	}
	ct, nonce, aad, _ := SealSecret(dek, "seo", "key1", []byte("value"))

	store := &fakeStore{rows: map[string]storedRow{
		"seo|key2": {ciphertext: ct, nonce: nonce, aad: aad}, // stored under key2 but AAD is key1
	}}
	svc := NewSecretsService(store, &directDEK{dek: dek})

	_, err := svc.Get(context.Background(), "seo", "key2")
	if !errors.Is(err, ErrSecretDecrypt) {
		t.Errorf("want ErrSecretDecrypt, got %v", err)
	}
}

func TestSecretsService_Get_WrongDEK(t *testing.T) {
	// Seal with one DEK, decrypt with another. AEAD verify fails.
	dek1 := make([]byte, dekSize)
	dek2 := make([]byte, dekSize)
	_, _ = io.ReadFull(rand.Reader, dek1)
	_, _ = io.ReadFull(rand.Reader, dek2)

	ct, nonce, aad, _ := SealSecret(dek1, "seo", "key", []byte("v"))

	store := &fakeStore{rows: map[string]storedRow{
		"seo|key": {ciphertext: ct, nonce: nonce, aad: aad},
	}}
	svc := NewSecretsService(store, &directDEK{dek: dek2})

	_, err := svc.Get(context.Background(), "seo", "key")
	if !errors.Is(err, ErrSecretDecrypt) {
		t.Errorf("want ErrSecretDecrypt, got %v", err)
	}
}

func TestAESGCMCrypto_UnwrapDEK_RoundTrip(t *testing.T) {
	kek := make([]byte, dekSize)
	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, kek); err != nil {
		t.Fatalf("kek gen: %v", err)
	}
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		t.Fatalf("dek gen: %v", err)
	}

	// Wrap the DEK ourselves under the KEK with the same scheme
	// UnwrapDEK expects.
	nonce := make([]byte, gcmNonceSize)
	_, _ = io.ReadFull(rand.Reader, nonce)
	block, _ := aes.NewCipher(kek)
	aead, _ := cipher.NewGCM(block)
	wrapped := aead.Seal(nil, nonce, dek, []byte("plugin-dek:seo"))
	blob := append(append([]byte{}, nonce...), wrapped...)

	provider := &fakeDEK{deks: map[string][]byte{"seo": blob}}
	crypto, err := NewAESGCMCrypto(kek, provider)
	if err != nil {
		t.Fatalf("NewAESGCMCrypto: %v", err)
	}

	got, err := crypto.UnwrapDEK(context.Background(), "seo")
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if string(got) != string(dek) {
		t.Errorf("UnwrapDEK returned wrong key")
	}
}

func TestAESGCMCrypto_UnwrapDEK_BadKEK(t *testing.T) {
	kek1 := make([]byte, dekSize)
	kek2 := make([]byte, dekSize)
	dek := make([]byte, dekSize)
	_, _ = io.ReadFull(rand.Reader, kek1)
	_, _ = io.ReadFull(rand.Reader, kek2)
	_, _ = io.ReadFull(rand.Reader, dek)

	nonce := make([]byte, gcmNonceSize)
	_, _ = io.ReadFull(rand.Reader, nonce)
	block, _ := aes.NewCipher(kek1)
	aead, _ := cipher.NewGCM(block)
	wrapped := aead.Seal(nil, nonce, dek, []byte("plugin-dek:seo"))
	blob := append(append([]byte{}, nonce...), wrapped...)

	provider := &fakeDEK{deks: map[string][]byte{"seo": blob}}
	// Use kek2 to unwrap a DEK that was wrapped under kek1.
	crypto, _ := NewAESGCMCrypto(kek2, provider)
	_, err := crypto.UnwrapDEK(context.Background(), "seo")
	if !errors.Is(err, ErrSecretDecrypt) {
		t.Errorf("want ErrSecretDecrypt, got %v", err)
	}
}

func TestNewAESGCMCrypto_KEKLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		kek := make([]byte, n)
		_, err := NewAESGCMCrypto(kek, &fakeDEK{deks: nil})
		if err == nil {
			t.Errorf("len=%d: expected error, got nil", n)
		}
	}
}

func TestSealSecret_DeterministicAAD(t *testing.T) {
	dek := make([]byte, dekSize)
	_, _ = io.ReadFull(rand.Reader, dek)
	_, _, aad1, _ := SealSecret(dek, "seo", "key", []byte("v"))
	_, _, aad2, _ := SealSecret(dek, "seo", "key", []byte("v"))
	if string(aad1) != string(aad2) {
		t.Errorf("AAD is non-deterministic")
	}
	_, _, aad3, _ := SealSecret(dek, "other", "key", []byte("v"))
	if string(aad1) == string(aad3) {
		t.Errorf("AAD should differ by slug")
	}
}

func TestSealSecret_FreshNonce(t *testing.T) {
	dek := make([]byte, dekSize)
	_, _ = io.ReadFull(rand.Reader, dek)
	_, n1, _, _ := SealSecret(dek, "seo", "key", []byte("v"))
	_, n2, _, _ := SealSecret(dek, "seo", "key", []byte("v"))
	if string(n1) == string(n2) {
		t.Errorf("nonce should be fresh per Seal")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	cases := []struct {
		a, b []byte
		want bool
	}{
		{[]byte("abc"), []byte("abc"), true},
		{[]byte("abc"), []byte("abd"), false},
		{[]byte("abc"), []byte("abcd"), false},
		{nil, nil, true},
		{[]byte{}, nil, true},
	}
	for _, c := range cases {
		if got := constantTimeEqual(c.a, c.b); got != c.want {
			t.Errorf("constantTimeEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
