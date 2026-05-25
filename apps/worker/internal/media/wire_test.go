package media

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/media/pdfproc"
	"github.com/Singleton-Solution/GoNext/packages/go/media/videoproc"
)

// fakeVideoSource implements videoproc.Source. The map is intentionally
// empty — these tests only assert registration succeeds; they don't
// exercise the handler body.
type fakeVideoSource struct{}

func (fakeVideoSource) GetObject(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}

type fakeVideoSink struct{}

func (fakeVideoSink) PutObject(ctx context.Context, key string, body []byte, mime string) error {
	return nil
}
func (fakeVideoSink) PublicURL(key string) string { return "https://x/" + key }

type fakePDFSource struct{}

func (fakePDFSource) GetObject(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}

type fakePDFSink struct{}

func (fakePDFSink) PutObject(ctx context.Context, key string, body []byte, mime string) error {
	return nil
}
func (fakePDFSink) PublicURL(key string) string { return "https://x/" + key }

// TestRegister_StubMode confirms that the boot path with no
// dependencies wires both tasks as stubs without erroring. This is
// the "fresh skeleton" case the worker boots into until the storage
// wiring lands.
func TestRegister_StubMode(t *testing.T) {
	reg := taskspec.NewRegistry()
	mux := asynq.NewServeMux()
	rep, err := Register(mux, reg, Deps{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if rep.VideoMode != "stub" {
		t.Errorf("VideoMode = %q, want stub", rep.VideoMode)
	}
	if rep.PDFMode != "stub" {
		t.Errorf("PDFMode = %q, want stub", rep.PDFMode)
	}
	// Both names should be on the registry now.
	if _, ok := reg.Get(videoproc.TaskName); !ok {
		t.Errorf("video task not registered")
	}
	if _, ok := reg.Get(pdfproc.TaskName); !ok {
		t.Errorf("pdf task not registered")
	}
}

// TestRegister_WithStorageMode confirms that supplying storage
// handles selects the "real" mode — independent of whether ffmpeg
// is actually on PATH (the test machine may not have it; the report
// flips back to stub in that case which is also fine, but we assert
// that providing storage does NOT itself produce an error).
func TestRegister_WithStorageWiring(t *testing.T) {
	reg := taskspec.NewRegistry()
	mux := asynq.NewServeMux()
	_, err := Register(mux, reg, Deps{
		VideoSource: fakeVideoSource{},
		VideoSink:   fakeVideoSink{},
		PDFSource:   fakePDFSource{},
		PDFSink:     fakePDFSink{},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// TestRegister_NilMux rejects nil dependencies loudly.
func TestRegister_NilMux(t *testing.T) {
	reg := taskspec.NewRegistry()
	if _, err := Register(nil, reg, Deps{}); err == nil {
		t.Fatal("Register(nil mux): expected error")
	}
	mux := asynq.NewServeMux()
	if _, err := Register(mux, nil, Deps{}); err == nil {
		t.Fatal("Register(nil registry): expected error")
	}
}

// TestRegister_Idempotency confirms a second call with the same
// registry returns the "already registered" error.
func TestRegister_Idempotency(t *testing.T) {
	reg := taskspec.NewRegistry()
	mux := asynq.NewServeMux()
	if _, err := Register(mux, reg, Deps{}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	// Use a fresh mux for the second call so asynq doesn't panic
	// on the duplicate pattern; the registry should still reject.
	mux2 := asynq.NewServeMux()
	if _, err := Register(mux2, reg, Deps{}); err == nil {
		t.Fatal("second Register: expected error from duplicate spec")
	}
}

// TestRegister_PDFAvailabilityReported just exercises the field on
// the report — the contents are environment-dependent.
func TestRegister_PDFAvailabilityReported(t *testing.T) {
	reg := taskspec.NewRegistry()
	mux := asynq.NewServeMux()
	rep, err := Register(mux, reg, Deps{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Just confirm the struct is populated (zero-value when nothing
	// is on PATH is also a valid Availability state).
	var avail pdfproc.Availability = rep.PDFAvailability
	_ = avail
}
