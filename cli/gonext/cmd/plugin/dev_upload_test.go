package plugin

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDevProject lays out a minimal "project" on disk that the
// uploader can read: a manifest.json at the root and a built artifact
// at build/plugin.wasm.
func writeDevProject(t *testing.T, manifest string, wasm []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "plugin.wasm"), wasm, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	return dir
}

func TestHTTPUploader_PostsMultipart(t *testing.T) {
	manifest := `{"apiVersion":"gonext.io/v1","name":"demo","version":"0.1.0","entry":"server/plugin.wasm","capabilities":["http.fetch"]}`
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	dir := writeDevProject(t, manifest, wasm)

	var gotPath, gotCT string
	var gotManifest, gotWASM []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")

		mediaType, params, err := mime.ParseMediaType(gotCT)
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("Content-Type = %q (%v)", gotCT, err)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		_ = params

		readPart := func(field string) []byte {
			fhs := r.MultipartForm.File[field]
			if len(fhs) != 1 {
				t.Fatalf("want 1 file for %s; got %d", field, len(fhs))
			}
			f, err := fhs[0].Open()
			if err != nil {
				t.Fatalf("open %s: %v", field, err)
			}
			defer f.Close()
			b, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("read %s: %v", field, err)
			}
			return b
		}
		gotManifest = readPart("manifest")
		gotWASM = readPart("wasm")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u := httpUploader{Client: srv.Client()}
	if err := u.Upload(context.Background(), srv.URL, dir); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if gotPath != "/_/plugins/dev/install" {
		t.Errorf("path = %q; want /_/plugins/dev/install", gotPath)
	}
	if string(gotManifest) != manifest {
		t.Errorf("manifest part != source")
	}
	if string(gotWASM) != string(wasm) {
		t.Errorf("wasm part != source")
	}
}

func TestHTTPUploader_HostWithTrailingSlash(t *testing.T) {
	dir := writeDevProject(t, `{}`, []byte{0x00, 0x61, 0x73, 0x6d})
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u := httpUploader{Client: srv.Client()}
	// Append a trailing slash to make sure joinURL handles it.
	if err := u.Upload(context.Background(), srv.URL+"/", dir); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotPath != "/_/plugins/dev/install" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestHTTPUploader_HostSubpath(t *testing.T) {
	dir := writeDevProject(t, `{}`, []byte{0x00, 0x61, 0x73, 0x6d})
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u := httpUploader{Client: srv.Client()}
	if err := u.Upload(context.Background(), srv.URL+"/edge", dir); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotPath != "/edge/_/plugins/dev/install" {
		t.Errorf("path = %q; want /edge/_/plugins/dev/install", gotPath)
	}
}

func TestHTTPUploader_PropagatesHostError(t *testing.T) {
	dir := writeDevProject(t, `{}`, []byte{0x00, 0x61, 0x73, 0x6d})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "manifest rejected: missing name", http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	u := httpUploader{Client: srv.Client()}
	err := u.Upload(context.Background(), srv.URL, dir)
	if err == nil {
		t.Fatalf("want error on 422")
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "manifest rejected") {
		t.Errorf("error %q missing status / body preview", err)
	}
}

func TestHTTPUploader_MissingWASM(t *testing.T) {
	dir := t.TempDir()
	// Write only the manifest — no build/plugin.wasm.
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	u := httpUploader{}
	err := u.Upload(context.Background(), "http://example.invalid", dir)
	if err == nil {
		t.Fatalf("want error when wasm is missing")
	}
	if !strings.Contains(err.Error(), "read wasm") {
		t.Errorf("error %q does not mention wasm", err)
	}
}

func TestHTTPUploader_RejectsRelativeHost(t *testing.T) {
	dir := writeDevProject(t, `{}`, []byte{0x00, 0x61, 0x73, 0x6d})
	u := httpUploader{}
	err := u.Upload(context.Background(), "/relative/path", dir)
	if err == nil {
		t.Fatalf("want error for relative host")
	}
}

func TestJoinURL_Errors(t *testing.T) {
	if _, err := joinURL("://", devInstallPath); err == nil {
		t.Errorf("want parse error for malformed scheme")
	}
	if _, err := joinURL("not-a-url", devInstallPath); err == nil {
		t.Errorf("want error for missing scheme/host")
	}
}
