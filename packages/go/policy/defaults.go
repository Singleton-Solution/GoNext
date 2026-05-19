package policy

// DefaultRoleCapabilities returns the hierarchical, data-driven map from
// built-in Role to the CapabilitySet that role holds.
//
// The hierarchy (each row strictly contains the row above it):
//
//	subscriber  : read
//	contributor : subscriber + draft authoring (edit_posts, delete_posts)
//	author      : contributor + publish own + upload media
//	editor      : author + manage everything authored by others
//	admin       : editor + site/plugin/theme/user/comment management
//	super_admin : admin + manage_install (filesystem, migrations)
//
// Each call returns a fresh map so callers can mutate without surprising
// other callers. The inner sets are also fresh copies for the same reason.
//
// This is the data backing BasicPolicy. The DB-backed cut (a future
// issue) will read the same shape out of role_capabilities, so any
// downstream code that builds against this map keeps working when the
// DB is plugged in.
func DefaultRoleCapabilities() map[Role]CapabilitySet {
	subscriber := NewCapabilitySet(
		CapRead,
	)

	contributor := subscriber.Union(NewCapabilitySet(
		CapEditPosts,
		CapDeletePosts,
	))

	author := contributor.Union(NewCapabilitySet(
		CapPublishPosts,
		CapEditPublishedPosts,
		CapDeletePublishedPosts,
		CapUploadFiles,
		// Authors own their own media library uploads. Read is paired
		// with upload so an author can browse their previous uploads
		// in the inserter; delete stays at the editor tier so a
		// confused author can't blow away a published asset.
		CapMediaUpload,
		CapMediaRead,
	))

	editor := author.Union(NewCapabilitySet(
		// Posts (others).
		CapEditOthersPosts,
		CapEditPrivatePosts,
		CapReadPrivatePosts,
		CapDeleteOthersPosts,
		CapDeletePrivatePosts,
		// Pages (full set — editors own the page surface).
		CapEditPages,
		CapEditPublishedPages,
		CapEditOthersPages,
		CapEditPrivatePages,
		CapReadPrivatePages,
		CapDeletePages,
		CapDeletePublishedPages,
		CapDeleteOthersPages,
		CapDeletePrivatePages,
		CapPublishPages,
		// Taxonomies + media moderation.
		CapManageCategories,
		CapManageTags,
		CapEditOthersMedia,
		// Editors can also remove media uploaded by other operators.
		// The author tier can upload + read but not delete; the editor
		// tier gains delete so the moderation buck stops here.
		CapMediaDelete,
		// Comment moderation.
		CapModerateComments,
		CapEditComment,
	))

	admin := editor.Union(NewCapabilitySet(
		// Site.
		CapManageOptions,
		CapSystemRead,
		// Users.
		CapListUsers,
		CapCreateUsers,
		CapEditUsers,
		CapDeleteUsers,
		CapPromoteUsers,
		// Plugins + themes.
		CapInstallPlugins,
		CapManagePlugins,
		CapActivatePlugins,
		CapManagePluginSettings,
		CapInstallThemes,
		CapManageThemes,
		CapSwitchThemes,
		CapEditThemes,
		CapThemeEditParts,
		CapThemeCustomize,
		// Background jobs / DLQ inspection.
		CapJobsAdmin,
		// Webhook subscription administration.
		CapWebhooksManage,
	))

	superAdmin := admin.Union(NewCapabilitySet(
		CapManageInstall,
	))

	return map[Role]CapabilitySet{
		RoleSubscriber:  subscriber,
		RoleContributor: contributor,
		RoleAuthor:      author,
		RoleEditor:      editor,
		RoleAdmin:       admin,
		RoleSuperAdmin:  superAdmin,
	}
}
