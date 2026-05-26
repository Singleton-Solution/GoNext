package comments

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Tunables for the public surface. The numbers below match the spec
// in this issue's design note; they're not yet promoted to config
// because changing them is a code review (not an ops review) decision
// for now.
const (
	defaultListLimit = 50
	maxListLimit     = 100

	// Content length bounds. The lower bound (1) prevents an empty
	// submission; the upper bound (5000) is generous enough for a
	// long-form reply without enabling spam-scale dumps.
	minContentLength = 1
	maxContentLength = 5000

	// Spam thresholds — see doc.go.
	maxURLsBeforeSpam = 5

	// Rate limit: an IP that submits more than this in
	// rateLimitWindow is treated as spammy, not 429-throttled. The
	// difference matters: 429 tells a legitimate-but-chatty visitor
	// "slow down"; "spam" hides the row until a moderator approves
	// it. We pick the latter because the bar to forge an IP is low
	// enough that a 429 would just push attackers onto rotating
	// proxies, while the moderation queue is the right place to
	// catch flooders.
	maxCommentsPerIPInWindow = 10
	rateLimitWindow          = 5 * time.Minute

	// Hard rate limit: even legitimate visitors can't submit more
	// than this within hardRateLimitWindow. Past this we 429 — at
	// some point even a friendly chatty visitor needs to slow down
	// so the comments table doesn't grow unboundedly.
	maxCommentsPerIPHard      = 30
	hardRateLimitWindow       = 5 * time.Minute
	maxBodyBytes              = 32 * 1024
)

// Deps is the dependency bag for Mount.
type Deps struct {
	// Store persists comments. Required.
	Store Store

	// Logger receives structured log lines. nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// AllowOrigin, when non-empty, is echoed verbatim in the
	// Access-Control-Allow-Origin header on the response. Typically
	// the public-site BaseURL. Empty disables CORS — useful in tests
	// and in deployments where the API is co-hosted with the site.
	AllowOrigin string

	// Now is an optional clock injection point for tests. nil falls
	// back to time.Now.
	Now func() time.Time

	// CurrentUserID, when non-nil, is called per-request to resolve
	// the commenter's user ID for logged-in submissions. nil falls
	// back to the principal's UserID (or empty when no principal is
	// on the context — the public surface is anonymous-first, the
	// auth middleware decorates the request opportunistically).
	CurrentUserID func(*http.Request) string

	// CurrentDisplayName, when non-nil, returns the logged-in user's
	// display name. Mirrors the admin package's wiring point.
	CurrentDisplayName func(*http.Request) string

	// Hooks, when non-nil, is the filter-bus the submit handler
	// fires the pre_submit hook through. Plugins register on
	// rest.comments.pre_submit to mutate, reject, or stamp a
	// moderation verdict on a candidate row before it lands.
	// nil disables the hook (the default code path runs).
	Hooks HookBus

	// DupChecker, when non-nil, is consulted to drop duplicate
	// content from the same IP inside a short window. Typically
	// the same object as Store (the MemoryStore implements both).
	DupChecker DupChecker
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("rest/comments: Store is required")
	}
	return nil
}

// handlers is the resolved-Deps form.
type handlers struct {
	store          Store
	logger         *slog.Logger
	allowOrigin    string
	now            func() time.Time
	currentUID     func(*http.Request) string
	currentDisplay func(*http.Request) string

	// hooks is the optional filter bus invoked from submit() before
	// the row hits the store. nil disables the chain.
	hooks HookBus

	// dup is the optional duplicate-content gate. nil falls through
	// to the legacy code path (rate-limit + classify only).
	dup DupChecker

	// ipMu guards the in-process IP rate-limit table. The table is
	// non-authoritative — it's a best-effort throttle for the case
	// where the Postgres backend hasn't seen the most recent burst
	// yet (replica lag, in-process tests). The Postgres
	// CommentsByIP query remains the source of truth.
	ipMu sync.Mutex
	ips  map[string][]time.Time
}

// Mount wires the public comments routes onto mux. The base is
// typically "/api/v1/posts" so the resulting routes are
// "/api/v1/posts/{id}/comments".
//
// Route tree:
//
//	GET    {base}/{id}/comments — list approved comments
//	POST   {base}/{id}/comments — submit a new comment
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	_, err := mountForTest(mux, base, deps)
	return err
}

// mountForTest is the implementation of Mount that also returns the
// constructed handlers. Used by the package's tests to exercise the
// rate-limit table directly; the public surface is Mount.
func mountForTest(mux *http.ServeMux, base string, deps Deps) (*handlers, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.CurrentUserID == nil {
		deps.CurrentUserID = func(r *http.Request) string {
			if pr, ok := policy.FromContext(r.Context()); ok {
				return pr.UserID
			}
			return ""
		}
	}
	if deps.CurrentDisplayName == nil {
		deps.CurrentDisplayName = func(*http.Request) string { return "" }
	}

	h := &handlers{
		store:          deps.Store,
		logger:         deps.Logger,
		allowOrigin:    strings.TrimRight(deps.AllowOrigin, "/"),
		now:            deps.Now,
		currentUID:     deps.CurrentUserID,
		currentDisplay: deps.CurrentDisplayName,
		hooks:          deps.Hooks,
		dup:            deps.DupChecker,
		ips:            make(map[string][]time.Time),
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/{id}/comments", h.cors(http.HandlerFunc(h.list)))
	mux.Handle("POST "+base+"/{id}/comments", h.cors(http.HandlerFunc(h.submit)))
	mux.Handle("OPTIONS "+base+"/{id}/comments", h.cors(http.HandlerFunc(h.preflight)))
	return h, nil
}

// cors wraps a handler with the Access-Control-Allow-Origin /
// Access-Control-Allow-Credentials headers. We only echo the
// configured origin — wildcard '*' is incompatible with credentials
// and we want the comment form's CSRF cookie to ride along.
func (h *handlers) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.allowOrigin != "" {
			origin := r.Header.Get("Origin")
			// Echo back only an exact match. Browsers reject mismatches
			// at the SOP layer, but explicit equality is the safest
			// posture: an attacker who can set Origin can't widen the
			// allowlist past what the operator configured.
			if origin == h.allowOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
			}
		}
		next.ServeHTTP(w, r)
	})
}

// preflight is the OPTIONS handler. The CORS middleware above has
// already set the headers; we just emit 204 No Content.
func (h *handlers) preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// clientIP returns the source IP of the request, preferring the
// leftmost X-Forwarded-For entry when behind a trusted proxy.
// The "trusted" part is delegated to the upstream proxy
// middleware; here we just read the header.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// leftmost = original client (per RFC 7239's chain semantics).
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		ip := strings.TrimSpace(xff)
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recordIPSubmission appends now to the in-process IP table and
// drops timestamps older than rateLimitWindow. Returns the count of
// submissions in the active window AFTER recording the new one.
func (h *handlers) recordIPSubmission(ip string) int {
	if ip == "" {
		return 0
	}
	cutoff := h.now().Add(-rateLimitWindow)
	h.ipMu.Lock()
	defer h.ipMu.Unlock()
	stamps := h.ips[ip]
	pruned := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, h.now())
	h.ips[ip] = pruned
	return len(pruned)
}

// countIPSubmissions returns the in-process count for ip without
// mutating the table. Used by the hard rate-limit gate before we
// touch the store (which is the more expensive lookup).
func (h *handlers) countIPSubmissions(ip string) int {
	if ip == "" {
		return 0
	}
	cutoff := h.now().Add(-hardRateLimitWindow)
	h.ipMu.Lock()
	defer h.ipMu.Unlock()
	stamps := h.ips[ip]
	n := 0
	for _, t := range stamps {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}

// decodeSubmitBody reads the JSON body into a submit payload.
// Enforces maxBodyBytes and rejects unknown fields.
type submitBody struct {
	ParentID     string `json:"parent_id"`
	AuthorName   string `json:"author_name"`
	AuthorEmail  string `json:"author_email"`
	Content      string `json:"content"`
}

func decodeSubmitBody(r *http.Request) (submitBody, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	var body submitBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return body, err
	}
	if dec.More() {
		return body, errors.New("trailing data")
	}
	return body, nil
}
