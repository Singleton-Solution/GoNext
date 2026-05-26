package videoproc

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

// TaskName is the on-wire identifier for the video transcoding task.
// Exported so the upload handler can pass the same constant to
// taskspec.Enqueue, and so a future admin-UI "retranscode this asset"
// button can target the same handler.
const TaskName = "media.video.transcode"

// DefaultQueue is the queue name the upload pipeline lands tasks on.
// "media" is the dedicated queue for media processing; the worker
// drains it at a lower priority than the critical queue so a backlog
// of transcodes can't starve email or webhook delivery.
const DefaultQueue = "media"

// DefaultMaxRetry caps how many times asynq will re-run a failing
// transcode. 2 is intentionally low — a transcode failure is almost
// always permanent (unsupported codec, corrupt input) rather than
// transient. The retry covers the case where ffmpeg's own download
// of an external font fails mid-run.
const DefaultMaxRetry = 2

// DefaultTimeout bounds a single transcode invocation. 10 minutes
// covers a multi-hundred-MB 720p source on a modest CPU; longer
// videos either need a beefier worker or a follow-up that splits the
// transcode into per-segment jobs.
const DefaultTimeout = 10 * time.Minute

// Payload is the JSON shape the upload handler enqueues. AssetID and
// StorageKey both ride the wire so the worker can fetch the original
// bytes by key and attribute the resulting playlist back to the row
// by id.
type Payload struct {
	AssetID    string `json:"asset_id"`
	StorageKey string `json:"storage_key"`
	MIMEType   string `json:"mime_type"`
}

// payloadSchemaRaw is the JSON Schema validated by taskspec.Enqueue
// before a payload reaches the queue. Kept close to the struct
// definition so the schema and the Go type drift only when a
// maintainer touches both files.
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

// Source is the read side of the storage layer the handler uses to
// pull original bytes. Wire surface kept tiny so an in-memory test
// fake stays cheap.
type Source interface {
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// Sink is the write side. The handler writes the playlist + every
// segment file the encoder produced. KeyPrefix on the handler config
// determines where the playlist lives (e.g. "hls/<media-id>/").
type Sink interface {
	PutObject(ctx context.Context, key string, body []byte, mimeType string) error

	// PublicURL returns the externally addressable URL for key. The
	// handler stores this on media.hls_url for the player to consume.
	PublicURL(key string) string
}

// HLSWriter is the optional hook that records "asset X now has an
// HLS playlist at URL Y" on the media row. Nil writer is allowed —
// the handler still produces a playlist, but the row's hls_url
// column stays NULL and the public player will fall back to the
// original mp4.
type HLSWriter interface {
	SetHLSURL(ctx context.Context, assetID, hlsURL string) error
}

// HandlerDeps is the dependency bag for NewHandler.
type HandlerDeps struct {
	// Source pulls original bytes. Required.
	Source Source

	// Sink writes the playlist + segment files. Required.
	Sink Sink

	// HLSWriter persists the playlist URL on the media row. Optional.
	HLSWriter HLSWriter

	// Runner spawns the actual ffmpeg subprocess. Required for the
	// production path; tests substitute a recording fake.
	Runner Runner

	// WorkDir is a writable directory the handler uses for the
	// per-job scratch space. The handler creates a subdirectory per
	// invocation and removes it on completion. Empty means
	// os.TempDir().
	WorkDir string

	// KeyPrefix is the storage-key prefix for HLS output. Defaults to
	// "hls/" — the per-job prefix is "<KeyPrefix><asset-id>/".
	KeyPrefix string

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// Options pass through to Transcode. See TranscodeOptions.
	Options TranscodeOptions
}

// NewHandler returns a TaskSpec.Handler closure that runs the
// pipeline end-to-end.
//
// The handler:
//
//   1. Parses the payload.
//   2. Skips non-video MIME types (logs and returns nil).
//   3. Fetches the source bytes via Source.GetObject.
//   4. Writes them to a scratch file under WorkDir.
//   5. Invokes Transcode against an output subdirectory.
//   6. Uploads every file in the output directory to Sink under
//      "<KeyPrefix><asset-id>/<filename>".
//   7. Computes the playlist's public URL and writes it to the row
//      via HLSWriter.
//   8. Cleans up the scratch directory.
//
// Errors mid-pipeline wrap with the stage so the worker log line
// reads "videoproc.task: fetch source ...: ..." or "videoproc.task:
// transcode ...: ...". asynq's retry path is driven by the wrapped
// error — a missing-binary error from the runner does NOT retry.
func NewHandler(deps HandlerDeps) func(context.Context, []byte) error {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.KeyPrefix == "" {
		deps.KeyPrefix = "hls/"
	}
	if deps.WorkDir == "" {
		deps.WorkDir = os.TempDir()
	}
	return func(ctx context.Context, raw []byte) error {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("videoproc.task: parse payload: %w", err)
		}
		if p.StorageKey == "" || p.AssetID == "" {
			return errors.New("videoproc.task: storage_key and asset_id are required")
		}
		if p.MIMEType != "" && !IsSupportedMIME(p.MIMEType) {
			deps.Logger.InfoContext(ctx,
				"videoproc.task: skipping non-video MIME",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
				slog.String("mime_type", p.MIMEType),
			)
			return nil
		}
		if deps.Source == nil || deps.Sink == nil || deps.Runner == nil {
			return errors.New("videoproc.task: Source, Sink, and Runner must be wired")
		}

		body, err := deps.Source.GetObject(ctx, p.StorageKey)
		if err != nil {
			return fmt.Errorf("videoproc.task: fetch source %q: %w", p.StorageKey, err)
		}

		// Build the per-job scratch directory. The pattern includes
		// the asset id so a directory listing in /tmp is diagnosable.
		jobDir, err := os.MkdirTemp(deps.WorkDir, "videoproc-"+sanitize(p.AssetID)+"-*")
		if err != nil {
			return fmt.Errorf("videoproc.task: mkdir scratch: %w", err)
		}
		defer os.RemoveAll(jobDir)

		inputPath := filepath.Join(jobDir, "input"+ext(p.StorageKey))
		if err := os.WriteFile(inputPath, body, 0o600); err != nil {
			return fmt.Errorf("videoproc.task: write input: %w", err)
		}
		outDir := filepath.Join(jobDir, "out")
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			return fmt.Errorf("videoproc.task: mkdir out: %w", err)
		}

		if err := Transcode(ctx, deps.Runner, inputPath, outDir, deps.Options); err != nil {
			return fmt.Errorf("videoproc.task: transcode: %w", err)
		}

		// Walk the output directory and upload everything. The
		// playlist references segments by filename, so we just
		// re-create the relative structure under the storage prefix.
		entries, err := os.ReadDir(outDir)
		if err != nil {
			return fmt.Errorf("videoproc.task: read out dir: %w", err)
		}
		opts := deps.Options.resolved()
		prefix := deps.KeyPrefix + p.AssetID + "/"
		playlistKey := prefix + opts.PlaylistName
		uploaded := 0
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			b, err := os.ReadFile(filepath.Join(outDir, name))
			if err != nil {
				return fmt.Errorf("videoproc.task: read output %q: %w", name, err)
			}
			mime := segmentMIME(name)
			if err := deps.Sink.PutObject(ctx, prefix+name, b, mime); err != nil {
				return fmt.Errorf("videoproc.task: put output %q: %w", name, err)
			}
			uploaded++
		}

		hlsURL := deps.Sink.PublicURL(playlistKey)
		if deps.HLSWriter != nil {
			if err := deps.HLSWriter.SetHLSURL(ctx, p.AssetID, hlsURL); err != nil {
				return fmt.Errorf("videoproc.task: write hls url: %w", err)
			}
		}

		deps.Logger.InfoContext(ctx,
			"videoproc.task: transcoded asset",
			slog.String("asset_id", p.AssetID),
			slog.String("storage_key", p.StorageKey),
			slog.String("hls_url", hlsURL),
			slog.Int("files", uploaded),
		)
		return nil
	}
}

// NewSpec returns the TaskSpec ready to register into a Registry.
// Callers wiring the worker process call NewSpec at boot; callers
// building the producer side (the admin upload path) pass TaskName to
// taskspec.Enqueue.
func NewSpec(deps HandlerDeps) (taskspec.TaskSpec, error) {
	schema, err := jsonschemautil.Compile("https://gonext.example/media-video-transcode.json", payloadSchemaRaw)
	if err != nil {
		return taskspec.TaskSpec{}, fmt.Errorf("videoproc: compile payload schema: %w", err)
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
// for every payload. Used at worker boot when ffmpeg is not on PATH:
// registering the stub keeps Enqueue calls (from the API upload
// handler) from erroring out with "unknown task" while the worker
// gracefully no-ops the work.
//
// The stub still validates the payload schema — so a malformed
// payload from the producer still trips the validator. We only
// short-circuit the handler body, not the upstream contract.
func NewStubSpec(logger *slog.Logger) (taskspec.TaskSpec, error) {
	schema, err := jsonschemautil.Compile("https://gonext.example/media-video-transcode.json", payloadSchemaRaw)
	if err != nil {
		return taskspec.TaskSpec{}, fmt.Errorf("videoproc: compile payload schema: %w", err)
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
				"videoproc.task: ffmpeg not on PATH, skipping transcode",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
			)
			return nil
		},
	}, nil
}

// IsSupportedMIME reports whether mime falls in the family the
// transcoder accepts. The check is permissive on purpose — ffmpeg
// can decode almost anything; we only skip non-video types up-front
// so a stray image upload doesn't burn a worker slot on a guaranteed
// no-op.
func IsSupportedMIME(mime string) bool {
	return strings.HasPrefix(strings.ToLower(mime), "video/")
}

// PayloadSchema returns the compiled schema, useful for tests that
// want to validate a payload outside the Enqueue path.
func PayloadSchema() ([]byte, error) {
	out := make([]byte, len(payloadSchemaRaw))
	copy(out, payloadSchemaRaw)
	return out, nil
}

// segmentMIME maps an HLS output filename to its Content-Type so the
// storage bucket records a useful header for HTTP clients.
//
//   * index.m3u8 → application/vnd.apple.mpegurl
//   * <name>.ts  → video/mp2t
//
// Anything else gets octet-stream — ffmpeg can be coaxed into
// producing oddities (init.mp4 for fMP4), but the default HLS output
// is m3u8 + ts and we cover those.
func segmentMIME(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(filename, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(filename, ".mp4"):
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

// ext returns a safe filename extension for the input scratch file.
// We do not trust the storage key's extension — the on-PATH ffmpeg
// auto-detects format from content — but giving it the right
// extension helps with the demuxer's heuristics for unusual files.
func ext(storageKey string) string {
	idx := strings.LastIndex(storageKey, ".")
	if idx < 0 || idx == len(storageKey)-1 {
		return ".bin"
	}
	candidate := strings.ToLower(storageKey[idx:])
	// Allow only ASCII alnum + dot. A storage key with a weird
	// extension shouldn't end up as a shell metacharacter on disk.
	for _, c := range candidate[1:] {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return ".bin"
		}
	}
	return candidate
}

// sanitize returns a filesystem-safe slug of s. The output is used
// only inside a controlled MkdirTemp pattern, so we only need to
// strip the characters that would break the pattern (path
// separators).
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
