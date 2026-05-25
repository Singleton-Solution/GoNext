package render

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	blockrender "github.com/Singleton-Solution/GoNext/packages/go/blocks/render"
)

// maxBodyBytes caps the JSON payload accepted by the preview
// endpoint. Editor saves carry well under this; the cap is a defence
// against pathological clients (or a runaway integration test) DOSing
// the renderer with a huge tree.
const maxBodyBytes int64 = 4 << 20 // 4 MiB

// PreviewRequest is the request body for POST /api/v1/render/preview.
//
// Shape:
//
//	{
//	  "blocks": [ ... block tree ... ],
//	  "context": { "postId": "...", "postType": "..." }
//	}
//
// Blocks is the same shape stored in `posts.content_blocks` (a flat
// array of root nodes; each node carries `type`, `attributes`, and
// optional `innerBlocks`). Context is the optional root context map
// the walker seeds the recursion with — postId / postType / queryId
// are the canonical keys, but any string-keyed map is accepted.
type PreviewRequest struct {
	Blocks  json.RawMessage        `json:"blocks"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// PreviewError mirrors the per-block failure shape from the
// walker's WalkError, with the underlying error already stringified
// so it travels through JSON cleanly.
type PreviewError struct {
	// Path is a JSON-pointer-like location of the failing block
	// in the input tree, e.g. "/0/innerBlocks/2".
	Path string `json:"path"`
	// BlockType is the type of the failing block, when known.
	BlockType string `json:"block_type,omitempty"`
	// Message is the human-readable error string.
	Message string `json:"message"`
}

// PreviewResponse is the body returned by POST /api/v1/render/preview.
//
// HTML is the rendered tree, safe to drop into an iframe / preview
// pane. Errors is the per-block failure list collected by the
// walker; the renderer never blanks out a tree on an unknown block —
// it emits a placeholder and surfaces the error here so the editor
// can show a banner.
type PreviewResponse struct {
	HTML   string         `json:"html"`
	Errors []PreviewError `json:"errors,omitempty"`
}

// Deps is the dependency bundle for [Mount]. Registry is required
// (the renderer with no registry can't produce anything useful);
// Logger defaults to slog.Default when nil.
type Deps struct {
	Registry *blockrender.Registry
	Logger   *slog.Logger
}

// Handler is the HTTP entry point. Constructed once at boot and
// reused; safe for concurrent requests because the walker / registry
// are themselves concurrency-safe for reads.
type Handler struct {
	walker *blockrender.Walker
	logger *slog.Logger
}

// NewHandler wraps Deps into a *Handler. Returns an error rather
// than panicking so the boot path can surface a wiring mistake
// alongside the rest of the route table.
func NewHandler(deps Deps) (*Handler, error) {
	if deps.Registry == nil {
		return nil, errors.New("rest/render: Deps.Registry is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		walker: blockrender.New(deps.Registry),
		logger: logger.With(slog.String("component", "rest.render")),
	}, nil
}

// ServeHTTP implements http.Handler.
//
// POST /api/v1/render/preview accepts a PreviewRequest body and
// returns a PreviewResponse. Method other than POST returns 405 with
// Allow: POST. A non-JSON or oversized body returns 400.
//
// The handler does not require auth at this layer — the admin
// middleware mounted above gates access to the editor surface.
// Mounting the route outside the admin middleware chain would expose
// the renderer publicly, which we explicitly do not do in main.go.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		router.WriteError(w, http.StatusMethodNotAllowed,
			"method_not_allowed", "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader returns a typed error on overflow we
		// could fish out, but the failure mode is the same as
		// any other read error from the editor: surface a 400.
		router.WriteError(w, http.StatusBadRequest, "body_too_large",
			"request body too large")
		return
	}

	if len(body) == 0 {
		router.WriteError(w, http.StatusBadRequest, "empty_body",
			"request body is empty")
		return
	}

	var req PreviewRequest
	if err := json.Unmarshal(body, &req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_json",
			"request body is not valid JSON")
		return
	}

	tree, err := blockrender.DecodeTree(req.Blocks)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_blocks",
			"blocks payload is not a valid block tree")
		return
	}

	ctx := blockrender.Context(req.Context)
	res := h.walker.Walk(tree, ctx)

	resp := PreviewResponse{
		HTML: string(res.HTML),
	}
	if len(res.Errors) > 0 {
		resp.Errors = make([]PreviewError, 0, len(res.Errors))
		for _, e := range res.Errors {
			resp.Errors = append(resp.Errors, PreviewError{
				Path:      e.Path,
				BlockType: e.BlockType,
				Message:   e.Err.Error(),
			})
		}
		// Log at debug level — the response carries the same
		// information, and an unknown block in the editor is an
		// expected occurrence (the admin user dragged in a
		// plugin block whose plugin isn't active here).
		h.logger.Debug("render preview returned with errors",
			slog.Int("errors", len(res.Errors)))
	}

	router.WriteJSON(w, http.StatusOK, resp)
}

// Mount installs the preview handler at base+"/render/preview".
// base is the public REST root, typically "/api/v1".
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	h, err := NewHandler(deps)
	if err != nil {
		return err
	}
	mux.Handle("POST "+base+"/render/preview", h)
	return nil
}
