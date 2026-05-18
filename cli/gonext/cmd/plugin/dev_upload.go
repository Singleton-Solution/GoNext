package plugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Uploader abstracts the host-install POST so the orchestrator can be
// unit-tested without a real network round-trip. Production wires this
// to [httpUploader].
type Uploader interface {
	// Upload reads <projectDir>/build/plugin.wasm and
	// <projectDir>/manifest.json, packages them as multipart/form-data,
	// and POSTs to ${host}/_/plugins/dev/install.
	//
	// The manifest is sent as a form field; the WASM as a file part.
	// The host endpoint is responsible for validating the manifest
	// (the lifecycle Manager already does this on Install) and
	// activating the plugin.
	//
	// Returning a non-nil error fails the upload phase. In watch mode
	// the dev loop swallows that error and waits for the next change.
	Upload(ctx context.Context, host, projectDir string) error
}

// httpUploader is the production [Uploader]. It does not retry —
// transient network errors are surfaced so the operator sees them; in
// watch mode the next save triggers a fresh attempt.
type httpUploader struct {
	Client *http.Client
}

// devInstallPath is the URL path appended to --host for the dev install
// endpoint. The host-side implementation lives in a follow-up issue;
// see the PR description for the contract this CLI assumes.
const devInstallPath = "/_/plugins/dev/install"

// Upload satisfies [Uploader].
func (u httpUploader) Upload(ctx context.Context, host, projectDir string) error {
	body, contentType, err := buildUploadBody(projectDir)
	if err != nil {
		return err
	}

	target, err := joinURL(host, devInstallPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", target, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("dev host returned %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return nil
}

// buildUploadBody constructs the multipart/form-data body the dev
// install endpoint expects. The shape is:
//
//	wasm      — file part, the compiled WASM module (build/plugin.wasm)
//	manifest  — file part, the canonical manifest.json bytes
//
// We send both as file parts (rather than one as a form value) so the
// host can stream-validate the manifest before reading the larger WASM
// payload — symmetric to how the .gnplugin archive layout works in
// production.
func buildUploadBody(projectDir string) (*bytes.Buffer, string, error) {
	wasmPath := filepath.Join(projectDir, "build", "plugin.wasm")
	manifestPath := filepath.Join(projectDir, "manifest.json")

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, "", fmt.Errorf("read wasm: %w", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", fmt.Errorf("read manifest: %w", err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := addFilePart(w, "manifest", "manifest.json", "application/json", manifestBytes); err != nil {
		return nil, "", err
	}
	if err := addFilePart(w, "wasm", "plugin.wasm", "application/wasm", wasmBytes); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return &buf, w.FormDataContentType(), nil
}

// addFilePart writes one file-part to w with an explicit Content-Type
// header. multipart.Writer.CreateFormFile uses application/octet-stream
// by default — overriding lets the host route on Content-Type without
// sniffing.
func addFilePart(w *multipart.Writer, field, filename, contentType string, body []byte) error {
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename),
	}
	h["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("create %s part: %w", field, err)
	}
	if _, err := part.Write(body); err != nil {
		return fmt.Errorf("write %s part: %w", field, err)
	}
	return nil
}

// joinURL safely appends path to host, tolerating a trailing slash on
// host. We don't use net/url's ResolveReference because it has subtle
// edge cases when host's path is "" vs "/".
func joinURL(host, path string) (string, error) {
	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse --host: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("--host %q is not an absolute URL", host)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}
