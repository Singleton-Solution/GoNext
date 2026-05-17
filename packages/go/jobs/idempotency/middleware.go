package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// errorBody is the wire shape of the structured error envelope the
// middleware returns. It matches the wider GoNext error convention
// (single error_code + human-readable message, optional details map).
// We intentionally do NOT use packages/go/httpx/errors so this
// package stays self-contained — idempotency is a low-level concern
// and shouldn't reach back up the dependency graph.
type errorBody struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// Error codes the middleware emits. Keep these in sync with the OpenAPI
// errors enumeration in docs/04-http-api.md (when that section lands).
const (
	// errCodeInvalidKey covers a malformed Idempotency-Key header
	// (empty, oversize, control bytes). 400.
	errCodeInvalidKey = "idempotency_key_invalid"

	// errCodeReused is the 422 returned when the same key is sent
	// with a different request body or route — almost always a
	// client bug.
	errCodeReused = "idempotency_key_reused"

	// errCodePending is the 409 returned when the original request
	// is still running. Clients retry with backoff.
	errCodePending = "idempotency_key_pending"

	// errCodeBodyTooLarge is the 413 we return when the request
	// body exceeds the buffering ceiling.
	errCodeBodyTooLarge = "idempotency_body_too_large"

	// errCodeBackend is the 503 we return when the backing store
	// can't be reached. The client should retry; we deliberately
	// do NOT fall back to a non-idempotent path because that would
	// silently break the contract.
	errCodeBackend = "idempotency_backend_unavailable"
)

// Config is the [Middleware] tuning surface. Zero values pick sane
// defaults so the common case is `New(store)` with no config.
type Config struct {
	// TTL is the lifetime of stored entries. Defaults to
	// [DefaultTTL] (24h).
	TTL time.Duration

	// MaxBodySize caps the request body we'll buffer to compute the
	// canonical hash. Defaults to [DefaultMaxBodySize] (1 MiB).
	MaxBodySize int64

	// MethodAllowList restricts which HTTP methods get idempotency
	// handling. Defaults to {POST, PUT, PATCH, DELETE} — GET / HEAD /
	// OPTIONS are by definition idempotent and don't need the cache.
	// An empty list (zero value) uses the default; pass {"*"} to
	// match everything.
	MethodAllowList []string

	// Now is the time source. Defaults to time.Now. Tests inject a
	// frozen clock here.
	Now func() time.Time

	// Logger is the slog destination for replay events. Nil →
	// slog.Default.
	Logger *slog.Logger
}

// Middleware is an HTTP middleware that wraps an inner handler with
// the Idempotency-Key state machine. See the package doc for the
// outcomes.
//
// The middleware is safe for concurrent use across many goroutines.
// One instance per process is the expected pattern.
type Middleware struct {
	store   Store
	ttl     time.Duration
	maxBody int64
	methods map[string]struct{}
	now     func() time.Time
	logger  *slog.Logger
}

// New constructs a Middleware around store. Pass cfg to override
// defaults; a zero Config picks them all up automatically.
func New(store Store, cfg Config) *Middleware {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	mb := cfg.MaxBodySize
	if mb <= 0 {
		mb = DefaultMaxBodySize
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	methodSet := make(map[string]struct{})
	methods := cfg.MethodAllowList
	if len(methods) == 0 {
		methods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	}
	for _, m := range methods {
		methodSet[m] = struct{}{}
	}

	return &Middleware{
		store:   store,
		ttl:     ttl,
		maxBody: mb,
		methods: methodSet,
		now:     now,
		logger:  logger,
	}
}

// Wrap returns an http.Handler that runs the idempotency state
// machine in front of inner. Requests that fall outside the method
// allowlist or lack the Idempotency-Key header pass straight through.
func (m *Middleware) Wrap(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Method check first: cheaper than parsing the header.
		if !m.methodApplies(r.Method) {
			inner.ServeHTTP(w, r)
			return
		}

		k, hasHeader, err := NewKeyFromRequest(r, m.maxBody)
		if !hasHeader {
			// No Idempotency-Key — pass through, the operation isn't
			// idempotency-protected. The caller chose not to opt in.
			inner.ServeHTTP(w, r)
			return
		}
		if err != nil {
			m.rejectInvalid(w, r, err)
			return
		}

		ctx := r.Context()
		outcome, res, cErr := m.store.Claim(ctx, k, m.ttl)
		if cErr != nil {
			m.rejectBackend(w, r, cErr)
			return
		}

		switch outcome {
		case ClaimReplay:
			m.replay(w, r, k, res)
			return
		case ClaimMismatch:
			m.rejectMismatch(w, r, k)
			return
		case ClaimPending:
			m.rejectPending(w, r, k)
			return
		case ClaimNew:
			m.dispatch(w, r, inner, k)
			return
		default:
			m.rejectBackend(w, r, errors.New("idempotency: unknown outcome"))
			return
		}
	})
}

// methodApplies reports whether the configured allowlist covers m.
// "*" matches everything; an explicit method match wins.
func (m *Middleware) methodApplies(method string) bool {
	if _, ok := m.methods["*"]; ok {
		return true
	}
	_, ok := m.methods[method]
	return ok
}

// dispatch runs the inner handler, captures its response, stores the
// snapshot, and writes the same response back to the client. The
// capturing response writer buffers the body in memory — same trade-
// off as the request hash: we need to store the full body anyway,
// so streaming wouldn't save anything.
func (m *Middleware) dispatch(w http.ResponseWriter, r *http.Request, inner http.Handler, k Key) {
	cw := &captureWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
		body:           &bytes.Buffer{},
	}
	inner.ServeHTTP(cw, r)

	// Decide terminal status from the captured code. 2xx ⇒ succeeded,
	// everything else ⇒ failed. Both states are stored — see the
	// rationale on [StatusFailed].
	status := StatusSucceeded
	if cw.status < 200 || cw.status >= 300 {
		status = StatusFailed
	}
	result := Result{Code: cw.status, Body: cw.body.Bytes()}

	// Write through to the store. We deliberately use a fresh,
	// short-deadline context: the original request's context may
	// have been cancelled by the client right after they got the
	// response, and we still want to persist the result so a retry
	// hits the cache.
	finCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.store.Finish(finCtx, k, status, result, m.ttl); err != nil {
		// Storing failed — log and move on. The client already got
		// its response (cw flushed inline); a stale claim in the
		// store will TTL out on its own.
		m.logger.WarnContext(r.Context(), "idempotency: store.Finish failed",
			slog.String("key", k.Value),
			slog.String("err", err.Error()))
	}
}

// replay writes the stored response without invoking inner. We add
// an Idempotency-Replayed: true header so clients can observe the
// cache hit when debugging.
func (m *Middleware) replay(w http.ResponseWriter, r *http.Request, k Key, res Result) {
	w.Header().Set("Idempotency-Replayed", "true")
	// Default to JSON; the inner handler set its own Content-Type
	// on the first run, but we don't store headers separately —
	// the body is the load-bearing part. Operators who need full
	// header fidelity should serialize them into the body envelope.
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	code := res.Code
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	if len(res.Body) > 0 {
		_, _ = w.Write(res.Body)
	}
	m.logger.DebugContext(r.Context(), "idempotency: replay hit",
		slog.String("key", k.Value),
		slog.Int("code", code))
}

// rejectInvalid is the 400 path for a malformed header. We
// distinguish "body too large" (413) from generic malformations.
func (m *Middleware) rejectInvalid(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrBodyTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, errCodeBodyTooLarge,
			"request body exceeds the idempotency buffering limit")
		return
	}
	writeError(w, http.StatusBadRequest, errCodeInvalidKey, err.Error())
}

// rejectMismatch is the 422 path. We surface the error_code so
// clients can branch on it programmatically — the message is purely
// for humans.
func (m *Middleware) rejectMismatch(w http.ResponseWriter, r *http.Request, k Key) {
	m.logger.WarnContext(r.Context(), "idempotency: key reused with different body",
		slog.String("key", k.Value))
	writeError(w, http.StatusUnprocessableEntity, errCodeReused,
		"the Idempotency-Key was previously used for a different request")
}

// rejectPending is the 409 path. We hint at the right client
// behaviour in the message — retry with backoff.
func (m *Middleware) rejectPending(w http.ResponseWriter, r *http.Request, k Key) {
	m.logger.InfoContext(r.Context(), "idempotency: concurrent replay",
		slog.String("key", k.Value))
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusConflict, errCodePending,
		"a request with this Idempotency-Key is still in progress; retry shortly")
}

// rejectBackend is the 503 path for a store outage. We do NOT fall
// through to the inner handler because that would silently violate
// the idempotency contract — better to fail loudly so the client
// retries against a healthy replica.
func (m *Middleware) rejectBackend(w http.ResponseWriter, r *http.Request, err error) {
	m.logger.ErrorContext(r.Context(), "idempotency: backend error",
		slog.String("err", err.Error()))
	writeError(w, http.StatusServiceUnavailable, errCodeBackend,
		"idempotency store is temporarily unavailable")
}

// writeError serializes a structured error response with the given
// status code. Errors during the write are unrecoverable (the
// response is already half-sent) and logged at the call site.
func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	body, err := json.Marshal(errorBody{ErrorCode: errCode, Message: msg})
	if err != nil {
		// Should be unreachable — fixed-shape struct. Fall back to
		// plain text so we still respond.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(msg))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// captureWriter is the http.ResponseWriter wrapper used in
// [Middleware.dispatch] to mirror the response into a buffer for
// storage. It flushes to the underlying writer inline so the client
// observes the same latency profile as the un-wrapped handler.
type captureWriter struct {
	http.ResponseWriter

	status      int
	body        *bytes.Buffer
	wroteHeader bool
}

func (c *captureWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.status = status
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	// Mirror into the buffer first. If the buffer write fails (out
	// of memory), we still pass through to the underlying writer so
	// the client gets a response; the missing snapshot will surface
	// at Finish-time.
	c.body.Write(p)
	return c.ResponseWriter.Write(p)
}
