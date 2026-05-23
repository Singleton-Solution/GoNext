package imageproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
)

// TaskName is the on-wire identifier for the media-processing task.
// Exported so the upload handler can pass the same constant to
// taskspec.Enqueue, and so a future admin-UI "reprocess this asset"
// button can target the same handler.
const TaskName = "media.process"

// DefaultQueue is the queue name the upload pipeline lands tasks on.
// "default" rather than "critical": image processing is best-effort —
// a delayed thumbnail is bad but not page-down. Sites that want
// thumbnails generated synchronously can flip the spec's Queue to
// "critical" at registration time.
const DefaultQueue = "default"

// DefaultMaxRetry caps how many times asynq will re-run a failing
// task. 3 covers a transient storage hiccup (the GET-original path is
// the most likely failure mode) while keeping a permanently-broken
// upload from cycling forever.
const DefaultMaxRetry = 3

// DefaultTimeout bounds a single Run invocation. A 1536px-edge image
// fits in well under 5 seconds on modest hardware; the buffer is
// generous so a cold CPU or contended worker doesn't trip the
// deadline unnecessarily.
const DefaultTimeout = 30 * time.Second

// Payload is the JSON shape the upload handler enqueues. AssetID and
// StorageKey both ride the wire because the worker uses StorageKey to
// fetch the original bytes and uses AssetID to attribute variants
// back to the row when the storage layer reports variant URLs in
// list responses.
type Payload struct {
	// AssetID is the media row's primary key. Round-trips through
	// the variant manifest write so an admin "reprocess" call can
	// look the row up by ID alone.
	AssetID string `json:"asset_id"`

	// StorageKey is the original upload's object key (e.g.
	// "2026/01/<uuid>-photo.jpg"). The handler fetches the bytes
	// from this key, runs Process, and writes siblings.
	StorageKey string `json:"storage_key"`

	// MIMEType is the sniffed content type of the source. The
	// handler uses it to skip non-image uploads early — the upload
	// path may enqueue every asset for simplicity, with the per-
	// MIME filter living here.
	MIMEType string `json:"mime_type"`
}

// payloadSchema is the JSON Schema validated by taskspec.Enqueue
// before a payload reaches the queue. Keeping the schema close to the
// struct definition means the schema and the Go type can drift only
// if a maintainer touches both files, which surfaces as a test
// failure.
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
// pull original bytes. The wire surface is small enough that tests
// can supply a map-backed fake; production wires it to the same
// minio.Client that backs the admin upload handler.
type Source interface {
	// GetObject returns the bytes stored at key, or an error. A
	// missing key MUST return an error — the handler treats absence
	// as a permanent failure and does not retry.
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// Sink is the write side. Variant siblings land here at
// SourceKey + Variant.KeySuffix. The MIMEType is passed through so
// the bucket records a useful Content-Type even when the suffix
// extension would be ambiguous.
type Sink interface {
	PutObject(ctx context.Context, key string, body []byte, mimeType string) error
}

// ManifestWriter is the optional hook that lets the handler record
// "asset X now has these variants" on the media row. The default
// wiring passes the in-memory store's MarkProcessed method; a nil
// writer is allowed (the handler still produces variants, but the
// list response will not show variant URLs).
type ManifestWriter interface {
	MarkProcessed(ctx context.Context, assetID string, variants []ManifestEntry) error
}

// ManifestEntry is one variant's metadata as seen by the store. The
// shape mirrors imageproc.Variant but drops the bytes and adds the
// storage key — the store layer only needs to know "this variant
// exists at this address, was this size, in this format".
type ManifestEntry struct {
	Name       VariantName `json:"name"`
	Format     Format      `json:"format"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	StorageKey string      `json:"storage_key"`
	MIMEType   string      `json:"mime_type"`
}

// HandlerDeps is the dependency bag for NewHandler. Source and Sink
// are required; ManifestWriter and Logger are optional.
type HandlerDeps struct {
	Source         Source
	Sink           Sink
	ManifestWriter ManifestWriter
	Logger         *slog.Logger
	Options        ProcessOptions
}

// NewHandler returns a TaskSpec.Handler closure that runs the
// pipeline end-to-end. The closure captures deps by value so the
// caller can construct it once at boot and re-register it across
// multiple registries (the registry-merge case taskspec docs).
//
// Errors:
//   - source GetObject failures wrap as transient (asynq retries).
//   - decode failures (ErrUnsupportedFormat) wrap as permanent — the
//     bytes will not decode any better on a retry.
//   - sink PutObject failures wrap as transient.
//   - manifest writer failures wrap as transient.
//
// The handler currently distinguishes transient vs permanent only
// in the error message; the asynq SkipRetry path lives one level
// up in apps/worker where the worker can branch on the error type
// in front of every taskspec handler. This is deliberate: the
// handler doesn't import asynq.
func NewHandler(deps HandlerDeps) func(context.Context, []byte) error {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return func(ctx context.Context, raw []byte) error {
		var p Payload
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("imageproc.task: parse payload: %w", err)
		}
		if p.StorageKey == "" || p.AssetID == "" {
			return errors.New("imageproc.task: storage_key and asset_id are required")
		}
		if p.MIMEType != "" && !IsSupportedMIME(p.MIMEType) {
			deps.Logger.InfoContext(ctx,
				"imageproc.task: skipping unsupported MIME",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
				slog.String("mime_type", p.MIMEType),
			)
			return nil
		}
		if deps.Source == nil || deps.Sink == nil {
			return errors.New("imageproc.task: Source and Sink must be wired")
		}

		body, err := deps.Source.GetObject(ctx, p.StorageKey)
		if err != nil {
			return fmt.Errorf("imageproc.task: fetch source %q: %w", p.StorageKey, err)
		}

		result, err := Process(ctx, bytes.NewReader(body), deps.Options)
		if err != nil {
			return fmt.Errorf("imageproc.task: process %q: %w", p.StorageKey, err)
		}

		manifest := make([]ManifestEntry, 0, len(result.Variants))
		for _, v := range result.Variants {
			variantKey := p.StorageKey + v.KeySuffix
			if err := deps.Sink.PutObject(ctx, variantKey, v.Bytes, v.Format.MIMEType()); err != nil {
				return fmt.Errorf("imageproc.task: put variant %s: %w", variantKey, err)
			}
			manifest = append(manifest, ManifestEntry{
				Name:       v.Name,
				Format:     v.Format,
				Width:      v.Width,
				Height:     v.Height,
				StorageKey: variantKey,
				MIMEType:   v.Format.MIMEType(),
			})
		}
		for _, w := range result.Warnings {
			deps.Logger.InfoContext(ctx,
				"imageproc.task: warning during processing",
				slog.String("asset_id", p.AssetID),
				slog.String("storage_key", p.StorageKey),
				slog.String("warning", w),
			)
		}

		if deps.ManifestWriter != nil {
			if err := deps.ManifestWriter.MarkProcessed(ctx, p.AssetID, manifest); err != nil {
				return fmt.Errorf("imageproc.task: persist manifest: %w", err)
			}
		}

		deps.Logger.InfoContext(ctx,
			"imageproc.task: processed asset",
			slog.String("asset_id", p.AssetID),
			slog.String("storage_key", p.StorageKey),
			slog.Int("variants", len(manifest)),
			slog.Int("warnings", len(result.Warnings)),
		)
		return nil
	}
}

// NewSpec returns the TaskSpec ready to register into a
// taskspec.Registry. The schema is compiled once at first call (the
// jsonschema package caches the compiled draft for repeated lookups).
//
// Callers wiring the worker process call NewSpec at boot and Register
// the result; callers building the producer side (the admin upload
// path) pass the same Name to taskspec.Enqueue.
func NewSpec(deps HandlerDeps) (taskspec.TaskSpec, error) {
	schema, err := jsonschemautil.Compile("https://gonext.example/media-process.json", payloadSchemaRaw)
	if err != nil {
		return taskspec.TaskSpec{}, fmt.Errorf("imageproc: compile payload schema: %w", err)
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

// PayloadSchema returns the compiled schema, useful for tests that
// want to validate a payload outside the Enqueue path. Exported
// rather than the raw bytes so callers can't accidentally drift the
// schema from what the registry sees.
func PayloadSchema() ([]byte, error) {
	// Defensive copy so a caller mutating the slice doesn't poison
	// the package's source of truth.
	out := make([]byte, len(payloadSchemaRaw))
	copy(out, payloadSchemaRaw)
	return out, nil
}
