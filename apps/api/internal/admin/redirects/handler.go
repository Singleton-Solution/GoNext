package redirects

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	rdr "github.com/Singleton-Solution/GoNext/packages/go/redirects"
)

// Deps is the dependency bag for Mount.
type Deps struct {
	Store  rdr.Store
	Engine *rdr.Engine
	Logger *slog.Logger
	Now    func() time.Time
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/redirects: Store is required")
	}
	return nil
}

type handlers struct {
	store  rdr.Store
	engine *rdr.Engine
	logger *slog.Logger
	now    func() time.Time
}

// Mount wires the /redirects routes onto mux under base.
//
// Route tree:
//
//	GET    {base}                 — list (paginated, ?search= filter)
//	GET    {base}/top             — top-traffic redirects
//	POST   {base}                 — create
//	POST   {base}/test-regex      — validate a regex without persisting
//	GET    {base}/{id}            — fetch one
//	PUT    {base}/{id}            — update
//	DELETE {base}/{id}            — delete
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
	h := &handlers{
		store:  deps.Store,
		engine: deps.Engine,
		logger: deps.Logger,
		now:    deps.Now,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.list))
	mux.Handle("GET "+base+"/top", http.HandlerFunc(h.top))
	mux.Handle("POST "+base, http.HandlerFunc(h.create))
	mux.Handle("POST "+base+"/test-regex", http.HandlerFunc(h.testRegex))
	mux.Handle("GET "+base+"/{id}", http.HandlerFunc(h.get))
	mux.Handle("PUT "+base+"/{id}", http.HandlerFunc(h.update))
	mux.Handle("DELETE "+base+"/{id}", http.HandlerFunc(h.delete))
	return nil
}

// RuleView is the on-wire shape returned to the admin UI. Mirrors
// rdr.Rule but flattens timestamps to string form so the UI doesn't
// have to do Go-vs-JSON time parsing gymnastics.
type RuleView struct {
	ID              string     `json:"id"`
	SourcePath      string     `json:"source_path"`
	DestinationPath string     `json:"destination_path"`
	Status          int        `json:"status"`
	IsRegex         bool       `json:"is_regex"`
	HitCount        int64      `json:"hit_count"`
	LastHitAt       *time.Time `json:"last_hit_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       *string    `json:"created_by,omitempty"`
}

func toView(r rdr.Rule) RuleView {
	v := RuleView{
		ID:              r.ID.String(),
		SourcePath:      r.SourcePath,
		DestinationPath: r.DestinationPath,
		Status:          r.Status,
		IsRegex:         r.IsRegex,
		HitCount:        r.HitCount,
		CreatedAt:       r.CreatedAt,
	}
	if !r.LastHitAt.IsZero() {
		t := r.LastHitAt
		v.LastHitAt = &t
	}
	if r.CreatedBy.Valid {
		s := r.CreatedBy.UUID.String()
		v.CreatedBy = &s
	}
	return v
}

type ruleRequest struct {
	SourcePath      string `json:"source_path"`
	DestinationPath string `json:"destination_path"`
	Status          int    `json:"status"`
	IsRegex         bool   `json:"is_regex"`
}

func (req ruleRequest) toRule() rdr.Rule {
	return rdr.Rule{
		SourcePath:      strings.TrimSpace(req.SourcePath),
		DestinationPath: strings.TrimSpace(req.DestinationPath),
		Status:          req.Status,
		IsRegex:         req.IsRegex,
	}
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	before := parseBefore(r.URL.Query().Get("before"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	rules, err := h.store.List(r.Context(), before, limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/redirects: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list redirects")
		return
	}
	out := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		if search != "" && !strings.Contains(strings.ToLower(rule.SourcePath), strings.ToLower(search)) {
			continue
		}
		out = append(out, toView(rule))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"data":       out,
		"pagination": map[string]any{"next_cursor": nextCursor(rules)},
	})
}

func (h *handlers) top(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 20)
	// Top is sourced from the durable store; the engine's pending
	// counters aren't authoritative until they flush. The admin tab
	// is OK with "as of last flush" precision.
	snap, err := h.store.Snapshot(r.Context())
	if err != nil {
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load redirects")
		return
	}
	// Sort by hit_count DESC.
	for i := 0; i < len(snap); i++ {
		for j := i + 1; j < len(snap); j++ {
			if snap[j].HitCount > snap[i].HitCount {
				snap[i], snap[j] = snap[j], snap[i]
			}
		}
	}
	if len(snap) > limit {
		snap = snap[:limit]
	}
	out := make([]RuleView, 0, len(snap))
	for _, rule := range snap {
		if rule.HitCount == 0 {
			continue
		}
		out = append(out, toView(rule))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	rule := req.toRule()
	if rule.Status == 0 {
		rule.Status = 301
	}
	// Server-side regex compile check: refusing here gives the operator
	// a 400 instead of an opaque store error.
	if rule.IsRegex {
		if _, err := regexp.Compile(rule.SourcePath); err != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_regex", err.Error())
			return
		}
	}
	created, err := h.store.Create(r.Context(), rule)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Reload the engine so the new rule is hot immediately. A failure
	// here is non-fatal — the rule is durable and will load on the
	// next reload cycle anyway.
	if h.engine != nil {
		_ = h.engine.Reload(r.Context())
	}
	router.WriteJSON(w, http.StatusCreated, toView(created))
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "redirect id is not a valid uuid")
		return
	}
	got, err := h.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	router.WriteJSON(w, http.StatusOK, toView(got))
}

func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "redirect id is not a valid uuid")
		return
	}
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	rule := req.toRule()
	rule.ID = id
	if rule.Status == 0 {
		rule.Status = 301
	}
	if rule.IsRegex {
		if _, err := regexp.Compile(rule.SourcePath); err != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_regex", err.Error())
			return
		}
	}
	updated, err := h.store.Update(r.Context(), rule)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if h.engine != nil {
		_ = h.engine.Reload(r.Context())
	}
	router.WriteJSON(w, http.StatusOK, toView(updated))
}

func (h *handlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "redirect id is not a valid uuid")
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if h.engine != nil {
		_ = h.engine.Reload(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

// testRegex is the playground endpoint backing the admin UI's regex
// tester. The admin types a pattern + a sample path and gets back the
// match result (with capture groups) without ever persisting a rule.
type testRegexRequest struct {
	Pattern     string `json:"pattern"`
	Destination string `json:"destination"`
	SamplePath  string `json:"sample_path"`
}

type testRegexResponse struct {
	Compiles    bool     `json:"compiles"`
	Error       string   `json:"error,omitempty"`
	Matches     bool     `json:"matches"`
	Captures    []string `json:"captures,omitempty"`
	Destination string   `json:"destination,omitempty"`
}

func (h *handlers) testRegex(w http.ResponseWriter, r *http.Request) {
	var req testRegexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}
	resp := testRegexResponse{}
	pat, err := regexp.Compile(req.Pattern)
	if err != nil {
		resp.Compiles = false
		resp.Error = err.Error()
		router.WriteJSON(w, http.StatusOK, resp)
		return
	}
	resp.Compiles = true
	m := pat.FindStringSubmatchIndex(req.SamplePath)
	if m == nil {
		router.WriteJSON(w, http.StatusOK, resp)
		return
	}
	resp.Matches = true
	groups := pat.FindStringSubmatch(req.SamplePath)
	if len(groups) > 1 {
		resp.Captures = groups[1:]
	}
	if req.Destination != "" {
		resp.Destination = string(pat.ExpandString(nil, req.Destination, req.SamplePath, m))
	}
	router.WriteJSON(w, http.StatusOK, resp)
}

// =============================================================================
// Helpers
// =============================================================================

func parseID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func parseLimit(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 500 {
		return fallback
	}
	return n
}

func parseBefore(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nextCursor(rules []rdr.Rule) string {
	if len(rules) == 0 {
		return ""
	}
	return rules[len(rules)-1].CreatedAt.Format(time.RFC3339Nano)
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rdr.ErrInvalidRule):
		router.WriteError(w, http.StatusBadRequest, "invalid_rule", err.Error())
	case errors.Is(err, rdr.ErrDuplicate):
		router.WriteError(w, http.StatusConflict, "duplicate_rule", err.Error())
	case errors.Is(err, rdr.ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "redirect store error")
	}
}
