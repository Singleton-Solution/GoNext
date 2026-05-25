package sign

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type mockRekor struct {
	t          time.Time
	err        error
	calledWith int64
}

func (m *mockRekor) LookupByLogIndex(ctx context.Context, idx int64) (time.Time, error) {
	m.calledWith = idx
	return m.t, m.err
}

type mockKeys struct {
	pem []byte
	err error
}

func (m *mockKeys) PublicKeyPEM(ctx context.Context) ([]byte, error) {
	return m.pem, m.err
}

func buildBundleFS(t *testing.T, manifest, wasm string, pub Publisher, sb CosignBundle, sig string) fs.FS {
	t.Helper()
	sbBody, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal cosign bundle: %v", err)
	}
	pBody, err := json.Marshal(pub)
	if err != nil {
		t.Fatalf("marshal publisher: %v", err)
	}
	return fstest.MapFS{
		"manifest.json":             {Data: []byte(manifest)},
		"server/plugin.wasm":        {Data: []byte(wasm)},
		"signatures/cosign.sig":     {Data: []byte(sig)},
		"signatures/cosign.bundle":  {Data: sbBody},
		"signatures/publisher.json": {Data: pBody},
	}
}

func TestCanonicalDigestExcludesSignatures(t *testing.T) {
	t.Parallel()
	base := fstest.MapFS{
		"manifest.json":      {Data: []byte("{}")},
		"server/plugin.wasm": {Data: []byte("wasm-bytes")},
	}
	withSig := fstest.MapFS{
		"manifest.json":             {Data: []byte("{}")},
		"server/plugin.wasm":        {Data: []byte("wasm-bytes")},
		"signatures/cosign.sig":     {Data: []byte("anything")},
		"signatures/cosign.bundle":  {Data: []byte(`{"signedIdentity":"x"}`)},
		"signatures/publisher.json": {Data: []byte(`{"identity":"github.com/foo"}`)},
	}
	d1, err := CanonicalDigest(base)
	if err != nil {
		t.Fatalf("digest base: %v", err)
	}
	d2, err := CanonicalDigest(withSig)
	if err != nil {
		t.Fatalf("digest withSig: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("signatures/ leaked into digest: %s != %s", d1, d2)
	}
}

func TestCanonicalDigestDifferentBundles(t *testing.T) {
	t.Parallel()
	a := fstest.MapFS{"manifest.json": {Data: []byte("a")}}
	b := fstest.MapFS{"manifest.json": {Data: []byte("b")}}
	da, _ := CanonicalDigest(a)
	db, _ := CanonicalDigest(b)
	if da == db {
		t.Fatalf("different bundles produced the same digest")
	}
}

func TestVerifyKeylessHappyPath(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{
		CertPEM:             "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----\n",
		RekorLogID:          "log-id-1",
		RekorLogIndex:       42,
		RekorIntegratedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		SignedIdentity:      "github.com/Singleton-Solution/seo",
		Issuer:              "https://token.actions.githubusercontent.com",
	}
	pub := Publisher{Identity: "github.com/Singleton-Solution", DisplayName: "Singleton"}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK-SIG"))
	bundle := buildBundleFS(t, `{"name":"seo"}`, "wasm", pub, sb, sig)

	rekor := &mockRekor{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	v := NewVerifier(rekor, nil).WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	})

	got, err := v.Verify(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Mode != "keyless" {
		t.Fatalf("mode = %q, want keyless", got.Mode)
	}
	if got.Identity.Value != "Singleton-Solution/seo" {
		t.Fatalf("identity = %v", got.Identity)
	}
	if rekor.calledWith != 42 {
		t.Fatalf("rekor not called with log index 42 (got %d)", rekor.calledWith)
	}
}

func TestVerifyIdentityMismatchRejected(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{
		CertPEM:             "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----\n",
		RekorLogIndex:       1,
		RekorIntegratedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		SignedIdentity:      "github.com/Attacker/Evil",
	}
	pub := Publisher{Identity: "github.com/Singleton-Solution"}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	rekor := &mockRekor{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	v := NewVerifier(rekor, nil).WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	})

	_, err := v.Verify(context.Background(), bundle)
	if err == nil {
		t.Fatalf("Verify accepted mismatched identity")
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("error = %v, want ErrIdentityMismatch", err)
	}
}

func TestVerifyRekorUnreachable(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{
		CertPEM:        "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----\n",
		RekorLogIndex:  1,
		SignedIdentity: "github.com/Singleton-Solution",
	}
	pub := Publisher{Identity: "github.com/Singleton-Solution"}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	v := NewVerifier(nil, nil)
	_, err := v.Verify(context.Background(), bundle)
	if err == nil {
		t.Fatalf("expected Rekor-unreachable error")
	}
	if !errors.Is(err, ErrRekorUnreachable) {
		t.Fatalf("error = %v, want ErrRekorUnreachable", err)
	}
}

func TestVerifyRekorFuture(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{
		CertPEM:             "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----\n",
		RekorLogIndex:       1,
		RekorIntegratedTime: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		SignedIdentity:      "github.com/Singleton-Solution",
	}
	pub := Publisher{Identity: "github.com/Singleton-Solution"}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	rekor := &mockRekor{t: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	v := NewVerifier(rekor, nil).WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})

	_, err := v.Verify(context.Background(), bundle)
	if err == nil {
		t.Fatalf("expected future-timestamp rejection")
	}
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("error = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyKeyedHappyPath(t *testing.T) {
	t.Parallel()
	keyPEM := []byte("-----BEGIN PUBLIC KEY-----\nMOCK\n-----END PUBLIC KEY-----\n")
	expectedID := MakeKeyedIdentity(keyPEM)

	sb := CosignBundle{SignedIdentity: expectedID.String()}
	pub := Publisher{Identity: expectedID.String()}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	v := NewVerifier(nil, &mockKeys{pem: keyPEM}).WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})

	got, err := v.Verify(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Verify keyed: %v", err)
	}
	if got.Mode != "keyed" {
		t.Fatalf("mode = %q, want keyed", got.Mode)
	}
}

func TestVerifyKeyedFromBundleKey(t *testing.T) {
	t.Parallel()
	keyPEM := "-----BEGIN PUBLIC KEY-----\nBUNDLE-LOCAL\n-----END PUBLIC KEY-----\n"
	expectedID := MakeKeyedIdentity([]byte(keyPEM))

	sb := CosignBundle{SignedIdentity: expectedID.String()}
	pub := Publisher{Identity: expectedID.String(), PublicKeyPEM: keyPEM}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	v := NewVerifier(nil, nil)
	got, err := v.Verify(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Verify keyed (bundle-local key): %v", err)
	}
	if got.Mode != "keyed" {
		t.Fatalf("mode = %q", got.Mode)
	}
}

func TestVerifyKeyedNoKey(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{SignedIdentity: "sha256:" + strings.Repeat("ab", 32)}
	pub := Publisher{Identity: "sha256:" + strings.Repeat("ab", 32)}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{}`, "wasm", pub, sb, sig)

	v := NewVerifier(nil, &mockKeys{pem: nil})
	_, err := v.Verify(context.Background(), bundle)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("error = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyMissingSignature(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{"manifest.json": {Data: []byte("{}")}}
	v := NewVerifier(nil, nil)
	_, err := v.Verify(context.Background(), bundle)
	if !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("error = %v, want ErrMissingSignature", err)
	}
}

func TestVerifyCacheHit(t *testing.T) {
	t.Parallel()
	sb := CosignBundle{
		CertPEM:             "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----\n",
		RekorLogIndex:       7,
		RekorIntegratedTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		SignedIdentity:      "github.com/Singleton-Solution/seo",
	}
	pub := Publisher{Identity: "github.com/Singleton-Solution"}
	sig := base64.StdEncoding.EncodeToString([]byte("MOCK"))
	bundle := buildBundleFS(t, `{"name":"seo"}`, "wasm", pub, sb, sig)

	rekor := &mockRekor{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	v := NewVerifier(rekor, nil).WithClock(func() time.Time {
		return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	})

	if _, err := v.Verify(context.Background(), bundle); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	rekor.calledWith = 0
	if _, err := v.Verify(context.Background(), bundle); err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if rekor.calledWith != 0 {
		t.Fatalf("cache miss: Rekor called again (idx=%d)", rekor.calledWith)
	}
}

func TestEnvKeySource(t *testing.T) {
	t.Parallel()
	src := &EnvKeySource{
		LookupEnv: func(k string) (string, bool) {
			if k == EnvPublicKey {
				return "PEM-BODY", true
			}
			return "", false
		},
	}
	got, err := src.PublicKeyPEM(context.Background())
	if err != nil {
		t.Fatalf("PublicKeyPEM: %v", err)
	}
	if string(got) != "PEM-BODY" {
		t.Fatalf("got %q", got)
	}

	src2 := &EnvKeySource{
		LookupEnv: func(k string) (string, bool) {
			if k == EnvPublicKeyFile {
				return "/path/to/key.pem", true
			}
			return "", false
		},
		ReadFile: func(p string) ([]byte, error) {
			if p != "/path/to/key.pem" {
				t.Fatalf("unexpected path %q", p)
			}
			return []byte("FILE-PEM"), nil
		},
	}
	got, err = src2.PublicKeyPEM(context.Background())
	if err != nil {
		t.Fatalf("PublicKeyPEM file: %v", err)
	}
	if string(got) != "FILE-PEM" {
		t.Fatalf("got %q", got)
	}

	empty := &EnvKeySource{LookupEnv: func(string) (string, bool) { return "", false }}
	got, err = empty.PublicKeyPEM(context.Background())
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestFingerprintPublicKeyStable(t *testing.T) {
	t.Parallel()
	lf := []byte("-----BEGIN PUBLIC KEY-----\nMOCK\n-----END PUBLIC KEY-----\n")
	crlf := []byte("-----BEGIN PUBLIC KEY-----\r\nMOCK\r\n-----END PUBLIC KEY-----\r\n")
	if FingerprintPublicKey(lf) != FingerprintPublicKey(crlf) {
		t.Fatal("CRLF and LF PEMs fingerprinted differently")
	}
}

func TestReadPublisher(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{
		"signatures/publisher.json": {Data: []byte(`{"identity":"github.com/foo","display_name":"Foo"}`)},
	}
	p, err := ReadPublisher(bundle)
	if err != nil {
		t.Fatalf("ReadPublisher: %v", err)
	}
	if p.Identity != "github.com/foo" || p.DisplayName != "Foo" {
		t.Fatalf("got %+v", p)
	}

	empty := fstest.MapFS{}
	if _, err := ReadPublisher(empty); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("empty bundle: got %v", err)
	}

	noIdentity := fstest.MapFS{
		"signatures/publisher.json": {Data: []byte(`{"display_name":"Foo"}`)},
	}
	if _, err := ReadPublisher(noIdentity); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("no identity: got %v", err)
	}
}
