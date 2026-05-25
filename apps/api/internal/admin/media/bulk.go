package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// MaxBulkSize caps a single bulk request. 500 covers the common
// "select all on the grid page" case (page size is 30, an operator
// would have to deliberately set limit=500 to fill this) while
// keeping the worst-case response shape bounded.
const MaxBulkSize = 500

// BulkDeps is the dependency bag the bulk sub-mount needs. Store is
// required; Policy is required by the gate. AltGenerator is the
// (stubbed) async pipeline that produces alt-text — nil means
// "ai-alt is disabled at this tier" and the bulk handler 503s for
// that op.
type BulkDeps struct {
	Store        Store
	Policy       policy.Policy
	Logger       *slog.Logger
	AltGenerator AltGenerator
}

// AltGenerator is the wire surface the bulk handler uses to fire
// the AI-alt-text pipeline. Production wires this to an Asynq
// adapter that enqueues a media.ai-alt task per id; tests use an
// in-process closure. Defined as an interface so the bulk handler
// has no transitive Asynq dependency.
//
// Enqueue is fire-and-forget: a non-nil error from one id should not
// abort the rest of the request. The handler records per-id failure
// in BulkResult.Failed.
type AltGenerator interface {
	Enqueue(ctx context.Context, assetID string) error
}

// AltGeneratorFunc lets a closure satisfy AltGenerator. Useful for
// tests and for the production wiring where the adapter is a thin
// wrapper around taskspec.Enqueue.
type AltGeneratorFunc func(ctx context.Context, assetID string) error

// Enqueue implements AltGenerator.
func (f AltGeneratorFunc) Enqueue(ctx context.Context, assetID string) error {
	return f(ctx, assetID)
}

// MountBulk wires the bulk endpoint onto mux at base/bulk.
// "base" is typically "/api/v1/admin/media".
//
//	POST {base}/bulk — body { op, ids, params }
//
// Op routes to the capability that op requires (delete -> delete,
// the rest -> upload). The op-level capability check happens AFTER
// the gate's media.read so a caller missing media.read can't probe
// for the existence of an id.
func MountBulk(mux *http.ServeMux, base string, deps BulkDeps) error {
	if deps.Store == nil {
		return errors.New("admin/media: bulk Store is required")
	}
	if deps.Policy == nil {
		return errors.New("admin/media: Policy is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &bulkHandlers{
		store:        deps.Store,
		policy:       deps.Policy,
		logger:       deps.Logger,
		altGenerator: deps.AltGenerator,
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("POST "+base+"/bulk", h.gate(policy.CapMediaRead, h.bulk))
	return nil
}

type bulkHandlers struct {
	store        Store
	policy       policy.Policy
	logger       *slog.Logger
	altGenerator AltGenerator
}

func (h *bulkHandlers) gate(cap policy.Capability, next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, cap, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// BulkOp names the supported bulk verbs. Defined as a typed string
// so the switch in dispatch is exhaustive at the language level (a
// new op forces a new case to land in dispatchOp).
type BulkOp string

const (
	BulkDelete BulkOp = "delete"
	BulkMove   BulkOp = "move"
	BulkTag    BulkOp = "tag"
	BulkAIAlt  BulkOp = "ai-alt"
)

// BulkRequest is the on-wire request shape. Params is a deferred
// decode (json.RawMessage) so the op-specific param parser sees the
// raw bytes; this keeps each branch's parser self-contained.
type BulkRequest struct {
	Op     BulkOp          `json:"op"`
	IDs    []string        `json:"ids"`
	Params json.RawMessage `json:"params,omitempty"`
}

// BulkResult is the on-wire response shape. Succeeded is a count
// because the caller usually only needs "how many landed". Failed
// is per-id so the operator can see exactly which row tripped what
// error. Empty Failed renders as omitted JSON so the success path
// stays terse.
type BulkResult struct {
	Op        BulkOp            `json:"op"`
	Succeeded int               `json:"succeeded"`
	Failed    map[string]string `json:"failed,omitempty"`
}

// moveParams is the params block for op=move. CollectionID nil
// moves the assets to the implicit root.
type moveParams struct {
	CollectionID *string `json:"collection_id,omitempty"`
}

// tagParams is the params block for op=tag. Add tags are merged
// into the row's existing tag list; Remove tags are filtered out.
// Set, when non-nil, replaces the list entirely (overriding the
// merge). The handler normalises every incoming tag (lowercase,
// trim, dedupe).
type tagParams struct {
	Add    []string  `json:"add,omitempty"`
	Remove []string  `json:"remove,omitempty"`
	Set    *[]string `json:"set,omitempty"`
}

// bulk dispatches the request to the per-op handler after the
// op-level capability check.
func (h *bulkHandlers) bulk(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	var req BulkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "could not parse json: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_ids", "ids must be non-empty")
		return
	}
	if len(req.IDs) > MaxBulkSize {
		router.WriteError(w, http.StatusBadRequest, "too_many_ids", "ids exceeds bulk size limit")
		return
	}

	// Op-level capability check. Delete needs media.delete; the
	// rest mutate metadata and use media.upload. ai-alt enqueues a
	// worker job but the action surfaces as a metadata write, so
	// it sits with the upload capability.
	switch req.Op {
	case BulkDelete:
		if d := h.policy.Can(pr, policy.CapMediaDelete, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	case BulkMove, BulkTag, BulkAIAlt:
		if d := h.policy.Can(pr, policy.CapMediaUpload, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	default:
		router.WriteError(w, http.StatusBadRequest, "invalid_op", "op must be one of delete|move|tag|ai-alt")
		return
	}

	result := h.dispatchOp(r.Context(), req)
	router.WriteJSON(w, http.StatusOK, result)
}

// dispatchOp is the per-op fan-out. Centralised so the test surface
// can hammer one entry point rather than five.
func (h *bulkHandlers) dispatchOp(ctx context.Context, req BulkRequest) BulkResult {
	result := BulkResult{Op: req.Op, Failed: map[string]string{}}
	switch req.Op {
	case BulkDelete:
		for _, id := range req.IDs {
			if err := h.store.SoftDelete(ctx, id); err != nil {
				result.Failed[id] = classifyErr(err)
				continue
			}
			result.Succeeded++
		}
	case BulkMove:
		var p moveParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				// The handler returns an error result rather than a
				// 400 because a partial-params failure should still
				// preserve the op name in the result for the UI to
				// surface; the caller can read result.Failed["_params"]
				// to see what went wrong.
				result.Failed["_params"] = "invalid_params"
				return result
			}
		}
		for _, id := range req.IDs {
			if err := h.store.SetCollection(ctx, id, p.CollectionID); err != nil {
				result.Failed[id] = classifyErr(err)
				continue
			}
			result.Succeeded++
		}
	case BulkTag:
		var p tagParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				result.Failed["_params"] = "invalid_params"
				return result
			}
		}
		for _, id := range req.IDs {
			asset, err := h.store.GetByID(ctx, id)
			if err != nil {
				result.Failed[id] = classifyErr(err)
				continue
			}
			next := computeTags(asset.Tags, p)
			if err := h.store.SetTags(ctx, id, next); err != nil {
				result.Failed[id] = classifyErr(err)
				continue
			}
			result.Succeeded++
		}
	case BulkAIAlt:
		if h.altGenerator == nil {
			// 503-equivalent in the per-id failure map: every id
			// fails with the same reason. We deliberately don't
			// short-circuit because the response shape needs to
			// echo the op name so the UI can render a useful
			// message.
			for _, id := range req.IDs {
				result.Failed[id] = "ai_alt_unavailable"
			}
			return result
		}
		for _, id := range req.IDs {
			if err := h.altGenerator.Enqueue(ctx, id); err != nil {
				result.Failed[id] = "enqueue_failed"
				continue
			}
			result.Succeeded++
		}
	}
	if len(result.Failed) == 0 {
		result.Failed = nil
	}
	return result
}

// classifyErr maps a store error into a short, stable error code
// for the per-id failure map. The codes are documented contracts
// the admin UI keys off of for translated error messages.
func classifyErr(err error) string {
	if errors.Is(err, ErrNotFound) {
		return "not_found"
	}
	return "internal_error"
}

// computeTags applies the tagParams to an existing tag list and
// returns the new list. Set replaces; Add merges; Remove filters.
// The order of operations is Set -> Add -> Remove so the caller can
// chain "replace and then add" if they want to.
func computeTags(existing []string, p tagParams) []string {
	// Set wins outright: when supplied, ignore existing and Add
	// and Remove apply to the new list.
	var base []string
	if p.Set != nil {
		base = normalizeTags(*p.Set)
	} else {
		base = normalizeTags(existing)
	}
	if len(p.Add) > 0 {
		merged := append(make([]string, 0, len(base)+len(p.Add)), base...)
		merged = append(merged, p.Add...)
		base = normalizeTags(merged)
	}
	if len(p.Remove) > 0 {
		remove := make(map[string]struct{}, len(p.Remove))
		for _, t := range p.Remove {
			t = strings.ToLower(strings.TrimSpace(t))
			if t != "" {
				remove[t] = struct{}{}
			}
		}
		filtered := base[:0]
		for _, t := range base {
			if _, drop := remove[t]; drop {
				continue
			}
			filtered = append(filtered, t)
		}
		base = filtered
	}
	return base
}

// normalizeTags lowercases, trims, and dedupes a tag slice. The
// sort makes the output deterministic; deduplication uses a map
// because the volume is small (operator tag lists are tens of
// entries, not thousands).
func normalizeTags(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// StubAltGenerator is the no-real-LLM placeholder used by the dev
// stack and by tests. It satisfies AltGenerator by writing a
// deterministic "auto-generated alt for image $id" string into the
// asset's alt_text via the store. The production wiring will
// replace this with the Asynq adapter that enqueues a real LLM
// task; the wire surface stays identical.
type StubAltGenerator struct {
	Store Store
}

// Enqueue applies the stub alt text immediately. We avoid the
// background queue here because the dev story is "see the change
// in the admin UI without spinning up the worker"; production
// wiring substitutes a real Enqueuer that posts to Asynq.
func (s *StubAltGenerator) Enqueue(ctx context.Context, assetID string) error {
	alt := "auto-generated alt for image " + assetID
	_, err := s.Store.UpdateMetadata(ctx, assetID, AssetUpdate{AltText: &alt})
	return err
}
