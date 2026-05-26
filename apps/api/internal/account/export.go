// Package account is the user-facing self-service surface. Issue #225.
//
// Today it ships the GDPR data-export endpoint
// (POST /api/v1/account/data/export) — the public companion to the
// admin Settings → Privacy form. The endpoint is gated by the
// [settings.PrivacyAllowGDPRSelfService] toggle: when an operator
// flips it off the endpoint returns 403, and the admin UI hides the
// user-facing affordance.
//
// The export itself is a synchronous JSON blob (small payload, low
// throughput) keyed by the caller's user ID. Future revisions can
// promote it to a background job; the wire shape is forward-compatible.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/settings"
)

// SettingsReader is the slice of [settings.Store] this package needs.
type SettingsReader interface {
	Read(ctx context.Context, key string) (any, error)
}

// ExportProducer assembles the per-user data export. Implementations
// pull from posts, comments, audit, etc. The contract is "return a
// JSON-serializable map keyed by domain"; the handler wraps it in the
// envelope.
type ExportProducer func(ctx context.Context, userID string) (map[string]any, error)

// Deps is the dependency bag for [Mount].
type Deps struct {
	Settings SettingsReader
	Producer ExportProducer
	Logger   *slog.Logger
}

func (d Deps) validate() error {
	if d.Settings == nil {
		return errors.New("account: Deps.Settings is required")
	}
	if d.Producer == nil {
		return errors.New("account: Deps.Producer is required")
	}
	return nil
}

type handlers struct {
	deps Deps
	log  *slog.Logger
}

// Mount wires the account routes onto mux. base is typically
// "/api/v1/account".
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{deps: deps, log: deps.Logger}
	base = strings.TrimRight(base, "/")
	mux.Handle("POST "+base+"/data/export", http.HandlerFunc(h.export))
	return nil
}

// export is the POST /api/v1/account/data/export handler.
//
// Auth path:
//   - The caller MUST carry a principal on the context (the auth
//     middleware enforces this upstream). Anonymous traffic gets 401.
//   - The operator MUST have flipped on PrivacyAllowGDPRSelfService.
//     A false value yields 403 with code "gdpr_disabled".
//
// On success the response is a JSON envelope:
//
//	{
//	  "exported_at": "2025-01-01T00:00:00Z",
//	  "user_id":     "<uuid>",
//	  "data":        { ... }
//	}
//
// The producer is responsible for the contents of `data`.
func (h *handlers) export(w http.ResponseWriter, r *http.Request) {
	p, ok := policy.FromContext(r.Context())
	if !ok || p.UserID == "" {
		router.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	v, err := h.deps.Settings.Read(r.Context(), settings.PrivacyAllowGDPRSelfService)
	if err != nil {
		h.log.ErrorContext(r.Context(), "account/export: read setting",
			slog.String("key", settings.PrivacyAllowGDPRSelfService),
			slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "privacy gate unavailable")
		return
	}
	enabled, _ := v.(bool)
	if !enabled {
		router.WriteError(w, http.StatusForbidden, "gdpr_disabled",
			"the operator has disabled user-facing data exports")
		return
	}

	data, err := h.deps.Producer(r.Context(), p.UserID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "account/export: producer", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to assemble export")
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	router.WriteJSON(w, http.StatusOK, exportEnvelope{
		ExportedAt: time.Now().UTC(),
		UserID:     p.UserID,
		Data:       data,
	})
}

type exportEnvelope struct {
	ExportedAt time.Time      `json:"exported_at"`
	UserID     string         `json:"user_id"`
	Data       map[string]any `json:"data"`
}

// MarshalEnvelope is exported for the OpenAPI generator. Tests that
// assert against the wire shape can decode into the same struct via
// json.Unmarshal.
func MarshalEnvelope(userID string, data map[string]any) ([]byte, error) {
	return json.Marshal(exportEnvelope{
		ExportedAt: time.Now().UTC(),
		UserID:     userID,
		Data:       data,
	})
}
