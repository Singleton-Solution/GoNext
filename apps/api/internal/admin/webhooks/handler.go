package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/webhooks/delivery"
)

// defaultListLimit is the page size when the client supplies no
// limit. Mirrors the rest of the admin surface.
const defaultListLimit = 30

// maxListLimit caps the page size; matches the jobs admin handler.
const maxListLimit = 100

// secretLen is the byte length of a freshly generated HMAC key. 32
// bytes (256 bits) is the standard for HMAC-SHA-256 — anything
// shorter loses entropy, anything longer is hashed down internally
// so there is no security benefit.
const secretLen = 32

// testRequestTimeout caps the Test endpoint's HTTP send. The
// operator clicks "Test" and waits — a 10s ceiling keeps the round
// trip short enough that the UI doesn't have to background it.
const testRequestTimeout = 10 * time.Second

// maxResponseBodyPreview caps how much of the test endpoint's
// response body we surface. 2 KiB is enough to read a typical
// "your secret is wrong" error without bloating the JSON envelope.
const maxResponseBodyPreview = 2 * 1024

// Deps is the dependency bag for Mount. Required fields are checked
// at Mount time rather than NPE'ing on the first request.
type Deps struct {
	// Store persists subscription rows + delivery audit entries.
	// Required.
	Store Store

	// Policy resolves the webhooks.manage capability check.
	// Required.
	Policy policy.Policy

	// HTTPClient is the HTTP client used to deliver the synthetic
	// test event. nil falls back to a clock-pinned client with the
	// testRequestTimeout. Production wiring can pass a client
	// instrumented with metrics/tracing.
	HTTPClient *http.Client

	// Now is the time source. nil falls back to time.Now. Tests pin
	// this so the signature header is deterministic across runs.
	Now func() time.Time

	// CurrentUserID, when non-nil, resolves the operator's user ID
	// for the created_by audit column. nil falls back to the
	// principal's UserID.
	CurrentUserID func(*http.Request) string

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/webhooks: Store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/webhooks: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form held inside the package.
type handlers struct {
	store      Store
	policy     policy.Policy
	httpClient *http.Client
	now        func() time.Time
	currentUID func(*http.Request) string
	logger     *slog.Logger
}

// Mount wires the webhooks admin routes onto mux under base
// (typically "/api/v1/admin/webhooks"). Returns an error rather
// than panicking so the boot path can surface it.
//
// Route tree (all gated by webhooks.manage):
//
//	GET    {base}                   — list subscriptions (paginated)
//	POST   {base}                   — create a subscription
//	GET    {base}/{id}              — fetch a subscription
//	PATCH  {base}/{id}              — partial update
//	DELETE {base}/{id}              — remove subscription
//	POST   {base}/{id}/test         — send a synthetic webhook.test event
//	POST   {base}/{id}/disable      — flip Active off (convenience)
//	POST   {base}/{id}/enable       — flip Active on (convenience)
//	GET    {base}/{id}/deliveries   — paginated audit log
//	GET    {base}/events            — event catalog the UI renders
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = &http.Client{Timeout: testRequestTimeout}
	}
	if deps.CurrentUserID == nil {
		deps.CurrentUserID = func(r *http.Request) string {
			if pr, ok := policy.FromContext(r.Context()); ok {
				return pr.UserID
			}
			return ""
		}
	}

	h := &handlers{
		store:      deps.Store,
		policy:     deps.Policy,
		httpClient: deps.HTTPClient,
		now:        deps.Now,
		currentUID: deps.CurrentUserID,
		logger:     deps.Logger,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.gate(h.list))
	mux.Handle("POST "+base, h.gate(h.create))
	mux.Handle("GET "+base+"/events", h.gate(h.events))
	mux.Handle("GET "+base+"/{id}", h.gate(h.get))
	mux.Handle("PATCH "+base+"/{id}", h.gate(h.update))
	mux.Handle("DELETE "+base+"/{id}", h.gate(h.delete))
	mux.Handle("POST "+base+"/{id}/test", h.gate(h.test))
	mux.Handle("POST "+base+"/{id}/disable", h.gate(h.disable))
	mux.Handle("POST "+base+"/{id}/enable", h.gate(h.enable))
	mux.Handle("GET "+base+"/{id}/deliveries", h.gate(h.deliveries))
	return nil
}

// gate wraps a handler with the auth + webhooks.manage check. Same
// shape as the jobs admin gate so operators looking at the codebase
// see a consistent pattern.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapWebhooksManage, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// list handles GET {base}. Query params: limit, cursor.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed")
		return
	}

	rows, next, err := h.store.List(r.Context(), limit, cursor)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list subscriptions")
		return
	}
	router.WriteJSON(w, http.StatusOK, router.Page[Subscription]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: router.EncodeCursor(next),
		},
	})
}

// create handles POST {base}.
func (h *handlers) create(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	var body SubscriptionCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	if err := validateCreate(body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	secret, err := generateSecret()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: secret generation failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to generate secret")
		return
	}

	sub, err := h.store.Create(r.Context(), body, secret, h.currentUID(r))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: create failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to create subscription")
		return
	}

	router.WriteJSON(w, http.StatusCreated, SubscriptionWithSecret{
		Subscription: sub,
		Secret:       hex.EncodeToString(secret),
	})
}

// get handles GET {base}/{id}.
func (h *handlers) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	sub, err := h.store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: get failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load subscription")
		return
	}
	router.WriteJSON(w, http.StatusOK, sub)
}

// update handles PATCH {base}/{id}.
func (h *handlers) update(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	var body SubscriptionUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	if err := validateUpdate(body); err != nil {
		router.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	sub, err := h.store.Update(r.Context(), id, body)
	if errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: update failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to update subscription")
		return
	}
	router.WriteJSON(w, http.StatusOK, sub)
}

// delete handles DELETE {base}/{id}.
func (h *handlers) delete(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	if err := h.store.Delete(r.Context(), id); errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: delete failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to delete subscription")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// disable / enable are convenience endpoints over the active flag.
// We expose them so the UI's row buttons don't have to construct a
// PATCH body (and so audit log entries can pin the action verb).
func (h *handlers) disable(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	h.flipActive(w, r, false)
}

func (h *handlers) enable(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	h.flipActive(w, r, true)
}

func (h *handlers) flipActive(w http.ResponseWriter, r *http.Request, target bool) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	sub, err := h.store.Update(r.Context(), id, SubscriptionUpdate{Active: &target})
	if errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: flip active failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to update subscription")
		return
	}
	router.WriteJSON(w, http.StatusOK, sub)
}

// events handles GET {base}/events. The UI calls this on the create
// form mount to populate the multi-select.
func (h *handlers) events(w http.ResponseWriter, _ *http.Request, _ policy.Principal) {
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"data": Catalog(),
	})
}

// test handles POST {base}/{id}/test. Sends a synthetic
// webhook.test event to the subscription's URL with a proper
// signature header, then returns the result synchronously.
//
// The endpoint is deliberately synchronous (rather than enqueueing
// onto the delivery worker): operators want a yes/no answer to "did
// my endpoint receive a properly-signed request?". An enqueue-based
// implementation would force a poll loop in the UI for what is
// fundamentally a one-shot reachability check.
func (h *handlers) test(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	sub, err := h.store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: test load failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load subscription")
		return
	}

	secret, err := h.store.Secret(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: test secret resolve failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to resolve secret")
		return
	}

	res := h.sendTest(r.Context(), sub, secret)

	// Record the attempt in the audit log so the UI's deliveries
	// table can show the test alongside real events. Status "test"
	// is excluded from the consecutive_failures counter so a
	// failing test doesn't flip a healthy subscription into the
	// degraded state.
	if err := h.store.RecordDelivery(r.Context(), Delivery{
		SubscriptionID: sub.ID,
		EventID:        fmt.Sprintf("test:%d", h.now().UnixNano()),
		EventType:      EventTypeTest,
		Attempt:        1,
		Status:         "test",
		ResponseCode:   res.ResponseCode,
		DurationMs:     res.DurationMs,
		Error:          res.Error,
		DeliveredAt:    h.now(),
	}); err != nil {
		// Audit failure shouldn't fail the user's action — log and
		// continue.
		h.logger.WarnContext(r.Context(), "admin/webhooks: test audit write failed", slog.Any("err", err))
	}

	router.WriteJSON(w, http.StatusOK, res)
}

// sendTest performs the synchronous HTTP send and returns the
// classified outcome. Pulled out of the handler so unit tests can
// exercise the wire format without going through the HTTP server.
func (h *handlers) sendTest(ctx context.Context, sub Subscription, secret []byte) TestResult {
	if !isAllowedURL(sub.URL) {
		return TestResult{Error: "subscription URL scheme is not allowed"}
	}

	body, err := json.Marshal(map[string]any{
		"event":           EventTypeTest,
		"subscription_id": sub.ID,
		"emitted_at":      h.now().Format(time.RFC3339Nano),
	})
	if err != nil {
		// json.Marshal on a map of plain Go primitives shouldn't fail;
		// guard against the future-proof "someone adds a non-encodable
		// field" by surfacing rather than panicking.
		return TestResult{Error: fmt.Sprintf("encode body: %v", err)}
	}

	now := h.now()
	sig := delivery.Sign(secret, now, body)

	ctx, cancel := context.WithTimeout(ctx, testRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return TestResult{Error: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(delivery.SignatureHeader, sig)
	req.Header.Set(delivery.EventTypeHeader, EventTypeTest)
	req.Header.Set(delivery.SubscriptionIDHeader, sub.ID)
	req.Header.Set(delivery.TimestampHeader, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(delivery.AttemptHeader, "1")

	start := h.now()
	resp, err := h.httpClient.Do(req)
	durationMs := int(h.now().Sub(start) / time.Millisecond)

	if err != nil {
		return TestResult{Error: err.Error(), DurationMs: durationMs}
	}
	defer resp.Body.Close()
	// Drain to a cap; the body content isn't surfaced in the result
	// envelope (the deliveries audit row carries a preview), so we
	// only care about preventing connection-reuse loss + bounding
	// memory.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBodyPreview))

	return TestResult{
		Delivered:    resp.StatusCode >= 200 && resp.StatusCode < 300,
		ResponseCode: resp.StatusCode,
		DurationMs:   durationMs,
	}
}

// deliveries handles GET {base}/{id}/deliveries.
func (h *handlers) deliveries(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "subscription id is required")
		return
	}
	// 404 on unknown subscription so the UI doesn't show an empty
	// deliveries pane when the operator typed an invalid ID into
	// the URL.
	if _, err := h.store.Get(r.Context(), id); errors.Is(err, ErrNotFound) {
		router.WriteError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: deliveries load failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load subscription")
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed")
		return
	}

	rows, next, err := h.store.ListDeliveries(r.Context(), id, limit, cursor)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/webhooks: deliveries list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list deliveries")
		return
	}
	router.WriteJSON(w, http.StatusOK, router.Page[Delivery]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: router.EncodeCursor(next),
		},
	})
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// parseLimit parses the optional `limit` query param. Returns the
// default when empty, clamps to maxListLimit, and rejects values
// outside [1, ∞).
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > maxListLimit {
		n = maxListLimit
	}
	return n, nil
}

// decodeCursor unwraps the opaque cursor that the list endpoints
// hand back. Empty string is the "first page" sentinel.
func decodeCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return router.ParseCursor(raw)
}

// generateSecret produces a fresh HMAC key of secretLen bytes.
func generateSecret() ([]byte, error) {
	b := make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	return b, nil
}

// validateCreate enforces required fields and the basic shape rules.
// The DB layer has CHECK constraints as a backstop; doing the check
// here too gives the operator an actionable 400 with a friendly
// message rather than a generic constraint violation.
func validateCreate(in SubscriptionCreate) error {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	if in.Name == "" {
		return errors.New("name is required")
	}
	if len(in.Name) > 200 {
		return errors.New("name must be 200 characters or fewer")
	}
	if in.URL == "" {
		return errors.New("url is required")
	}
	if !isAllowedURL(in.URL) {
		return errors.New("url must be an absolute http(s) URL")
	}
	if err := validateEvents(in.Events); err != nil {
		return err
	}
	return nil
}

// validateUpdate enforces shape rules on a partial update. nil
// pointers are skipped — that is the "leave alone" signal.
func validateUpdate(in SubscriptionUpdate) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return errors.New("name must not be empty")
		}
		if len(n) > 200 {
			return errors.New("name must be 200 characters or fewer")
		}
	}
	if in.URL != nil {
		u := strings.TrimSpace(*in.URL)
		if u == "" {
			return errors.New("url must not be empty")
		}
		if !isAllowedURL(u) {
			return errors.New("url must be an absolute http(s) URL")
		}
	}
	if in.Events != nil {
		if err := validateEvents(*in.Events); err != nil {
			return err
		}
	}
	return nil
}

// validateEvents enforces that every event name in the slice exists
// in the catalog. Empty slice is allowed (the subscription matches
// nothing, which the worker treats as inactive); the API does not
// silently drop unknown event names because that would let a
// fat-fingered subscription pass validation but never fire.
func validateEvents(events []string) error {
	if len(events) == 0 {
		return nil
	}
	valid := validEvents()
	for _, e := range events {
		if _, ok := valid[e]; !ok {
			return fmt.Errorf("event %q is not in the catalog", e)
		}
	}
	return nil
}

// isAllowedURL returns true for absolute URLs whose scheme is http or
// https. The handler enforces this both on create (so we don't
// persist a junk row) and on test send (so a row that was somehow
// inserted with a bad scheme can't be hit). The delivery worker has
// its own scheme allowlist as well.
func isAllowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}
