package pdfproc

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

// recordingRunner captures every Run invocation and optionally
// fabricates output via onRun.
type recordingRunner struct {
	mu    sync.Mutex
	calls []recordedCall
	err   error
	onRun func(binary string, args []string) error
}

type recordedCall struct {
	binary string
	args   []string
}

func (r *recordingRunner) Run(ctx context.Context, binary string, args []string) error {
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{binary: binary, args: append([]string(nil), args...)})
	r.mu.Unlock()
	if r.onRun != nil {
		if err := r.onRun(binary, args); err != nil {
			return err
		}
	}
	return r.err
}

// fakeSource is a Source backed by an in-memory map.
type fakeSource struct{ objects map[string][]byte }

func (f *fakeSource) GetObject(ctx context.Context, key string) ([]byte, error) {
	if b, ok := f.objects[key]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}

// fakeSink captures uploads.
type fakeSink struct {
	mu      sync.Mutex
	uploads []sinkUpload
}

type sinkUpload struct {
	key  string
	body []byte
	mime string
}

func (f *fakeSink) PutObject(ctx context.Context, key string, body []byte, mime string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, sinkUpload{key: key, body: bytes.Clone(body), mime: mime})
	return nil
}

func (f *fakeSink) PublicURL(key string) string { return "https://cdn.example/" + key }

// fakeTextWriter captures the persisted text for assertion.
type fakeTextWriter struct {
	assetID string
	text    string
	err     error
}

func (f *fakeTextWriter) SetText(ctx context.Context, id, text string) error {
	if f.err != nil {
		return f.err
	}
	f.assetID = id
	f.text = text
	return nil
}

// fakeThumbnailWriter captures the thumbnail key.
type fakeThumbnailWriter struct {
	assetID string
	key     string
}

func (f *fakeThumbnailWriter) SetPDFThumbnail(ctx context.Context, id, key string) error {
	f.assetID = id
	f.key = key
	return nil
}

// onRunFabricate emits the pdftoppm-style "<prefix>-1.png" image and
// the pdftotext-style "text.txt" file. The job directory is inferred
// from the args (last positional arg for both binaries is the
// output target).
func onRunFabricate(t *testing.T, withText, withImage bool) func(binary string, args []string) error {
	t.Helper()
	return func(binary string, args []string) error {
		switch binary {
		case PDFToPPMBinary:
			if !withImage {
				return nil
			}
			// Last arg is "<output_dir>/<prefix>"; append "-1.png"
			dest := args[len(args)-1] + "-1.png"
			return os.WriteFile(dest, []byte("FAKE_PNG"), 0o600)
		case PDFCPUBinary:
			if !withImage {
				return nil
			}
			outDir := args[len(args)-1]
			return os.WriteFile(filepath.Join(outDir, "page1.png"), []byte("FAKE_PNG"), 0o600)
		case PDFToTextBinary:
			if !withText {
				return nil
			}
			// Last arg is the output path.
			return os.WriteFile(args[len(args)-1], []byte("EXTRACTED TEXT CONTENT"), 0o600)
		}
		return nil
	}
}

// TestHandler_FullyAvailable drives the handler with both binaries
// "installed" and both outputs fabricated. Asserts both pipelines
// fire.
func TestHandler_FullyAvailable(t *testing.T) {
	src := &fakeSource{objects: map[string][]byte{
		"uploads/doc.pdf": []byte("FAKE_PDF_BYTES"),
	}}
	sink := &fakeSink{}
	text := &fakeTextWriter{}
	thumb := &fakeThumbnailWriter{}
	runner := &recordingRunner{onRun: onRunFabricate(t, true, true)}

	h := NewHandler(HandlerDeps{
		Source:          src,
		Sink:            sink,
		TextWriter:      text,
		ThumbnailWriter: thumb,
		Runner:          runner,
		Availability: Availability{
			PDFToPPMPath:  "/usr/bin/pdftoppm",
			PDFToTextPath: "/usr/bin/pdftotext",
		},
		KeyPrefix: "pdf-thumbs/",
	})

	payload, _ := json.Marshal(Payload{
		AssetID:    "asset-001",
		StorageKey: "uploads/doc.pdf",
		MIMEType:   "application/pdf",
	})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: unexpected error: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("runner called %d times, want 2 (pdftoppm + pdftotext)", len(runner.calls))
	}
	if len(sink.uploads) != 1 {
		t.Fatalf("sink got %d uploads, want 1 (thumbnail)", len(sink.uploads))
	}
	if !strings.HasSuffix(sink.uploads[0].key, "/thumb.png") {
		t.Errorf("thumbnail key = %q, want suffix /thumb.png", sink.uploads[0].key)
	}
	if sink.uploads[0].mime != "image/png" {
		t.Errorf("thumbnail mime = %q, want image/png", sink.uploads[0].mime)
	}
	if text.assetID != "asset-001" {
		t.Errorf("text.assetID = %q, want asset-001", text.assetID)
	}
	if text.text != "EXTRACTED TEXT CONTENT" {
		t.Errorf("text.text = %q, want EXTRACTED TEXT CONTENT", text.text)
	}
	if thumb.assetID != "asset-001" {
		t.Errorf("thumb.assetID = %q, want asset-001", thumb.assetID)
	}
}

// TestHandler_PDFCPUFallback confirms that when pdftoppm is absent but
// pdfcpu is present, the runner is invoked with the pdfcpu argv.
func TestHandler_PDFCPUFallback(t *testing.T) {
	src := &fakeSource{objects: map[string][]byte{
		"uploads/doc.pdf": []byte("FAKE_PDF"),
	}}
	sink := &fakeSink{}
	runner := &recordingRunner{onRun: onRunFabricate(t, false, true)}

	h := NewHandler(HandlerDeps{
		Source: src, Sink: sink, Runner: runner,
		Availability: Availability{
			PDFCPUPath: "/usr/bin/pdfcpu",
			// No pdftoppm and no pdftotext
		},
	})
	payload, _ := json.Marshal(Payload{
		AssetID:    "id-1",
		StorageKey: "uploads/doc.pdf",
		MIMEType:   "application/pdf",
	})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	if runner.calls[0].binary != PDFCPUBinary {
		t.Errorf("first call binary = %q, want %q", runner.calls[0].binary, PDFCPUBinary)
	}
	if len(sink.uploads) != 1 {
		t.Errorf("sink uploads = %d, want 1", len(sink.uploads))
	}
}

// TestHandler_TextOnly confirms that when no rendering binary is
// available but pdftotext is, only the text extraction step runs.
func TestHandler_TextOnly(t *testing.T) {
	src := &fakeSource{objects: map[string][]byte{
		"uploads/doc.pdf": []byte("PDF"),
	}}
	sink := &fakeSink{}
	text := &fakeTextWriter{}
	runner := &recordingRunner{onRun: onRunFabricate(t, true, false)}

	h := NewHandler(HandlerDeps{
		Source: src, Sink: sink, Runner: runner, TextWriter: text,
		Availability: Availability{PDFToTextPath: "/usr/bin/pdftotext"},
	})
	payload, _ := json.Marshal(Payload{AssetID: "x", StorageKey: "uploads/doc.pdf", MIMEType: "application/pdf"})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(sink.uploads) != 0 {
		t.Errorf("sink uploads = %d, want 0 (no rendering)", len(sink.uploads))
	}
	if text.text == "" {
		t.Error("text writer not invoked")
	}
}

// TestHandler_FullyUnavailable confirms a host with no binaries
// returns nil (skip-graceful) without spawning anything or writing.
func TestHandler_FullyUnavailable(t *testing.T) {
	src := &fakeSource{objects: map[string][]byte{"k": []byte("X")}}
	sink := &fakeSink{}
	runner := &recordingRunner{}

	h := NewHandler(HandlerDeps{
		Source: src, Sink: sink, Runner: runner,
		Availability: Availability{}, // empty: nothing on PATH
	})
	payload, _ := json.Marshal(Payload{AssetID: "id", StorageKey: "k", MIMEType: "application/pdf"})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner should not be called when no binaries available; got %d calls", len(runner.calls))
	}
	if len(sink.uploads) != 0 {
		t.Errorf("sink should not see uploads; got %d", len(sink.uploads))
	}
}

// TestHandler_SkipsNonPDF confirms non-PDF MIME types short-circuit.
func TestHandler_SkipsNonPDF(t *testing.T) {
	runner := &recordingRunner{}
	h := NewHandler(HandlerDeps{
		Source: &fakeSource{}, Sink: &fakeSink{}, Runner: runner,
		Availability: Availability{PDFToPPMPath: "/x", PDFToTextPath: "/y"},
	})
	payload, _ := json.Marshal(Payload{AssetID: "id", StorageKey: "k", MIMEType: "image/png"})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("non-PDF should not invoke runner")
	}
}

// TestHandler_TextTruncation confirms the size cap is enforced.
func TestHandler_TextTruncation(t *testing.T) {
	big := strings.Repeat("a", 1024)
	src := &fakeSource{objects: map[string][]byte{"k": []byte("PDF")}}
	sink := &fakeSink{}
	text := &fakeTextWriter{}
	runner := &recordingRunner{onRun: func(binary string, args []string) error {
		if binary == PDFToTextBinary {
			return os.WriteFile(args[len(args)-1], []byte(big), 0o600)
		}
		return nil
	}}
	h := NewHandler(HandlerDeps{
		Source: src, Sink: sink, Runner: runner, TextWriter: text,
		Availability: Availability{PDFToTextPath: "/x"},
		MaxTextBytes: 100,
	})
	payload, _ := json.Marshal(Payload{AssetID: "id", StorageKey: "k", MIMEType: "application/pdf"})
	if err := h(context.Background(), payload); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(text.text) != 100 {
		t.Errorf("text length = %d, want 100 (truncated)", len(text.text))
	}
}

// TestHandler_RejectsInvalidPayload confirms missing fields error.
func TestHandler_RejectsInvalidPayload(t *testing.T) {
	h := NewHandler(HandlerDeps{
		Source: &fakeSource{}, Sink: &fakeSink{}, Runner: &recordingRunner{},
	})
	if err := h(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

// TestProbe just exercises the function — it doesn't assert truth or
// falsity (the test machine may or may not have any of the binaries).
func TestProbe(t *testing.T) {
	a := Probe()
	_ = a.CanRender()
	_ = a.CanExtractText()
}

// TestNewSpec confirms the spec compiles cleanly with the bundled
// schema.
func TestNewSpec(t *testing.T) {
	spec, err := NewSpec(HandlerDeps{
		Source: &fakeSource{}, Sink: &fakeSink{}, Runner: &recordingRunner{},
	})
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	if spec.Name != TaskName {
		t.Errorf("spec.Name = %q, want %q", spec.Name, TaskName)
	}
	if spec.PayloadSchema == nil {
		t.Error("schema is nil")
	}
}

// TestNewStubSpec confirms the stub handler returns nil and the
// spec validates as expected.
func TestNewStubSpec(t *testing.T) {
	spec, err := NewStubSpec(nil)
	if err != nil {
		t.Fatalf("NewStubSpec: %v", err)
	}
	payload, _ := json.Marshal(Payload{AssetID: "id", StorageKey: "k"})
	if err := spec.Handler(context.Background(), payload); err != nil {
		t.Errorf("stub handler error: %v", err)
	}
}

// TestIsSupportedMIME pins the accepted MIME types.
func TestIsSupportedMIME(t *testing.T) {
	for _, tt := range []struct {
		mime string
		want bool
	}{
		{"application/pdf", true},
		{"APPLICATION/PDF", true},
		{" application/pdf ", true},
		{"application/x-pdf", false},
		{"image/jpeg", false},
		{"", false},
	} {
		if got := IsSupportedMIME(tt.mime); got != tt.want {
			t.Errorf("IsSupportedMIME(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}
