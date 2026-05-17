package posts

import (
	"errors"
	"log/slog"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// PostTypePost is the value for /api/v1/posts mounts.
const PostTypePost = "post"

// PostTypePage is the value for /api/v1/pages mounts.
const PostTypePage = "page"

// Deps is the dependency bundle passed to [Mount]. Every field is
// required except Logger (defaults to slog.Default).
//
// We keep a single Deps struct rather than a builder pattern because
// the dependency set is small, stable, and changes here would land in
// the same PR as the call-site change that needs them — the builder
// flexibility wouldn't earn its complexity.
type Deps struct {
	// Store is the persistence layer. Production wires a PgStore;
	// tests substitute MemoryStore.
	Store Store

	// Policy resolves capability questions. Both route-level
	// (handler entry gate) and object-level (author vs others) checks
	// run through this interface.
	Policy policy.Policy

	// Audit is the audit emitter. The handlers emit one event per
	// successful write (post.created, post.updated, post.trashed); a
	// nil emitter is tolerated (writes a noop) so test wiring stays
	// short.
	Audit *audit.Emitter

	// Logger receives structured log lines on internal errors. nil
	// falls back to slog.Default at Mount time.
	Logger *slog.Logger

	// PostType discriminates between the post and page mounts. The
	// /api/v1/posts mount sets PostTypePost; the /api/v1/pages mount
	// sets PostTypePage. The discriminator is reflected in capability
	// resolution (CapEditPosts vs CapEditPages) and in every store call.
	PostType string
}

// validate is called by [Mount] to fail fast on misconfigured wiring.
func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("posts.Mount: Deps.Store is required")
	}
	if d.Policy == nil {
		return errors.New("posts.Mount: Deps.Policy is required")
	}
	if d.PostType != PostTypePost && d.PostType != PostTypePage {
		return errors.New("posts.Mount: Deps.PostType must be 'post' or 'page'")
	}
	return nil
}

// capabilitySet bundles the capability slugs that the handler routes
// against. The post and page sets differ only in the literal slug
// names, so we resolve once at Mount time and pass the resolved struct
// into the handlers — no per-request string lookups.
type capabilitySet struct {
	edit         policy.Capability // edit_posts / edit_pages
	editOthers   policy.Capability // edit_others_posts / edit_others_pages
	publish      policy.Capability // publish_posts / publish_pages
	deleteOwn    policy.Capability // delete_posts / delete_pages
	deleteOthers policy.Capability // delete_others_posts / delete_others_pages
	readPrivate  policy.Capability // read_private_posts / read_private_pages
}

// capsFor returns the capability slugs for the given post type.
// Unrecognized types yield the post family — Mount validates the type
// upfront, so this fall-through is defensive only.
func capsFor(postType string) capabilitySet {
	if postType == PostTypePage {
		return capabilitySet{
			edit:         policy.CapEditPages,
			editOthers:   policy.CapEditOthersPages,
			publish:      policy.CapPublishPages,
			deleteOwn:    policy.CapDeletePages,
			deleteOthers: policy.CapDeleteOthersPages,
			readPrivate:  policy.CapReadPrivatePages,
		}
	}
	return capabilitySet{
		edit:         policy.CapEditPosts,
		editOthers:   policy.CapEditOthersPosts,
		publish:      policy.CapPublishPosts,
		deleteOwn:    policy.CapDeletePosts,
		deleteOthers: policy.CapDeleteOthersPosts,
		readPrivate:  policy.CapReadPrivatePosts,
	}
}
