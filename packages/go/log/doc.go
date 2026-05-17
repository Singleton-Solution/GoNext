// Package log is the GoNext process-wide structured logger.
//
// It wraps log/slog with three opinionated additions:
//
//   - A redacting handler that masks sensitive fields (passwords, tokens,
//     bearer/cookie headers, JWT-shaped strings, credit card numbers,
//     SSN, email — partial — and so on). Redaction is "fail-closed on
//     match, fail-open on parse error" — when the redactor isn't sure
//     a value is sensitive it lets it through.
//
//   - Context propagation: WithLogger / FromContext attach a logger to a
//     context, and WithRequest decorates the context-bound logger with
//     per-request fields (trace_id, span_id, request_id, user_id, etc.).
//     The HTTP middleware in packages/go/middleware sets these.
//
//   - A base logger pre-stamped with the binary's identity (service,
//     version, commit) from packages/go/buildinfo, so every log line
//     can be attributed to a specific build without operator config.
//
// Configuration comes from the environment by default:
//
//	GONEXT_LOG_LEVEL    DEBUG | INFO | WARN | ERROR   (default INFO)
//	GONEXT_LOG_FORMAT   json | text                   (default json)
//	GONEXT_LOG_ADDSRC   1 | 0                         (default 0)
//
// Application code should use the package-level helpers:
//
//	// At program start:
//	if _, err := log.Setup(os.Stdout, log.OptionsFromEnv("api")); err != nil {
//	    fmt.Fprintln(os.Stderr, "log setup failed:", err)
//	    os.Exit(1)
//	}
//
//	// In a request handler:
//	l := log.FromContext(ctx)
//	l.Info("post published", "post_id", id)
//
// See docs/10-observability.md §4 and ADR 0010 for design rationale.
package log
