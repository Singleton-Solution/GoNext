// Package media wires the heavy-media task handlers (HLS video
// transcoding and PDF thumbnail/text extraction) onto the worker's
// asynq mux.
//
// The package lives inside apps/worker (not in packages/go) because
// the wiring is binary-specific: it depends on the storage layer the
// worker chooses, the logger the worker spins up, and the registry
// the worker keeps. The handlers themselves live in packages/go/media/
// (videoproc, pdfproc) — only the boot-time wiring lives here.
//
// # Skip-graceful binary checks
//
// Both pipelines need on-PATH binaries (ffmpeg for video; pdftoppm
// /pdftotext for PDF). At boot the package probes the PATH and picks
// between the production handler and a stub. The stub still
// registers on the mux so Enqueue calls from the API don't error with
// "unknown task"; the stub just logs and returns nil for every
// payload. That keeps a deployment without the binaries running
// healthy — uploads succeed, rows commit, the derivative pipeline
// is a no-op.
//
// # Wiring contract
//
// Register accepts a Deps bag, registers the appropriate specs onto
// the worker's taskspec registry, and dispatches them onto the
// asynq mux. It returns a Report describing what was wired ("real"
// vs "stub") so the worker can log the decision and operators have a
// visible record of why a deployment isn't transcoding.
package media

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/media/pdfproc"
	"github.com/Singleton-Solution/GoNext/packages/go/media/videoproc"
)

// Deps is the dependency bag for Register. Storage handles are
// optional in the boot-time skeleton: when nil, Register installs the
// stub handlers (matching the case where ffmpeg/pdftoppm are missing
// on PATH). Operators wiring a real worker pass real Source/Sink
// implementations backed by their S3 layer.
type Deps struct {
	// VideoSource pulls original bytes for the transcoder. Optional;
	// nil triggers stub registration for media.video.transcode.
	VideoSource videoproc.Source

	// VideoSink writes the HLS playlist + segments. Optional.
	VideoSink videoproc.Sink

	// VideoHLSWriter updates the media row's hls_url column.
	// Optional.
	VideoHLSWriter videoproc.HLSWriter

	// VideoWorkDir is the scratch directory for the video pipeline.
	// Empty falls back to os.TempDir() inside the handler.
	VideoWorkDir string

	// PDFSource pulls original PDF bytes. Optional.
	PDFSource pdfproc.Source

	// PDFSink writes the page-1 thumbnail. Optional.
	PDFSink pdfproc.Sink

	// PDFTextWriter persists the extracted text to media_text.
	// Optional.
	PDFTextWriter pdfproc.TextWriter

	// PDFThumbnailWriter records the thumbnail storage key. Optional.
	PDFThumbnailWriter pdfproc.ThumbnailWriter

	// PDFWorkDir scratch directory. Empty falls back to os.TempDir().
	PDFWorkDir string

	// Logger receives structured log lines from the wiring layer.
	// nil falls back to slog.Default.
	Logger *slog.Logger
}

// Report describes what Register actually wired.
type Report struct {
	// VideoMode is "real" when ffmpeg was found on PATH and all
	// dependencies were wired, "stub" otherwise.
	VideoMode string

	// PDFMode is "real" or "stub" — same semantics as VideoMode.
	PDFMode string

	// FFmpegPath is the absolute path to ffmpeg when found, "".
	FFmpegPath string

	// PDFAvailability reports which PDF binaries were found on PATH.
	PDFAvailability pdfproc.Availability
}

// Register wires both heavy-media task handlers onto mux, falling
// back to stubs when the host's binaries or the caller's storage
// handles are missing.
//
// Logs at info for each task ("registered media.video.transcode in
// real mode" / "...in stub mode"). The log line is the operator's
// signal that the deployment supports HLS — a missing or stub line
// in the boot log is the first place to look when a video upload
// doesn't transcode.
func Register(mux *asynq.ServeMux, reg *taskspec.Registry, deps Deps) (Report, error) {
	if mux == nil {
		return Report{}, fmt.Errorf("worker/media: nil asynq mux")
	}
	if reg == nil {
		return Report{}, fmt.Errorf("worker/media: nil taskspec registry")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	report := Report{}

	// ──────────────────────────────────────────────────────────────
	// Video transcoding (media.video.transcode)
	// ──────────────────────────────────────────────────────────────
	ffmpegPath, ffmpegOK := videoproc.IsAvailable()
	report.FFmpegPath = ffmpegPath

	videoSpec, vmode, err := buildVideoSpec(deps, ffmpegOK, logger)
	if err != nil {
		return report, fmt.Errorf("worker/media: build video spec: %w", err)
	}
	if err := reg.Register(videoSpec); err != nil {
		return report, fmt.Errorf("worker/media: register video spec: %w", err)
	}
	mux.HandleFunc(videoSpec.Name, asynqAdapter(videoSpec.Handler))
	report.VideoMode = vmode
	logger.Info("worker/media: video task registered",
		slog.String("task", videoSpec.Name),
		slog.String("mode", vmode),
		slog.String("ffmpeg_path", ffmpegPath),
	)

	// ──────────────────────────────────────────────────────────────
	// PDF processing (media.pdf.process)
	// ──────────────────────────────────────────────────────────────
	pdfAvail := pdfproc.Probe()
	report.PDFAvailability = pdfAvail

	pdfSpec, pmode, err := buildPDFSpec(deps, pdfAvail, logger)
	if err != nil {
		return report, fmt.Errorf("worker/media: build pdf spec: %w", err)
	}
	if err := reg.Register(pdfSpec); err != nil {
		return report, fmt.Errorf("worker/media: register pdf spec: %w", err)
	}
	mux.HandleFunc(pdfSpec.Name, asynqAdapter(pdfSpec.Handler))
	report.PDFMode = pmode
	logger.Info("worker/media: pdf task registered",
		slog.String("task", pdfSpec.Name),
		slog.String("mode", pmode),
		slog.String("pdftoppm_path", pdfAvail.PDFToPPMPath),
		slog.String("pdftotext_path", pdfAvail.PDFToTextPath),
		slog.String("pdfcpu_path", pdfAvail.PDFCPUPath),
	)
	return report, nil
}

// buildVideoSpec picks between the real and stub video spec based on
// ffmpeg availability and dependency wiring.
//
// The stub fires when EITHER ffmpeg is missing on PATH OR the storage
// handles haven't been wired.
func buildVideoSpec(deps Deps, ffmpegOK bool, logger *slog.Logger) (taskspec.TaskSpec, string, error) {
	canRunReal := ffmpegOK && deps.VideoSource != nil && deps.VideoSink != nil
	if !canRunReal {
		spec, err := videoproc.NewStubSpec(logger)
		return spec, "stub", err
	}
	spec, err := videoproc.NewSpec(videoproc.HandlerDeps{
		Source:    deps.VideoSource,
		Sink:      deps.VideoSink,
		HLSWriter: deps.VideoHLSWriter,
		Runner:    videoproc.ExecRunner{},
		WorkDir:   deps.VideoWorkDir,
		Logger:    logger,
	})
	return spec, "real", err
}

// buildPDFSpec picks between the real and stub PDF spec.
func buildPDFSpec(deps Deps, avail pdfproc.Availability, logger *slog.Logger) (taskspec.TaskSpec, string, error) {
	canRunReal := (avail.CanRender() || avail.CanExtractText()) && deps.PDFSource != nil && deps.PDFSink != nil
	if !canRunReal {
		spec, err := pdfproc.NewStubSpec(logger)
		return spec, "stub", err
	}
	spec, err := pdfproc.NewSpec(pdfproc.HandlerDeps{
		Source:          deps.PDFSource,
		Sink:            deps.PDFSink,
		TextWriter:      deps.PDFTextWriter,
		ThumbnailWriter: deps.PDFThumbnailWriter,
		Runner:          pdfproc.ExecRunner{},
		Availability:    avail,
		WorkDir:         deps.PDFWorkDir,
		Logger:          logger,
	})
	return spec, "real", err
}

// asynqAdapter wraps a (ctx, []byte) taskspec.Handler in the asynq
// handler signature mux.HandleFunc expects.
//
// We duplicate the adapter that lives in taskspec.Dispatch here
// because the wiring layer wants per-task control: a future task
// may need bespoke registration (priority hints, middleware) that
// the all-or-nothing Dispatch helper doesn't offer.
func asynqAdapter(h func(context.Context, []byte) error) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		return h(ctx, t.Payload())
	}
}
