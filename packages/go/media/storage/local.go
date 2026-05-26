package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LocalDriver stores objects on the local filesystem rooted at
// LocalConfig.Root. Each object lives at <root>/<key>; intermediate
// directories are created on-demand at Put time with 0o755.
//
// Presigned URLs are server-relative paths backed by a one-shot
// HTTP handler (see LocalUploadHandler / LocalDownloadHandler). The
// signature is an HMAC-SHA256 of (op || key || expiry) keyed by a
// per-process secret minted at New time. The URLs are NOT portable
// across process restarts — the HMAC key rotates on restart, which
// invalidates outstanding presigned URLs but keeps the trust model
// simple (no on-disk secret to leak, no key-rotation story to
// document).
//
// The driver enforces a single-writer-per-key invariant via the
// keyMu map — two concurrent Puts to the same key serialise rather
// than racing. The cost is a sync.Mutex per active key; the map is
// pruned opportunistically on Put completion so an unbounded write
// pattern doesn't leak memory.
type LocalDriver struct {
	root     string
	publicBaseURL string
	signKey  []byte
	keyMu    sync.Map // map[string]*sync.Mutex
}

// LocalConfig configures a LocalDriver. Root is the only required
// field; an empty Root is rejected at New so the driver can't quietly
// write to "" (which resolves to the process CWD and is almost
// certainly not what the operator wanted).
type LocalConfig struct {
	// Root is the absolute or relative directory the driver stores
	// objects under. Will be created with 0o755 if missing.
	Root string

	// PublicBaseURL is the URL prefix prepended to each key in
	// PublicURL. Defaults to "/_/media" — the path the API server
	// mounts the static handler under. Override for tests or for
	// deployments that serve media from a sibling reverse proxy.
	PublicBaseURL string
}

// NewLocalDriver returns a LocalDriver rooted at cfg.Root.
//
// Validation runs at construction so an obvious misconfiguration
// shows up at process boot rather than mid-upload. The HMAC key for
// presigned URLs is minted from crypto/rand — one key per process,
// zero on-disk state.
func NewLocalDriver(cfg LocalConfig) (*LocalDriver, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, errors.New("storage/local: Root is required")
	}
	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("storage/local: create root: %w", err)
	}
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("storage/local: resolve root: %w", err)
	}
	base := cfg.PublicBaseURL
	if base == "" {
		base = "/_/media"
	}
	signKey := make([]byte, 32)
	if _, err := rand.Read(signKey); err != nil {
		return nil, fmt.Errorf("storage/local: mint sign key: %w", err)
	}
	return &LocalDriver{
		root:          abs,
		publicBaseURL: strings.TrimRight(base, "/"),
		signKey:       signKey,
	}, nil
}

// Root reports the resolved root directory the driver writes under.
// Useful for the upload handler that needs to know where to read
// uploaded bytes from during the one-shot download path.
func (l *LocalDriver) Root() string { return l.root }

// Put writes r to <root>/<key>. The parent directory is created
// on-demand. Returns the byte count.
func (l *LocalDriver) Put(ctx context.Context, key string, r io.Reader, mime string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return 0, err
	}
	mu := l.keyLock(key)
	mu.Lock()
	defer func() {
		mu.Unlock()
		l.keyMu.Delete(key)
	}()
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, fmt.Errorf("storage/local: mkdir: %w", err)
	}
	// Write to a sibling tempfile then rename — that way a concurrent
	// reader either sees the old object or the new one, never a
	// half-written byte stream.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("storage/local: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	n, copyErr := io.Copy(tmp, r)
	closeErr := tmp.Close()
	if copyErr != nil {
		cleanup()
		return 0, fmt.Errorf("storage/local: write body: %w", copyErr)
	}
	if closeErr != nil {
		cleanup()
		return 0, fmt.Errorf("storage/local: close tempfile: %w", closeErr)
	}
	if err := os.Rename(tmpName, full); err != nil {
		cleanup()
		return 0, fmt.Errorf("storage/local: rename: %w", err)
	}
	// Record the mime in a sidecar file so Stat can return it. The
	// alternative — sniffing the bytes on every Stat — would round-
	// trip a kilobyte off disk just to answer a metadata query.
	if mime != "" {
		_ = os.WriteFile(full+".mime", []byte(mime), 0o644)
	}
	return n, nil
}

// Get opens <root>/<key> for reading. The caller must Close the
// returned ReadCloser.
func (l *LocalDriver) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage/local: open: %w", err)
	}
	return f, nil
}

// Delete removes <root>/<key>. Missing is not an error.
func (l *LocalDriver) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage/local: remove: %w", err)
	}
	// Best-effort sidecar cleanup; failure here is logged-by-absence
	// only — a stale mime sidecar with no object is harmless.
	_ = os.Remove(full + ".mime")
	return nil
}

// Stat returns metadata for <root>/<key>. Returns ErrNotFound when
// the file does not exist.
func (l *LocalDriver) Stat(ctx context.Context, key string) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	full, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage/local: stat: %w", err)
	}
	mime := ""
	if b, err := os.ReadFile(full + ".mime"); err == nil {
		mime = strings.TrimSpace(string(b))
	}
	return &Object{
		Key:          key,
		Size:         fi.Size(),
		MimeType:     mime,
		LastModified: fi.ModTime(),
	}, nil
}

// Presign mints a signed server-relative URL for op on key valid
// for ttl. The signature is an HMAC of (op || key || expiry).
func (l *LocalDriver) Presign(_ context.Context, key string, op PresignOp, ttl time.Duration, mime string) (PresignedRequest, error) {
	if op != PresignPut && op != PresignGet {
		return PresignedRequest{}, fmt.Errorf("storage/local: unsupported op %q", op)
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	expires := time.Now().Add(ttl).UTC()
	exp := strconv.FormatInt(expires.Unix(), 10)
	sig := l.sign(string(op), key, exp)
	q := url.Values{}
	q.Set("op", string(op))
	q.Set("exp", exp)
	q.Set("sig", sig)
	if op == PresignPut && mime != "" {
		q.Set("mime", mime)
	}
	u := l.publicBaseURL + "/_presigned/" + url.PathEscape(key) + "?" + q.Encode()
	headers := map[string]string{}
	if op == PresignPut && mime != "" {
		headers["Content-Type"] = mime
	}
	return PresignedRequest{URL: u, Headers: headers, ExpiresAt: expires}, nil
}

// PublicURL returns the unauthenticated read URL for key. The
// LocalDriver's read handler does NOT check a signature on this
// path — the bytes are public; the URL itself is the capability.
// Callers that need an expiring URL use Presign with PresignGet.
func (l *LocalDriver) PublicURL(key string) string {
	return l.publicBaseURL + "/" + key
}

// VerifyPresignedURL is the verification half of Presign. Used by
// the one-shot upload/download handler the server mounts under the
// presigned path. Returns the verified op, key, and mime (for PUT)
// or an error if the signature is bad or expired.
func (l *LocalDriver) VerifyPresignedURL(rawQuery, key string) (PresignOp, string, error) {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", "", fmt.Errorf("storage/local: bad query: %w", err)
	}
	op := PresignOp(q.Get("op"))
	if op != PresignPut && op != PresignGet {
		return "", "", errors.New("storage/local: missing or bad op")
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		return "", "", errors.New("storage/local: bad exp")
	}
	if time.Now().Unix() > exp {
		return "", "", errors.New("storage/local: presigned URL expired")
	}
	want := l.sign(string(op), key, strconv.FormatInt(exp, 10))
	got := q.Get("sig")
	if !hmac.Equal([]byte(want), []byte(got)) {
		return "", "", errors.New("storage/local: signature mismatch")
	}
	return op, q.Get("mime"), nil
}

func (l *LocalDriver) sign(op, key, exp string) string {
	mac := hmac.New(sha256.New, l.signKey)
	mac.Write([]byte(op))
	mac.Write([]byte{0})
	mac.Write([]byte(key))
	mac.Write([]byte{0})
	mac.Write([]byte(exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// resolve joins key with the root and verifies the result stays
// inside it. Defends against the operator who hand-crafts a key
// containing ".." or an absolute path; the resolved location must
// be a descendent of l.root.
func (l *LocalDriver) resolve(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w: empty key", ErrInvalidKey)
	}
	if strings.Contains(key, "\\") {
		return "", fmt.Errorf("%w: backslash not allowed in key", ErrInvalidKey)
	}
	// Reject any literal ".." segment in the key BEFORE Clean
	// collapses them; "../etc/passwd" cleans to "/etc/passwd" which
	// looks fine after the fact but is exactly the escape we want to
	// reject. Iterate path segments explicitly.
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %s", ErrInvalidKey, key)
		}
	}
	if strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("%w: absolute paths not allowed: %s", ErrInvalidKey, key)
	}
	clean := path.Clean("/" + key)
	full := filepath.Join(l.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	// Final containment check — Clean above should have removed any
	// ".." but defence in depth here costs a single filepath.Rel.
	rel, err := filepath.Rel(l.root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %s", ErrInvalidKey, key)
	}
	return full, nil
}

func (l *LocalDriver) keyLock(key string) *sync.Mutex {
	v, _ := l.keyMu.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

