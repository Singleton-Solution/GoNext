package policy

// Capability is a named string identifying a capability slug. Like Role,
// it is a named type so that an unqualified "edit_posts" literal anywhere
// in a non-policy package is a build error. Plugins extend the set via
// RegisterCapability; core capabilities are the constants below.
type Capability string

// Built-in capability slugs. Grouped by area. The list is intentionally
// a superset of the strict WP set, kept verbatim so meta-capability
// mapping (docs/06-auth-permissions.md §6.3) stays straightforward. New
// capabilities should land here (for core) or via RegisterCapability
// (for plugins).
const (
	// Content (posts).
	CapRead                 Capability = "read"
	CapReadPrivatePosts     Capability = "read_private_posts"
	CapEditPosts            Capability = "edit_posts"
	CapEditPublishedPosts   Capability = "edit_published_posts"
	CapEditOthersPosts      Capability = "edit_others_posts"
	CapEditPrivatePosts     Capability = "edit_private_posts"
	CapDeletePosts          Capability = "delete_posts"
	CapDeletePublishedPosts Capability = "delete_published_posts"
	CapDeleteOthersPosts    Capability = "delete_others_posts"
	CapDeletePrivatePosts   Capability = "delete_private_posts"
	CapPublishPosts         Capability = "publish_posts"

	// Pages.
	CapEditPages            Capability = "edit_pages"
	CapEditPublishedPages   Capability = "edit_published_pages"
	CapEditOthersPages      Capability = "edit_others_pages"
	CapEditPrivatePages     Capability = "edit_private_pages"
	CapDeletePages          Capability = "delete_pages"
	CapDeletePublishedPages Capability = "delete_published_pages"
	CapDeleteOthersPages    Capability = "delete_others_pages"
	CapDeletePrivatePages   Capability = "delete_private_pages"
	CapPublishPages         Capability = "publish_pages"
	CapReadPrivatePages     Capability = "read_private_pages"

	// Taxonomies.
	CapManageCategories Capability = "manage_categories"
	CapManageTags       Capability = "manage_tags"

	// Media.
	CapUploadFiles     Capability = "upload_files"
	CapEditOthersMedia Capability = "edit_others_media"

	// Users.
	CapListUsers    Capability = "list_users"
	CapCreateUsers  Capability = "create_users"
	CapEditUsers    Capability = "edit_users"
	CapDeleteUsers  Capability = "delete_users"
	CapPromoteUsers Capability = "promote_users"

	// Site.
	CapManageOptions Capability = "manage_options"
	CapManageInstall Capability = "manage_install"
	// CapSystemRead grants read-only access to the operator-facing System
	// Status surface (DB/Redis/queue health, migration version, theme +
	// plugin inventory, disk usage, build info). It is intentionally a
	// READ capability — mutating system state is gated by manage_install.
	CapSystemRead Capability = "system_read"

	// Plugins / themes.
	CapInstallPlugins       Capability = "install_plugins"
	CapManagePlugins        Capability = "manage_plugins"
	CapActivatePlugins      Capability = "activate_plugins"
	CapManagePluginSettings Capability = "manage_plugin_settings"
	CapInstallThemes        Capability = "install_themes"
	CapManageThemes         Capability = "manage_themes"
	CapSwitchThemes         Capability = "switch_themes"
	CapEditThemes           Capability = "edit_themes"

	// Comments.
	CapModerateComments Capability = "moderate_comments"
	CapEditComment      Capability = "edit_comment"

	// Background jobs / DLQ administration. Holders can list archived
	// (dead-letter) tasks, replay them onto the active queue, discard
	// them, and apply redaction masks to sensitive payload fields before
	// display in the admin UI. Issue #262.
	CapJobsAdmin Capability = "jobs.admin"

	// Media library administration. The three caps split the surface so
	// a constrained operator role (e.g. "media moderator") can be
	// granted read + delete without also getting upload access — useful
	// when an external CMS owns the upload path but the admin UI still
	// renders the library.
	//
	//   - CapMediaUpload — POST /api/v1/admin/media.
	//   - CapMediaRead   — GET  /api/v1/admin/media (list) and /{id} (detail).
	//   - CapMediaDelete — DELETE /api/v1/admin/media/{id}.
	//
	// PATCH alt-text/caption is gated by CapMediaUpload because the
	// operator who can put files into the library is the same operator
	// who can describe them; carving a fourth cap for "edit metadata"
	// is more granularity than the admin surface needs today.
	CapMediaUpload Capability = "media.upload"
	CapMediaRead   Capability = "media.read"
	CapMediaDelete Capability = "media.delete"
)

// CapabilitySet is the resolved set of capabilities a Principal holds.
// It is a map for O(1) Has checks. The zero value is an empty (deny-all)
// set, which is the correct default.
type CapabilitySet map[Capability]struct{}

// NewCapabilitySet returns a CapabilitySet containing exactly the given
// capabilities. Duplicates are folded.
func NewCapabilitySet(caps ...Capability) CapabilitySet {
	s := make(CapabilitySet, len(caps))
	for _, c := range caps {
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether the set contains the capability.
func (s CapabilitySet) Has(c Capability) bool {
	_, ok := s[c]
	return ok
}

// Add inserts capabilities into the set. Returns the set for chaining.
func (s CapabilitySet) Add(caps ...Capability) CapabilitySet {
	for _, c := range caps {
		s[c] = struct{}{}
	}
	return s
}

// Union returns the union of s and t as a fresh set. Neither input is
// mutated.
func (s CapabilitySet) Union(t CapabilitySet) CapabilitySet {
	out := make(CapabilitySet, len(s)+len(t))
	for c := range s {
		out[c] = struct{}{}
	}
	for c := range t {
		out[c] = struct{}{}
	}
	return out
}

// All returns the capabilities in the set as a slice. Order is not
// guaranteed. Useful for logging and tests.
func (s CapabilitySet) All() []Capability {
	out := make([]Capability, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	return out
}
