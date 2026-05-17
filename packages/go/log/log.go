package log

import (
	"io"
	"log/slog"
)

// Setup builds a logger from Options, installs it as slog.Default, and
// returns it. Subsequent calls replace the prior default.
//
// w is the destination — typically os.Stdout for JSON-to-stdout logging
// per twelve-factor. Tests may pass a bytes.Buffer.
//
// Returns the configured logger and the validation error (if any).
// On error, slog.Default is left untouched.
func Setup(w io.Writer, o Options) (*slog.Logger, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     o.Level,
		AddSource: o.AddSource,
	}
	if o.Redact {
		handlerOpts.ReplaceAttr = redactAttr
	}

	var h slog.Handler
	switch o.Format {
	case FormatText:
		h = slog.NewTextHandler(w, handlerOpts)
	default:
		h = slog.NewJSONHandler(w, handlerOpts)
	}

	// Pre-stamp identity attrs on every line.
	base := slog.New(h).With(
		slog.String("service", o.Service),
		slog.String("version", o.Version),
		slog.String("commit", o.Commit),
	)

	slog.SetDefault(base)
	return base, nil
}
