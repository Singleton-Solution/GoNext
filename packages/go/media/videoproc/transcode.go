package videoproc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// FFmpegBinary is the on-PATH name of the binary the package looks up.
// Exported so an operator-side override (env var, config) can pin a
// vendored copy.
const FFmpegBinary = "ffmpeg"

// Runner is the injection seam between the package and the actual
// ffmpeg subprocess. The wire surface is intentionally tiny — a single
// command line plus a working directory — so a test fake can record
// invocations without rebuilding any complex state.
//
// Production wiring uses ExecRunner, which delegates to os/exec.
// Tests use a recording fake that captures argv, optionally writes
// fabricated output files to the destination directory, and returns a
// caller-supplied error.
type Runner interface {
	// Run invokes the binary with args. WorkingDir is optional; an
	// empty value means "inherit from the parent process". The
	// returned error wraps the binary's stderr when the exit code is
	// non-zero; callers compare against errors.Is(err, ErrBinaryMissing)
	// to distinguish "ffmpeg isn't installed" from "ffmpeg ran but
	// failed". Callers MUST honour ctx for cancellation — a long-
	// running transcode that exceeds the task timeout has to be
	// killed cleanly so asynq's retry path sees a cancelled error
	// rather than a hung worker.
	Run(ctx context.Context, args []string, workingDir string) error
}

// ErrBinaryMissing is returned by Runner.Run when the configured binary
// is not on PATH. The transcode task handler treats this as a permanent
// failure (no retry): a missing binary is a deployment fact, not a
// transient hiccup.
var ErrBinaryMissing = errors.New("videoproc: ffmpeg binary not found on PATH")

// IsAvailable reports whether ffmpeg is reachable on PATH. The check
// is a single exec.LookPath; safe to call at boot to gate task
// registration. Returns the empty string for the path component when
// the binary is not found.
//
// Callers should log a clear warning when this returns false so an
// operator can decide whether the missing binary is intentional
// (video transcoding not wanted on this deployment) or a config bug.
func IsAvailable() (string, bool) {
	p, err := exec.LookPath(FFmpegBinary)
	if err != nil {
		return "", false
	}
	return p, true
}

// ExecRunner is the production Runner backed by os/exec. The zero
// value is ready to use; Binary defaults to FFmpegBinary.
//
// The runner captures stderr into the returned error so a failing
// transcode surfaces ffmpeg's diagnostic output in the worker log,
// not an opaque "exit status 1".
type ExecRunner struct {
	// Binary overrides FFmpegBinary. Empty means "use the default".
	Binary string
}

// Run implements Runner.
func (e ExecRunner) Run(ctx context.Context, args []string, workingDir string) error {
	bin := e.Binary
	if bin == "" {
		bin = FFmpegBinary
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: %v", ErrBinaryMissing, err)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	// Combined output captures the encoder's stderr (where ffmpeg
	// writes its progress + errors) alongside stdout so the failure
	// log lines are informative.
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Trim to a reasonable cap so a runaway encoder doesn't write
		// megabytes into the error log. The first 4 KiB of ffmpeg's
		// banner is enough to diagnose any real failure.
		const maxOut = 4 * 1024
		snippet := string(out)
		if len(snippet) > maxOut {
			snippet = snippet[:maxOut] + "...(truncated)"
		}
		return fmt.Errorf("videoproc: ffmpeg failed: %w (output: %s)", err, strings.TrimSpace(snippet))
	}
	return nil
}

// TranscodeOptions controls a single Transcode call. The zero value
// is acceptable; defaults are applied by Transcode.
type TranscodeOptions struct {
	// SegmentSeconds is the HLS segment duration. 6s is the
	// reference value from the HLS spec — small enough that a slow
	// connection can start playback quickly, large enough that the
	// per-segment overhead (a TS file header, a manifest entry) is
	// amortised. Zero falls back to DefaultSegmentSeconds.
	SegmentSeconds int

	// Height is the target rendition height in pixels. The width is
	// computed from the source aspect ratio (the -vf scale filter
	// applies "trunc(oh*a/2)*2:720" to preserve the aspect and keep
	// the width even — H.264 requires even dimensions). Zero falls
	// back to DefaultHeight (720).
	Height int

	// PlaylistName is the manifest filename. Defaults to "index.m3u8";
	// override only if the public URL routing wants a stable
	// per-media filename that includes the id.
	PlaylistName string
}

// DefaultSegmentSeconds is the HLS segment duration used when the
// caller passes the zero value. Matches the HLS spec's reference.
const DefaultSegmentSeconds = 6

// DefaultHeight is the target rendition height in pixels for the v1
// single-rendition transcode.
const DefaultHeight = 720

// DefaultPlaylistName is the HLS manifest filename.
const DefaultPlaylistName = "index.m3u8"

// Transcode invokes the configured Runner to produce an HLS playlist
// at outputDir/PlaylistName from the source file at inputPath. The
// runner is responsible for spawning the actual subprocess; this
// function only assembles the argv and forwards to runner.Run.
//
// The argv is deterministic for a given options value — important for
// tests that assert on the exact flags, and for a future "redo this
// transcode with the same params" reprocess endpoint.
//
// Errors from the runner are wrapped with the input path so a
// pipeline log line is self-describing without the caller having to
// add it.
func Transcode(ctx context.Context, runner Runner, inputPath, outputDir string, opts TranscodeOptions) error {
	if runner == nil {
		return errors.New("videoproc: nil runner")
	}
	if inputPath == "" {
		return errors.New("videoproc: empty input path")
	}
	if outputDir == "" {
		return errors.New("videoproc: empty output directory")
	}
	opts = opts.resolved()
	args := BuildArgs(inputPath, outputDir, opts)
	if err := runner.Run(ctx, args, ""); err != nil {
		return fmt.Errorf("videoproc: transcode %q: %w", inputPath, err)
	}
	return nil
}

// resolved returns a copy of TranscodeOptions with defaults applied.
func (o TranscodeOptions) resolved() TranscodeOptions {
	if o.SegmentSeconds <= 0 {
		o.SegmentSeconds = DefaultSegmentSeconds
	}
	if o.Height <= 0 {
		o.Height = DefaultHeight
	}
	if o.PlaylistName == "" {
		o.PlaylistName = DefaultPlaylistName
	}
	return o
}

// BuildArgs assembles the ffmpeg argv for a single HLS transcode. The
// function is exported so tests can assert on the exact flags — the
// shape is a documented contract, not an implementation detail.
//
// The argv was chosen to match the task brief in #52:
//
//	ffmpeg -i <input>
//	       -vf scale=-2:<height>
//	       -c:v libx264 -preset veryfast -crf 23
//	       -c:a aac    -b:a 128k
//	       -hls_time <segment_seconds>
//	       -hls_playlist_type vod
//	       -f hls
//	       <output_dir>/<playlist>
//
// Notes:
//
//   * "-vf scale=-2:H" preserves aspect and forces an even width.
//     H.264 requires both dimensions even; the trunc(...)*2 trick is
//     replaced here by ffmpeg's own -2 marker which does the same.
//   * "-preset veryfast" trades a bit of bitrate efficiency for a
//     large reduction in CPU time. Worker CPU is the bottleneck on
//     a busy site; the storage-side cost of a slightly larger
//     segment is much cheaper than the latency cost of "slow".
//   * "-crf 23" is the H.264 quality knob; 23 is the ffmpeg default
//     and produces a perceptually transparent rendition for typical
//     720p web content.
//   * "-hls_playlist_type vod" tags the playlist as VOD (not LIVE)
//     so players cache aggressively and do not poll for updates.
func BuildArgs(inputPath, outputDir string, opts TranscodeOptions) []string {
	opts = opts.resolved()
	return []string{
		"-y", // overwrite the destination playlist on rerun
		"-i", inputPath,
		"-vf", fmt.Sprintf("scale=-2:%d", opts.Height),
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "128k",
		"-hls_time", fmt.Sprintf("%d", opts.SegmentSeconds),
		"-hls_playlist_type", "vod",
		"-f", "hls",
		outputDir + "/" + opts.PlaylistName,
	}
}
