package pdfproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
)

// TaskName is the on-wire identifier for the PDF processing task.
const TaskName = "media.pdf.process"

// DefaultQueue is the queue PDF tasks land on.
const DefaultQueue = "media"

// DefaultMaxRetry caps how many times asynq will re-run a failing
// PDF task. 2 is intentionally low — most failures (corrupt PDF,
// encrypted file) are permanent, and pdftoppm/pdftotext are
// deterministic.
const DefaultMaxRetry = 2

// DefaultTimeout bounds a single PDF processing invocation.
const DefaultTimeout = 3 * time.Minute

// DefaultMaxTextBytes caps the size of the extracted text. A PDF this
// big is almost certainly a scanned image stack; the extracted text
// for such a file is mostly OCR noise. Operators with larger PDFs
// can raise the cap via HandlerDeps.MaxTextBytes.
const DefaultMaxTextBytes = 4 * 1024 * 1024

// Payload is the JSON shape the upload handler enqueues.
type Payload struct {
	AssetID    string `json:"asset_id"`
	StorageKey string `json:"storage_key"`
	MIMEType   string `json:"mime_type"`
}

var payloadSchemaRaw = []byte(`{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": "object",
	"properties": {
		"asset_id":    {"type": "string", "minLength": 1},
		"storage_key": {"type": "string", "minLength": 1},
		"mime_type":   {"type": "string"}
	},
	"required": ["asset_id", "storage_key"],
	"additionalProperties": false
}`)

// Source reads original bytes from storage.
type Source interface {
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// Sink writes derivative artifacts (the thumbnail PNG) back to
// storage.
type Sink interface {
	PutObject(ctx context.Context, key string, body []byte, mimeType string) error
	PublicURL(key string) string
}

// TextWriter persists the extracted text on the media_text table.
type TextWriter interface {
	// SetText stores the full extracted text for assetID. The store
	// implementation is responsible for updating the generated
	// tsvector column. Idempotent — re-running on the same asset
	// replaces the previous extraction.
	SetText(ctx context.Context, assetID, fullText string) error
}

// ThumbnailWriter persists the thumbnail storage key on the media
// row. Optional — when nil, the handler still uploads the thumbnail
// but the row's variants list won't reference it.
type ThumbnailWriter interface {
	// SetPDFThumbnail records the storage key (and PublicURL via the
	// usual path-translation) for the page-1 thumbnail of assetID.
	SetPDFThumbnail(ctx context.Context, assetID, thumbnailStorageKey string) error
}

// HandlerDeps is the dependency bag for NewHandler.
type HandlerDeps struct {
	Source          Source
	Sink            Sink
	TextWriter      TextWriter
	ThumbnailWriter ThumbnailWriter
	Runner          Runner

	// Availability pre-computed at boot. Used to pick pdftoppm vs.
	// pdfcpu, and to decide whether text extraction runs at all.
	Availability Availability

	// WorkDir for per-job scratch. Defaults to os.TempDir().
	WorkDir string

	// KeyPrefix for the thumbnail upload. Defaults to "pdf-thumbs/".
	KeyPrefix string

	// MaxTextBytes overrides DefaultMaxTextBytes. Zero falls back to
	// the default.
	MaxTextBytes int

	// RenderOptions pass through to the thumbnail step.
	RenderOptions RenderOptions

	Logger *slog.Logger
}

// NewHandler returns a TaskSpec.Handler closure that runs the
// pipeline end-to-end. The handler is tolerant of partial
// availability: if pdftoppm is missing but pdftotext is present,
// text extraction still runs and the thumbnail step is skipped.
//
// On a fully-unavailable host (neither rendering nor text binaries
// present), the handler logs at warn and returns nil — the same
// behaviour as NewStubSpec, but selected at runtime based on Probe.
func NewHandler(deps HandlerDeps) func(context.Context, []byte) error {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.KeyPrefix == "" {
		deps.KeyPrefix = "pdf-thumbs/"
	}
	if deps.WorkDir == "" {
		deps.WorkDir = os.TempDir()
	}
	if deps.MaxTextBytes <= 0 {
		deps.MaxTextBytes = DefaultMaxTextBytes
	}
	return func(ctx context.Context, raw []byte) error {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("pdfproc.task: parse payload: %w", err)
		}
		if p.StorageKey == "" || p.AssetID == "" {
			return errors.New("pdfproc.task: storage_key and asset_id are required")
		}
		if p.MIMEType != "" && !IsSupportedMIME(p.MIMEType) {
			deps.Logger.InfoContext(ctx,
				"pdfproc.task: skipping non-PDF MIME",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
				slog.String("mime_type", p.MIMEType),
			)
			return nil
		}
		if deps.Source == nil || deps.Sink == nil || deps.Runner == nil {
			return errors.New("pdfproc.task: Source, Sink, and Runner must be wired")
		}

		// Skip-graceful: nothing to do if both binaries are absent.
		if !deps.Availability.CanRender() && !deps.Availability.CanExtractText() {
			deps.Logger.WarnContext(ctx,
				"pdfproc.task: no PDF binaries on PATH, skipping",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
			)
			return nil
		}

		body, err := deps.Source.GetObject(ctx, p.StorageKey)
		if err != nil {
			return fmt.Errorf("pdfproc.task: fetch source %q: %w", p.StorageKey, err)
		}

		jobDir, err := os.MkdirTemp(deps.WorkDir, "pdfproc-"+sanitize(p.AssetID)+"-*")
		if err != nil {
			return fmt.Errorf("pdfproc.task: mkdir scratch: %w", err)
		}
		defer os.RemoveAll(jobDir)

		inputPath := filepath.Join(jobDir, "input.pdf")
		if err := os.WriteFile(inputPath, body, 0o600); err != nil {
			return fmt.Errorf("pdfproc.task: write input: %w", err)
		}

		// 1. Thumbnail
		if deps.Availability.CanRender() {
			if err := renderThumbnail(ctx, deps, p, inputPath, jobDir); err != nil {
				// A thumbnail failure does NOT abort the job — text
				// extraction may still succeed and that's a useful
				// degraded outcome. Log and continue.
				deps.Logger.WarnContext(ctx,
					"pdfproc.task: thumbnail render failed",
					slog.String("asset_id", p.AssetID),
					slog.Any("err", err),
				)
			}
		}

		// 2. Text extraction
		if deps.Availability.CanExtractText() && deps.TextWriter != nil {
			if err := extractText(ctx, deps, p, inputPath, jobDir); err != nil {
				return fmt.Errorf("pdfproc.task: extract text: %w", err)
			}
		}

		deps.Logger.InfoContext(ctx,
			"pdfproc.task: processed PDF",
			slog.String("asset_id", p.AssetID),
			slog.String("storage_key", p.StorageKey),
		)
		return nil
	}
}

// renderThumbnail runs pdftoppm (preferred) or pdfcpu (fallback) to
// produce a PNG of page 1, uploads it to the sink, and writes the
// storage key to the thumbnail writer if one is wired.
func renderThumbnail(ctx context.Context, deps HandlerDeps, p Payload, inputPath, jobDir string) error {
	thumbDir := filepath.Join(jobDir, "thumb")
	if err := os.MkdirAll(thumbDir, 0o700); err != nil {
		return fmt.Errorf("mkdir thumb dir: %w", err)
	}
	opts := deps.RenderOptions.resolved()

	var producedFile string
	if deps.Availability.PDFToPPMPath != "" {
		args := BuildPDFToPPMArgs(inputPath, thumbDir, opts)
		if err := deps.Runner.Run(ctx, PDFToPPMBinary, args); err != nil {
			return fmt.Errorf("pdftoppm: %w", err)
		}
		// pdftoppm writes "<prefix>-1.png"
		producedFile = filepath.Join(thumbDir, opts.OutputPrefix+"-1.png")
	} else {
		args := BuildPDFCPUArgs(inputPath, thumbDir)
		if err := deps.Runner.Run(ctx, PDFCPUBinary, args); err != nil {
			return fmt.Errorf("pdfcpu: %w", err)
		}
		// pdfcpu writes an image file per page; pick the first one.
		entries, err := os.ReadDir(thumbDir)
		if err != nil {
			return fmt.Errorf("read pdfcpu output: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				producedFile = filepath.Join(thumbDir, e.Name())
				break
			}
		}
		if producedFile == "" {
			return errors.New("pdfcpu produced no image")
		}
	}

	thumbBytes, err := os.ReadFile(producedFile)
	if err != nil {
		return fmt.Errorf("read thumbnail %q: %w", producedFile, err)
	}
	thumbKey := deps.KeyPrefix + p.AssetID + "/thumb.png"
	if err := deps.Sink.PutObject(ctx, thumbKey, thumbBytes, "image/png"); err != nil {
		return fmt.Errorf("put thumbnail: %w", err)
	}
	if deps.ThumbnailWriter != nil {
		if err := deps.ThumbnailWriter.SetPDFThumbnail(ctx, p.AssetID, thumbKey); err != nil {
			return fmt.Errorf("record thumbnail key: %w", err)
		}
	}
	return nil
}

// extractText runs pdftotext and persists the result via the TextWriter.
func extractText(ctx context.Context, deps HandlerDeps, p Payload, inputPath, jobDir string) error {
	outputPath := filepath.Join(jobDir, "text.txt")
	args := BuildPDFToTextArgs(inputPath, outputPath)
	if err := deps.Runner.Run(ctx, PDFToTextBinary, args); err != nil {
		return fmt.Errorf("pdftotext: %w", err)
	}
	textBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("read extracted text: %w", err)
	}
	// Cap the stored payload. Truncation is preferred over rejection
	// because a multi-hundred-page PDF that exceeds the cap still has
	// useful first-N-MB of indexable content.
	if len(textBytes) > deps.MaxTextBytes {
		textBytes = textBytes[:deps.MaxTextBytes]
		deps.Logger.InfoContext(ctx,
			"pdfproc.task: extracted text truncated",
			slog.String("asset_id", p.AssetID),
			slog.Int("max_bytes", deps.MaxTextBytes),
		)
	}
	if err := deps.TextWriter.SetText(ctx, p.AssetID, string(textBytes)); err != nil {
		return fmt.Errorf("persist text: %w", err)
	}
	return nil
}

// NewSpec returns the TaskSpec ready to register into a Registry.
func NewSpec(deps HandlerDeps) (taskspec.TaskSpec, error) {
	schema, err := jsonschemautil.Compile("https://gonext.example/media-pdf-process.json", payloadSchemaRaw)
	if err != nil {
		return taskspec.TaskSpec{}, fmt.Errorf("pdfproc: compile payload schema: %w", err)
	}
	return taskspec.TaskSpec{
		Name:          TaskName,
		Queue:         DefaultQueue,
		MaxRetry:      DefaultMaxRetry,
		Timeout:       DefaultTimeout,
		PayloadSchema: schema,
		Handler:       NewHandler(deps),
	}, nil
}

// NewStubSpec returns a TaskSpec whose handler logs and returns nil
// for every payload. Used at worker boot when neither pdftoppm nor
// pdftotext is on PATH.
func NewStubSpec(logger *slog.Logger) (taskspec.TaskSpec, error) {
	schema, err := jsonschemautil.Compile("https://gonext.example/media-pdf-process.json", payloadSchemaRaw)
	if err != nil {
		return taskspec.TaskSpec{}, fmt.Errorf("pdfproc: compile payload schema: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return taskspec.TaskSpec{
		Name:          TaskName,
		Queue:         DefaultQueue,
		MaxRetry:      DefaultMaxRetry,
		Timeout:       DefaultTimeout,
		PayloadSchema: schema,
		Handler: func(ctx context.Context, raw []byte) error {
			var p Payload
			_ = json.Unmarshal(raw, &p)
			logger.WarnContext(ctx,
				"pdfproc.task: PDF binaries not on PATH, skipping",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
			)
			return nil
		},
	}, nil
}

// IsSupportedMIME reports whether mime is a PDF. We do NOT extend
// this to arbitrary application/* — the only thing pdftoppm/pdftotext
// can do is PDF.
func IsSupportedMIME(mime string) bool {
	return strings.EqualFold(strings.TrimSpace(mime), "application/pdf")
}

// PayloadSchema returns the compiled schema for tests.
func PayloadSchema() ([]byte, error) {
	out := make([]byte, len(payloadSchemaRaw))
	copy(out, payloadSchemaRaw)
	return out, nil
}

// sanitize returns a filesystem-safe slug of s for use inside a
// MkdirTemp pattern.
func sanitize(s string) string {
	if s == "" {
		return "anon"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
		case c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-' || c == '_':
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "anon"
	}
	return string(out)
}
