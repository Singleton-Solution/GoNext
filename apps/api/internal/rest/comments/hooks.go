// hooks.go wires the comment-submission moderation pipeline:
//
//   pre_comment filter — plugins may inspect the candidate submission
//                        and (a) mutate it, (b) reject it, or (c) stamp
//                        a verdict that overrides the default classifier.
//   duplicate-content  — same content from the same IP within a short
//                        window is dropped at the store boundary.
//   IP redaction cron  — last octet of every comment's IP is zeroed
//                        once the row is older than 30 days, so the
//                        moderation queue isn't a perpetual PII store.
//
// Each piece is independently engageable: the hook bus is optional
// (a deps.Hooks of nil disables the filter chain), duplicate detection
// is a store-level method, and the redaction cron is a separate
// scheduler entry the operator wires through packages/go/jobs/cron.
//
// The interaction with the existing classify() function is:
//
//   1. The handler still runs sanitiseContent.
//   2. The handler still runs hard-rate-limit.
//   3. NEW: the handler fires pre_comment via ApplyFilters with the
//      decoded body. A plugin returns either:
//        - the value unchanged + nil  → continue.
//        - hooks.ErrShortCircuit + a CommentVerdict → use the verdict
//          as the initial Status (bypassing classify()).
//        - any other error            → 400, with the error message
//          surfaced as the "detail" of the response problem.
//   4. Default behaviour unchanged when no plugin handles the hook.
package comments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HookName is the pre-comment filter hook name. Plugins register on
// this name; the bus invokes the chain before the comment row hits
// the store.
const HookName = "rest.comments.pre_submit"

// CommentVerdict is what a plugin filter returns to override the
// default classifier. The zero value means "no override" (let the
// classifier decide); a non-zero Status means "use this exact
// status".
type CommentVerdict struct {
	// Status, when non-empty, becomes the initial Status of the
	// persisted row. "approved" → row lands live; "spam" → row is
	// invisible to the public list; "pending" → moderation queue.
	Status Status

	// Reason is a human-readable note recorded alongside the row for
	// moderator triage. Optional; the bus does not validate.
	Reason string
}

// PreSubmitPayload is the value passed through the filter chain. The
// fields mirror the decoded SubmitInput plus the verdict slot a
// plugin may stamp.
//
// The struct is exported so plugin authors can type-assert the
// any-typed value the bus hands them. Mutating any of the input
// fields modifies the eventual persisted row.
type PreSubmitPayload struct {
	// Input is the validated submit payload. Filter handlers MAY
	// mutate Content / AuthorName / AuthorEmail (the canonical use
	// case for "rewrite this URL into a tracking link" or "strip
	// markdown the operator banned"). PostID/ParentID are
	// load-bearing for routing; mutating them is a programmer error.
	Input *SubmitInput

	// Verdict is the slot a moderation plugin fills to override the
	// default classifier. Leave zero to let classify() run.
	Verdict CommentVerdict
}

// HookBus is the minimal hook-dispatching surface this package
// requires. Defined as an interface so the comments package does
// not import packages/go/hooks directly — the API server wires the
// real bus through Deps.Hooks at boot time.
//
// The shape mirrors hooks.Bus.ApplyFilters; tests pass a stub.
type HookBus interface {
	ApplyFilters(ctx context.Context, name string, value any, args ...any) (any, error)
}

// ErrCommentRejected is returned by the filter chain when a plugin
// wants to drop the submission entirely. The handler maps this to a
// 422 Unprocessable Entity rather than a 400, because the request
// was syntactically valid — semantically a policy says no.
var ErrCommentRejected = errors.New("rest/comments: rejected by pre_submit hook")

// runPreSubmit applies the pre_submit filter chain. Returns the
// resolved verdict (possibly zero) and a non-nil error if the chain
// reported a hard reject. The error path bubbles up to the handler;
// the verdict path is used by submit() to override the classifier.
//
// When deps.Hooks is nil the function is a no-op — returns the zero
// verdict and a nil error, leaving the handler in its default code
// path.
func (h *handlers) runPreSubmit(ctx context.Context, in *SubmitInput) (CommentVerdict, error) {
	if h.hooks == nil {
		return CommentVerdict{}, nil
	}
	payload := &PreSubmitPayload{Input: in}
	out, err := h.hooks.ApplyFilters(ctx, HookName, payload)
	if err != nil {
		// hooks.ErrShortCircuit is not an exported sentinel from this
		// package (we can't import hooks.* without a cycle); test
		// instead for the well-known error message. The handler's
		// fallback is to treat any non-ErrCommentRejected error as
		// the reject signal — plugins that want to surface "I'm not
		// sure" should return a verdict with Status = "pending"
		// instead of an error.
		if errors.Is(err, ErrCommentRejected) {
			return CommentVerdict{}, ErrCommentRejected
		}
		// Short-circuit (hooks.ErrShortCircuit) with a verdict
		// attached: the bus returns (value, ErrShortCircuit) and we
		// pluck the verdict from the value.
		if isShortCircuit(err) {
			if p, ok := out.(*PreSubmitPayload); ok {
				return p.Verdict, nil
			}
			return CommentVerdict{}, nil
		}
		return CommentVerdict{}, fmt.Errorf("pre_submit hook: %w", err)
	}
	if p, ok := out.(*PreSubmitPayload); ok {
		return p.Verdict, nil
	}
	return CommentVerdict{}, nil
}

// isShortCircuit checks whether err is the bus's short-circuit
// sentinel. We can't import packages/go/hooks here (the comments
// package would gain a dep on the whole bus package); we recognise
// the sentinel by its Error() string, which the hooks package
// documents as stable.
func isShortCircuit(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "short-circuit filter chain")
}

// contentFingerprint is the duplicate-content key. SHA-256 of the
// normalised content, lowercased + whitespace-collapsed. Stable
// enough that "Hello World!" and "  hello   world!  " collide.
//
// Plain hash, no salt — the operator already trusts the store and
// the hash is only used as a dedupe key inside the comments table.
func contentFingerprint(content string) string {
	normalised := strings.ToLower(strings.Join(strings.Fields(content), " "))
	sum := sha256.Sum256([]byte(normalised))
	return hex.EncodeToString(sum[:])
}

// DuplicateContent reports whether the given fingerprint has already
// been submitted from authorIP within window. Implementations live
// alongside the Store; the in-memory store provides a default.
//
// Note this is NOT a Store method — it's a separate interface so
// stores that don't want to provide it (Postgres tests that don't
// seed the fingerprint column) can opt out without breaking the
// public surface. The handler treats a nil DupChecker as "no
// duplicate detection" and falls through.
type DupChecker interface {
	DuplicateContent(ctx context.Context, ip, fingerprint string, window time.Duration) (bool, error)
}

// dupWindow is the lookback for the duplicate-content gate. Five
// minutes catches the common "double-tapped the submit button"
// case + the trivial "I'll just keep posting the same affiliate
// link" case, without ballooning the index.
const dupWindow = 5 * time.Minute
