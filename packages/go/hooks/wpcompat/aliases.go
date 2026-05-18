package wpcompat

// Direction is the dispatch kind of an alias: filter (transforms a value
// through a chain) or action (fire-and-forget side effect). The two
// directions are first-class in the hook bus and behave differently on
// errors, panics, and short-circuits — wpcompat needs to know which kind
// to bridge so the right Bus method (ApplyFilters vs Do) is wired in.
type Direction uint8

const (
	// Filter is a value-transformation hook. The bus runs ApplyFilters
	// through its chain; each handler receives the running value and
	// returns the next value. WP names like the_content, the_title,
	// get_avatar, wp_title all map to filters.
	Filter Direction = iota
	// Action is a fire-and-forget side-effect hook. The bus runs Do,
	// invoking each handler in priority order and aggregating errors.
	// WP names like save_post, init, wp_head, template_redirect map to
	// actions.
	Action
)

// String renders the direction for log and error output. The lowercase
// form matches the canonical hook-kind label the bus already emits in
// metrics, so cross-referencing wpcompat error messages with hook
// metrics is a straight string match.
func (d Direction) String() string {
	switch d {
	case Filter:
		return "filter"
	case Action:
		return "action"
	default:
		return "unknown"
	}
}

// PayloadAdapter translates between a GoNext-native payload (a typed
// struct, usually) and the WP-shaped payload a ported plugin expects
// (frequently a bare string or a tuple of scalars). It is invoked by the
// bridge when forwarding a native dispatch to its WP alias.
//
// The adapter is intentionally a free function rather than an interface
// method because each alias has a one-off shape — defining a method per
// payload type for ~20 hooks would create more interface noise than the
// adapters save. Adapters live next to their entry in the Aliases map so
// the mapping is grep-able.
//
// Conventions:
//
//   - For filters, the adapter is given the running value before it is
//     passed to a WP-side handler, and the same adapter (in reverse)
//     translates the WP-side return value back to the GoNext shape. For
//     symmetry the same function is used in both directions; if the WP
//     shape and native shape are the same, return the input unchanged.
//
//   - For actions, the adapter receives the args slice (as passed to
//     Bus.Do) and may flatten or restructure it for the WP-side handler.
//     Action handlers do not return a value, so the return-trip is not
//     needed.
//
// A nil PayloadAdapter is interpreted as "pass through unchanged" — the
// most common case for filters whose native payload is already a plain
// string (e.g. the_content).
type PayloadAdapter func(in any) any

// Alias is one row in the WP-name -> native-name mapping table.
//
// Fields:
//
//   - NativeName is the canonical GoNext hook name the bus dispatches on.
//     This is the name core code fires; the WP name is purely a façade.
//
//   - Direction selects filter vs action — see Direction docstring.
//
//   - PayloadAdapter, when non-nil, runs on every forwarded payload so
//     a WP-side subscriber sees the shape they expect. nil means
//     pass-through, which is correct for the bulk of WP hooks whose
//     payload is already a string or simple scalar.
//
//   - Notes is a one-line description of what the WP hook is for and any
//     non-obvious mapping decision. The doc generator that produces the
//     appendix in docs/02-plugin-system.md reads this field. Keep it
//     under ~80 characters and end with a period.
//
// Aliases are intentionally value types, not pointers: the map is built
// once at init() and never mutated, so there is no risk of an alias-by-
// pointer being shared across forwarders and accidentally torn.
type Alias struct {
	NativeName     string
	Direction      Direction
	PayloadAdapter PayloadAdapter
	Notes          string
}

// WPPost is the WP-shaped post payload that save_post and similar
// action handlers expect. WordPress hands plugins three scalar args
// (ID, Post, Update); GoNext fires the same action with a typed struct.
// The WPPost type is wpcompat's lowest common denominator — it is the
// shape a plugin author would expect after porting a save_post handler
// without reading our docs.
//
// We keep this struct minimal on purpose. Plugins that want the full
// Post type pull it from packages/go/posts; the WPPost shape is what
// WP plugins were already using and is therefore the shape ports can
// drop in. Adding fields here is a one-way door — once a plugin is
// reading WPPost.Title, taking Title away is a breaking change.
type WPPost struct {
	ID     string
	Post   any // the native Post (caller-supplied type from packages/go/posts)
	Update bool
}

// WPComment mirrors WPPost for comment-related hooks.
type WPComment struct {
	ID      string
	Comment any
	Approve bool
}

// WPUser mirrors WPPost for user-related hooks (user_register, profile_update).
type WPUser struct {
	ID   string
	User any
}

// Aliases is the canonical WP-name -> Alias map. This is the table the
// bridge walks at Register time.
//
// Curated subset rationale: docs/02-plugin-system.md §15.3 calls out the
// open question of whether to ship the full WP hook surface or only the
// top-N. We pick a curated ~20 that cover the bulk of real-world WP
// plugins (page rendering, post lifecycle, init/shutdown, enqueue) and
// leave the long tail (the_meta, get_avatar_data, all_admin_notices, ...)
// unaliased. A plugin can still subscribe to a non-aliased native name
// directly via the bus; the alias table is for the names plugin authors
// reach for by reflex.
//
// The map is package-level and unmodified after init: tests in this
// package treat it as read-only.
var Aliases = map[string]Alias{
	// ─── Filters: page/post rendering ──────────────────────────────────
	"the_content": {
		NativeName:     "core.filter.the_content",
		Direction:      Filter,
		PayloadAdapter: nil, // string in, string out — no translation needed
		Notes:          "Post body HTML being prepared for the rendered page.",
	},
	"the_title": {
		NativeName:     "core.filter.the_title",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Post or page title being prepared for output.",
	},
	"the_excerpt": {
		NativeName:     "core.filter.the_excerpt",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Post excerpt HTML rendered above-the-fold on archive pages.",
	},
	"the_permalink": {
		NativeName:     "core.filter.the_permalink",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "URL string built for a post or page permalink.",
	},
	"wp_title": {
		NativeName:     "core.filter.wp_title",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Document <title> being assembled for the response.",
	},
	"body_class": {
		NativeName:     "core.filter.body_class",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Space-separated CSS classes applied to the <body> element.",
	},
	"post_class": {
		NativeName:     "core.filter.post_class",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Space-separated CSS classes applied to a post wrapper element.",
	},
	"comment_text": {
		NativeName:     "core.filter.comment_text",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Comment body HTML being rendered for display.",
	},
	"get_avatar": {
		NativeName:     "core.filter.get_avatar",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Avatar <img> markup being prepared for output.",
	},
	"login_redirect": {
		NativeName:     "core.filter.login_redirect",
		Direction:      Filter,
		PayloadAdapter: nil,
		Notes:          "Post-login redirect URL chosen for the user.",
	},

	// ─── Actions: lifecycle & rendering side-effects ───────────────────
	"init": {
		NativeName:     "core.lifecycle.init",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Once-per-process initialisation point, before any request handling.",
	},
	"wp_loaded": {
		NativeName:     "core.lifecycle.loaded",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "All plugins, themes, and core have finished loading.",
	},
	"wp_head": {
		NativeName:     "core.render.head",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Emit tags into the <head> of the rendered HTML response.",
	},
	"wp_footer": {
		NativeName:     "core.render.footer",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Emit markup just before </body> of the rendered HTML response.",
	},
	"wp_enqueue_scripts": {
		NativeName:     "core.assets.enqueue",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Register CSS/JS assets the frontend needs for the current request.",
	},
	"admin_enqueue_scripts": {
		NativeName:     "core.assets.enqueue_admin",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Register CSS/JS assets for the admin dashboard.",
	},
	"template_redirect": {
		NativeName:     "core.render.template_redirect",
		Direction:      Action,
		PayloadAdapter: nil,
		Notes:          "Fired before the response template is selected; redirect window.",
	},

	// ─── Actions: post lifecycle (payload-shape adapter) ───────────────
	"save_post": {
		NativeName:     "core.post.saved",
		Direction:      Action,
		PayloadAdapter: adaptSavePost,
		Notes:          "Post inserted or updated. Args: (ID, Post, Update).",
	},
	"publish_post": {
		NativeName:     "core.post.published",
		Direction:      Action,
		PayloadAdapter: adaptSavePost,
		Notes:          "Post transitions to the 'published' state.",
	},
	"delete_post": {
		NativeName:     "core.post.deleted",
		Direction:      Action,
		PayloadAdapter: adaptSavePost,
		Notes:          "Post permanently deleted.",
	},
	"user_register": {
		NativeName:     "core.user.created",
		Direction:      Action,
		PayloadAdapter: adaptUser,
		Notes:          "New user account created.",
	},
	"profile_update": {
		NativeName:     "core.user.updated",
		Direction:      Action,
		PayloadAdapter: adaptUser,
		Notes:          "Existing user profile updated.",
	},
	"comment_post": {
		NativeName:     "core.comment.created",
		Direction:      Action,
		PayloadAdapter: adaptComment,
		Notes:          "New comment submitted on a post.",
	},
}

// nativeIndex is the reverse map of Aliases: NativeName -> WP name. It is
// built once during init so the bridge can answer "is this native name
// aliased?" in O(1) without scanning the Aliases map on every dispatch.
//
// We expose it as nativeIndex (lowercase) because plugin authors should
// not need it — they look up WP names. The bridge implementation uses
// it internally.
var nativeIndex = func() map[string]string {
	m := make(map[string]string, len(Aliases))
	for wp, a := range Aliases {
		// If two WP names map to the same native (e.g. publish_post and
		// save_post could in principle both watch core.post.saved), the
		// reverse map only stores one. The bridge resolves multi-alias
		// fan-out via Aliases directly, so this is fine.
		m[a.NativeName] = wp
	}
	return m
}()

// nativeFanout maps each native name to the list of WP aliases pointing at
// it. Distinct WP names CAN intentionally target the same native event —
// e.g. publish_post and save_post both reasonably observe a post-save —
// in which case both forwarders fire so a plugin author who reaches for
// either WP name hears the event.
//
// Built once at init and treated as read-only thereafter.
var nativeFanout = func() map[string][]string {
	m := make(map[string][]string, len(Aliases))
	for wp, a := range Aliases {
		m[a.NativeName] = append(m[a.NativeName], wp)
	}
	return m
}()

// adaptSavePost packages a native "core.post.saved" payload into the
// WP-shaped WPPost a plugin's save_post handler expects.
//
// Native call site:  bus.Do(ctx, "core.post.saved", postID, post, isUpdate)
// WP plugin expects: handler receives a single value of type WPPost.
//
// The adapter is defensive: if the args don't match the expected shape
// (someone fired core.post.saved with one arg, say), it returns the input
// unchanged so the plugin sees something rather than crashing. Defensive
// adapters trade off "wrong shape silently delivered" against "panic in
// a forwarding goroutine"; in this package the former is preferred since
// plugin authors can see the wrong-shape value in their handler and
// report it, whereas a forwarder panic is invisible.
func adaptSavePost(in any) any {
	args, ok := in.([]any)
	if !ok || len(args) < 3 {
		return in
	}
	id, _ := args[0].(string)
	upd, _ := args[2].(bool)
	return WPPost{ID: id, Post: args[1], Update: upd}
}

// adaptUser packages a native "core.user.*" payload into WPUser.
// Native call site:  bus.Do(ctx, "core.user.created", userID, user)
// WP plugin expects: a single WPUser value.
func adaptUser(in any) any {
	args, ok := in.([]any)
	if !ok || len(args) < 2 {
		return in
	}
	id, _ := args[0].(string)
	return WPUser{ID: id, User: args[1]}
}

// adaptComment packages a native "core.comment.created" payload into
// WPComment.
// Native call site: bus.Do(ctx, "core.comment.created", commentID, comment, approved)
// WP plugin expects: a single WPComment value.
func adaptComment(in any) any {
	args, ok := in.([]any)
	if !ok || len(args) < 3 {
		return in
	}
	id, _ := args[0].(string)
	approve, _ := args[2].(bool)
	return WPComment{ID: id, Comment: args[1], Approve: approve}
}
