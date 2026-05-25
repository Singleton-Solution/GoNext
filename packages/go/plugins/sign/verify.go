package sign

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"
)

// In-bundle paths used by both signing and verification.
//
// publisherPath carries the publisher identity declaration as a sibling
// of the signature artefacts. We keep it out of manifest.json because
// the gonext.io/v1 manifest schema is frozen with
// additionalProperties:false — adding a top-level "publisher" key would
// be a schema break.
const (
	sigBlobPath   = "signatures/cosign.sig"
	sigBundlePath = "signatures/cosign.bundle"
	publisherPath = "signatures/publisher.json"
)

// Publisher is the on-disk shape of signatures/publisher.json.
type Publisher struct {
	Identity     string `json:"identity"`
	DisplayName  string `json:"display_name,omitempty"`
	PublicKeyPEM string `json:"public_key,omitempty"`
}

// ErrMissingSignature is returned when the bundle has no signatures/
// directory at all.
var ErrMissingSignature = errors.New("sign: bundle has no cosign signature")

// ErrSignatureInvalid is returned when the signature blob exists but
// fails verification.
var ErrSignatureInvalid = errors.New("sign: cosign signature is invalid")

// ErrRekorUnreachable is returned by Verify when keyless verification
// requires hitting Rekor but the RekorClient is nil or errored.
var ErrRekorUnreachable = errors.New("sign: Rekor transparency log is unreachable")

// CanonicalDigest computes the digest cosign signs over. The digest is
// computed on the bundle bytes with the signatures/ directory STRIPPED,
// because the signature can't cover itself.
func CanonicalDigest(fsys fs.FS) (string, error) {
	var paths []string
	if err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(p, "signatures/") {
			return nil
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign: walk bundle: %w", err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		f, err := fsys.Open(p)
		if err != nil {
			return "", fmt.Errorf("sign: open %q: %w", p, err)
		}
		body, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return "", fmt.Errorf("sign: read %q: %w", p, err)
		}
		_, _ = fmt.Fprintf(h, "%d\n%s\n%d\n", len(p), p, len(body))
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CosignBundle is the JSON shape cosign writes to signatures/cosign.bundle.
type CosignBundle struct {
	CertPEM             string `json:"cert,omitempty"`
	RekorLogID          string `json:"rekorLogID,omitempty"`
	RekorLogIndex       int64  `json:"rekorLogIndex,omitempty"`
	RekorIntegratedTime int64  `json:"rekorIntegratedTime,omitempty"`
	SignedIdentity      string `json:"signedIdentity"`
	Issuer              string `json:"issuer,omitempty"`
}

// VerificationResult is what Verify returns on success and what the
// cache stores.
type VerificationResult struct {
	Digest              string
	Identity            Identity
	Mode                string // "keyless" or "keyed"
	RekorLogIndex       int64
	RekorIntegratedTime time.Time
	VerifiedAt          time.Time
}

// RekorClient is the seam to the transparency log.
type RekorClient interface {
	LookupByLogIndex(ctx context.Context, logIndex int64) (time.Time, error)
}

// KeySource is the seam to the keyed (air-gapped) trust root.
type KeySource interface {
	PublicKeyPEM(ctx context.Context) ([]byte, error)
}

// Verifier is the entry point. Construct one at host boot. Safe for
// concurrent use.
type Verifier struct {
	rekor  RekorClient
	keys   KeySource
	now    func() time.Time
	skew   time.Duration
	cacheM sync.Mutex
	cache  map[string]VerificationResult
}

// NewVerifier returns a Verifier wired to the given seams. Either
// argument may be nil; the verifier degrades gracefully.
func NewVerifier(rekor RekorClient, keys KeySource) *Verifier {
	return &Verifier{
		rekor: rekor,
		keys:  keys,
		now:   time.Now,
		skew:  5 * time.Minute,
		cache: map[string]VerificationResult{},
	}
}

// WithClock overrides the verifier's clock. Tests use this to drive
// Rekor's "future timestamp" guard deterministically.
func (v *Verifier) WithClock(now func() time.Time) *Verifier {
	v.now = now
	return v
}

// ReadPublisher pulls signatures/publisher.json from the bundle.
// Returns ErrMissingSignature if the file doesn't exist.
func ReadPublisher(bundle fs.FS) (Publisher, error) {
	body, err := fs.ReadFile(bundle, publisherPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Publisher{}, ErrMissingSignature
		}
		return Publisher{}, fmt.Errorf("sign: read %s: %w", publisherPath, err)
	}
	var p Publisher
	if err := json.Unmarshal(body, &p); err != nil {
		return Publisher{}, fmt.Errorf("sign: parse %s: %w", publisherPath, err)
	}
	if strings.TrimSpace(p.Identity) == "" {
		return Publisher{}, fmt.Errorf("%w: publisher.json missing identity", ErrInvalidIdentity)
	}
	return p, nil
}

// Verify checks a bundle's cosign signature against the publisher
// identity declared in signatures/publisher.json.
func (v *Verifier) Verify(ctx context.Context, bundle fs.FS) (VerificationResult, error) {
	if bundle == nil {
		return VerificationResult{}, errors.New("sign: nil bundle fs")
	}

	publisher, err := ReadPublisher(bundle)
	if err != nil {
		return VerificationResult{}, err
	}
	declaredIdentity, err := ParseIdentity(publisher.Identity)
	if err != nil {
		return VerificationResult{}, fmt.Errorf("sign: declared identity: %w", err)
	}

	sigBlob, err := fs.ReadFile(bundle, sigBlobPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return VerificationResult{}, ErrMissingSignature
		}
		return VerificationResult{}, fmt.Errorf("sign: read %s: %w", sigBlobPath, err)
	}
	sigBundleBytes, err := fs.ReadFile(bundle, sigBundlePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return VerificationResult{}, fmt.Errorf("%w: missing %s sidecar", ErrMissingSignature, sigBundlePath)
		}
		return VerificationResult{}, fmt.Errorf("sign: read %s: %w", sigBundlePath, err)
	}

	var sb CosignBundle
	if err := json.Unmarshal(sigBundleBytes, &sb); err != nil {
		return VerificationResult{}, fmt.Errorf("%w: parse cosign bundle: %v", ErrSignatureInvalid, err)
	}

	digest, err := CanonicalDigest(bundle)
	if err != nil {
		return VerificationResult{}, fmt.Errorf("sign: digest: %w", err)
	}

	if cached, ok := v.cacheGet(digest); ok {
		return cached, nil
	}

	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigBlob))); err != nil {
		return VerificationResult{}, fmt.Errorf("%w: signature blob is not valid base64: %v", ErrSignatureInvalid, err)
	}

	if sb.SignedIdentity == "" {
		return VerificationResult{}, fmt.Errorf("%w: cosign bundle is missing signedIdentity", ErrSignatureInvalid)
	}

	observed, err := ParseIdentity(sb.SignedIdentity)
	if err != nil {
		return VerificationResult{}, fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}

	mode := "keyless"
	var rekorTime time.Time
	if sb.CertPEM != "" {
		if v.rekor == nil {
			return VerificationResult{}, ErrRekorUnreachable
		}
		t, err := v.rekor.LookupByLogIndex(ctx, sb.RekorLogIndex)
		if err != nil {
			return VerificationResult{}, fmt.Errorf("%w: %v", ErrRekorUnreachable, err)
		}
		embedded := time.Unix(sb.RekorIntegratedTime, 0).UTC()
		if !embedded.IsZero() && abs(t.Sub(embedded)) > v.skew {
			return VerificationResult{}, fmt.Errorf("%w: Rekor time %s disagrees with bundle %s",
				ErrSignatureInvalid, t.UTC(), embedded)
		}
		if t.After(v.now().Add(v.skew)) {
			return VerificationResult{}, fmt.Errorf("%w: Rekor entry is in the future (%s)",
				ErrSignatureInvalid, t.UTC())
		}
		rekorTime = t.UTC()
	} else {
		mode = "keyed"
		var key []byte
		if strings.TrimSpace(publisher.PublicKeyPEM) != "" {
			key = []byte(publisher.PublicKeyPEM)
		} else if v.keys != nil {
			key, err = v.keys.PublicKeyPEM(ctx)
			if err != nil {
				return VerificationResult{}, fmt.Errorf("%w: load public key: %v", ErrSignatureInvalid, err)
			}
		}
		if len(key) == 0 {
			return VerificationResult{}, fmt.Errorf("%w: no public key available for keyed verification", ErrSignatureInvalid)
		}
		expected := MakeKeyedIdentity(key)
		if err := VerifyIdentity(expected, observed); err != nil {
			return VerificationResult{}, fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
		}
	}

	if err := VerifyIdentity(declaredIdentity, observed); err != nil {
		return VerificationResult{}, err
	}

	result := VerificationResult{
		Digest:              digest,
		Identity:            observed,
		Mode:                mode,
		RekorLogIndex:       sb.RekorLogIndex,
		RekorIntegratedTime: rekorTime,
		VerifiedAt:          v.now().UTC(),
	}
	v.cachePut(digest, result)
	return result, nil
}

func (v *Verifier) cacheGet(digest string) (VerificationResult, bool) {
	v.cacheM.Lock()
	defer v.cacheM.Unlock()
	r, ok := v.cache[digest]
	return r, ok
}

func (v *Verifier) cachePut(digest string, r VerificationResult) {
	v.cacheM.Lock()
	defer v.cacheM.Unlock()
	v.cache[digest] = r
}

// ClearCache drops every cached verification.
func (v *Verifier) ClearCache() {
	v.cacheM.Lock()
	defer v.cacheM.Unlock()
	for k := range v.cache {
		delete(v.cache, k)
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// AppendSignatureToZip writes the signature artefacts + publisher
// declaration into a zip writer.
func AppendSignatureToZip(w *zip.Writer, sigBlob []byte, sigBundle CosignBundle, publisher Publisher) error {
	if err := writeZipEntry(w, sigBlobPath, sigBlob); err != nil {
		return err
	}
	sbBody, err := json.MarshalIndent(sigBundle, "", "  ")
	if err != nil {
		return fmt.Errorf("sign: marshal cosign bundle: %w", err)
	}
	sbBody = append(sbBody, '\n')
	if err := writeZipEntry(w, sigBundlePath, sbBody); err != nil {
		return err
	}
	pBody, err := json.MarshalIndent(publisher, "", "  ")
	if err != nil {
		return fmt.Errorf("sign: marshal publisher: %w", err)
	}
	pBody = append(pBody, '\n')
	return writeZipEntry(w, publisherPath, pBody)
}

func writeZipEntry(w *zip.Writer, path string, body []byte) error {
	fw, err := w.Create(path)
	if err != nil {
		return fmt.Errorf("sign: zip create %q: %w", path, err)
	}
	if _, err := fw.Write(body); err != nil {
		return fmt.Errorf("sign: zip write %q: %w", path, err)
	}
	return nil
}

// SignatureArtefactPaths returns the in-bundle paths the sign + verify
// pair use.
func SignatureArtefactPaths() (sigPath, bundlePath, publisherJSON string) {
	return sigBlobPath, sigBundlePath, publisherPath
}

// MarshalCanonicalBundleForSigning returns the byte stream Verify hashes
// to produce CanonicalDigest. Exposed so the CLI sign step can produce
// a sigBlob that covers exactly the digest the host will compute at
// verification time.
func MarshalCanonicalBundleForSigning(fsys fs.FS) ([]byte, error) {
	var paths []string
	if err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(p, "signatures/") {
			return nil
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		f, err := fsys.Open(p)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(&buf, "%d\n%s\n%d\n", len(p), p, len(body))
		_, _ = buf.Write(body)
		_, _ = buf.Write([]byte{'\n'})
	}
	return buf.Bytes(), nil
}
