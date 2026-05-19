package webhooks

// EventDescriptor is one entry in the event catalog the admin UI
// renders in the create form's multi-select. Name is the dotted
// identifier the producer emits (matches the events[] filter on the
// subscription); Description is the human-readable label the operator
// reads when choosing.
type EventDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Catalog is the in-package registry of webhook events available for
// subscription. The list is intentionally a Go constant for now —
// having one source of truth here means a plugin can't subscribe to
// events that aren't part of the core surface without first being
// registered explicitly. A plugin-extensibility seam (mirroring
// policy.RegisterCapability) can land later if demand materializes;
// the HTTP contract stays the same.
//
// The reserved "webhook.test" entry exists so the test endpoint has
// a stable type to emit — operators don't subscribe to it directly
// (a Test send hits whatever events[] the subscription configures),
// but the synthetic delivery is tagged with this name so subscribers
// can recognize and ack it without further routing.
func Catalog() []EventDescriptor {
	return []EventDescriptor{
		// Reserved — used by the operator-triggered Test endpoint.
		{Name: EventTypeTest, Description: "Synthetic event emitted by the admin Test action. Subscribers should treat as a ping."},

		// Content lifecycle.
		{Name: "post.published", Description: "A post transitioned to published status."},
		{Name: "post.updated", Description: "A published post had its content or metadata updated."},
		{Name: "post.deleted", Description: "A post was moved to trash or permanently deleted."},
		{Name: "page.published", Description: "A page transitioned to published status."},
		{Name: "page.updated", Description: "A published page was edited."},

		// Comments.
		{Name: "comment.created", Description: "A new comment was submitted (any moderation status)."},
		{Name: "comment.approved", Description: "A comment was approved and is now public."},

		// Users.
		{Name: "user.created", Description: "A new user account was created."},
		{Name: "user.role_changed", Description: "An operator changed a user's role."},

		// Media.
		{Name: "media.uploaded", Description: "A new media item was uploaded to the library."},

		// Plugins (operator interest is high — marketplace use cases).
		{Name: "plugin.installed", Description: "A plugin was installed onto the host."},
		{Name: "plugin.activated", Description: "An installed plugin was activated."},
	}
}

// EventTypeTest is the reserved event type used by the operator-
// triggered Test endpoint. Distinct from real events so subscribers
// can choose to ack and discard without further processing.
const EventTypeTest = "webhook.test"

// validEvents builds a lookup set from the catalog. Returns true for
// any name present in the catalog, including the reserved test event.
// Cheap to recompute — the catalog is < 20 entries.
func validEvents() map[string]struct{} {
	cat := Catalog()
	out := make(map[string]struct{}, len(cat))
	for _, e := range cat {
		out[e.Name] = struct{}{}
	}
	return out
}
