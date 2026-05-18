package wprest

import (
	"context"
	"log/slog"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Audit event types emitted by the write shim. Stringified here so a
// single grep over the codebase enumerates the full set the WP shim
// produces — important for SIEM dashboards that pin alerts to a closed
// list of event names.
//
// The naming convention is `wprest.<resource>.<verb>` so the WP shim
// events are distinguishable from native `post.<verb>` events in the
// same store; a caller filtering by event type prefix can route them
// to a separate sink without parsing the whole event.
const (
	EventPostCreated    = "wprest.post.created"
	EventPostUpdated    = "wprest.post.updated"
	EventPostDeleted    = "wprest.post.deleted"
	EventPageCreated    = "wprest.page.created"
	EventPageUpdated    = "wprest.page.updated"
	EventPageDeleted    = "wprest.page.deleted"
	EventUserCreated    = "wprest.user.created"
	EventUserUpdated    = "wprest.user.updated"
	EventUserDeleted    = "wprest.user.deleted"
	EventTermCreated    = "wprest.term.created"
	EventTermUpdated    = "wprest.term.updated"
	EventTermDeleted    = "wprest.term.deleted"
)

// emitAudit is the success-path audit emitter. A nil deps.Audit is
// tolerated (test mode); an emit error is logged but never surfaced to
// the caller — audit failure must not break a user-facing write.
//
// resourceType is the singular kind ("post", "page", "user", "category",
// "tag"). resourceID is the string form of the legacy_int_id (or the
// native id for users/terms when no legacy id exists). metadata carries
// any extra context the event should retain (status transitions, slug
// changes); a nil metadata is fine.
func (h *handlers) emitAudit(ctx context.Context, pr policy.Principal, eventType, resourceType, resourceID string, metadata map[string]any) {
	if h.deps.Audit == nil {
		return
	}
	em := h.deps.Audit.WithActor(pr.UserID)
	opts := []audit.EmitOption{
		audit.WithTarget(resourceType, resourceID),
	}
	if len(metadata) > 0 {
		opts = append(opts, audit.WithMetadata(metadata))
	}
	if err := em.Emit(ctx, eventType, opts...); err != nil {
		h.deps.Logger.WarnContext(ctx, "wprest: audit emit failed",
			slog.String("event", eventType),
			slog.String("resource_type", resourceType),
			slog.String("resource_id", resourceID),
			slog.Any("err", err),
		)
	}
}
