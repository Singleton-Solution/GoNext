package log

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
)

// Format names a slog handler kind.
type Format string

const (
	// FormatJSON emits one JSON object per log line. Default. Required in production.
	FormatJSON Format = "json"
	// FormatText emits human-readable key=value lines. Use in dev only.
	FormatText Format = "text"
)

// Options configures Setup.
type Options struct {
	// Service is the binary's logical name ("api", "worker", "cli", "wpc-seo").
	// Pre-stamped on every line as service=...; required.
	Service string

	// Version, Commit are pre-stamped on every line. If empty, populated from
	// buildinfo.Get(Service).
	Version string
	Commit  string

	// Level is the minimum level emitted. Defaults to INFO.
	Level slog.Level

	// Format selects the handler. Defaults to FormatJSON.
	Format Format

	// AddSource emits the file:line of the call site. Off by default — adds
	// noticeable overhead; enable only when debugging.
	AddSource bool

	// Redact disables the redactor when set to false. Default is true.
	// Tests may want raw output; production must leave this true.
	Redact bool
}

// OptionsFromEnv builds Options from environment variables, with sensible
// defaults. service is the binary name (e.g. "api"). Honors:
//
//	GONEXT_LOG_LEVEL    DEBUG | INFO | WARN | ERROR
//	GONEXT_LOG_FORMAT   json | text
//	GONEXT_LOG_ADDSRC   1 | 0
//
// Unknown values fall back to the default; never panics.
func OptionsFromEnv(service string) Options {
	opts := Options{
		Service: service,
		Level:   slog.LevelInfo,
		Format:  FormatJSON,
		Redact:  true,
	}

	if lvl := os.Getenv("GONEXT_LOG_LEVEL"); lvl != "" {
		switch strings.ToUpper(strings.TrimSpace(lvl)) {
		case "DEBUG":
			opts.Level = slog.LevelDebug
		case "INFO":
			opts.Level = slog.LevelInfo
		case "WARN", "WARNING":
			opts.Level = slog.LevelWarn
		case "ERROR":
			opts.Level = slog.LevelError
		}
	}

	if fmt := os.Getenv("GONEXT_LOG_FORMAT"); fmt != "" {
		switch strings.ToLower(strings.TrimSpace(fmt)) {
		case "json":
			opts.Format = FormatJSON
		case "text":
			opts.Format = FormatText
		}
	}

	if src := os.Getenv("GONEXT_LOG_ADDSRC"); src == "1" || strings.EqualFold(src, "true") {
		opts.AddSource = true
	}

	// Populate Version / Commit from buildinfo if caller didn't override.
	if opts.Version == "" || opts.Commit == "" {
		bi := buildinfo.Get(service)
		if opts.Version == "" {
			opts.Version = bi.Version
		}
		if opts.Commit == "" {
			opts.Commit = bi.Commit
		}
	}

	return opts
}

// validate returns an error if Options would produce an unusable logger.
func (o Options) validate() error {
	if o.Service == "" {
		return fmt.Errorf("log.Options: Service is required")
	}
	switch o.Format {
	case FormatJSON, FormatText:
	default:
		return fmt.Errorf("log.Options: unknown Format %q (want json or text)", o.Format)
	}
	switch o.Level {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
	default:
		return fmt.Errorf("log.Options: unsupported Level %v (want DEBUG/INFO/WARN/ERROR)", o.Level)
	}
	return nil
}
