package pdfproc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Binary names looked up on PATH.
const (
	PDFToPPMBinary  = "pdftoppm"
	PDFCPUBinary    = "pdfcpu"
	PDFToTextBinary = "pdftotext"
)

// Runner is the injection seam between the package and the actual
// subprocess invocations. A single Runner serves both pdftoppm and
// pdftotext — the runtime arguments differentiate the call, not the
// runner identity.
type Runner interface {
	// Run invokes binary with args. The returned error wraps the
	// binary's combined output when the exit code is non-zero;
	// callers compare against errors.Is(err, ErrBinaryMissing) to
	// distinguish missing-tool from runtime failures.
	Run(ctx context.Context, binary string, args []string) error
}

// ErrBinaryMissing is returned by ExecRunner when the requested
// binary is not on PATH. The PDF task handler treats this as a
// permanent failure (no retry) for the per-call binary; the
// worker-boot path uses IsAvailable to register the stub spec
// instead of trying-and-failing every job.
var ErrBinaryMissing = errors.New("pdfproc: binary not found on PATH")

// Availability reports which PDF binaries are reachable on PATH. The
// renderer is degraded when pdftoppm is missing but pdfcpu is
// present — the task handler picks the available one at runtime.
type Availability struct {
	// PDFToPPMPath is the absolute path to pdftoppm if found, "" if
	// not.
	PDFToPPMPath string

	// PDFCPUPath is the absolute path to pdfcpu if found, "" if not.
	// Used as the rendering fallback when pdftoppm is absent.
	PDFCPUPath string

	// PDFToTextPath is the absolute path to pdftotext if found, "".
	PDFToTextPath string
}

// CanRender reports whether at least one of the supported rendering
// binaries is available. The handler's thumbnail step short-circuits
// to "no thumbnail produced" when this is false.
func (a Availability) CanRender() bool {
	return a.PDFToPPMPath != "" || a.PDFCPUPath != ""
}

// CanExtractText reports whether pdftotext is available.
func (a Availability) CanExtractText() bool {
	return a.PDFToTextPath != ""
}

// Probe checks every binary the package can use. Safe to call at boot
// to gate task registration.
func Probe() Availability {
	a := Availability{}
	if p, err := exec.LookPath(PDFToPPMBinary); err == nil {
		a.PDFToPPMPath = p
	}
	if p, err := exec.LookPath(PDFCPUBinary); err == nil {
		a.PDFCPUPath = p
	}
	if p, err := exec.LookPath(PDFToTextBinary); err == nil {
		a.PDFToTextPath = p
	}
	return a
}

// ExecRunner is the production Runner backed by os/exec. The runner
// captures stderr alongside stdout so a failing invocation surfaces
// the binary's diagnostic output in the worker log.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, binary string, args []string) error {
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%w: %v", ErrBinaryMissing, err)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		const maxOut = 4 * 1024
		snippet := string(out)
		if len(snippet) > maxOut {
			snippet = snippet[:maxOut] + "...(truncated)"
		}
		return fmt.Errorf("pdfproc: %s failed: %w (output: %s)", binary, err, strings.TrimSpace(snippet))
	}
	return nil
}

// RenderOptions controls the thumbnail rendering step.
type RenderOptions struct {
	// DPI is the resolution of the rendered page. Higher = sharper +
	// bigger output. 150 is a good "looks good in the admin grid
	// without bloating the bucket" default.
	DPI int

	// OutputPrefix is the filename prefix pdftoppm uses
	// (it produces "<prefix>-1.png" for page 1). Defaults to "thumb".
	OutputPrefix string
}

// DefaultDPI is the rendering resolution when the caller passes 0.
const DefaultDPI = 150

// DefaultOutputPrefix is the filename prefix when the caller passes "".
const DefaultOutputPrefix = "thumb"

// resolved returns a copy of RenderOptions with defaults applied.
func (o RenderOptions) resolved() RenderOptions {
	if o.DPI <= 0 {
		o.DPI = DefaultDPI
	}
	if o.OutputPrefix == "" {
		o.OutputPrefix = DefaultOutputPrefix
	}
	return o
}

// BuildPDFToPPMArgs assembles the argv that produces a PNG of page 1
// of inputPath in outputDir. The shape is a documented contract — the
// task brief in #60 names the exact flags.
//
//	pdftoppm -png -f 1 -l 1 -r <dpi> input <output_dir>/<prefix>
//
// pdftoppm appends "-1.png" to the prefix, so the resulting file is
// "<output_dir>/<prefix>-1.png".
func BuildPDFToPPMArgs(inputPath, outputDir string, opts RenderOptions) []string {
	opts = opts.resolved()
	return []string{
		"-png",
		"-f", "1",
		"-l", "1",
		"-r", fmt.Sprintf("%d", opts.DPI),
		inputPath,
		outputDir + "/" + opts.OutputPrefix,
	}
}

// BuildPDFCPUArgs assembles the fallback argv when pdftoppm is missing
// and pdfcpu is available. pdfcpu's "extract -mode i" pulls images
// from the document; we then pick page 1 in the handler code.
// Documented as a fallback because pdftoppm is generally more
// reliable; pdfcpu serves as the "works without poppler" escape
// hatch.
//
//	pdfcpu extract -mode image -pages 1 <input> <output_dir>
func BuildPDFCPUArgs(inputPath, outputDir string) []string {
	return []string{
		"extract",
		"-mode", "image",
		"-pages", "1",
		inputPath,
		outputDir,
	}
}

// BuildPDFToTextArgs assembles the argv for pdftotext. The single "-"
// destination writes to stdout so we don't have to round-trip through
// a temp file; the task handler captures the runner's output.
//
//	pdftotext -enc UTF-8 -nopgbrk <input> <output>
//
// The handler passes a real file path rather than "-" so the size
// cap can be enforced on disk (avoid loading multi-MiB into memory
// from a stdout pipe).
func BuildPDFToTextArgs(inputPath, outputPath string) []string {
	return []string{
		"-enc", "UTF-8",
		"-nopgbrk",
		inputPath,
		outputPath,
	}
}
