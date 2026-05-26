package storage

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// LocalUploadHandler returns an http.Handler that accepts PUTs at
// {presignBase}/_presigned/{key}?op=put&exp=...&sig=...&mime=... and
// writes the body to the LocalDriver. It is the server-side
// counterpart to the URLs LocalDriver.Presign returns; without this
// handler the presigned URLs have nowhere to land.
//
// The handler is mounted under the same base path as the public-read
// handler (typically /_/media) so a single mux entry can dispatch
// both reads (GET / HEAD) and writes (PUT). The "/_presigned/"
// segment in the URL is the discriminator.
//
// Errors are returned as plain HTTP status codes — this package does
// not depend on the API server's router helpers (cyclic import
// otherwise). The admin layer's presign handler can wrap this with
// the JSON error envelope if it ever needs a typed body; the current
// callers (browsers performing direct uploads) treat the status code
// as authoritative.
func LocalUploadHandler(driver *LocalDriver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discriminate by path segment. The URL is shaped:
		//   <base>/_presigned/<urlencoded-key>?op=put&...
		// We strip <base>/_presigned/ to recover the key.
		const marker = "/_presigned/"
		idx := strings.Index(r.URL.Path, marker)
		if idx < 0 {
			// Not a presigned path; let the public read handler take
			// it. We treat absence of the marker as 404 here because
			// this handler should only be mounted under the presigned
			// prefix.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		rawKey := r.URL.Path[idx+len(marker):]
		key, err := pathUnescape(rawKey)
		if err != nil {
			http.Error(w, "bad key encoding", http.StatusBadRequest)
			return
		}
		// Defence in depth: ensure the key resolves inside the root.
		if _, err := driver.resolve(key); err != nil {
			http.Error(w, "invalid key", http.StatusBadRequest)
			return
		}
		op, mime, err := driver.VerifyPresignedURL(r.URL.RawQuery, key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		switch op {
		case PresignPut:
			if r.Method != http.MethodPut {
				w.Header().Set("Allow", http.MethodPut)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if mime == "" {
				mime = r.Header.Get("Content-Type")
			}
			defer r.Body.Close()
			n, err := driver.Put(r.Context(), key, r.Body, mime)
			if err != nil {
				if errors.Is(err, ErrInvalidKey) {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				http.Error(w, "upload failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Bytes-Written", itoa(n))
			w.WriteHeader(http.StatusNoContent)
		case PresignGet:
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			rc, err := driver.Get(r.Context(), key)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				http.Error(w, "read failed", http.StatusInternalServerError)
				return
			}
			defer rc.Close()
			obj, _ := driver.Stat(r.Context(), key)
			if obj != nil && obj.MimeType != "" {
				w.Header().Set("Content-Type", obj.MimeType)
			}
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodHead {
				return
			}
			_, _ = io.Copy(w, rc)
		default:
			http.Error(w, "unsupported op", http.StatusBadRequest)
		}
	})
}

// pathUnescape reverses the url.PathEscape Presign performs on the
// key when building the URL. The leading slash that may survive the
// marker split is trimmed before decoding so path.Clean does not
// short-circuit on an absolute path.
func pathUnescape(p string) (string, error) {
	q := strings.SplitN(p, "?", 2)[0]
	q = strings.TrimLeft(q, "/")
	dec, err := url.PathUnescape(q)
	if err != nil {
		return "", err
	}
	return path.Clean(dec), nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
