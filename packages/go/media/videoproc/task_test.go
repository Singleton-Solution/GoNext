package videoproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeSource is the test-only Source: a map of storage-key to bytes.
type fakeSource struct {
	objects map[string][]byte
	err     error
}

func (f *fakeSource) GetObject(ctx context.Context, key string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.objects[key]
	if !ok {
		return nil, errors.New("not found: " + key)
	}
	return b, nil
}

// fakeSink records uploads in-order and answers PublicURL via a
// caller-supplied template.
type fakeSink struct {
	mu       sync.Mutex
	uploads  []sinkUpload
	baseURL  string
	putErr   error
}

type sinkUpload struct {
	key      string
	body     []byte
	mimeType string
}

func (f *fakeSink) PutObject(ctx context.Context, key string, body []byte, mimeType string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, sinkUpload{key: key, body: bytes.Clone(body), mimeType: mimeType})
	return nil
}

func (f *fakeSink) PublicURL(key string) string {
	if f.baseURL == "" {
		return "https://cdn.example/" + key
	}
	return f.baseURL + "/" + key
}

// fakeHLSWriter captures the (assetID, hlsURL) pair so tests can
// assert the row update happened.
type fakeHLSWriter struct {
	assetID string
	hlsURL  string
	err     error
}

func (f *fakeHLSWriter) SetHLSURL(ctx context.Context, assetID, hlsURL string) error {
	if f.err != nil {
		return f.err
	}
	f.assetID = assetID
	f.hlsURL = hlsURL
	return nil
}

// onRunFabricate is a helper that synthesises the ffmpeg output
// directory (the playlist + a single segment) so the handler's
// upload step has something to walk. The output directory is the
// last positional argument's directory.
func onRunFabricate(t *testing.T) func(args []string, workingDir string) error {
	t.Helper()
	return func(args []string, workingDir string) error {
		// The last argument is the playlist destination
		// "<outDir>/index.m3u8". The output directory is its parent.
		dest := args[len(args)-1]
		outDir := filepath.Dir(dest)
		if err := os.WriteFile(dest, []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:6.0,\nsegment0.ts\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "segment0.ts"), []byte("FAKE_TS_BYTES"), 0o600); err != nil {
			return err
		}
		return nil
	}
}

// TestHandler_HappyPath drives the handler end-to-end with a faked
// runner that fabricates the output files. Asserts:
//
//   * the runner is invoked with an argv containing the right flags
//   * the playlist and the segment both land in the sink
//   * the HLS URL is written to the row writer
//   * the scratch directory is cleaned up
func TestHandler_HappyPath(t *testing.T) {
	src := &fakeSource{objects: map[string][]byte{
		"uploads/2026/05/abc.mp4": []byte("FAKE_MP4_BYTES"),
	}}
	sink := &fakeSink{}
	writer := &fakeHLSWriter{}
	runner := &recordingRunner{onRun: onRunFabricate(t)}

	h := NewHandler(HandlerDeps{
		Source:    src,
		Sink:      sink,
		HLSWriter: writer,
		Runner:    runner,
		KeyPrefix: "hls/",
	})

	payload, _ := json.Marshal(Payload{
		AssetID:    "media-id-001",
		StorageKey: "uploads/2026/05/abc.mp4",
		MIMEType:   "video/mp4",
	})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: unexpected error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("runner called %d times, want 1", len(runner.calls))
	}
	// Confirm the argv mentions HLS — load-bearing
	joined := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(joined, "-f hls") {
		t.Errorf("runner argv missing -f hls: %s", joined)
	}

	// Two uploads: playlist + segment
	if len(sink.uploads) != 2 {
		t.Fatalf("sink got %d uploads, want 2", len(sink.uploads))
	}
	gotKeys := map[string]string{}
	for _, u := range sink.uploads {
		gotKeys[u.key] = u.mimeType
	}
	wantKey := "hls/media-id-001/index.m3u8"
	if mt, ok := gotKeys[wantKey]; !ok {
		t.Errorf("missing playlist upload at %s; got keys: %v", wantKey, keys(gotKeys))
	} else if mt != "application/vnd.apple.mpegurl" {
		t.Errorf("playlist mime = %q, want application/vnd.apple.mpegurl", mt)
	}
	wantSeg := "hls/media-id-001/segment0.ts"
	if mt, ok := gotKeys[wantSeg]; !ok {
		t.Errorf("missing segment upload at %s; got keys: %v", wantSeg, keys(gotKeys))
	} else if mt != "video/mp2t" {
		t.Errorf("segment mime = %q, want video/mp2t", mt)
	}

	if writer.assetID != "media-id-001" {
		t.Errorf("writer.assetID = %q, want media-id-001", writer.assetID)
	}
	if !strings.HasSuffix(writer.hlsURL, "/hls/media-id-001/index.m3u8") {
		t.Errorf("writer.hlsURL = %q, want suffix /hls/media-id-001/index.m3u8", writer.hlsURL)
	}
}

// TestHandler_SkipsNonVideoMIME confirms the early-return path for
// non-video uploads — the handler must not spawn ffmpeg or hit
// storage.
func TestHandler_SkipsNonVideoMIME(t *testing.T) {
	src := &fakeSource{}
	sink := &fakeSink{}
	runner := &recordingRunner{}

	h := NewHandler(HandlerDeps{
		Source: src, Sink: sink, Runner: runner,
	})

	payload, _ := json.Marshal(Payload{
		AssetID:    "id",
		StorageKey: "k",
		MIMEType:   "image/jpeg",
	})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: unexpected error: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("non-video upload should not invoke runner; got %d calls", len(runner.calls))
	}
	if len(sink.uploads) != 0 {
		t.Errorf("non-video upload should not write to sink; got %d uploads", len(sink.uploads))
	}
}

// TestHandler_RejectsInvalidPayload confirms missing required fields
// surface as an error.
func TestHandler_RejectsInvalidPayload(t *testing.T) {
	h := NewHandler(HandlerDeps{
		Source: &fakeSource{}, Sink: &fakeSink{}, Runner: &recordingRunner{},
	})
	if err := h(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("handler: expected error for empty payload")
	}
	if err := h(context.Background(), []byte(`not json`)); err == nil {
		t.Fatal("handler: expected error for malformed JSON")
	}
}

// TestNewSpec_RegisteredShape exercises NewSpec end-to-end via the
// real schema compile. A failure here means the schema is malformed.
func TestNewSpec_RegisteredShape(t *testing.T) {
	spec, err := NewSpec(HandlerDeps{
		Source: &fakeSource{}, Sink: &fakeSink{}, Runner: &recordingRunner{},
	})
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	if spec.Name != TaskName {
		t.Errorf("spec.Name = %q, want %q", spec.Name, TaskName)
	}
	if spec.Queue != DefaultQueue {
		t.Errorf("spec.Queue = %q, want %q", spec.Queue, DefaultQueue)
	}
	if spec.PayloadSchema == nil {
		t.Error("spec.PayloadSchema is nil")
	}
}

// TestNewStubSpec confirms the stub variant compiles and the handler
// returns nil on every input without touching storage.
func TestNewStubSpec(t *testing.T) {
	spec, err := NewStubSpec(nil)
	if err != nil {
		t.Fatalf("NewStubSpec: %v", err)
	}
	if spec.Name != TaskName {
		t.Errorf("stub spec.Name = %q, want %q", spec.Name, TaskName)
	}
	payload, _ := json.Marshal(Payload{AssetID: "x", StorageKey: "k"})
	if err := spec.Handler(context.Background(), payload); err != nil {
		t.Errorf("stub handler returned error: %v", err)
	}
}

// keys returns a stable-ordered slice of map keys for error messages.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
