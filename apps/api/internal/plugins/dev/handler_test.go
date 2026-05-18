package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
)

// validManifestJSON is a syntactically valid gonext.io/v1 manifest used
// by the happy-path tests. Tests that need to vary a single field
// rewrite the JSON literal in place — there's no helper function
// because the rewrites are short and grouping them inline keeps test
// intent obvious.
const validManifestJSON = `{
  "apiVersion": "gonext.io/v1",
  "name": "gn-dev-test",
  "version": "1.0.0",
  "entry": "build/plugin.wasm",
  "capabilities": ["kv.read", "audit.emit"]
}`

// fakeWASM is a placeholder WASM payload. The lifecycle Manager in
// these tests uses NoopRuntime, so it never executes the bytes; any
// non-empty payload satisfies the size + presence checks.
var fakeWASM = []byte("\x00asm\x01\x00\x00\x00")

// devToken is the shared token used across tests. Picking a long
// value matches the recommended operator setup.
const devToken = "dev-token-xxxxxxxxxxxxxxxxxxxxxxxxxxx"

// newTestManager builds a Manager whose dependencies are all in-memory
// so the test runs without a database or filesystem.
func newTestManager(t *testing.T) *lifecycle.Manager {
	t.Helper()
	return lifecycle.NewManager(
		lifecycle.NewMemoryStorage(),
		audit.NewEmitter(audit.NewMemoryStore()),
	)
}

// buildMultipart constructs a multipart/form-data body with one or
// both of the parts the dev surface expects. Passing nil for a payload
// omits that part entirely — used by the "missing part" tests.
func buildMultipart(t *testing.T, manifest, wasm []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if manifest != nil {
		mfw, err := w.CreateFormFile("manifest", "manifest.json")
		if err != nil {
			t.Fatalf("create manifest part: %v", err)
		}
		if _, err := mfw.Write(manifest); err != nil {
			t.Fatalf("write manifest part: %v", err)
		}
	}
	if wasm != nil {
		ww, err := w.CreateFormFile("wasm", "plugin.wasm")
		if err != nil {
			t.Fatalf("create wasm part: %v", err)
		}
		if _, err := ww.Write(wasm); err != nil {
			t.Fatalf("write wasm part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// doRequest executes a multipart POST against handler with the given
// token (or empty for no header).
func doRequest(handler http.Handler, body io.Reader, contentType, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/_/plugins/dev/install", body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Dev-Token", token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestHandler_HappyPath_Install(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp uploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rr.Body.String())
	}
	if resp.Action != actionInstalled {
		t.Errorf("action: got %q, want %q", resp.Action, actionInstalled)
	}
	if resp.Plugin.Name != "gn-dev-test" {
		t.Errorf("plugin.name: got %q, want %q", resp.Plugin.Name, "gn-dev-test")
	}
	if resp.Plugin.Version != "1.0.0" {
		t.Errorf("plugin.version: got %q, want %q", resp.Plugin.Version, "1.0.0")
	}
	if resp.Warnings == nil {
		t.Errorf("warnings: got nil slice, want empty array")
	}

	// Verify via lifecycle that the plugin was actually installed +
	// activated. This is the contract the PR description called out.
	p, err := mgr.Get(context.Background(), "gn-dev-test")
	if err != nil {
		t.Fatalf("lifecycle.Get: %v", err)
	}
	if p.State != lifecycle.StateActive {
		t.Errorf("state: got %q, want %q", p.State, lifecycle.StateActive)
	}
	if got, want := len(resp.Capabilities), 2; got != want {
		t.Errorf("capabilities length: got %d, want %d (got %v)", got, want, resp.Capabilities)
	}
}

func TestHandler_HappyPath_Reload(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	// First upload installs.
	body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr := doRequest(h, body, ct, devToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("first upload: got %d, body: %s", rr.Code, rr.Body.String())
	}

	// Capture row state for later diff.
	first, err := mgr.Get(context.Background(), "gn-dev-test")
	if err != nil {
		t.Fatalf("Get after first upload: %v", err)
	}

	// Second upload reloads (same name, bumped version).
	manifest2 := strings.Replace(validManifestJSON, `"version": "1.0.0"`, `"version": "1.0.1"`, 1)
	body2, ct2 := buildMultipart(t, []byte(manifest2), fakeWASM)
	rr2 := doRequest(h, body2, ct2, devToken)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second upload: got %d, body: %s", rr2.Code, rr2.Body.String())
	}
	var resp uploadResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Action != actionReloaded {
		t.Errorf("action: got %q, want %q", resp.Action, actionReloaded)
	}
	if resp.Plugin.Version != "1.0.1" {
		t.Errorf("plugin.version: got %q, want %q", resp.Plugin.Version, "1.0.1")
	}

	// Lifecycle: still active, single row, new version.
	got, err := mgr.Get(context.Background(), "gn-dev-test")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.State != lifecycle.StateActive {
		t.Errorf("state after reload: got %q, want %q", got.State, lifecycle.StateActive)
	}
	if got.Version != "1.0.1" {
		t.Errorf("row version after reload: got %q, want %q", got.Version, "1.0.1")
	}

	// List: exactly one row, not two — proves no duplicate registration.
	all, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("row count after reload: got %d, want 1; rows=%+v", len(all), all)
	}

	_ = first // included for future diff-style assertions if the row shape grows.
}

func TestHandler_ManifestSchemaFail_422(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	// Drop the required `name` field → schema error.
	badManifest := `{
  "apiVersion": "gonext.io/v1",
  "version": "1.0.0",
  "entry": "build/plugin.wasm"
}`
	body, ct := buildMultipart(t, []byte(badManifest), fakeWASM)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422; body: %s", rr.Code, rr.Body.String())
	}
	var resp errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, rr.Body.String())
	}
	if resp.Code != codeManifestInvalid {
		t.Errorf("code: got %q, want %q", resp.Code, codeManifestInvalid)
	}
	if len(resp.Errors) == 0 {
		t.Errorf("expected at least one validation error, got none")
	}
}

func TestHandler_WASMTooLarge_413(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	// MaxWASMBytes + 1 byte → 413.
	big := make([]byte, MaxWASMBytes+1)
	body, ct := buildMultipart(t, []byte(validManifestJSON), big)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body: %s", rr.Code, rr.Body.String())
	}
	var resp errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != codePayloadTooLarge {
		t.Errorf("code: got %q, want %q", resp.Code, codePayloadTooLarge)
	}
}

func TestHandler_ManifestTooLarge_413(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	// Make the manifest part itself exceed the cap. We pad a valid
	// manifest with whitespace so it still parses if it ever got that
	// far (it won't — the cap kicks in first).
	pad := strings.Repeat(" ", MaxManifestBytes)
	bigManifest := validManifestJSON + pad
	body, ct := buildMultipart(t, []byte(bigManifest), fakeWASM)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_MissingManifestPart_400(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	body, ct := buildMultipart(t, nil, fakeWASM)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
	var resp errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Message, "manifest") {
		t.Errorf("message should mention manifest: %q", resp.Message)
	}
}

func TestHandler_MissingWASMPart_400(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	body, ct := buildMultipart(t, []byte(validManifestJSON), nil)
	rr := doRequest(h, body, ct, devToken)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_WrongToken_401(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr := doRequest(h, body, ct, "wrong-token-value-very-different-len")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body: %s", rr.Code, rr.Body.String())
	}
	var resp errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != codeUnauthorized {
		t.Errorf("code: got %q, want %q", resp.Code, codeUnauthorized)
	}
}

func TestHandler_MissingTokenHeader_401(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr := doRequest(h, body, ct, "")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rr.Code)
	}
}

func TestHandler_EmptyConfiguredToken_AlwaysRejects(t *testing.T) {
	mgr := newTestManager(t)
	// DevMode on, but DevToken empty: handler must refuse every
	// request, including ones that send no header.
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: ""}, mgr)

	body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr := doRequest(h, body, ct, "anything-here")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("with token: got %d, want 401", rr.Code)
	}

	body2, ct2 := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
	rr2 := doRequest(h, body2, ct2, "")
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("without token: got %d, want 401", rr2.Code)
	}
}

func TestHandler_NotMultipart_400(t *testing.T) {
	mgr := newTestManager(t)
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, mgr)

	req := httptest.NewRequest(http.MethodPost, "/_/plugins/dev/install",
		strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Dev-Token", devToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

// fakeMgr is a Manager that delays Install by gating it on a channel.
// Used by the concurrent-reload race test to keep the in-flight slot
// held while the test fires N more requests against the same slug.
type fakeMgr struct {
	mu          sync.Mutex
	rows        map[string]lifecycle.Plugin
	installs    int32
	gate        chan struct{} // signals when Install should proceed
	gateArrived chan struct{} // signals the test that Install has entered
}

func newFakeMgr() *fakeMgr {
	return &fakeMgr{
		rows:        map[string]lifecycle.Plugin{},
		gate:        make(chan struct{}),
		gateArrived: make(chan struct{}, 1),
	}
}

func (f *fakeMgr) Get(_ context.Context, slug string) (lifecycle.Plugin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[slug]
	if !ok {
		return lifecycle.Plugin{}, fmt.Errorf("%w: %q", lifecycle.ErrNotFound, slug)
	}
	return p, nil
}

func (f *fakeMgr) Install(_ context.Context, _ io.Reader) (string, error) {
	atomic.AddInt32(&f.installs, 1)
	// Signal arrival exactly once (buffered channel size 1).
	select {
	case f.gateArrived <- struct{}{}:
	default:
	}
	<-f.gate
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows["gn-dev-test"] = lifecycle.Plugin{
		Slug: "gn-dev-test", Version: "1.0.0", State: lifecycle.StateInstalled,
	}
	return "gn-dev-test", nil
}

func (f *fakeMgr) Activate(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.rows[slug]
	p.State = lifecycle.StateActive
	f.rows[slug] = p
	return nil
}

func (f *fakeMgr) Deactivate(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.rows[slug]
	p.State = lifecycle.StateInactive
	f.rows[slug] = p
	return nil
}

func (f *fakeMgr) Uninstall(_ context.Context, slug string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, slug)
	return nil
}

func TestHandler_ConcurrentReload_OneWinsRest409(t *testing.T) {
	const N = 10

	fm := newFakeMgr()
	h := Mount(config.PluginsConfig{DevMode: true, DevToken: devToken}, fm)

	var wg sync.WaitGroup
	results := make(chan int, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, ct := buildMultipart(t, []byte(validManifestJSON), fakeWASM)
			rr := doRequest(h, body, ct, devToken)
			results <- rr.Code
		}()
	}

	// Wait for the first goroutine to enter Install (and thus hold
	// the per-slug inflight slot). All other goroutines must by
	// definition hit the LoadOrStore-already-present branch — but
	// only after they finish parsing multipart + validating manifest.
	// We poll until N-1 fast-path losers have completed; the one
	// remaining goroutine is the winner blocked on f.gate. This is
	// race-free because the inflight slot is held until installOrReload
	// returns, which can't happen until we close the gate below.
	<-fm.gateArrived

	deadline := time.Now().Add(5 * time.Second)
	losers := make([]int, 0, N-1)
	for len(losers) < N-1 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d losers; got %d", N-1, len(losers))
		}
		select {
		case code := <-results:
			losers = append(losers, code)
		default:
			runtime.Gosched()
		}
	}
	close(fm.gate)
	wg.Wait()

	// Drain the winning response (the one that was blocked in Install).
	var winner int
	select {
	case winner = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for winner response")
	}
	close(results)

	var ok, conflict, other int
	for _, code := range append(losers, winner) {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			other++
		}
	}
	if ok != 1 {
		t.Errorf("expected exactly one 200, got %d (conflict=%d, other=%d)", ok, conflict, other)
	}
	if conflict != N-1 {
		t.Errorf("expected %d conflict responses, got %d (ok=%d, other=%d)", N-1, conflict, ok, other)
	}
	if installs := atomic.LoadInt32(&fm.installs); installs != 1 {
		t.Errorf("Install call count: got %d, want 1", installs)
	}
}

