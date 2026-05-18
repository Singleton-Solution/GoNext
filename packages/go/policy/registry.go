package policy

import (
	"fmt"
	"sort"
	"sync"
)

// registeredCapability is a single entry in the capability registry. It
// carries the slug and a human-readable description; the description is
// what gets rendered in the admin UI when an operator decides which
// roles should hold the cap. (See docs/06-auth-permissions.md §6.2.)
type registeredCapability struct {
	Name        Capability
	Description string
}

// capRegistry is the process-wide capability registry. It is the
// plugin-extensibility seam: a plugin's init code calls RegisterCapability
// once per user-facing capability it adds, and the host (in a future
// issue) uses the registry to seed rows in the capabilities table and
// to drive the "grant to roles" admin UI.
//
// In P0, enforcement is intentionally minimal: registry membership does
// not by itself grant the cap to any role. The DB-backed cut will add
// the role assignment step.
type capRegistry struct {
	mu  sync.RWMutex
	all map[Capability]registeredCapability
}

var globalRegistry = newRegistry()

func newRegistry() *capRegistry {
	r := &capRegistry{all: map[Capability]registeredCapability{}}
	// Pre-seed the registry with every built-in capability so that the
	// "is this cap known?" check is meaningful from the moment the
	// process starts. Plugins add their own on top.
	for _, c := range builtinCapabilityDescriptions() {
		r.all[c.Name] = c
	}
	return r
}

// RegisterCapability adds a capability slug to the process-wide registry.
// Calling it twice with the same name is a programming error and panics
// (a plugin should not double-register, and core should certainly not).
//
// name is the capability slug as it will appear in role assignments
// (e.g. "manage_forms"). description is the human-readable label shown
// to operators; keep it short and verb-led ("Create, edit, and delete
// form submissions").
//
// In P0, the registry is in-memory and reset on process restart. The
// DB-backed cut persists it into the capabilities table on plugin
// install (see docs/06-auth-permissions.md §6.2). Registering a
// capability here does NOT grant it to any role; that step is operator-
// driven.
//
// Safe for concurrent use.
func RegisterCapability(name, description string) {
	globalRegistry.register(Capability(name), description)
}

func (r *capRegistry) register(name Capability, description string) {
	if name == "" {
		panic("policy: RegisterCapability called with empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.all[name]; exists {
		panic(fmt.Sprintf("policy: capability %q is already registered", name))
	}
	r.all[name] = registeredCapability{Name: name, Description: description}
}

// LookupCapability reports whether a capability is registered and
// returns its description. It does not allocate when the cap is missing.
func LookupCapability(name Capability) (description string, ok bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	c, ok := globalRegistry.all[name]
	if !ok {
		return "", false
	}
	return c.Description, true
}

// RegisteredCapabilities returns every registered capability slug, sorted
// lexicographically. Useful for admin UIs and diagnostics. The returned
// slice is a fresh copy.
func RegisteredCapabilities() []Capability {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	out := make([]Capability, 0, len(globalRegistry.all))
	for c := range globalRegistry.all {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// resetRegistryForTest replaces the global registry with a fresh one
// pre-seeded only with the built-in caps. Test-only. The unexported name
// keeps it out of the public surface; in-package tests reach for it
// directly.
func resetRegistryForTest() {
	globalRegistry = newRegistry()
}

// builtinCapabilityDescriptions returns the (Capability, description)
// list that pre-seeds the registry. Keeping this in one place makes the
// admin UI's "list of known caps" call site simple — it's just
// RegisteredCapabilities() — and gives the migration a single source of
// truth for the seed.
func builtinCapabilityDescriptions() []registeredCapability {
	return []registeredCapability{
		// Content.
		{CapRead, "Read content (logged in)."},
		{CapReadPrivatePosts, "Read posts whose status is private."},
		{CapEditPosts, "Create and edit your own posts."},
		{CapEditPublishedPosts, "Edit your own posts after publication."},
		{CapEditOthersPosts, "Edit posts authored by others."},
		{CapEditPrivatePosts, "Edit posts whose status is private."},
		{CapDeletePosts, "Delete your own posts."},
		{CapDeletePublishedPosts, "Delete your own published posts."},
		{CapDeleteOthersPosts, "Delete posts authored by others."},
		{CapDeletePrivatePosts, "Delete posts whose status is private."},
		{CapPublishPosts, "Publish posts."},

		// Pages.
		{CapEditPages, "Create and edit your own pages."},
		{CapEditPublishedPages, "Edit your own pages after publication."},
		{CapEditOthersPages, "Edit pages authored by others."},
		{CapEditPrivatePages, "Edit pages whose status is private."},
		{CapReadPrivatePages, "Read pages whose status is private."},
		{CapDeletePages, "Delete your own pages."},
		{CapDeletePublishedPages, "Delete your own published pages."},
		{CapDeleteOthersPages, "Delete pages authored by others."},
		{CapDeletePrivatePages, "Delete pages whose status is private."},
		{CapPublishPages, "Publish pages."},

		// Taxonomies.
		{CapManageCategories, "Create, edit, and delete categories."},
		{CapManageTags, "Create, edit, and delete tags."},

		// Media.
		{CapUploadFiles, "Upload files to the media library."},
		{CapEditOthersMedia, "Edit and replace media uploaded by others."},

		// Users.
		{CapListUsers, "List user accounts."},
		{CapCreateUsers, "Create user accounts."},
		{CapEditUsers, "Edit user accounts."},
		{CapDeleteUsers, "Delete user accounts."},
		{CapPromoteUsers, "Change a user's role."},

		// Site.
		{CapManageOptions, "Edit site settings."},
		{CapManageInstall, "Run migrations and manage the install on disk."},
		{CapSystemRead, "View the System Status page (DB, Redis, queues, migrations, themes, plugins, disk, build info)."},

		// Plugins / themes.
		{CapInstallPlugins, "Install new plugins."},
		{CapManagePlugins, "Update or uninstall plugins."},
		{CapActivatePlugins, "Enable or disable installed plugins."},
		{CapManagePluginSettings, "Edit plugin settings."},
		{CapInstallThemes, "Install new themes."},
		{CapManageThemes, "Update or uninstall themes."},
		{CapSwitchThemes, "Change the active theme."},
		{CapEditThemes, "Edit theme files."},

		// Comments.
		{CapModerateComments, "Approve, mark spam, or delete comments."},
		{CapEditComment, "Edit a comment's content."},

		// Background jobs.
		{CapJobsAdmin, "Inspect, replay, discard, or redact archived background jobs."},
	}
}
