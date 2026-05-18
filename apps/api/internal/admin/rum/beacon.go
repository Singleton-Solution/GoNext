package rum

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// BeaconHandler is the POST /_/rum/beacon handler. It is
// intentionally anonymous (no auth middleware) because the public
// theme's beacon library runs in every visitor's browser; gating
// it on a session would require a login wall before any
// measurement, which defeats the point.
//
// Defence in depth comes from four places:
//
//  1. The endpoint is mounted under "/_/" — by convention these
//     are operational endpoints not part of the public API; an
//     operator may further restrict at the reverse-proxy layer.
//
//  2. Body size is capped at MaxBodyBytes via http.MaxBytesReader.
//     A slow reader can't pin a goroutine on a long body — the
//     reader returns *http.MaxBytesError after the cap is hit.
//
//  3. Batch size is capped at MaxBatchSize. A client that wants
//     to send more events must do so across multiple requests
//     (which the per-IP rate limiter then throttles).
//
//  4. The rate-limit middleware (wired by the caller via
//     ratelimit.Middleware) drops requests that exceed the
//     operator-configured per-IP rate. We do not enforce a
//     specific policy in this package — different deployments
//     want different envelopes — but Mount accepts a Limiter and
//     wraps the handler with it.
//
// On success the response is 204 No Content. On rejection the
// response is a problem-details JSON body via router.WriteError;
// the client doesn't act on the response (sendBeacon discards
// it) but a developer inspecting Network shouldn't see "200 OK"
// when the body was rejected.
type BeaconHandler struct {
	store  EventStore
	now    func() time.Time
	logger *slog.Logger
}

// NewBeaconHandler wires a BeaconHandler around the given store.
// now is the clock used to stamp ts on inserted rows; tests pin
// it, production passes time.Now. logger is the structured
// logger; nil falls back to slog.Default.
func NewBeaconHandler(store EventStore, now func() time.Time, logger *slog.Logger) (*BeaconHandler, error) {
	if store == nil {
		return nil, errors.New("rum.NewBeaconHandler: store is required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BeaconHandler{store: store, now: now, logger: logger}, nil
}

// ServeHTTP implements http.Handler.
func (h *BeaconHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Beacon is POST-only. GET to /_/rum/beacon is a probe;
		// 405 with a hint header is the polite answer.
		w.Header().Set("Allow", "POST")
		router.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	// MaxBytesReader so a 20 MiB body doesn't OOM the server.
	// The reader fires *http.MaxBytesError once the cap is
	// exceeded, which we surface as 413.
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			router.WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large", "beacon body exceeds 16KiB")
			return
		}
		// Truncated read or client disconnect — log and 400.
		// 5xx would imply server fault; this is a client-shaped
		// failure and the beacon library doesn't retry on 4xx,
		// which is the behaviour we want.
		h.logger.WarnContext(r.Context(), "rum.beacon: body read failed",
			slog.String("err", err.Error()),
		)
		router.WriteError(w, http.StatusBadRequest, "read_failed", "failed to read body")
		return
	}

	// Trim ASCII whitespace so a body like " { ... } " parses.
	body = trimSpace(body)
	if len(body) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_body", "beacon body is empty")
		return
	}

	var batch Batch
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&batch); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "beacon body is not valid JSON")
		return
	}

	if err := validateBatch(batch); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_batch", err.Error())
		return
	}

	if err := h.store.Insert(r.Context(), h.now().UTC(), batch.Events); err != nil {
		h.logger.ErrorContext(r.Context(), "rum.beacon: insert failed",
			slog.String("err", err.Error()),
			slog.Int("event_count", len(batch.Events)),
		)
		router.WriteError(w, http.StatusInternalServerError, "insert_failed", "failed to persist beacon")
		return
	}

	// 204 No Content — the beacon library never reads the body.
	// We deliberately skip Cache-Control: the caller's CDN
	// shouldn't see this endpoint, but if it does, sending no
	// Cache-Control lets the proxy default (which is "don't
	// cache POSTs") apply.
	w.WriteHeader(http.StatusNoContent)
}

// trimSpace removes ASCII whitespace from both ends of b without
// allocating. strings.TrimSpace would allocate; bytes.TrimSpace
// returns a sub-slice but pulls in the bytes package for a
// 6-line function. Inlining keeps the hot path lean.
func trimSpace(b []byte) []byte {
	lo, hi := 0, len(b)
	for lo < hi && isASCIISpace(b[lo]) {
		lo++
	}
	for hi > lo && isASCIISpace(b[hi-1]) {
		hi--
	}
	return b[lo:hi]
}

func isASCIISpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
