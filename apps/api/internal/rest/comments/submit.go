package comments

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// submit handles POST /api/v1/posts/{id}/comments.
//
// Flow:
//
//  1. Resolve post id (path param).
//  2. Decode + validate body (parent_id, author_name, author_email,
//     content). Anonymous commenters MUST supply author_name;
//     logged-in commenters fall back to their profile.
//  3. Sanitise content (strip HTML tags + escape).
//  4. Hard-rate-limit by IP. Past the hard cap, 429.
//  5. Spam-classify. The result is the initial status: 'pending'
//     for benign rows, 'spam' for caught rows. Logged-in users
//     bypass spam-detect (the rate limit still applies).
//  6. Persist via the store.
//  7. Return 201 with {comment, pending}.
//
// Status codes:
//
//	201 Created           — happy path.
//	400 Bad Request       — body validation failed.
//	404 Not Found         — post (or parent comment) missing.
//	413 Payload Too Large — body exceeds maxBodyBytes.
//	429 Too Many Requests — hard rate limit hit.
func (h *handlers) submit(w http.ResponseWriter, r *http.Request) {
	postID := r.PathValue("id")
	if postID == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "post id is required")
		return
	}

	body, err := decodeSubmitBody(r)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			router.WriteError(w, http.StatusRequestEntityTooLarge,
				"content_too_large", "request body exceeds the size limit")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "invalid_body",
			"request body must be a JSON object with content")
		return
	}

	// Resolve the logged-in commenter (if any). The public surface is
	// anonymous-friendly; the auth middleware decorates the request
	// opportunistically when a session cookie is present.
	uid := h.currentUID(r)
	loggedIn := uid != ""

	// Anonymous → author_name required, no email required (we do let
	// folks supply one for gravatar but it's optional).
	displayName := strings.TrimSpace(body.AuthorName)
	if !loggedIn {
		if displayName == "" {
			router.WriteError(w, http.StatusBadRequest, "missing_name",
				"author_name is required for anonymous comments")
			return
		}
		if len(displayName) > 100 {
			router.WriteError(w, http.StatusBadRequest, "name_too_long",
				"author_name exceeds the maximum length")
			return
		}
	} else {
		// Logged-in commenters: take the display name from the auth
		// hook, falling back to the body value if the hook is
		// unwired (tests).
		if d := strings.TrimSpace(h.currentDisplay(r)); d != "" {
			displayName = d
		}
	}

	email := strings.TrimSpace(body.AuthorEmail)
	if email != "" && (!strings.Contains(email, "@") || len(email) > 254) {
		router.WriteError(w, http.StatusBadRequest, "invalid_email",
			"author_email is not a valid address")
		return
	}

	// Content validation. We sanitise FIRST so the length check sees
	// what the persisted body looks like — otherwise a 5000-char
	// "<a><a><a>..." payload would slip past as 5000 chars and become
	// a much shorter sanitised body.
	content := sanitizeContent(body.Content)
	if len(content) < minContentLength {
		router.WriteError(w, http.StatusBadRequest, "missing_content",
			"content is required")
		return
	}
	if len(content) > maxContentLength {
		router.WriteError(w, http.StatusBadRequest, "content_too_long",
			"content exceeds the maximum length")
		return
	}

	ip := clientIP(r)

	// Hard rate limit (429). Uses the in-process table for the fast
	// path; the store-backed count is a defence-in-depth check for
	// the case where multiple API instances are running behind a
	// load balancer.
	if n := h.countIPSubmissions(ip); n >= maxCommentsPerIPHard {
		router.WriteError(w, http.StatusTooManyRequests, "rate_limited",
			"too many comments from this IP; try again later")
		return
	}
	if !loggedIn && ip != "" {
		since := h.now().Add(-hardRateLimitWindow)
		if stored, err := h.store.CommentsByIP(r.Context(), ip, since); err == nil && stored >= maxCommentsPerIPHard {
			router.WriteError(w, http.StatusTooManyRequests, "rate_limited",
				"too many comments from this IP; try again later")
			return
		}
	}

	// Spam classification. Logged-in commenters skip the classifier
	// (the rate limit catches abuse here too). The decision is
	// captured before we touch the store so the audit trail of "what
	// did the classifier decide?" stays in one place.
	initialStatus := h.classify(r.Context(), body.Content, content, ip, loggedIn)

	// Validate parent (if any). We rely on the store to fail-fast
	// with ErrNotFound / ErrParentMismatch — both surface as 404 /
	// 400 in the response.
	in := SubmitInput{
		PostID:          postID,
		ParentID:        strings.TrimSpace(body.ParentID),
		AuthorUserID:    uid,
		AuthorName:      displayName,
		AuthorEmail:     email,
		Content:         content,
		AuthorIP:        ip,
		AuthorUserAgent: r.Header.Get("User-Agent"),
	}

	// Duplicate-content gate. The fingerprint is normalised content
	// SHA-256; same IP + same fingerprint within dupWindow drops the
	// row at 422. Anonymous submissions only — logged-in users are
	// trusted to know whether they meant to repost.
	if h.dup != nil && !loggedIn && ip != "" {
		fp := contentFingerprint(content)
		if dup, err := h.dup.DuplicateContent(r.Context(), ip, fp, dupWindow); err == nil && dup {
			router.WriteError(w, http.StatusUnprocessableEntity, "duplicate_content",
				"identical content was submitted from this IP recently")
			return
		}
	}

	// pre_submit hook chain. Plugins may mutate the input, reject
	// the row, or stamp a moderation verdict that overrides the
	// classifier. The handler treats:
	//   verdict.Status non-empty → use it as initialStatus.
	//   ErrCommentRejected      → 422.
	//   any other error         → 400 with the error message.
	verdict, hookErr := h.runPreSubmit(r.Context(), &in)
	if hookErr != nil {
		if errors.Is(hookErr, ErrCommentRejected) {
			router.WriteError(w, http.StatusUnprocessableEntity, "rejected",
				"comment was rejected by a moderation plugin")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "pre_submit_error", hookErr.Error())
		return
	}
	if verdict.Status != "" {
		initialStatus = verdict.Status
	}

	// Best-effort post existence check before we record an IP
	// submission. We want the IP rate limiter to count
	// successful-ish submissions, not 404s.
	if exists, err := h.store.PostExists(r.Context(), postID); err == nil && !exists {
		router.WriteError(w, http.StatusNotFound, "not_found", "post not found")
		return
	}

	created, err := h.store.Submit(r.Context(), in, initialStatus)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			router.WriteError(w, http.StatusNotFound, "not_found",
				"post or parent comment not found")
		case errors.Is(err, ErrParentMismatch):
			router.WriteError(w, http.StatusBadRequest, "parent_mismatch",
				"parent comment belongs to a different post")
		case errors.Is(err, ErrEmptyContent):
			router.WriteError(w, http.StatusBadRequest, "missing_content",
				"content is required")
		default:
			h.logger.ErrorContext(r.Context(), "rest/comments: submit failed",
				slog.String("post_id", postID),
				slog.Any("err", err),
			)
			router.WriteError(w, http.StatusInternalServerError, "internal_error",
				"failed to create comment")
		}
		return
	}

	// Record the IP submission for the in-process rate limiter. We
	// do this AFTER the store accepted the row so a 404 / 400 on a
	// malformed parent doesn't count toward the visitor's budget.
	_ = h.recordIPSubmission(ip)

	router.WriteJSON(w, http.StatusCreated, Created{
		Comment: created,
		Pending: initialStatus == StatusPending,
	})
}

// classify is the spam-check stub. It returns the moderation state
// the comment should land in: 'approved' for trusted authors,
// 'pending' for everyone else's first attempt, 'spam' when one of
// the trivial rules trips. The real scorer (issue #190) replaces
// this function.
func (h *handlers) classify(ctx interface {
	Done() <-chan struct{}
	Err() error
}, raw, sanitized, ip string, loggedIn bool) Status {
	_ = ctx // reserved for the real scorer's request context
	if countURLs(raw) > maxURLsBeforeSpam {
		return StatusSpam
	}
	// Long content after sanitisation suggests a copy-paste dump.
	// Still 'pending' (not 'spam') for borderline cases — a real
	// thoughtful long-form reply shouldn't be auto-classified as
	// spam by length alone.
	_ = sanitized
	// Soft IP rate limit: above maxCommentsPerIPInWindow within
	// rateLimitWindow → spam classification (in-process count is
	// the proxy; the real check would be the store-backed one).
	if !loggedIn && h.countIPSubmissions(ip) >= maxCommentsPerIPInWindow {
		return StatusSpam
	}
	if loggedIn {
		// Logged-in commenters bypass moderation for now. Once
		// per-user trust grows (auto-approve for established users)
		// this gains a real rule.
		return StatusApproved
	}
	return StatusPending
}
