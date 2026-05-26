package videoproc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingRunner is the test-only Runner implementation. It captures
// the argv of every Run call (so assertions can inspect the exact
// flags ffmpeg would have received) and returns a caller-supplied
// error so the "ffmpeg failed mid-encode" path can be exercised
// without a subprocess.
type recordingRunner struct {
	calls []recordedCall
	err   error
	// onRun, when set, is invoked before the runner returns its
	// caller-supplied error. Tests use it to fabricate the output
	// directory contents so the handler's upload step has something
	// to walk.
	onRun func(args []string, workingDir string) error
}

type recordedCall struct {
	args       []string
	workingDir string
}

func (r *recordingRunner) Run(ctx context.Context, args []string, workingDir string) error {
	cp := append([]string(nil), args...)
	r.calls = append(r.calls, recordedCall{args: cp, workingDir: workingDir})
	if r.onRun != nil {
		if err := r.onRun(args, workingDir); err != nil {
			return err
		}
	}
	return r.err
}

// TestBuildArgs_DefaultShape pins the argv the transcoder hands to
// ffmpeg. The shape is a contract (the task brief in #52 names the
// flags); a maintainer who changes it must also bump the test so the
// change is intentional rather than accidental.
func TestBuildArgs_DefaultShape(t *testing.T) {
	args := BuildArgs("/in/source.mp4", "/out", TranscodeOptions{})

	// We don't require an exact slice match because the position of
	// individual flags is allowed to evolve; we DO require the
	// load-bearing flags to be present and well-formed.
	want := []string{
		"-i", "/in/source.mp4",
		"-vf", "scale=-2:720",
		"-c:v", "libx264",
		"-c:a", "aac",
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-f", "hls",
	}
	joined := strings.Join(args, " ")
	for i := 0; i < len(want); i += 2 {
		pair := want[i] + " " + want[i+1]
		if !strings.Contains(joined, pair) {
			t.Errorf("BuildArgs missing pair %q in %q", pair, joined)
		}
	}
	// The last argument is the playlist destination — load-bearing.
	if !strings.HasSuffix(args[len(args)-1], "/index.m3u8") {
		t.Errorf("BuildArgs: last arg = %q, want suffix /index.m3u8", args[len(args)-1])
	}
}

// TestBuildArgs_CustomOptions confirms the option overrides flow into
// the argv. The test also indirectly verifies the resolved() defaults
// only fire when the caller passes the zero value.
func TestBuildArgs_CustomOptions(t *testing.T) {
	args := BuildArgs("/in.mp4", "/out", TranscodeOptions{
		SegmentSeconds: 4,
		Height:         1080,
		PlaylistName:   "stream.m3u8",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "scale=-2:1080") {
		t.Errorf("height override missing: %q", joined)
	}
	if !strings.Contains(joined, "-hls_time 4") {
		t.Errorf("segment override missing: %q", joined)
	}
	if !strings.HasSuffix(args[len(args)-1], "/stream.m3u8") {
		t.Errorf("playlist override missing: %q", args[len(args)-1])
	}
}

// TestTranscode_InvokesRunner is the happy-path: the runner is called
// with the expected argv shape and no error bubbles up.
func TestTranscode_InvokesRunner(t *testing.T) {
	r := &recordingRunner{}
	err := Transcode(context.Background(), r, "/in.mp4", "/out", TranscodeOptions{})
	if err != nil {
		t.Fatalf("Transcode: unexpected error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("recordingRunner: got %d calls, want 1", len(r.calls))
	}
	if r.calls[0].args[0] != "-y" {
		t.Errorf("expected first arg to be -y (overwrite), got %q", r.calls[0].args[0])
	}
}

// TestTranscode_RunnerErrorPropagates confirms a non-nil error from
// the runner surfaces with the input path in the wrapped chain so
// log lines are diagnosable.
func TestTranscode_RunnerErrorPropagates(t *testing.T) {
	sentinel := errors.New("encoder crashed")
	r := &recordingRunner{err: sentinel}
	err := Transcode(context.Background(), r, "/in.mp4", "/out", TranscodeOptions{})
	if err == nil {
		t.Fatal("Transcode: expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Transcode: error not wrapping sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "/in.mp4") {
		t.Errorf("Transcode error should mention input path: %v", err)
	}
}

// TestTranscode_NilRunnerRejected ensures the package can't be called
// without an injected runner — production wires ExecRunner; a nil
// here is almost certainly a wiring bug we want to surface loudly.
func TestTranscode_NilRunnerRejected(t *testing.T) {
	err := Transcode(context.Background(), nil, "/in.mp4", "/out", TranscodeOptions{})
	if err == nil {
		t.Fatal("Transcode with nil runner: expected error")
	}
}

// TestIsSupportedMIME confirms only video/* gets through. The
// permissive prefix-match is intentional — the transcoder shouldn't
// have to know about every codec under video/*.
func TestIsSupportedMIME(t *testing.T) {
	for _, tt := range []struct {
		mime string
		want bool
	}{
		{"video/mp4", true},
		{"video/quicktime", true},
		{"VIDEO/MP4", true},
		{"image/jpeg", false},
		{"application/pdf", false},
		{"", false},
	} {
		got := IsSupportedMIME(tt.mime)
		if got != tt.want {
			t.Errorf("IsSupportedMIME(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

// TestIsAvailable does not assert truth or falsity — the test machine
// may or may not have ffmpeg installed. It only ensures the function
// returns without panicking and that the boolean and path agree.
func TestIsAvailable(t *testing.T) {
	path, ok := IsAvailable()
	if ok && path == "" {
		t.Error("IsAvailable: ok=true but path is empty")
	}
	if !ok && path != "" {
		t.Error("IsAvailable: ok=false but path is non-empty")
	}
}

// TestExecRunner_MissingBinary confirms the runner returns
// ErrBinaryMissing (and does not panic) when the configured binary
// cannot be found on PATH. A real test substitutes a binary that
// definitely doesn't exist.
func TestExecRunner_MissingBinary(t *testing.T) {
	r := ExecRunner{Binary: "definitely-not-a-real-binary-fooblat"}
	err := r.Run(context.Background(), []string{}, "")
	if err == nil {
		t.Fatal("ExecRunner.Run: expected error for missing binary")
	}
	if !errors.Is(err, ErrBinaryMissing) {
		t.Errorf("ExecRunner.Run: expected ErrBinaryMissing, got %v", err)
	}
}
